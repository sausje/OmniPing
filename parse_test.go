package main

import (
	"slices"
	"testing"
)

func TestParseTargets(t *testing.T) {
	in := string(rune(0xFEFF)) + "# my list\r\n192.168.0.1 Modem\r\n\r\n[Targets]\r\nTarget1=192.168.1.20 Raspberry Pi\r\n" +
		"google.com, Google, Internet\r\nnas.lan\tNAS\tLAN\r\n[2001:db8::1] v6 host\r\nhttp://nope/\r\n"
	got, bad := parseTargets(in)
	want := []NewTarget{
		{Host: "192.168.0.1", Name: "Modem"},
		{Host: "192.168.1.20", Name: "Raspberry Pi"},
		{Host: "google.com", Name: "Google", Group: "Internet"},
		{Host: "nas.lan", Name: "NAS", Group: "LAN"},
		{Host: "[2001:db8::1]", Name: "v6 host"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d targets: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(bad) != 1 || bad[0] != "http://nope/" {
		t.Errorf("bad = %q", bad)
	}
}

func TestParsePorts(t *testing.T) {
	got, bad := parseTargets("192.168.1.10:3389 Desktop\nexample.com:443, Web, Internet\n[2001:db8::1]:22\n2001:db8::2\nhost:99999\n")
	want := []NewTarget{
		{Host: "192.168.1.10", Name: "Desktop", Port: 3389},
		{Host: "example.com", Name: "Web", Group: "Internet", Port: 443},
		{Host: "2001:db8::1", Port: 22},
		{Host: "2001:db8::2"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %+v (bad %v), want %+v", got, bad, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(bad) != 1 {
		t.Errorf("bad lines %v, want the one with port 99999", bad)
	}
	if s := hostPort("2001:db8::1", 22); s != "[2001:db8::1]:22" {
		t.Errorf("hostPort = %q", s)
	}
}

func TestParseDNSChecks(t *testing.T) {
	got, bad := parseTargets("dns 1.1.1.1 example.com\nDNS 8.8.8.8 google.com. Google DNS\ndns 9.9.9.9 quad9.net, Quad9, DNS\ndns 192.168.1.1:5353 nas.local\tLocal\nDNS 1.1.1.1\ndns 1.1.1.1 bad..name")
	want := []NewTarget{
		{Host: "1.1.1.1", Lookup: "example.com"},
		{Host: "8.8.8.8", Name: "Google DNS", Lookup: "google.com"},
		{Host: "9.9.9.9", Name: "Quad9", Group: "DNS", Lookup: "quad9.net"},
		{Host: "192.168.1.1", Port: 5353, Name: "Local", Lookup: "nas.local"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if len(bad) != 2 {
		t.Errorf("bad lines %q, want the two without a valid name", bad)
	}
}
