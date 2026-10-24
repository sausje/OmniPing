package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetrics(t *testing.T) {
	app, _ := outageApp(t)
	run(app, 0, 12, func(i int, id string) bool { return id == "b" && i >= 10 })
	w := httptest.NewRecorder()
	app.serveMetrics(w, httptest.NewRequest("GET", "/metrics", nil))
	body := w.Body.String()
	for _, want := range []string{
		`omniping_up{id="a",target="A",address="",group="Internet"} 1`,
		`omniping_up{id="b",target="B",address="",group="Internet"} 0`,
		`omniping_loss_ratio{id="b",target="B",address="",group="Internet"} 0.2`,
		`omniping_rtt_milliseconds{id="a",target="A",address="",group="Internet"} 5`,
		"# TYPE omniping_jitter_milliseconds gauge",
		"omniping_alerts_firing 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
}

func TestRestoreBackup(t *testing.T) {
	app, _ := outageApp(t)
	app.engine = newEngine(app)
	backup := map[string]any{
		"targets": []map[string]any{
			{"id": "a", "name": "Router", "host": "192.168.1.1", "ip": "192.168.1.1", "group": "LAN"},
			{"name": "No ID yet", "host": "192.168.1.2"},
			{"name": "Broken", "host": "not a host!"},
		},
		"rules":    []map[string]any{{"id": "r1", "name": "Restored rule", "enabled": true}},
		"channels": []map[string]any{{"id": "log", "name": "Log", "type": "log", "enabled": true}},
		"settings": map[string]any{"intervalMs": 2000},
	}
	b, _ := json.Marshal(backup)
	w := httptest.NewRecorder()
	app.importConfig(w, httptest.NewRequest("POST", "/api/import/config", bytes.NewReader(b)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	ts := app.cfg.targets()
	if len(ts) != 2 || ts[0].ID != "a" || ts[0].Name != "Router" || ts[1].ID == "" {
		t.Fatalf("targets after restore: %+v", ts)
	}
	if s := app.cfg.settings(); s.IntervalMs != 2000 || s.TimeoutMs != 2000 {
		t.Errorf("settings after restore: interval %d, timeout %d (missing values should get defaults)", s.IntervalMs, s.TimeoutMs)
	}
	if _, ok := app.cfg.channel("browser"); !ok {
		t.Error("the built-in browser channel is missing after the restore")
	}
	before, err := os.ReadFile(filepath.Join(app.dataDir, "config.before-restore.json"))
	if err != nil || !strings.Contains(string(before), `"name": "D"`) {
		t.Errorf("the settings before the restore were not kept: %v", err)
	}

	w = httptest.NewRecorder()
	app.importConfig(w, httptest.NewRequest("POST", "/api/import/config", strings.NewReader(`{"hello": 1}`)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("a file that is not a backup: status %d, want 400", w.Code)
	}
}

// A DNS check is added from text, named after what it asks, and exported so
// that it reads back the same.
func TestDNSCheckTarget(t *testing.T) {
	app, _ := outageApp(t)
	app.engine = newEngine(app)
	code, m := call(app, "POST", "/api/targets", `{"text": "dns 1.1.1.1 example.com\ndns 1.1.1.1 example.com Again\ndns 1.1.1.1 example.org Other"}`, fromHere, "127.0.0.1:8080")
	if code != 200 {
		t.Fatalf("adding: %d %v", code, m)
	}
	var dns []Target
	for _, x := range app.cfg.targets() {
		if x.Lookup != "" {
			dns = append(dns, x)
		}
	}
	if len(dns) != 2 || dns[0].Name != "1.1.1.1 → example.com" || dns[1].Name != "Other" {
		t.Fatalf("DNS targets %+v, want example.com once and example.org", dns)
	}
	w := httptest.NewRecorder()
	app.exportTargets(w, httptest.NewRequest("GET", "/api/export/targets", nil))
	list := w.Body.String()
	for _, want := range []string{"dns 1.1.1.1 example.com\r\n", "dns 1.1.1.1 example.org Other\r\n"} {
		if !strings.Contains(list, want) {
			t.Errorf("the export lacks %q:\n%s", want, list)
		}
	}
	back, bad := parseTargets(list)
	n := 0
	for _, b := range back {
		if b.Lookup != "" {
			n++
		}
	}
	if n != 2 || len(bad) != 0 {
		t.Errorf("read back %d DNS checks, bad %q", n, bad)
	}

	code, _ = call(app, "PATCH", "/api/targets/"+dns[0].ID, `{"lookup": "not a name"}`, fromHere, "127.0.0.1:8080")
	if code != 400 {
		t.Errorf("a bad name to look up: %d, want 400", code)
	}
	code, _ = call(app, "PATCH", "/api/targets/"+dns[0].ID, `{"lookup": ""}`, fromHere, "127.0.0.1:8080")
	if x, _ := app.cfg.target(dns[0].ID); code != 200 || x.Lookup != "" {
		t.Errorf("back to a ping: %d, lookup %q", code, x.Lookup)
	}
}
