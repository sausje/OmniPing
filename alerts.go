package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Event is one entry in the alert history.
type Event struct {
	ID         string     `json:"id"`
	T          int64      `json:"t"`
	Kind       string     `json:"kind"` // start, end, ipchange, test, trace
	Rule       string     `json:"rule"`
	RuleName   string     `json:"ruleName"`
	Target     string     `json:"target"`
	TargetName string     `json:"targetName"`
	Title      string     `json:"title"`
	Text       string     `json:"text"`
	Sent       []Delivery `json:"sent"`
	// Held names the together rule that was firing, so nothing was sent.
	Held string `json:"held,omitempty"`
	// Trace is the traceroute of a "trace" event (traceroute.go).
	Trace []TraceHop `json:"trace,omitempty"`
}

// Delivery records whether a notification reached its channel.
type Delivery struct {
	Channel string `json:"channel"`
	Name    string `json:"name"`
	State   string `json:"state"` // sending, ok, failed
	Err     string `json:"err,omitempty"`
}

// ActiveAlert is a rule that is currently firing for a target.
type ActiveAlert struct {
	Rule     string `json:"rule"`
	RuleName string `json:"ruleName"`
	Target   string `json:"target"`
	Since    int64  `json:"since"`
	Reason   string `json:"reason"`
	// Targets lists the targets of a together rule; Target is empty then.
	Targets []string `json:"targets,omitempty"`
}

type ruleState struct {
	active   bool
	since    int64
	reason   string
	lastSent map[int]int64
	pending  int64    // when the conditions were first met, while waiting to fire
	held     string   // together rule that was firing when this alert started
	members  []string // targets of a together rule when it started
}

// Alerts evaluates rules after every sample and fires their actions.
type Alerts struct {
	app *App

	mu     sync.Mutex
	states map[string]*ruleState // "rule/target"
	events []*Event
	path   string
	dirty  chan struct{}
	saveMu sync.Mutex
}

const maxEvents = 5000

func newAlerts(app *App, path string) *Alerts {
	a := &Alerts{app: app, states: map[string]*ruleState{}, path: path, dirty: make(chan struct{}, 1)}
	a.loadEvents()
	go a.saveLoop()
	return a
}

// save asks for events.jsonl to be written within a few seconds, so a burst
// of alerts and delivery updates costs one write instead of dozens (easier
// on SD cards).
func (a *Alerts) save() {
	select {
	case a.dirty <- struct{}{}:
	default:
	}
}

func (a *Alerts) saveLoop() {
	for range a.dirty {
		time.Sleep(5 * time.Second)
		a.persist()
	}
}

func (a *Alerts) loadEvents() {
	f, err := os.Open(a.path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			a.events = append(a.events, &ev)
		}
	}
	if len(a.events) > maxEvents {
		a.events = a.events[len(a.events)-maxEvents:]
	}
	for _, ev := range a.events {
		for i := range ev.Sent {
			if st := ev.Sent[i].State; st == "sending" || st == "retrying" {
				ev.Sent[i].State = "failed"
				ev.Sent[i].Err = "interrupted by a restart"
			}
		}
	}
}

// persist writes events.jsonl now (save writes it soon). Events are encoded
// under the lock because deliveries update them from other goroutines.
func (a *Alerts) persist() {
	var buf bytes.Buffer
	a.mu.Lock()
	for _, ev := range a.events {
		b, _ := json.Marshal(ev)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	a.mu.Unlock()
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		log.Printf("saving the alert history: %v", err)
		return
	}
	os.Rename(tmp, a.path)
}

// Events returns the newest events, optionally for one target.
func (a *Alerts) Events(target string, limit int) []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []Event{}
	for i := len(a.events) - 1; i >= 0 && len(out) < limit; i-- {
		ev := a.events[i]
		if target == "" || ev.Target == target {
			out = append(out, ev.clone())
		}
	}
	return out
}

// Active lists alerts that are currently firing.
func (a *Alerts) Active() []ActiveAlert {
	a.mu.Lock()
	defer a.mu.Unlock()
	var names map[string]string
	a.app.cfg.read(func(c *Config) {
		names = map[string]string{}
		for _, r := range c.Rules {
			names[r.ID] = r.Name
		}
	})
	out := []ActiveAlert{}
	for key, st := range a.states {
		if !st.active {
			continue
		}
		rule, target, _ := strings.Cut(key, "/")
		if _, ok := names[rule]; !ok {
			continue
		}
		al := ActiveAlert{Rule: rule, RuleName: names[rule], Target: target, Since: st.since, Reason: st.reason}
		if target == "*" {
			al.Target, al.Targets = "", slices.Clone(st.members)
		}
		out = append(out, al)
	}
	slices.SortFunc(out, func(x, y ActiveAlert) int { return int(x.Since - y.Since) })
	return out
}

// ResetRule forgets the state of a rule (after it was edited or deleted).
func (a *Alerts) ResetRule(ruleID string) {
	a.mu.Lock()
	for key := range a.states {
		if strings.HasPrefix(key, ruleID+"/") {
			delete(a.states, key)
		}
	}
	a.mu.Unlock()
	a.app.hub.Broadcast("active", a.Active())
}

// ResetAll forgets every alert (when monitoring stops).
func (a *Alerts) ResetAll() {
	a.mu.Lock()
	a.states = map[string]*ruleState{}
	a.mu.Unlock()
	a.app.hub.Broadcast("active", a.Active())
}

// ResetTarget forgets the state of a target (after it was deleted).
func (a *Alerts) ResetTarget(id string) {
	a.mu.Lock()
	for key := range a.states {
		if strings.HasSuffix(key, "/"+id) {
			delete(a.states, key)
		}
	}
	a.mu.Unlock()
	a.app.hub.Broadcast("active", a.Active())
}

func (a *Alerts) condition(c Cond, id string, now int64) (bool, string) {
	switch c.Type {
	case "loss", "latency":
		n := max(c.Samples, 1)
		smp := a.app.store.Last(id, n)
		if len(smp) < n {
			return false, ""
		}
		lost, sum := 0, 0.0
		for _, x := range smp {
			if x.R < 0 {
				lost++
			} else {
				sum += float64(x.R)
			}
		}
		if c.Type == "loss" {
			pct := float64(lost) * 100 / float64(n)
			if pct >= c.Value && lost > 0 {
				return true, fmt.Sprintf("packet loss %.0f%% over the last %d samples", pct, n)
			}
			return false, ""
		}
		if got := n - lost; got > 0 {
			if avg := sum / float64(got); avg >= c.Value {
				return true, fmt.Sprintf("average latency %s over the last %d samples", fmtMs(avg), n)
			}
		}
	case "noreply":
		first, last := a.app.store.Seen(id)
		ref := last
		if ref == 0 {
			ref = first
		}
		if ref > 0 && float64(now-ref) >= c.Value*1000 {
			return true, "no reply for " + fmtDuration(now-ref)
		}
	}
	return false, ""
}

func (a *Alerts) evaluate(r Rule, id string, now int64) (bool, string) {
	all := r.Match == "all"
	met, used := all, 0
	var reasons []string
	for _, c := range r.Conds {
		if !c.On || c.Type == "ipchange" {
			continue
		}
		used++
		ok, why := a.condition(c, id, now)
		if ok {
			reasons = append(reasons, why)
		}
		if all && !ok {
			met = false
		}
		if !all && ok {
			met = true
		}
	}
	if used == 0 {
		return false, ""
	}
	return met, strings.Join(reasons, ", ")
}

// OnSample re-evaluates the rules of a target after a new sample.
func (a *Alerts) OnSample(id string, now int64) {
	t, ok := a.app.cfg.target(id)
	if !ok {
		return
	}
	rules := a.app.cfg.rulesFor(&t)
	changed := false
	off := t.offAt(now)
	// Rules that treat their targets as one go first, so the others can see
	// whether one of them is firing.
	for _, r := range rules {
		if r.Together && a.onGroupSample(r, id, now) {
			changed = true
		}
	}
	grace, holder := a.holdInfo(rules, &t)
	for _, r := range rules {
		// While it's expected to be off, nothing changes for its own rules.
		if r.Together || off {
			continue
		}
		met, reason := a.evaluate(r, id, now)
		key := r.ID + "/" + id
		a.mu.Lock()
		st := a.state(key)
		var kind string
		switch {
		case met && !st.active:
			// Near a together rule, wait a little: if it turns out the whole
			// group is down, this alert is held back instead of sent.
			if st.pending == 0 {
				st.pending = now
			}
			if holder == "" && now-st.pending < grace {
				break
			}
			kind, st.active, st.since, st.reason = "start", true, st.pending, reason
			st.held, st.pending = holder, 0
			changed = true
		case met:
			kind, st.reason = "each", reason
		case st.active:
			kind, st.active = "end", false
			changed = true
		default:
			kind, st.pending = "ok", 0
		}
		since, held := st.since, st.held
		if kind == "end" {
			st.held = ""
		}
		var acts []int
		if held == "" {
			acts = st.due(r, kind, now)
		}
		a.mu.Unlock()
		if len(acts) > 0 || kind == "start" || kind == "end" {
			a.fire(r, t, kind, reason, since, now, acts, held)
		}
	}
	if changed {
		a.app.hub.Broadcast("active", a.Active())
	}
}

// state returns the state for a rule/target key; a.mu must be held.
func (a *Alerts) state(key string) *ruleState {
	st := a.states[key]
	if st == nil {
		st = &ruleState{lastSent: map[int]int64{}}
		a.states[key] = st
	}
	return st
}

// due lists the actions of r that run now for an alert in state kind.
func (st *ruleState) due(r Rule, kind string, now int64) []int {
	var acts []int
	for i, act := range r.Actions {
		if !slices.Contains(act.When, kind) {
			continue
		}
		if act.RepeatMin > 0 && (kind == "each" || kind == "ok") && now-st.lastSent[i] < int64(act.RepeatMin)*60_000 {
			continue
		}
		st.lastSent[i] = now
		acts = append(acts, i)
	}
	return acts
}

// holdInfo looks at the together rules among a target's rules. grace is how
// long a new alert from another rule waits, so that a group outage is
// recognised first; holder names a together rule that is firing now.
func (a *Alerts) holdInfo(rules []Rule, t *Target) (grace int64, holder string) {
	s := a.app.cfg.settings()
	interval := int64(s.IntervalMs)
	if t.IntervalMs > 0 {
		interval = int64(t.IntervalMs)
	}
	for _, r := range rules {
		if !r.Together {
			continue
		}
		var d int64
		for _, c := range r.Conds {
			switch {
			case !c.On:
			case c.Type == "noreply":
				d = max(d, int64(c.Value*1000))
			case c.Type == "loss" || c.Type == "latency":
				d = max(d, int64(c.Samples)*interval)
			}
		}
		grace = max(grace, d+int64(s.TimeoutMs)+2*interval)
		a.mu.Lock()
		st := a.states[r.ID+"/*"]
		if holder == "" && st != nil && st.active {
			holder = r.Name
		}
		a.mu.Unlock()
	}
	return grace, holder
}

// onGroupSample evaluates a together rule after a sample of one of its
// targets. It fires when every running target in its scope meets the
// conditions, and ends as soon as one no longer does. It reports whether
// the rule started or ended.
func (a *Alerts) onGroupSample(r Rule, id string, now int64) bool {
	var members []Target
	for _, t := range a.app.cfg.targets() {
		// A member that's expected to be off doesn't count: the rest
		// being down is still the whole group.
		if !t.Paused && r.Scope.covers(&t) && !t.offAt(now) {
			members = append(members, t)
		}
	}
	if len(members) == 0 {
		return false
	}
	met, first := true, ""
	names := make([]string, len(members))
	ids := make([]string, len(members))
	for i, m := range members {
		names[i], ids[i] = cmp.Or(m.Name, m.Host), m.ID
		if !met {
			continue
		}
		ok, why := a.evaluate(r, m.ID, now)
		if !ok {
			met = false
		} else if first == "" {
			first = firstReason(why)
		}
	}
	reason := joinNames(names) + ": " + first
	group := Target{Name: joinNames(names)}

	a.mu.Lock()
	st := a.state(r.ID + "/*")
	var kind string
	changed := false
	switch {
	case met && !st.active:
		kind, st.active, st.since, st.reason, st.members = "start", true, now, reason, ids
		changed = true
	case met:
		kind, st.reason = "each", reason
	case st.active:
		kind, st.active = "end", false
		reason = st.reason
		changed = true
	default:
		kind = "ok"
	}
	since := st.since
	var acts []int
	// Every sample of every member arrives here; let repeating actions run
	// once per round, on the first member's samples.
	if kind == "start" || kind == "end" || id == members[0].ID {
		acts = st.due(r, kind, now)
	}
	a.mu.Unlock()
	if len(acts) > 0 || kind == "start" || kind == "end" {
		a.fire(r, group, kind, reason, since, now, acts, "")
	}
	if kind == "start" {
		go a.app.traceOutage(r, members)
	}
	return changed
}

// joinNames lists names as "A", "A and B" or "A, B and C".
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// OnIPChange fires rules that watch for a changed address.
func (a *Alerts) OnIPChange(id, oldIP, newIP string) {
	t, ok := a.app.cfg.target(id)
	if !ok {
		return
	}
	now := time.Now().UnixMilli()
	for _, r := range a.app.cfg.rulesFor(&t) {
		if r.Together {
			continue
		}
		watch := false
		for _, c := range r.Conds {
			if c.Type == "ipchange" && c.On {
				watch = true
			}
		}
		if !watch {
			continue
		}
		var acts []int
		for i, act := range r.Actions {
			if slices.Contains(act.When, "start") || slices.Contains(act.When, "each") {
				acts = append(acts, i)
			}
		}
		a.fire(r, t, "ipchange", fmt.Sprintf("address changed from %s to %s", oldIP, newIP), now, now, acts, "")
	}
}

// Test sends a test notification through every action of a rule.
func (a *Alerts) Test(r Rule) {
	t := Target{ID: "", Name: "Test target", Host: "192.0.2.1", IP: "192.0.2.1"}
	acts := make([]int, len(r.Actions))
	for i := range acts {
		acts[i] = i
	}
	now := time.Now().UnixMilli()
	a.fire(r, t, "test", "this is a test of the rule's actions", now, now, acts, "")
}

func (a *Alerts) message(r Rule, t Target, kind, reason string, since, now int64) (title, body string) {
	name := t.Name
	if name == "" {
		name = t.Host
	}
	where := name
	if t.IP != "" && t.IP != name {
		where = fmt.Sprintf("%s (%s)", name, t.IP)
	}
	format := a.app.cfg.settings().DateFormat
	when := fmtTime(now, format)
	switch {
	case r.Together && kind == "start":
		title = r.Name
		body = fmt.Sprintf("%s. Rule “%s”, %s.", reason, r.Name, when)
	case r.Together && kind == "each":
		title = "Still: " + r.Name
		body = fmt.Sprintf("%s, since %s.", reason, fmtDuration(now-since)+" ago")
	case r.Together && kind == "end":
		title = "Over: " + r.Name
		body = fmt.Sprintf("%s lasted %s, from %s to %s. Everything answers again.", r.Name, fmtDuration(now-since), fmtClock(since, now, format), fmtClock(now, now, format))
	case r.Together && kind == "ok":
		title = r.Name + ": all is well"
		body = fmt.Sprintf("%s answer. Rule “%s”, %s.", name, r.Name, when)
	}
	switch {
	case title != "":
	case kind == "start":
		title = fmt.Sprintf("%s: %s", name, firstReason(reason))
		body = fmt.Sprintf("%s: %s. Rule “%s”, %s.", where, reason, r.Name, when)
	case kind == "each":
		title = fmt.Sprintf("%s: still %s", name, firstReason(reason))
		body = fmt.Sprintf("%s: %s, since %s.", where, reason, fmtDuration(now-since)+" ago")
	case kind == "end":
		title = fmt.Sprintf("%s is OK again", name)
		body = fmt.Sprintf("%s recovered after %s. Rule “%s”, %s.", where, fmtDuration(now-since), r.Name, when)
	case kind == "ok":
		title = fmt.Sprintf("%s is OK", name)
		body = fmt.Sprintf("%s: no problems. Rule “%s”, %s.", where, r.Name, when)
	case kind == "ipchange":
		title = fmt.Sprintf("%s changed address", name)
		body = fmt.Sprintf("%s: %s. Rule “%s”, %s.", name, reason, r.Name, when)
	default:
		title = "OmniPing test notification"
		body = fmt.Sprintf("Test from rule “%s”, %s. If you can read this, the channel works.", r.Name, when)
	}
	tmpl := r.Message
	if kind == "end" {
		tmpl = r.MessageEnd
	}
	if tmpl != "" && kind != "test" {
		repl := strings.NewReplacer(
			"{target}", name, "{ip}", t.IP, "{host}", t.Host, "{group}", t.Group,
			"{rule}", r.Name, "{state}", kind, "{reason}", reason, "{time}", when,
			"{duration}", fmtDuration(now-since),
			"{since}", fmtClock(since, now, format), "{until}", fmtClock(now, now, format),
		)
		body = repl.Replace(tmpl)
	}
	return title, body
}

// fmtClock shows a time as 15:04, with the date in front when it is not on
// the same day as ref.
func fmtClock(ms, ref int64, format string) string {
	t, r := time.UnixMilli(ms), time.UnixMilli(ref)
	if t.YearDay() == r.YearDay() && t.Year() == r.Year() {
		return t.Format("15:04")
	}
	return strings.TrimSuffix(fmtTime(ms, format), t.Format(":05"))
}

func firstReason(reason string) string {
	s, _, _ := strings.Cut(reason, ",")
	if s == "" {
		return "problem"
	}
	return s
}

// fire records an event and sends the actions acts. held names a together
// rule that is firing; the event is then only recorded.
func (a *Alerts) fire(r Rule, t Target, kind, reason string, since, now int64, acts []int, held string) {
	title, body := a.message(r, t, kind, reason, since, now)
	ev := &Event{
		ID: newID(), T: now, Kind: kind, Rule: r.ID, RuleName: r.Name,
		Target: t.ID, TargetName: t.Name, Title: title, Text: body, Held: held,
	}
	if held != "" {
		acts = nil
	}
	logged := kind != "each" && kind != "ok"
	// List every delivery before sending: they report back from other
	// goroutines and must find ev.Sent complete.
	var chans []Channel
	for _, i := range acts {
		if ch, ok := a.app.cfg.channel(r.Actions[i].Channel); ok && ch.Enabled {
			chans = append(chans, ch)
			ev.Sent = append(ev.Sent, Delivery{Channel: ch.ID, Name: ch.Name, State: "sending"})
		}
	}
	if logged {
		a.mu.Lock()
		a.events = append(a.events, ev)
		if len(a.events) > maxEvents {
			a.events = a.events[len(a.events)-maxEvents:]
		}
		cp := ev.clone()
		a.mu.Unlock()
		a.app.hub.Broadcast("event", cp)
		a.save()
	}
	n := Notice{Kind: kind, Title: title, Body: body, Target: t, Rule: r, Reason: reason, Time: now}
	for idx, ch := range chans {
		update := func(state string, err error) {
			if !logged {
				return
			}
			a.mu.Lock()
			ev.Sent[idx].State, ev.Sent[idx].Err = state, ""
			if err != nil {
				ev.Sent[idx].Err = err.Error()
			}
			cp := ev.clone()
			a.mu.Unlock()
			a.app.hub.Broadcast("event", cp)
			a.save()
		}
		done := func(err error) {
			if err != nil {
				update("failed", err)
			} else {
				update("ok", nil)
			}
		}
		a.app.notifier.Send(ch, n, done, func(err error) { update("retrying", err) })
	}
}

func (ev *Event) clone() Event {
	cp := *ev
	cp.Sent = slices.Clone(ev.Sent)
	cp.Trace = slices.Clone(ev.Trace)
	return cp
}

// add keeps an event that isn't a notification, like a traceroute.
func (a *Alerts) add(ev *Event) {
	a.mu.Lock()
	a.events = append(a.events, ev)
	if len(a.events) > maxEvents {
		a.events = a.events[len(a.events)-maxEvents:]
	}
	cp := ev.clone()
	a.mu.Unlock()
	a.app.hub.Broadcast("event", cp)
	a.save()
}

func fmtMs(v float64) string {
	switch {
	case v < 1:
		return fmt.Sprintf("%.2f ms", v)
	case v < 100:
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.0f ms", v)
}

func fmtDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min %d s", int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days %d h", int(d.Hours())/24, int(d.Hours())%24)
}

func fmtTime(ms int64, format string) string {
	t := time.UnixMilli(ms)
	switch format {
	case "ymd":
		return t.Format("2006-01-02 15:04:05")
	case "mdy":
		return t.Format("01/02/2006 15:04:05")
	}
	return t.Format("02-01-2006 15:04:05")
}

// logPath turns the file of a log channel into a path inside the data
// folder's logs folder. Anything else is refused, because the page has no
// login and could otherwise append to any file, settings included.
func logPath(dataDir, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "logs/alerts.log"
	}
	p = filepath.Clean(filepath.FromSlash(p))
	if !filepath.IsLocal(p) || !strings.HasPrefix(filepath.ToSlash(p), "logs/") {
		return "", errors.New("the log file must be in the logs folder, like logs/alerts.log")
	}
	return filepath.Join(dataDir, p), nil
}
