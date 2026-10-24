package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A running OmniPing notes where its page is in instance.json in the data
// folder, so a second start can open that page instead of failing on the
// port.
type instanceInfo struct {
	URL     string `json:"url"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

const instanceFile = "instance.json"

func writeInstance(dataDir string, in instanceInfo) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, instanceFile), b, 0o644)
}

func removeInstance(dataDir string) {
	os.Remove(filepath.Join(dataDir, instanceFile))
}

func readInstance(dataDir string) (instanceInfo, error) {
	var in instanceInfo
	b, err := os.ReadFile(filepath.Join(dataDir, instanceFile))
	if err != nil {
		return in, err
	}
	err = json.Unmarshal(b, &in)
	return in, err
}

// omniPingHeader marks OmniPing's answers, so a busy port can be told apart
// from another program. ownerHeader tells whose OmniPing it is: on a PC with
// several Windows accounts, another user's OmniPing is not this one.
const (
	omniPingHeader = "X-OmniPing"
	ownerHeader    = "X-OmniPing-Owner"
)

// ownerID identifies the account OmniPing runs as, without naming it.
var ownerID = sync.OnceValue(func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	h := sha256.Sum256([]byte(strings.ToLower(u.Uid)))
	return hex.EncodeToString(h[:8])
})

// probeOmniPing asks baseURL (like http://127.0.0.1:8080/) whether it is an
// OmniPing, and whose.
func probeOmniPing(baseURL string) (ok bool, owner string) {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(strings.TrimSuffix(baseURL, "/") + "/healthz")
	if err != nil {
		return false, ""
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK && resp.Header.Get(omniPingHeader) != "", resp.Header.Get(ownerHeader)
}

// isOmniPing tells whether an OmniPing answers at baseURL.
func isOmniPing(baseURL string) bool {
	ok, _ := probeOmniPing(baseURL)
	return ok
}

// isOurOmniPing tells whether an OmniPing of this user answers at baseURL.
func isOurOmniPing(baseURL string) bool {
	ok, owner := probeOmniPing(baseURL)
	return ok && owner == ownerID()
}
