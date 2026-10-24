package main

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestScopeExceptions(t *testing.T) {
	targets := []*Target{{ID: "a", Group: "Internet"}, {ID: "pc", Group: "LAN"}, {ID: "nas", Group: "LAN"}}
	for _, c := range []struct {
		name string
		s    Scope
		want []string
	}{
		{"all", Scope{All: true}, []string{"a", "pc", "nas"}},
		{"all except a target", Scope{All: true, ExceptTargets: []string{"pc"}}, []string{"a", "nas"}},
		{"all except a group", Scope{All: true, ExceptGroups: []string{"LAN"}}, []string{"a"}},
		{"a group except a target", Scope{Groups: []string{"LAN"}, ExceptTargets: []string{"pc"}}, []string{"nas"}},
	} {
		var got []string
		for _, x := range targets {
			if c.s.covers(x) {
				got = append(got, x.ID)
			}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: covers %v, want %v", c.name, got, c.want)
		}
	}
}

func TestExceptedTargetSendsNothing(t *testing.T) {
	app, sent := outageApp(t)
	app.cfg.update(func(c *Config) error {
		c.Rules = c.Rules[:1] // only "Packet loss", for all targets
		c.Rules[0].Scope.ExceptTargets = []string{"d"}
		return nil
	})
	run(app, 0, 30, func(i int, id string) bool { return i >= 12 && id == "d" })
	if got := sent(1); len(got) != 0 {
		t.Fatalf("an excepted target sent %d notifications: %v", len(got), got)
	}
	run(app, 30, 50, func(i int, id string) bool { return id == "d" || (i >= 35 && id == "b") })
	if got := sent(1); len(got) != 1 || got[0]["target"] != "B" {
		t.Fatalf("got %v, want one alert for B", got)
	}
}

func TestExceptionsFollowGroupsAndTargets(t *testing.T) {
	app, _ := outageApp(t)
	app.engine = newEngine(app)
	app.cfg.update(func(c *Config) error {
		c.Groups = []string{"Internet", "LAN"}
		c.Rules[0].Scope.ExceptTargets = []string{"c", "d"}
		c.Rules[0].Scope.ExceptGroups = []string{"LAN"}
		return nil
	})
	w := httptest.NewRecorder()
	app.putGroups(w, httptest.NewRequest("PUT", "/api/groups", strings.NewReader(`{"groups":["Internet","LAN"],"rename":{"from":"LAN","to":"Home"}}`)))
	if w.Code != 200 {
		t.Fatalf("rename: status %d", w.Code)
	}
	app.removeTargets([]string{"c"})
	var s Scope
	app.cfg.read(func(c *Config) { s = c.Rules[0].Scope })
	if !slices.Equal(s.ExceptGroups, []string{"Home"}) || !slices.Equal(s.ExceptTargets, []string{"d"}) {
		t.Errorf("exceptions after renaming LAN and deleting c: groups %v, targets %v", s.ExceptGroups, s.ExceptTargets)
	}
}
