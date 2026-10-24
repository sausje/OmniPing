package main

import "testing"

func TestLogPath(t *testing.T) {
	for _, p := range []string{"", "logs/alerts.log", "logs/lan/router.log", " logs/x.txt "} {
		if _, err := logPath("data", p); err != nil {
			t.Errorf("logPath(%q) refused: %v", p, err)
		}
	}
	for _, p := range []string{"config.json", "events.jsonl", "../x.log", "logs/../config.json", "/etc/passwd", "logs", "history/x"} {
		if got, err := logPath("data", p); err == nil {
			t.Errorf("logPath(%q) = %q, want refused", p, got)
		}
	}
}
