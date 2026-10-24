package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// TargetDTO is a target as the browser sees it.
type TargetDTO struct {
	Target
	Error string `json:"error,omitempty"`
	Count int64  `json:"count"`
}

// Info describes the server for the status bar and the About page.
type Info struct {
	Version     string `json:"version"`
	Mode        string `json:"mode"`
	PingerError string `json:"pingerError,omitempty"`
	Hostname    string `json:"hostname"`
	DataDir     string `json:"dataDir"`
	Started     int64  `json:"started"`
	Now         int64  `json:"now"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Samples     int64  `json:"samples"`
	DiskBytes   int64  `json:"diskBytes"`
	MemBytes    uint64 `json:"memBytes"`
	Scripts     bool   `json:"scripts"`
	// Desktop: the Windows app, with "This computer" settings and Windows
	// notifications.
	Desktop bool `json:"desktop"`
	// Install is how it was installed elsewhere (see installKind).
	Install string `json:"install,omitempty"`
	// UTCOffset is the server clock's offset from UTC in seconds, for the
	// times targets are expected to be off.
	UTCOffset int `json:"utcOffset"`
	// Update is a newer release, when there is one.
	Update *UpdateInfo `json:"update,omitempty"`
}

// State is everything a page needs to draw itself.
type State struct {
	Settings Settings      `json:"settings"`
	Targets  []TargetDTO   `json:"targets"`
	Groups   []string      `json:"groups"`
	Channels []Channel     `json:"channels"`
	Rules    []*Rule       `json:"rules"`
	Comments []*Comment    `json:"comments"`
	Active   []ActiveAlert `json:"active"`
	Info     Info          `json:"info"`
}

func (a *App) targetDTO(t Target) TargetDTO {
	return TargetDTO{Target: t, Error: a.engine.Err(t.ID), Count: a.store.Count(t.ID)}
}

func (a *App) info() Info {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	host, _ := os.Hostname()
	mode := "not available"
	if a.pinger != nil {
		mode = a.pinger.Mode()
	}
	info := Info{
		Version: version, Mode: mode, PingerError: a.pingerErr, Hostname: host, DataDir: a.shownDataDir(),
		Started: a.started.UnixMilli(), Now: time.Now().UnixMilli(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		Samples: a.store.Total(), DiskBytes: a.diskBytes.Load(), MemBytes: m.Sys - m.HeapReleased,
		Scripts: a.allowScripts, Desktop: a.desktop(), Update: a.update.Load(),
	}
	if !info.Desktop {
		info.Install = installKind()
	}
	_, info.UTCOffset = time.Now().Zone()
	return info
}

func (a *App) state() State {
	var s State
	a.cfg.read(func(c *Config) {
		s.Settings = c.Settings
		s.Settings.Columns = slices.Clone(c.Settings.Columns)
		s.Groups = slices.Clone(c.Groups)
		for _, t := range c.Targets {
			if t.Group != "" && !slices.Contains(s.Groups, t.Group) {
				s.Groups = append(s.Groups, t.Group)
			}
		}
		for _, ch := range c.Channels {
			cp := *ch
			cp.Config = map[string]string{}
			for k, v := range ch.Config {
				if v != "" && isSecret(k) {
					v = masked
				}
				cp.Config[k] = v
			}
			s.Channels = append(s.Channels, cp)
		}
		for _, r := range c.Rules {
			cp := *r
			s.Rules = append(s.Rules, &cp)
		}
		for _, cm := range c.Comments {
			cp := *cm
			s.Comments = append(s.Comments, &cp)
		}
	})
	for _, t := range a.cfg.targets() {
		s.Targets = append(s.Targets, a.targetDTO(t))
	}
	if s.Targets == nil {
		s.Targets = []TargetDTO{}
	}
	if s.Rules == nil {
		s.Rules = []*Rule{}
	}
	if s.Comments == nil {
		s.Comments = []*Comment{}
	}
	if s.Channels == nil {
		s.Channels = []Channel{}
	}
	s.Active = a.alerts.Active()
	s.Info = a.info()
	return s
}

// changed tells every page to pick up the new configuration.
func (a *App) changed() { a.hub.Broadcast("state", a.state()) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("could not read the request: %v", err)
	}
	return nil
}

func (a *App) routes(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.state()) })
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.info()) })
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(omniPingHeader, version)
		w.Header().Set(ownerHeader, ownerID())
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /metrics", a.serveMetrics)
	mux.HandleFunc("POST /api/import/config", a.importConfig)
	mux.HandleFunc("GET /api/report", a.getReport)
	mux.HandleFunc("GET /login", a.login)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("GET /api/auth", a.getAuth)
	mux.HandleFunc("PUT /api/auth", a.putAuth)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("GET /api/summary", a.getSummary)
	mux.HandleFunc("POST /api/summary/send", a.sendSummaryNow)
	mux.HandleFunc("PUT /api/settings", a.putSettings)
	mux.HandleFunc("POST /api/targets", a.addTargets)
	mux.HandleFunc("PATCH /api/targets/{id}", a.patchTarget)
	mux.HandleFunc("DELETE /api/targets/{id}", a.deleteTarget)
	mux.HandleFunc("POST /api/targets/order", a.orderTargets)
	mux.HandleFunc("POST /api/targets/clear", a.clearTargets)
	mux.HandleFunc("POST /api/targets/group", a.groupTargets)
	mux.HandleFunc("POST /api/groups/auto", a.autoGroupAll)
	mux.HandleFunc("PUT /api/groups", a.putGroups)
	mux.HandleFunc("GET /api/samples", a.getSamples)
	mux.HandleFunc("GET /api/events", a.getEvents)
	mux.HandleFunc("POST /api/rules", a.saveRule)
	mux.HandleFunc("PUT /api/rules/{id}", a.saveRule)
	mux.HandleFunc("DELETE /api/rules/{id}", a.deleteRule)
	mux.HandleFunc("POST /api/rules/{id}/test", a.testRule)
	mux.HandleFunc("POST /api/channels", a.saveChannel)
	mux.HandleFunc("PUT /api/channels/{id}", a.saveChannel)
	mux.HandleFunc("DELETE /api/channels/{id}", a.deleteChannel)
	mux.HandleFunc("POST /api/channels/test", a.testChannel)
	mux.HandleFunc("POST /api/comments", a.addComment)
	mux.HandleFunc("DELETE /api/comments/{id}", a.deleteComment)
	mux.HandleFunc("GET /api/discover", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.disc.State()) })
	mux.HandleFunc("POST /api/discover", a.startDiscover)
	mux.HandleFunc("POST /api/discover/stop", func(w http.ResponseWriter, r *http.Request) { a.disc.Stop(); writeJSON(w, 200, a.disc.State()) })
	mux.HandleFunc("GET /api/export/targets", a.exportTargets)
	mux.HandleFunc("GET /api/export/csv", a.exportCSV)
	mux.HandleFunc("GET /api/export/config", a.exportConfig)
	mux.HandleFunc("GET /api/app", a.getApp)
	mux.HandleFunc("PUT /api/app", a.putApp)
	mux.HandleFunc("POST /api/app/open", a.openAppFile)
	mux.HandleFunc("POST /api/app/move", a.moveData)
	mux.Handle("GET /api/stream", a.hub)
	mux.Handle("GET /", static)
	return mux
}

func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var patch map[string]json.RawMessage
	if err := readJSON(r, &patch); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var old, now Settings
	err := a.cfg.update(func(c *Config) error {
		cur, _ := json.Marshal(c.Settings)
		var m map[string]json.RawMessage
		json.Unmarshal(cur, &m)
		for k, v := range patch {
			m[k] = v
		}
		merged, _ := json.Marshal(m)
		var s Settings
		if err := json.Unmarshal(merged, &s); err != nil {
			return fmt.Errorf("invalid setting: %v", err)
		}
		s.normalize()
		old, c.Settings, now = c.Settings, s, s
		return nil
	})
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.settingsChanged(old, now)
	writeJSON(w, 200, now)
}

// settingsChanged does what a change from old to now needs and tells every
// page.
func (a *App) settingsChanged(old, now Settings) {
	if old.MemorySamples != now.MemorySamples {
		a.store.SetMemory(now.MemorySamples)
	}
	if old.IntervalMs != now.IntervalMs || old.StaggerMs != now.StaggerMs || (!old.Running && now.Running) {
		a.engine.Sync(true)
	}
	if old.Running && !now.Running {
		a.alerts.ResetAll() // without samples, firing alerts could never end
	}
	if old.CheckUpdates != now.CheckUpdates {
		if now.CheckUpdates {
			select {
			case a.updateNow <- struct{}{}:
			default:
			}
		} else {
			a.update.Store(nil)
			a.hub.Broadcast("info", a.info())
		}
	}
	if old.RawDays != now.RawDays || old.SummaryDays != now.SummaryDays {
		go a.store.Cleanup(now.RawDays, now.SummaryDays)
	}
	a.changed()
	if old.Running != now.Running {
		go a.pushStatus() // not on the caller's goroutine, which may be the tray's
	}
}

// setPaused stops or restarts pinging, like the Start/Stop button. The
// tray menu uses it.
func (a *App) setPaused(paused bool) error {
	var old, now Settings
	a.cfg.update(func(c *Config) error {
		old = c.Settings
		c.Settings.Running = !paused
		now = c.Settings
		return nil
	})
	a.settingsChanged(old, now)
	return nil
}

func (a *App) addTargets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text    string      `json:"text"`
		Group   string      `json:"group"`
		Targets []NewTarget `json:"targets"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	list, bad := parseTargets(req.Text)
	list = append(list, req.Targets...)
	if len(list) == 0 {
		msg := "enter an IP address or a name, like 192.168.1.1 or google.com"
		if len(bad) > 0 {
			msg = fmt.Sprintf("“%s” is not an IP address or a host name", bad[0])
		}
		fail(w, 400, msg)
		return
	}
	s := a.cfg.settings()
	var added []string
	var skipped []string
	a.cfg.update(func(c *Config) error {
		have := map[string]bool{}
		for _, t := range c.Targets {
			have[t.key()] = true
		}
		for _, nt := range list {
			host := strings.Trim(nt.Host, "[]")
			t := &Target{ID: newID(), Name: nt.Name, Host: host, Group: nt.Group, Port: clamp(nt.Port, 0, 65535), Lookup: nt.Lookup, Paused: !s.AutoStart, Added: time.Now().UnixMilli()}
			if ip, err := netip.ParseAddr(host); err == nil {
				t.IP = ip.Unmap().String()
				t.Host = t.IP
			} else {
				t.DNSName = host
			}
			if have[t.key()] {
				skipped = append(skipped, t.address(false))
				continue
			}
			have[t.key()] = true
			if t.Group == "" {
				t.Group = strings.TrimSpace(req.Group)
			}
			if s.AutoGroup {
				assignAutoGroup(c, t)
			}
			if t.Name == "" {
				t.Name = t.address(false)
			}
			if t.Group != "" && !slices.Contains(c.Groups, t.Group) {
				c.Groups = append(c.Groups, t.Group)
			}
			c.Targets = append(c.Targets, t)
			added = append(added, t.ID)
		}
		return nil
	})
	for _, id := range added {
		a.store.Ensure(id)
		a.engine.ResolveNow(id)
	}
	a.engine.Sync(false)
	a.changed()
	writeJSON(w, 200, map[string]any{"added": added, "skipped": skipped, "invalid": bad})
}

func (a *App) patchTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p struct {
		Name       *string    `json:"name"`
		Host       *string    `json:"host"`
		Group      *string    `json:"group"`
		Paused     *bool      `json:"paused"`
		Hidden     *bool      `json:"hidden"`
		IntervalMs *int       `json:"intervalMs"`
		Port       *int       `json:"port"`
		Lookup     *string    `json:"lookup"`
		Off        *[]OffTime `json:"off"`
	}
	if err := readJSON(r, &p); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if p.Host != nil && !looksLikeHost(*p.Host) {
		fail(w, 400, "that is not an IP address or a host name")
		return
	}
	if p.Lookup != nil {
		*p.Lookup = strings.TrimSuffix(strings.TrimSpace(*p.Lookup), ".")
		if *p.Lookup != "" && !validLookup(*p.Lookup) {
			fail(w, 400, "that is not a name to look up, like example.com")
			return
		}
	}
	if p.Off != nil {
		if err := checkOffTimes(*p.Off); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	rehost := false
	err := a.cfg.update(func(c *Config) error {
		for _, t := range c.Targets {
			if t.ID != id {
				continue
			}
			if p.Name != nil {
				t.Name = strings.TrimSpace(*p.Name)
				if t.Name == "" {
					t.Name = t.Host
				}
			}
			if p.Group != nil {
				t.Group, t.AutoGroup = strings.TrimSpace(*p.Group), false
				if t.Group != "" && !slices.Contains(c.Groups, t.Group) {
					c.Groups = append(c.Groups, t.Group)
				}
			}
			if p.Paused != nil {
				t.Paused = *p.Paused
			}
			if p.Hidden != nil {
				t.Hidden = *p.Hidden
			}
			if p.IntervalMs != nil {
				t.IntervalMs = 0
				if *p.IntervalMs > 0 {
					t.IntervalMs = clamp(*p.IntervalMs, 100, 3_600_000)
				}
			}
			if p.Port != nil {
				t.Port = clamp(*p.Port, 0, 65535)
			}
			if p.Lookup != nil {
				t.Lookup = *p.Lookup
			}
			if p.Off != nil {
				t.Off = slices.Clone(*p.Off)
			}
			if p.Host != nil && strings.Trim(*p.Host, "[]") != t.Host {
				t.Host = strings.Trim(strings.TrimSpace(*p.Host), "[]")
				t.IP, t.DNSName = "", ""
				if ip, err := netip.ParseAddr(t.Host); err == nil {
					t.IP = ip.Unmap().String()
				} else {
					t.DNSName = t.Host
				}
				rehost = true
			}
			return nil
		}
		return errors.New("target not found")
	})
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	if rehost {
		a.engine.ResolveNow(id)
	}
	if p.Paused != nil && *p.Paused {
		a.alerts.ResetTarget(id) // a paused target sends no samples, so its alerts could never end
	}
	a.engine.Sync(false)
	a.changed()
	t, _ := a.cfg.target(id)
	writeJSON(w, 200, a.targetDTO(t))
}

func (a *App) removeTargets(ids []string) {
	for _, id := range ids {
		a.engine.Stop(id)
	}
	a.cfg.update(func(c *Config) error {
		c.Targets = slices.DeleteFunc(c.Targets, func(t *Target) bool { return slices.Contains(ids, t.ID) })
		c.Comments = slices.DeleteFunc(c.Comments, func(cm *Comment) bool { return slices.Contains(ids, cm.Target) })
		gone := func(id string) bool { return slices.Contains(ids, id) }
		for _, rule := range c.Rules {
			rule.Scope.Targets = slices.DeleteFunc(rule.Scope.Targets, gone)
			rule.Scope.ExceptTargets = slices.DeleteFunc(rule.Scope.ExceptTargets, gone)
		}
		return nil
	})
	for _, id := range ids {
		a.store.Remove(id)
		a.alerts.ResetTarget(id)
	}
}

func (a *App) deleteTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.cfg.target(id); !ok {
		fail(w, 404, "target not found")
		return
	}
	a.removeTargets([]string{id})
	a.changed()
	writeJSON(w, 200, map[string]string{"deleted": id})
}

func (a *App) clearTargets(w http.ResponseWriter, r *http.Request) {
	var ids []string
	for _, t := range a.cfg.targets() {
		ids = append(ids, t.ID)
	}
	a.removeTargets(ids)
	a.changed()
	writeJSON(w, 200, map[string]int{"deleted": len(ids)})
}

func (a *App) orderTargets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.cfg.update(func(c *Config) error {
		pos := map[string]int{}
		for i, id := range req.IDs {
			pos[id] = i
		}
		slices.SortStableFunc(c.Targets, func(x, y *Target) int {
			px, okx := pos[x.ID]
			py, oky := pos[y.ID]
			switch {
			case okx && oky:
				return px - py
			case okx:
				return -1
			case oky:
				return 1
			}
			return 0
		})
		return nil
	})
	a.changed()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) putGroups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Groups []string `json:"groups"`
		Rename *struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"rename"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.cfg.update(func(c *Config) error {
		if req.Groups != nil {
			c.Groups = slices.DeleteFunc(slices.Clone(req.Groups), func(g string) bool { return strings.TrimSpace(g) == "" })
		}
		if rn := req.Rename; rn != nil && rn.From != "" {
			to := strings.TrimSpace(rn.To)
			for _, t := range c.Targets {
				if t.Group == rn.From {
					t.Group = to
				}
			}
			for i, g := range c.Groups {
				if g == rn.From {
					c.Groups[i] = to
				}
			}
			seen := map[string]bool{} // renaming onto an existing group merges the two
			c.Groups = slices.DeleteFunc(c.Groups, func(g string) bool {
				drop := g == "" || seen[g]
				seen[g] = true
				return drop
			})
			for _, rule := range c.Rules {
				for _, gs := range []*[]string{&rule.Scope.Groups, &rule.Scope.ExceptGroups} {
					for i, g := range *gs {
						if g == rn.From {
							(*gs)[i] = to
						}
					}
					*gs = slices.DeleteFunc(*gs, func(g string) bool { return g == "" }) // ungrouped
				}
			}
		}
		return nil
	})
	a.changed()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) getSamples(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now().UnixMilli()
	to, err := strconv.ParseInt(q.Get("to"), 10, 64)
	if err != nil || to <= 0 {
		to = now
	}
	from, err := strconv.ParseInt(q.Get("from"), 10, 64)
	if err != nil || from <= 0 || from >= to {
		from = to - 5*60_000
	}
	maxPts, err := strconv.Atoi(q.Get("max"))
	if err != nil || maxPts <= 0 {
		maxPts = 1500
	}
	maxPts = min(maxPts, 20_000)
	s := a.cfg.settings()
	targets := a.cfg.targets()
	want := map[string]bool{}
	if ids := q.Get("ids"); ids != "" && ids != "all" {
		for _, id := range strings.Split(ids, ",") {
			want[id] = true
		}
	}
	out := []any{}
	for _, t := range targets {
		if len(want) > 0 && !want[t.ID] {
			continue
		}
		iv := int64(s.IntervalMs)
		if t.IntervalMs > 0 {
			iv = int64(t.IntervalMs)
		}
		out = append(out, a.store.Query(t.ID, from, to, maxPts, iv))
	}
	writeJSON(w, 200, map[string]any{"from": from, "to": to, "now": now, "series": out})
}

func (a *App) getEvents(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 200
	}
	writeJSON(w, 200, a.alerts.Events(r.URL.Query().Get("target"), min(limit, maxEvents)))
}

var condTypes = []string{"loss", "latency", "noreply", "ipchange"}
var whenTypes = []string{"start", "each", "end", "ok"}

func (a *App) saveRule(w http.ResponseWriter, r *http.Request) {
	var rule Rule
	if err := readJSON(r, &rule); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if id := r.PathValue("id"); id != "" {
		rule.ID = id
	} else {
		rule.ID = newID()
	}
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		rule.Name = "Alert"
	}
	if rule.Match != "all" {
		rule.Match = "any"
	}
	rule.Conds = slices.DeleteFunc(rule.Conds, func(c Cond) bool { return !slices.Contains(condTypes, c.Type) })
	for i := range rule.Conds {
		c := &rule.Conds[i]
		c.Samples = clamp(c.Samples, 1, 100_000)
		c.Value = max(c.Value, 0)
	}
	var chans []string
	a.cfg.read(func(c *Config) {
		for _, ch := range c.Channels {
			chans = append(chans, ch.ID)
		}
	})
	rule.Actions = slices.DeleteFunc(rule.Actions, func(act Action) bool { return !slices.Contains(chans, act.Channel) })
	for i := range rule.Actions {
		act := &rule.Actions[i]
		act.When = slices.DeleteFunc(act.When, func(s string) bool { return !slices.Contains(whenTypes, s) })
		act.RepeatMin = clamp(act.RepeatMin, 0, 100_000)
	}
	if rule.Scope.Targets == nil {
		rule.Scope.Targets = []string{}
	}
	if rule.Scope.Groups == nil {
		rule.Scope.Groups = []string{}
	}
	created := r.PathValue("id") == ""
	err := a.cfg.update(func(c *Config) error {
		for i, x := range c.Rules {
			if x.ID == rule.ID {
				c.Rules[i] = &rule
				return nil
			}
		}
		if !created {
			return errors.New("rule not found")
		}
		c.Rules = append(c.Rules, &rule)
		return nil
	})
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	a.alerts.ResetRule(rule.ID)
	a.changed()
	writeJSON(w, 200, rule)
}

func (a *App) deleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.cfg.update(func(c *Config) error {
		c.Rules = slices.DeleteFunc(c.Rules, func(x *Rule) bool { return x.ID == id })
		return nil
	})
	a.alerts.ResetRule(id)
	a.changed()
	writeJSON(w, 200, map[string]string{"deleted": id})
}

func (a *App) testRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var rule *Rule
	a.cfg.read(func(c *Config) {
		for _, x := range c.Rules {
			if x.ID == id {
				cp := *x
				rule = &cp
			}
		}
	})
	if rule == nil {
		fail(w, 404, "rule not found")
		return
	}
	a.alerts.Test(*rule)
	writeJSON(w, 202, map[string]bool{"ok": true})
}

var channelTypes = []string{"browser", "ntfy", "telegram", "discord", "pushover", "gotify", "webhook", "email", "log", "script", "windows"}

// mergeSecrets keeps stored secrets when the browser sends back the mask.
func (a *App) mergeSecrets(ch *Channel) {
	old, ok := a.cfg.channel(ch.ID)
	if !ok {
		return
	}
	for k, v := range ch.Config {
		if v == masked {
			ch.Config[k] = old.Config[k]
		}
	}
}

func (a *App) saveChannel(w http.ResponseWriter, r *http.Request) {
	var ch Channel
	if err := readJSON(r, &ch); err != nil {
		fail(w, 400, err.Error())
		return
	}
	created := r.PathValue("id") == ""
	if created {
		ch.ID = newID()
	} else {
		ch.ID = r.PathValue("id")
	}
	if !slices.Contains(channelTypes, ch.Type) {
		fail(w, 400, "unknown channel type")
		return
	}
	if ch.Config == nil {
		ch.Config = map[string]string{}
	}
	if ch.Type == "script" && !a.allowScripts {
		fail(w, 403, errScriptsOff.Error())
		return
	}
	// One from a restored backup can still be turned off.
	if ch.Type == "windows" && ch.Enabled && !a.desktop() {
		fail(w, 400, errNoDesktop.Error())
		return
	}
	if ch.Type == "log" {
		if _, err := logPath(a.dataDir, ch.Config["path"]); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	ch.Name = strings.TrimSpace(ch.Name)
	if ch.Name == "" {
		ch.Name = ch.Type
	}
	a.mergeSecrets(&ch)
	err := a.cfg.update(func(c *Config) error {
		for i, x := range c.Channels {
			if x.ID == ch.ID {
				if x.ID == "browser" {
					ch.Type = "browser"
				}
				c.Channels[i] = &ch
				return nil
			}
		}
		if !created {
			return errors.New("channel not found")
		}
		c.Channels = append(c.Channels, &ch)
		return nil
	})
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	a.changed()
	writeJSON(w, 200, map[string]string{"id": ch.ID})
}

func (a *App) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "browser" {
		fail(w, 400, "the built-in browser channel can be turned off but not deleted")
		return
	}
	a.cfg.update(func(c *Config) error {
		c.Channels = slices.DeleteFunc(c.Channels, func(x *Channel) bool { return x.ID == id })
		for _, rule := range c.Rules {
			rule.Actions = slices.DeleteFunc(rule.Actions, func(act Action) bool { return act.Channel == id })
		}
		return nil
	})
	a.changed()
	writeJSON(w, 200, map[string]string{"deleted": id})
}

func (a *App) testChannel(w http.ResponseWriter, r *http.Request) {
	var ch Channel
	if err := readJSON(r, &ch); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if ch.Config == nil {
		ch.Config = map[string]string{}
	}
	a.mergeSecrets(&ch)
	now := time.Now().UnixMilli()
	n := Notice{
		Kind: "test", Title: "OmniPing test notification",
		Body:   fmt.Sprintf("Sent from OmniPing on %s at %s. If you can read this, the channel works.", a.info().Hostname, fmtTime(now, a.cfg.settings().DateFormat)),
		Target: Target{Name: "Test", Host: "192.0.2.1", IP: "192.0.2.1"}, Rule: Rule{Name: "Test"}, Reason: "test", Time: now,
	}
	if err := a.notifier.SendNow(ch, n); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) addComment(w http.ResponseWriter, r *http.Request) {
	var cm Comment
	if err := readJSON(r, &cm); err != nil {
		fail(w, 400, err.Error())
		return
	}
	cm.Text = strings.TrimSpace(cm.Text)
	if cm.Text == "" {
		fail(w, 400, "write something first")
		return
	}
	if cm.T <= 0 {
		cm.T = time.Now().UnixMilli()
	}
	cm.ID = newID()
	a.cfg.update(func(c *Config) error {
		c.Comments = append(c.Comments, &cm)
		return nil
	})
	a.changed()
	writeJSON(w, 200, cm)
}

func (a *App) deleteComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.cfg.update(func(c *Config) error {
		c.Comments = slices.DeleteFunc(c.Comments, func(x *Comment) bool { return x.ID == id })
		return nil
	})
	a.changed()
	writeJSON(w, 200, map[string]string{"deleted": id})
}

func (a *App) startDiscover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Start     string `json:"start"`
		Count     int    `json:"count"`
		TimeoutMs int    `json:"timeoutMs"`
		Names     bool   `json:"names"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	timeout := time.Duration(clamp(req.TimeoutMs, 100, 10_000)) * time.Millisecond
	if err := a.disc.Start(req.Start, req.Count, timeout, req.Names); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, a.disc.State())
}

func (a *App) exportTargets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="omniping-targets.lst"`)
	fmt.Fprintf(w, "# OmniPing targets, %s\r\n# One per line: address, then an optional alias.\r\n", time.Now().Format("2006-01-02 15:04"))
	group := ""
	for _, t := range a.cfg.targets() {
		if t.Group != group {
			group = t.Group
			fmt.Fprintf(w, "# group: %s\r\n", group)
		}
		hp := hostPort(t.Host, t.Port)
		if t.Lookup != "" {
			hp = "dns " + hp + " " + t.Lookup
		}
		if t.Name != "" && t.Name != t.address(false) {
			fmt.Fprintf(w, "%s %s\r\n", hp, t.Name)
		} else {
			fmt.Fprintf(w, "%s\r\n", hp)
		}
	}
}

func (a *App) exportCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	if to <= 0 {
		to = time.Now().UnixMilli()
	}
	if from <= 0 || from >= to {
		from = to - 3_600_000
	}
	want := map[string]bool{}
	if ids := q.Get("ids"); ids != "" && ids != "all" {
		for _, id := range strings.Split(ids, ",") {
			want[id] = true
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="omniping-%s.csv"`, time.UnixMilli(from).Format("20060102-1504")))
	cw := csv.NewWriter(w)
	cw.Write([]string{"time", "target", "address", "rtt_ms", "lost"})
	for _, t := range a.cfg.targets() {
		if len(want) > 0 && !want[t.ID] {
			continue
		}
		res := a.store.Query(t.ID, from, to, 1<<30, 1000)
		for i, ts := range res.T {
			rtt, lost := "", "1"
			if res.R[i] >= 0 {
				rtt, lost = strconv.FormatFloat(float64(res.R[i]), 'f', 3, 32), "0"
			}
			cw.Write([]string{time.UnixMilli(ts).Format("2006-01-02 15:04:05.000"), t.Name, t.IP, rtt, lost})
		}
	}
	cw.Flush()
}

func (a *App) exportConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="omniping-config.json"`)
	a.cfg.read(func(c *Config) {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(c)
	})
}

// handler is the whole web server: the API and the page. The page has no
// login, so changes that another website tries to make through a visitor's
// browser (cross-site request forgery) are refused.
func (a *App) handler(static http.Handler) http.Handler {
	h := http.NewCrossOriginProtection().Handler(a.routes(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Windows app runs on the same computer as the browser, at a
		// known address. A web page whose own name was made to point at
		// 127.0.0.1 (DNS rebinding) could otherwise use the whole API, so
		// only answer to an IP address or this computer's own name.
		if a.desktop() && !hostAllowed(requestHost(r), a.onNetwork()) {
			http.Error(w, "OmniPing only answers to an IP address, localhost or this computer's name. To open it by another name, add that name to OMNIPING_ALLOWED_HOSTS (see the Windows page of OmniPing's documentation).", http.StatusMisdirectedRequest)
			return
		}
		if !openPath(r.URL.Path) && !a.authorized(r) {
			requireAuth(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// dnsSuffixes are the networks' names for this computer's domain, like
// fritz.box; a variable so tests can choose them.
var dnsSuffixes = localDNSSuffixes

// onNetwork tells whether other devices can reach the page, rather than
// only this computer.
func (a *App) onNetwork() bool {
	if a.web == nil {
		return false
	}
	host, _, _ := a.web.addr()
	if host == "localhost" {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err != nil || !ip.IsLoopback()
}

// hostAllowed tells whether the page's address names this computer: an IP
// address, localhost, the computer's name on its own, or a name listed in
// OMNIPING_ALLOWED_HOSTS. A website can make a name of its own, like
// mypc.example.com, point at 127.0.0.1, so the computer's name with a domain
// is refused, except when OmniPing is open to other devices (network): then
// mypc.local and the network's own suffix (like mypc.fritz.box) work too.
// The network chooses those, so on a network you don't trust, with other
// devices off, they don't count.
func hostAllowed(host string, network bool) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if slices.Contains(allowedHosts(), host) {
		return true
	}
	name, err := os.Hostname()
	if err != nil || name == "" {
		return false
	}
	first, domain, _ := strings.Cut(host, ".")
	if !strings.EqualFold(first, strings.SplitN(name, ".", 2)[0]) {
		return false
	}
	if domain == "" {
		return true
	}
	return network && (domain == "local" || slices.Contains(dnsSuffixes(), domain))
}

// allowedHosts are the extra names in OMNIPING_ALLOWED_HOSTS (separated by
// commas or spaces), lower-case: a hosts-file alias or a reverse proxy's
// name, say.
func allowedHosts() []string {
	var out []string
	for _, h := range strings.FieldsFunc(os.Getenv("OMNIPING_ALLOWED_HOSTS"), func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, strings.ToLower(strings.TrimSuffix(h, ".")))
	}
	return out
}

// importConfig restores a settings backup (from "Download settings backup"):
// targets, groups, alert rules, channels, comments and settings all come
// from the file. History on disk stays, and targets in the backup pick up
// their history again. The settings before the restore are kept in
// config.before-restore.json.
func (a *App) importConfig(w http.ResponseWriter, r *http.Request) {
	cfg := defaultConfig()
	cfg.Targets, cfg.Rules, cfg.Channels = nil, nil, nil
	if err := readJSON(r, &cfg); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if cfg.Targets == nil && cfg.Rules == nil && cfg.Channels == nil {
		fail(w, 400, "this file is not an OmniPing settings backup")
		return
	}
	cfg.Settings.normalize()
	seen := map[string]bool{}
	cfg.Targets = slices.DeleteFunc(cfg.Targets, func(t *Target) bool { return t == nil || !looksLikeHost(t.Host) })
	for _, t := range cfg.Targets {
		if t.ID == "" || seen[t.ID] || strings.ContainsAny(t.ID, `/\.`) {
			t.ID = newID()
		}
		seen[t.ID] = true
		if t.Name == "" {
			t.Name = t.Host
		}
	}
	cfg.Rules = slices.DeleteFunc(cfg.Rules, func(x *Rule) bool { return x == nil })
	cfg.Channels = slices.DeleteFunc(cfg.Channels, func(x *Channel) bool { return x == nil })
	cfg.Comments = slices.DeleteFunc(cfg.Comments, func(x *Comment) bool { return x == nil })
	if !slices.ContainsFunc(cfg.Channels, func(c *Channel) bool { return c.ID == "browser" }) {
		cfg.Channels = append([]*Channel{defaultConfig().Channels[0]}, cfg.Channels...)
	}
	for _, c := range cfg.Channels {
		if c.Config == nil {
			c.Config = map[string]string{}
		}
	}
	if cfg.Groups == nil {
		cfg.Groups = []string{}
	}
	if cfg.Comments == nil {
		cfg.Comments = []*Comment{}
	}
	if cfg.Rules == nil {
		cfg.Rules = []*Rule{}
	}

	var before []byte
	a.cfg.read(func(c *Config) { before, _ = json.MarshalIndent(c, "", "  ") })
	if err := os.WriteFile(filepath.Join(a.dataDir, "config.before-restore.json"), before, 0o644); err != nil {
		fail(w, 500, "could not keep a copy of the current settings: "+err.Error())
		return
	}
	a.cfg.replace(cfg)
	a.store.SetMemory(cfg.Settings.MemorySamples)
	for _, t := range cfg.Targets {
		a.store.Ensure(t.ID)
		if t.IP == "" {
			a.engine.ResolveNow(t.ID)
		}
	}
	a.alerts.ResetAll()
	a.engine.Sync(true)
	a.changed()
	writeJSON(w, 200, map[string]int{"targets": len(cfg.Targets), "rules": len(cfg.Rules), "channels": len(cfg.Channels)})
}

// groupTargets moves targets into a group ("" ungroups them), creating the
// group when it is new.
func (a *App) groupTargets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs   []string `json:"ids"`
		Group string   `json:"group"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	group := strings.TrimSpace(req.Group)
	moved := 0
	a.cfg.update(func(c *Config) error {
		for _, t := range c.Targets {
			if slices.Contains(req.IDs, t.ID) {
				t.Group, t.AutoGroup = group, false
				moved++
			}
		}
		if group != "" && !slices.Contains(c.Groups, group) {
			c.Groups = append(c.Groups, group)
		}
		return nil
	})
	a.changed()
	writeJSON(w, 200, map[string]any{"moved": moved, "group": group})
}

// autoGroupAll puts every target without a group in an automatic group.
func (a *App) autoGroupAll(w http.ResponseWriter, r *http.Request) {
	grouped := 0
	a.cfg.update(func(c *Config) error {
		for _, t := range c.Targets {
			if t.Group == "" {
				assignAutoGroup(c, t)
				if t.Group != "" {
					grouped++
				}
			}
		}
		return nil
	})
	a.changed()
	writeJSON(w, 200, map[string]int{"grouped": grouped})
}
