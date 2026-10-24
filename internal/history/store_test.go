package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRing(t *testing.T) {
	g := newRing(16)
	for i := range 40 {
		g.push(int64(i), float32(i))
	}
	if g.n != 16 || g.first() != 24 || g.last() != 39 {
		t.Fatalf("ring holds n=%d first=%d last=%d", g.n, g.first(), g.last())
	}
	if i := g.search(30); i != 6 {
		t.Fatalf("search(30) = %d, want 6", i)
	}
	g = g.resized(20)
	if g.n != 16 || g.first() != 24 {
		t.Fatalf("resize lost data: n=%d first=%d", g.n, g.first())
	}
}

func TestAppendFlushReload(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 13, 23, 58, 0, 0, time.UTC).UnixMilli()
	// 15 minutes at 1 s, crossing midnight; every 7th sample lost.
	for i := range 900 {
		r := float32(1 + i%5)
		if i%7 == 0 {
			r = Lost
		}
		st.Append("a", start+int64(i)*1000, r)
	}
	if err := st.Flush(true); err != nil {
		t.Fatal(err)
	}

	// A fresh store with a small memory must read old data back from disk.
	st2, _ := Open(dir, 50)
	st2.Ensure("a")
	if c := st2.Count("a"); c != 900 {
		t.Fatalf("count after reload = %d, want 900", c)
	}
	res := st2.Query("a", start, start+899_000, 1000, 1000)
	if res.Mode != "raw" || len(res.T) != 900 {
		t.Fatalf("raw query: mode=%s n=%d", res.Mode, len(res.T))
	}
	lost := 0
	for _, r := range res.R {
		if r < 0 {
			lost++
		}
	}
	if lost != 129 {
		t.Fatalf("lost = %d, want 129", lost)
	}

	// One-minute buckets come from the .min files.
	res = st2.Query("a", start, start+899_000, 10, 1000)
	if res.Mode != "buckets" || res.Step != 120_000 {
		t.Fatalf("bucket query: mode=%s step=%d", res.Mode, res.Step)
	}
	total := int32(0)
	for _, n := range res.N {
		total += n
	}
	if total != 900 {
		t.Fatalf("buckets hold %d samples, want 900", total)
	}
	if res.Min[0] != 1 || res.Max[0] != 5 {
		t.Fatalf("first bucket min/max = %v/%v", res.Min[0], res.Max[0])
	}
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	st, _ := Open(dir, 100)
	old := time.Now().AddDate(0, 0, -40).UnixMilli()
	st.Append("a", old, 1)
	st.Append("a", time.Now().UnixMilli(), 1)
	st.Flush(true)
	st.Cleanup(30, 0)
	st2, _ := Open(dir, 100)
	st2.Ensure("a")
	if c := st2.Count("a"); c != 1 {
		t.Fatalf("after cleanup count = %d, want 1", c)
	}
}

func TestTornRecordIsDropped(t *testing.T) {
	dir := t.TempDir()
	st, _ := Open(dir, 100)
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC).UnixMilli()
	st.Append("a", start, 1.5)
	if err := st.Flush(false); err != nil {
		t.Fatal(err)
	}
	// A power cut in the middle of the next write leaves 3 stray bytes.
	name := filepath.Join(dir, "a", "20261013.raw")
	f, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{1, 2, 3})
	f.Close()

	st.Append("a", start+1000, 2.5)
	st.Append("a", start+2000, Lost)
	if err := st.Flush(false); err != nil {
		t.Fatal(err)
	}
	got, err := readRaw(name, 0, start+10_000)
	if err != nil {
		t.Fatal(err)
	}
	want := []Sample{{start, 1.5}, {start + 1000, 2.5}, {start + 2000, Lost}}
	if len(got) != len(want) {
		t.Fatalf("read %d samples, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d = %v, want %v", i, got[i], want[i])
		}
	}
}
