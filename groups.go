package main

import (
	"net/netip"
	"slices"
	"strings"
)

// Automatic groups sort targets into the local network, public DNS servers
// and the rest of the internet, so the table reads like a map of the
// connection. They only fill in an empty group; one chosen by hand wins.

const (
	kindLocal    = "local"
	kindDNS      = "dns"
	kindInternet = "internet"
)

// autoGroups lists, per kind, the name a new group gets and the names of an
// existing group that mean the same (case doesn't matter).
var autoGroups = []struct {
	kind  string
	names []string
}{
	{kindLocal, []string{"Local network", "LAN", "Local", "Home", "Home network", "Thuis", "Network"}},
	{kindDNS, []string{"DNS servers", "DNS", "Public DNS"}},
	{kindInternet, []string{"Internet", "WAN", "External", "Online"}},
}

// publicDNS holds well-known public DNS resolvers.
var publicDNS = map[string]bool{
	"1.1.1.1": true, "1.0.0.1": true, "2606:4700:4700::1111": true, "2606:4700:4700::1001": true, // Cloudflare
	"8.8.8.8": true, "8.8.4.4": true, "2001:4860:4860::8888": true, "2001:4860:4860::8844": true, // Google
	"9.9.9.9": true, "149.112.112.112": true, "2620:fe::fe": true, "2620:fe::9": true, // Quad9
	"208.67.222.222": true, "208.67.220.220": true, // OpenDNS
	"94.140.14.14": true, "94.140.15.15": true, // AdGuard
	"76.76.2.0": true, "76.76.10.0": true, // Control D
}

// localSuffixes are name endings used only inside home and office networks.
var localSuffixes = []string{".local", ".lan", ".home", ".internal", ".home.arpa", ".localdomain", ".intranet", ".fritz.box"}

// kindOf tells what a target is from its address, or from its name while the
// address is not known yet. ok is false when that cannot be told yet.
func kindOf(host, ip string) (kind string, ok bool) {
	if a, err := netip.ParseAddr(ip); err == nil {
		a = a.Unmap()
		switch {
		case publicDNS[a.String()]:
			return kindDNS, true
		case a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast():
			return kindLocal, true
		}
		return kindInternet, true
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h != "" && !strings.Contains(h, ".") {
		return kindLocal, true // a bare name like "nas" only resolves locally
	}
	for _, s := range localSuffixes {
		if strings.HasSuffix(h, s) {
			return kindLocal, true
		}
	}
	return "", false
}

// assignAutoGroup gives t an automatic group if it has none and its kind is
// known; otherwise it marks t to be grouped once its name resolves. The
// caller holds the config lock.
func assignAutoGroup(c *Config, t *Target) {
	if t.Group != "" {
		t.AutoGroup = false
		return
	}
	kind, ok := kindOf(t.Host, t.IP)
	if !ok {
		t.AutoGroup = true
		return
	}
	t.AutoGroup = false
	t.Group = groupFor(c, kind)
}

// groupFor returns the group for a kind: an existing group with a matching
// name, or a new one placed in the order local, DNS, internet.
func groupFor(c *Config, kind string) string {
	rank := slices.IndexFunc(autoGroups, func(g struct {
		kind  string
		names []string
	}) bool {
		return g.kind == kind
	})
	have := map[string]string{} // lower-case name -> name as written
	for _, g := range c.Groups {
		have[strings.ToLower(g)] = g
	}
	for _, t := range c.Targets {
		if t.Group != "" {
			have[strings.ToLower(t.Group)] = t.Group
		}
	}
	for _, n := range autoGroups[rank].names {
		if g, ok := have[strings.ToLower(n)]; ok {
			if !slices.Contains(c.Groups, g) {
				c.Groups = append(c.Groups, g)
			}
			return g
		}
	}
	name := autoGroups[rank].names[0]
	// Put it before the first automatic group that comes after it.
	at := len(c.Groups)
	for i, g := range c.Groups {
		if r := autoRank(g); r > rank {
			at = i
			break
		}
	}
	c.Groups = slices.Insert(c.Groups, at, name)
	return name
}

func autoRank(group string) int {
	for i, g := range autoGroups {
		for _, n := range g.names {
			if strings.EqualFold(n, group) {
				return i
			}
		}
	}
	return -1
}
