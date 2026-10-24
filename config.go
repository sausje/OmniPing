package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Settings are the options of the page (General, Display, Packet) plus what
// a server needs (storage, DNS). They apply to every browser.
type Settings struct {
	Running   bool `json:"running"`   // global Start/Stop
	AutoStart bool `json:"autoStart"` // start pinging new targets right away
	AutoGroup bool `json:"autoGroup"` // put new targets without a group in Local network, DNS servers or Internet

	IntervalMs int `json:"intervalMs"` // time between pings to one target
	TimeoutMs  int `json:"timeoutMs"`  // a reply later than this counts as lost
	StaggerMs  int `json:"staggerMs"`  // gap between pings to different targets
	PacketSize int `json:"packetSize"` // payload bytes

	SamplesToInclude int      `json:"samplesToInclude"`
	WarnMs           int      `json:"warnMs"` // yellow from here
	CritMs           int      `json:"critMs"` // red from here
	ShowLegend       bool     `json:"showLegend"`
	DrawCurrent      bool     `json:"drawCurrent"` // × for the current sample
	DrawMinMax       bool     `json:"drawMinMax"`
	ScaleMode        string   `json:"scaleMode"` // "auto" or "fixed"
	ScaleMs          int      `json:"scaleMs"`
	LossScale        int      `json:"lossScale"`  // % at the top of the graph; 0 hides loss
	ZoneTint         bool     `json:"zoneTint"`   // tint graph backgrounds by latency zone
	GraphScale       string   `json:"graphScale"` // graph column: "log" or "linear"
	TimelineSpanSec  int      `json:"timelineSpanSec"`
	TimelineHeight   int      `json:"timelineHeight"`
	DateFormat       string   `json:"dateFormat"` // "dmy", "ymd", "mdy"
	Columns          []string `json:"columns"`    // visible table columns

	MemorySamples int  `json:"memorySamples"` // per target
	RawDays       int  `json:"rawDays"`       // keep every sample this many days (0 = forever)
	SummaryDays   int  `json:"summaryDays"`   // keep one-minute summaries this many days (0 = forever)
	FlushSec      int  `json:"flushSec"`      // write to disk every N seconds
	DNSRefreshMin int  `json:"dnsRefreshMin"` // look names up again every N minutes
	CheckUpdates  bool `json:"checkUpdates"`  // ask GitHub once a day for a newer version

	// Weekly summary: a short message on WeeklyDay (0 Sunday … 6 Saturday)
	// at WeeklyTime ("15:04", the server's time zone) to WeeklyChannels.
	Weekly         bool     `json:"weekly"`
	WeeklyDay      int      `json:"weeklyDay"`
	WeeklyTime     string   `json:"weeklyTime"`
	WeeklyChannels []string `json:"weeklyChannels"`
}

var allColumns = []string{"loss", "cur", "avg", "min", "max", "jitter", "address", "graph"}

func defaultSettings() Settings {
	return Settings{
		Running: true, AutoStart: true, AutoGroup: true,
		IntervalMs: 1000, TimeoutMs: 2000, StaggerMs: 50, PacketSize: 56,
		SamplesToInclude: 10, WarnMs: 200, CritMs: 500, ShowLegend: true,
		DrawCurrent: true, DrawMinMax: true, ScaleMode: "auto", ScaleMs: 500,
		LossScale: 30, GraphScale: "log", TimelineSpanSec: 300, TimelineHeight: 56,
		DateFormat: "dmy", Columns: slices.Clone(allColumns),
		MemorySamples: 200_000, RawDays: 90, SummaryDays: 0, FlushSec: 30, DNSRefreshMin: 10, CheckUpdates: true,
		WeeklyDay: 1, WeeklyTime: "09:00", WeeklyChannels: []string{},
	}
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

func (s *Settings) normalize() {
	d := defaultSettings()
	s.IntervalMs = clamp(s.IntervalMs, 100, 3_600_000)
	s.TimeoutMs = clamp(s.TimeoutMs, 100, 60_000)
	s.StaggerMs = clamp(s.StaggerMs, 0, 5_000)
	s.PacketSize = clamp(s.PacketSize, 0, 65_000)
	s.SamplesToInclude = clamp(s.SamplesToInclude, 1, 1_000_000)
	s.WarnMs = clamp(s.WarnMs, 1, 600_000)
	s.CritMs = clamp(s.CritMs, s.WarnMs, 600_000)
	if s.ScaleMode != "fixed" {
		s.ScaleMode = "auto"
	}
	s.ScaleMs = clamp(s.ScaleMs, 1, 600_000)
	s.LossScale = clamp(s.LossScale, 0, 100)
	if s.GraphScale != "linear" {
		s.GraphScale = "log"
	}
	s.TimelineSpanSec = clamp(s.TimelineSpanSec, 30, 90*86_400)
	s.TimelineHeight = clamp(s.TimelineHeight, 32, 400)
	if !slices.Contains([]string{"dmy", "ymd", "mdy"}, s.DateFormat) {
		s.DateFormat = d.DateFormat
	}
	cols := s.Columns[:0:0]
	for _, c := range s.Columns {
		if slices.Contains(allColumns, c) && !slices.Contains(cols, c) {
			cols = append(cols, c)
		}
	}
	if s.Columns == nil {
		cols = d.Columns
	}
	s.Columns = cols
	s.MemorySamples = clamp(s.MemorySamples, 1_000, 10_000_000)
	s.RawDays = clamp(s.RawDays, 0, 100_000)
	s.SummaryDays = clamp(s.SummaryDays, 0, 100_000)
	s.FlushSec = clamp(s.FlushSec, 1, 3_600)
	s.DNSRefreshMin = clamp(s.DNSRefreshMin, 1, 10_080)
	s.WeeklyDay = clamp(s.WeeklyDay, 0, 6)
	if _, err := time.Parse("15:04", s.WeeklyTime); err != nil {
		s.WeeklyTime = d.WeeklyTime
	}
	chans := []string{}
	for _, id := range s.WeeklyChannels {
		if id != "" && !slices.Contains(chans, id) {
			chans = append(chans, id)
		}
	}
	s.WeeklyChannels = chans
}

// Target is one host being pinged.
type Target struct {
	ID         string `json:"id"`
	Name       string `json:"name"`    // alias shown in the table
	Host       string `json:"host"`    // what the user typed: IP or DNS name
	IP         string `json:"ip"`      // address being pinged
	DNSName    string `json:"dnsName"` // reverse lookup or the typed name
	Group      string `json:"group"`
	Paused     bool   `json:"paused"`
	Hidden     bool   `json:"hidden"`         // timeline graph hidden
	IntervalMs int    `json:"intervalMs"`     // 0 = use the default
	Port       int    `json:"port,omitempty"` // 0 = ICMP ping; otherwise time a TCP connection to this port
	// Lookup makes it a DNS check: ask the DNS server at IP (on Port, or 53)
	// for this name instead of pinging it.
	Lookup string `json:"lookup,omitempty"`
	// Off lists the times it is expected to be off (offtime.go).
	Off   []OffTime `json:"off,omitempty"`
	Added int64     `json:"added"`
	// AutoGroup: put the target in an automatic group once its name resolves.
	AutoGroup bool `json:"autoGroup,omitempty"`
}

// key identifies what a target measures, to spot duplicates.
func (t *Target) key() string {
	k := strings.ToLower(t.Host) + ":" + strconv.Itoa(t.Port)
	if t.Lookup != "" {
		k += " dns " + strings.ToLower(t.Lookup)
	}
	return k
}

// address is what the target measures, for lists and labels: "1.1.1.1",
// "192.168.1.10:3389", or "1.1.1.1 → example.com" for a DNS check. With
// ip, the address being pinged (empty until resolved) rather than what was
// typed.
func (t *Target) address(ip bool) string {
	h := t.Host
	if ip {
		h = t.IP
	}
	if t.Lookup != "" {
		return hostPort(h, t.Port) + " → " + t.Lookup
	}
	return hostPort(h, t.Port)
}

// Channel is a configured way to deliver notifications.
type Channel struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config"`
}

// Rule is an alert: conditions, actions, and the targets it watches.
type Rule struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Match   string   `json:"match"` // "any" or "all"
	Conds   []Cond   `json:"conds"`
	Actions []Action `json:"actions"`
	Scope   Scope    `json:"scope"`
	// Together treats the targets in scope as one: the rule fires once when
	// all of them meet the conditions (the internet is down, say), and holds
	// back other rules' alerts for those targets meanwhile.
	Together   bool   `json:"together"`
	Message    string `json:"message"`    // optional text template
	MessageEnd string `json:"messageEnd"` // optional text template for when it ends
}

type Cond struct {
	Type    string  `json:"type"` // loss, latency, noreply, ipchange
	On      bool    `json:"on"`
	Value   float64 `json:"value"`   // % (loss), ms (latency) or seconds (noreply)
	Samples int     `json:"samples"` // window for loss and latency
}

type Action struct {
	Channel   string   `json:"channel"`
	When      []string `json:"when"`      // start, each, end, ok
	RepeatMin int      `json:"repeatMin"` // limit "each" to once per N minutes
}

// Scope picks the targets a rule watches: all of them, or chosen targets and
// groups, minus the exceptions.
type Scope struct {
	All           bool     `json:"all"`
	Targets       []string `json:"targets"`
	Groups        []string `json:"groups"`
	ExceptTargets []string `json:"exceptTargets,omitempty"`
	ExceptGroups  []string `json:"exceptGroups,omitempty"`
}

func (s Scope) covers(t *Target) bool {
	in := s.All || slices.Contains(s.Targets, t.ID) || (t.Group != "" && slices.Contains(s.Groups, t.Group))
	out := slices.Contains(s.ExceptTargets, t.ID) || (t.Group != "" && slices.Contains(s.ExceptGroups, t.Group))
	return in && !out
}

// Comment is a note pinned to a moment on the timeline.
type Comment struct {
	ID     string `json:"id"`
	Target string `json:"target"` // empty = all targets
	T      int64  `json:"t"`
	Text   string `json:"text"`
}

// Config is everything saved in config.json.
type Config struct {
	Settings Settings   `json:"settings"`
	Targets  []*Target  `json:"targets"`
	Groups   []string   `json:"groups"`
	Channels []*Channel `json:"channels"`
	Rules    []*Rule    `json:"rules"`
	Comments []*Comment `json:"comments"`
}

func defaultConfig() Config {
	return Config{
		Settings: defaultSettings(),
		Targets:  []*Target{},
		Groups:   []string{},
		Channels: []*Channel{
			{ID: "browser", Name: "Open OmniPing pages", Type: "browser", Enabled: true, Config: map[string]string{"sound": "on"}},
			{ID: "log", Name: "Alert log file", Type: "log", Enabled: true, Config: map[string]string{"path": "logs/alerts.log"}},
		},
		Rules: []*Rule{{
			ID: newID(), Name: "Packet loss", Enabled: true, Match: "any",
			Conds: []Cond{
				{Type: "loss", On: true, Value: 20, Samples: 30}, // 6 lost pings in 30: one or two drops are not an alert
				{Type: "latency", On: false, Value: 500, Samples: 10},
				{Type: "noreply", On: true, Value: 30},
				{Type: "ipchange", On: false},
			},
			Actions: []Action{
				{Channel: "browser", When: []string{"start", "end"}},
				{Channel: "log", When: []string{"start", "end"}},
			},
			Scope: Scope{All: true},
		}},
		Comments: []*Comment{},
	}
}

func newID() string {
	const abc = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

// configStore guards the config and saves it shortly after each change.
type configStore struct {
	mu     sync.RWMutex
	saveMu sync.Mutex // one save at a time (the save loop, shutdown, a restore)
	cfg    Config
	path   string
	dirty  chan struct{}
}

func loadConfig(path string) (*configStore, error) {
	cs := &configStore{path: path, dirty: make(chan struct{}, 1)}
	cfg, err := readConfig(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// A power cut can leave config.json empty or cut short; the copy
		// from the save before is in config.json.bak.
		if bak, berr := readConfig(path + ".bak"); berr == nil {
			log.Printf("%s is damaged (%v); using the backup from the save before", path, err)
			cfg, err = bak, nil
		}
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		cs.cfg = defaultConfig()
		cs.markDirty()
	case err != nil:
		return nil, err
	default:
		cs.cfg = cfg
	}
	cs.cfg.Settings.normalize()
	for _, t := range cs.cfg.Targets {
		if t.ID == "" {
			t.ID = newID()
		}
	}
	return cs, nil
}

func readConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return Config{}, errors.New("the file is empty")
	}
	cfg := defaultConfig()
	cfg.Rules, cfg.Channels = nil, nil
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cs *configStore) markDirty() {
	select {
	case cs.dirty <- struct{}{}:
	default:
	}
}

// saveLoop writes the config after changes, at most twice a second.
func (cs *configStore) saveLoop(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-cs.dirty:
			time.Sleep(500 * time.Millisecond)
			if err := cs.save(); err != nil {
				log.Printf("saving %s: %v", cs.path, err)
			}
		}
	}
}

func (cs *configStore) save() error {
	cs.mu.RLock()
	b, err := json.MarshalIndent(cs.cfg, "", "  ")
	cs.mu.RUnlock()
	if err != nil {
		return err
	}
	cs.saveMu.Lock()
	defer cs.saveMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(cs.path), 0o755); err != nil {
		return err
	}
	// Write a new file, make sure it is on the disk, keep the old one as
	// .bak, then swap the new one in: a power cut at any moment leaves a
	// complete config.json or a complete backup.
	tmp := cs.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(cs.path); err == nil && len(old) > 0 {
		os.WriteFile(cs.path+".bak", old, 0o644)
	}
	return os.Rename(tmp, cs.path)
}

// replace swaps in a whole configuration (restoring a backup).
func (cs *configStore) replace(c Config) {
	cs.mu.Lock()
	cs.cfg = c
	cs.mu.Unlock()
	cs.markDirty()
}

// read runs f with the config locked for reading.
func (cs *configStore) read(f func(c *Config)) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	f(&cs.cfg)
}

// update runs f with the config locked for writing and schedules a save.
func (cs *configStore) update(f func(c *Config) error) error {
	cs.mu.Lock()
	err := f(&cs.cfg)
	cs.mu.Unlock()
	if err == nil {
		cs.markDirty()
	}
	return err
}

func (cs *configStore) settings() Settings {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	s := cs.cfg.Settings
	s.Columns = slices.Clone(s.Columns)
	return s
}

func (cs *configStore) target(id string) (Target, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	for _, t := range cs.cfg.Targets {
		if t.ID == id {
			return *t, true
		}
	}
	return Target{}, false
}

func (cs *configStore) targets() []Target {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	out := make([]Target, len(cs.cfg.Targets))
	for i, t := range cs.cfg.Targets {
		out[i] = *t
	}
	return out
}

func (cs *configStore) channel(id string) (Channel, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	for _, c := range cs.cfg.Channels {
		if c.ID == id {
			cp := *c
			cp.Config = map[string]string{}
			for k, v := range c.Config {
				cp.Config[k] = v
			}
			return cp, true
		}
	}
	return Channel{}, false
}

// rulesFor returns copies of the enabled rules that watch a target.
func (cs *configStore) rulesFor(t *Target) []Rule {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	var out []Rule
	for _, r := range cs.cfg.Rules {
		if r.Enabled && r.Scope.covers(t) {
			out = append(out, *r)
		}
	}
	return out
}

// secretKeys are channel settings never sent back to the browser in full.
var secretKeys = []string{"password", "token", "secret"}

const masked = "••••••••"

func isSecret(key string) bool {
	k := strings.ToLower(key)
	for _, s := range secretKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}
