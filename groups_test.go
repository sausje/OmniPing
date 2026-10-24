package main

import (
	"slices"
	"testing"
)

func TestKindOf(t *testing.T) {
	for _, c := range []struct{ host, ip, kind string }{
		{"192.168.1.20", "192.168.1.20", kindLocal},
		{"10.0.0.1", "10.0.0.1", kindLocal},
		{"fe80::1", "fe80::1", kindLocal},
		{"8.8.8.8", "8.8.8.8", kindDNS},
		{"one.one.one.one", "1.1.1.1", kindDNS},
		{"google.com", "142.250.1.1", kindInternet},
		{"nas", "", kindLocal},
		{"printer.local", "", kindLocal},
		{"router.home.arpa", "", kindLocal},
		{"google.com", "", ""},
	} {
		got, ok := kindOf(c.host, c.ip)
		if got != c.kind || ok != (c.kind != "") {
			t.Errorf("kindOf(%q, %q) = %q, %v; want %q", c.host, c.ip, got, ok, c.kind)
		}
	}
}

func TestAutoGroups(t *testing.T) {
	c := defaultConfig()
	add := func(host, ip string) *Target {
		x := &Target{Host: host, IP: ip}
		assignAutoGroup(&c, x)
		c.Targets = append(c.Targets, x)
		return x
	}
	if g := add("8.8.8.8", "8.8.8.8").Group; g != "DNS servers" {
		t.Errorf("DNS target in %q", g)
	}
	if g := add("google.com", "142.250.1.1").Group; g != "Internet" {
		t.Errorf("internet target in %q", g)
	}
	if g := add("192.168.1.1", "192.168.1.1").Group; g != "Local network" {
		t.Errorf("local target in %q", g)
	}
	// New automatic groups keep the order local, DNS, internet.
	if want := []string{"Local network", "DNS servers", "Internet"}; !slices.Equal(c.Groups, want) {
		t.Errorf("groups %v, want %v", c.Groups, want)
	}
	// A name waits until it resolves.
	if x := add("example.com", ""); x.Group != "" || !x.AutoGroup {
		t.Errorf("unresolved name: group %q, pending %v", x.Group, x.AutoGroup)
	}
	// Existing groups with a matching name are used, whatever they are called.
	c = defaultConfig()
	c.Groups = []string{"Internet", "LAN"}
	if g := add("192.168.1.9", "192.168.1.9").Group; g != "LAN" {
		t.Errorf("local target went to %q, want the existing LAN group", g)
	}
	if len(c.Groups) != 2 {
		t.Errorf("groups %v: no new group expected", c.Groups)
	}
	// A group chosen by hand wins.
	if x := (&Target{Host: "8.8.8.8", IP: "8.8.8.8", Group: "Mine"}); func() string { assignAutoGroup(&c, x); return x.Group }() != "Mine" {
		t.Error("an own group was replaced")
	}
}
