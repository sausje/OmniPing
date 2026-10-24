package main

import "testing"

func TestConsoleWanted(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--console"}, true},
		{[]string{"-console"}, true},
		{[]string{"-data", `D:\x`, "--console", "-addr", ":9593"}, true},
		{[]string{"--console=true"}, true},
		{[]string{"--console=false"}, false},
		{[]string{"--autostart"}, false},
		{[]string{"--", "--console"}, false},
		{[]string{"-test.run", "TestX"}, false},
	}
	for _, tc := range tests {
		if got := consoleWanted(tc.args); got != tc.want {
			t.Errorf("consoleWanted(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
