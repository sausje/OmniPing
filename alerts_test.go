package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sausje/omniping/internal/history"
)

// outageApp builds an App without pinging, with targets a, b and c in group
// Internet and d in LAN, a per-target "Packet loss" rule and a together rule
// "Internet down". Both send to a webhook that records what arrives.
func outageApp(t *testing.T) (*App, func(int) []map[string]any) {
	dir := t.TempDir()
	cfg, err := loadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(dir, "history"), 10_000)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)

	app := &App{cfg: cfg, dataDir: dir, hub: newHub(), store: store}
	app.notifier = newNotifier(app)
	app.alerts = newAlerts(app, filepath.Join(dir, "events.jsonl"))
	hook := []Action{{Channel: "hook", When: []string{"start", "end"}}}
	cfg.update(func(c *Config) error {
		c.Channels = []*Channel{{ID: "hook", Name: "hook", Type: "webhook", Enabled: true, Config: map[string]string{"url": srv.URL}}}
		c.Targets = []*Target{
			{ID: "a", Name: "A", Host: "a", Group: "Internet"},
			{ID: "b", Name: "B", Host: "b", Group: "Internet"},
			{ID: "c", Name: "C", Host: "c", Group: "Internet"},
			{ID: "d", Name: "D", Host: "d", Group: "LAN"},
		}
		c.Rules = []*Rule{
			{ID: "loss", Name: "Packet loss", Enabled: true, Match: "any", Scope: Scope{All: true}, Actions: hook,
				Conds: []Cond{{Type: "loss", On: true, Value: 20, Samples: 10}}},
			{ID: "net", Name: "Internet down", Enabled: true, Match: "any", Together: true, Scope: Scope{Groups: []string{"Internet"}},
				Actions:    []Action{{Channel: "hook", When: []string{"end"}}},
				Conds:      []Cond{{Type: "loss", On: true, Value: 100, Samples: 3}},
				MessageEnd: "The internet was down for {duration} ({since} – {until}). It's back up now."},
		}
		return nil
	})
	for _, id := range []string{"a", "b", "c", "d"} {
		store.Ensure(id)
	}
	// sent waits until n notifications arrived (and a moment for extras).
	sent := func(n int) []map[string]any {
		deadline := time.Now().Add(3 * time.Second)
		for {
			mu.Lock()
			k := len(got)
			mu.Unlock()
			if k >= n || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
	return app, sent
}

var t0 = time.Date(2026, 10, 13, 14, 0, 0, 0, time.Local).UnixMilli()

// run feeds rounds of one sample per target, one second apart; down says
// which targets lose their sample in a round.
func run(app *App, from, to int, down func(round int, id string) bool) {
	for i := from; i < to; i++ {
		now := t0 + int64(i)*1000
		for _, id := range []string{"a", "b", "c", "d"} {
			r := float32(5)
			if down(i, id) {
				r = -1
			}
			app.store.Append(id, now, r)
			app.alerts.OnSample(id, now)
		}
	}
}

func TestOutageSendsOneMessage(t *testing.T) {
	app, sent := outageApp(t)
	internet := func(id string) bool { return id != "d" }
	run(app, 0, 60, func(i int, id string) bool { return i >= 12 && i < 32 && internet(id) })

	got := sent(1)
	if len(got) != 1 {
		t.Fatalf("got %d notifications, want only the end of the outage: %v", len(got), got)
	}
	if got[0]["title"] != "Over: Internet down" || got[0]["message"] != "The internet was down for 18 s (14:00 – 14:00). It's back up now." {
		t.Errorf("unexpected notification: %v", got[0])
	}
	// The per-target alerts are still in the history, marked as held back.
	evs := app.alerts.Events("a", 10)
	if len(evs) != 2 || evs[0].Held != "Internet down" || evs[1].Held != "Internet down" {
		t.Errorf("events for a: %+v", evs)
	}
	if act := app.alerts.Active(); len(act) != 0 {
		t.Errorf("still active: %+v", act)
	}
}

func TestOneTargetDownStillAlerts(t *testing.T) {
	app, sent := outageApp(t)
	run(app, 0, 60, func(i int, id string) bool { return i >= 12 && i < 32 && id == "a" })

	got := sent(2)
	if len(got) != 2 {
		t.Fatalf("got %d notifications, want start and end for A: %v", len(got), got)
	}
	for _, m := range got {
		if m["rule"] != "Packet loss" || m["target"] != "A" {
			t.Errorf("unexpected notification: %v", m)
		}
	}
}
