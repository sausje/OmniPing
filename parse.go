package main

import (
	"net/netip"
	"strconv"
	"strings"
	"unicode"
)

// NewTarget is one line of user input: what to ping, plus an optional alias and group.
type NewTarget struct {
	Host  string `json:"host"`
	Name  string `json:"name"`
	Group string `json:"group"`
	Port  int    `json:"port"` // TCP port to time instead of pinging
	// Lookup: a DNS check, asking the server at Host for this name.
	Lookup string `json:"lookup"`
}

// parseTargets reads what people type in the "Enter new target" box or keep in
// MultiPing list files (.lst, .mpl, .txt):
//
//	192.168.1.20
//	192.168.10.133 My Server Name     (alias after the first space)
//	google.com, Google, Internet       (CSV: host, alias, group)
//	nas.lan<TAB>NAS<TAB>LAN           (tab separated)
//	192.168.1.10:3389 Desktop         (a port: time a TCP connection instead of pinging)
//	dns 1.1.1.1 example.com Cloudflare (a DNS check: ask 1.1.1.1 for example.com)
//
// Blank lines and lines starting with #, ; or // are skipped.
func parseTargets(text string) (out []NewTarget, bad []string) {
	text = strings.TrimPrefix(text, string(rune(0xFEFF))) // byte order mark from Windows editors
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") ||
			strings.HasPrefix(line, "//") || strings.HasPrefix(line, "<") || isSection(line) {
			continue
		}
		if _, v, ok := strings.Cut(line, "="); ok { // "Target1=192.168.1.1" in ini-style files
			line = strings.TrimSpace(v)
		}
		// "dns <server> <name>", then the alias and group as usual.
		var lookup string
		if l := strings.ToLower(line); strings.HasPrefix(l, "dns ") || strings.HasPrefix(l, "dns	") {
			server, rest := cutWord(strings.TrimSpace(line[4:]))
			lookup, rest = cutWord(strings.TrimLeft(rest, " 	"))
			lookup = strings.TrimSuffix(lookup, ".")
			if server == "" || !validLookup(lookup) {
				bad = append(bad, line)
				continue
			}
			line = server + rest
		}
		var host, name, group string
		switch {
		case strings.Contains(line, "\t"):
			f := strings.Split(line, "\t")
			host, name = f[0], get(f, 1)
			group = get(f, 2)
		case strings.Contains(line, ","):
			f := strings.Split(line, ",")
			host, name, group = f[0], get(f, 1), get(f, 2)
		default:
			host, name, _ = strings.Cut(line, " ")
		}
		host, port := splitPort(strings.TrimSpace(host))
		name = strings.Trim(strings.TrimSpace(name), `"`)
		group = strings.TrimSpace(group)
		if !looksLikeHost(host) {
			bad = append(bad, line)
			continue
		}
		out = append(out, NewTarget{Host: host, Name: name, Group: group, Port: port, Lookup: lookup})
	}
	return out, bad
}

// cutWord cuts s at the first space, tab or comma; rest keeps that
// separator, so the line still reads as "host alias" or "host, alias, group".
func cutWord(s string) (word, rest string) {
	if i := strings.IndexAny(s, " 	,"); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}

// splitPort separates "host:port" and "[ipv6]:port". A bare IPv6 address
// keeps its colons.
func splitPort(s string) (string, int) {
	h, p := s, ""
	switch {
	case strings.HasPrefix(s, "["):
		if i := strings.Index(s, "]:"); i > 0 {
			h, p = s[1:i], s[i+2:]
		}
	case strings.Count(s, ":") == 1:
		h, p, _ = strings.Cut(s, ":")
	}
	if n, err := strconv.Atoi(p); err == nil && n > 0 && n < 65536 {
		return h, n
	}
	return s, 0
}

// isSection spots ini headers like "[Targets]" (but not "[2001:db8::1]").
func isSection(l string) bool {
	if !strings.HasPrefix(l, "[") || !strings.HasSuffix(l, "]") {
		return false
	}
	_, err := netip.ParseAddr(l[1 : len(l)-1])
	return err != nil
}

func get(f []string, i int) string {
	if i < len(f) {
		return strings.TrimSpace(f[i])
	}
	return ""
}

// looksLikeHost accepts IP addresses and DNS names.
func looksLikeHost(s string) bool {
	s = strings.Trim(s, "[]")
	if s == "" || len(s) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return !strings.HasPrefix(s, ".") && !strings.HasPrefix(s, "-")
}

// hostPort writes a host with its port, if it has one ("[ipv6]:port" for IPv6).
func hostPort(host string, port int) string {
	if port == 0 {
		return host
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}
