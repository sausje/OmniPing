// Package history keeps ping samples: the newest ones in memory for fast
// graphs, and everything on disk in small per-day files.
//
// On disk, each target has a folder with two kinds of files per UTC day:
//
//	YYYYMMDD.raw  8 bytes per sample: uint32 ms since midnight UTC, int32 round trip in µs (-1 = lost)
//	YYYYMMDD.min  20 bytes per minute: uint16 minute of day, uint16 samples, uint16 lost, uint16 unused,
//	              float32 min, avg, max in ms
//
// Writes are batched (see Flush) so an SD card sees one small append per file
// every few seconds instead of one write per ping.
package history

import (
	"cmp"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Lost marks a sample without a reply.
const Lost float32 = -1

// Sample is one ping result. R is the round trip in milliseconds, or Lost.
type Sample struct {
	T int64   `json:"t"` // unix milliseconds
	R float32 `json:"r"`
}

const (
	rawSize = 8
	minSize = 20
	dayMs   = 86_400_000
	minMs   = 60_000
)

type minuteAcc struct {
	minute   int64 // unix minutes
	n, lost  int
	min, max float32
	sum      float64
}

func (a *minuteAcc) add(minute int64, r float32) {
	if a.n == 0 {
		*a = minuteAcc{minute: minute, min: float32(math.Inf(1)), max: float32(math.Inf(-1))}
	}
	a.n++
	if r < 0 {
		a.lost++
		return
	}
	a.sum += float64(r)
	a.min = min(a.min, r)
	a.max = max(a.max, r)
}

func (a *minuteAcc) rec() minuteRec {
	m := minuteRec{Minute: a.minute, N: a.n, Lost: a.lost, Min: -1, Avg: -1, Max: -1}
	if got := a.n - a.lost; got > 0 {
		m.Min, m.Max, m.Avg = a.min, a.max, float32(a.sum/float64(got))
	}
	return m
}

type minuteRec struct {
	Minute        int64
	N, Lost       int
	Min, Avg, Max float32
}

type series struct {
	mu        sync.Mutex
	dir       string
	mem       *ring
	memAll    bool // memory holds every sample this target ever had
	pending   []Sample
	acc       minuteAcc
	pendMin   []minuteRec
	lastReply int64
	firstSeen int64
	count     int64
}

// Store holds the history of all targets.
type Store struct {
	dir    string
	mu     sync.RWMutex
	series map[string]*series
	memCap int
}

// Open prepares a store rooted at dir. memCap is the number of samples per
// target kept in memory.
func Open(dir string, memCap int) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, series: map[string]*series{}, memCap: max(memCap, 16)}, nil
}

func (st *Store) get(id string) *series {
	st.mu.RLock()
	s := st.series[id]
	st.mu.RUnlock()
	return s
}

// Ensure loads (or creates) the history of a target. Recent samples from disk
// are read back into memory so graphs survive a restart.
func (st *Store) Ensure(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.series[id]; ok {
		return
	}
	s := &series{dir: filepath.Join(st.dir, id), mem: newRing(st.memCap)}
	s.load()
	st.series[id] = s
}

func (s *series) load() {
	names, _ := filepath.Glob(filepath.Join(s.dir, "*.raw"))
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var chunks [][]Sample
	have := 0
	s.memAll = true
	for i, name := range names {
		info, err := os.Stat(name)
		if err != nil {
			continue
		}
		s.count += info.Size() / rawSize
		if have >= s.mem.capacity() || i >= 14 {
			s.memAll = false
			continue
		}
		smp, err := readRaw(name, math.MinInt64, math.MaxInt64)
		if err != nil {
			continue
		}
		chunks = append(chunks, smp)
		have += len(smp)
	}
	var all []Sample
	for i := len(chunks) - 1; i >= 0; i-- {
		all = append(all, chunks[i]...)
	}
	if len(all) > s.mem.capacity() {
		all = all[len(all)-s.mem.capacity():]
		s.memAll = false
	}
	for _, x := range all {
		s.mem.push(x.T, x.R)
		if x.R >= 0 {
			s.lastReply = x.T
		}
	}
	if len(all) > 0 {
		s.firstSeen = all[0].T
	}
}

// Append records a sample. Times must not go backwards; if the clock does,
// the sample is nudged forward by a millisecond.
func (st *Store) Append(id string, t int64, r float32) {
	s := st.get(id)
	if s == nil {
		st.Ensure(id)
		s = st.get(id)
	}
	if r >= 0 {
		r = float32(math.Round(float64(r)*1000) / 1000)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mem.n > 0 && t <= s.mem.last() {
		t = s.mem.last() + 1
	}
	s.mem.push(t, r)
	s.pending = append(s.pending, Sample{t, r})
	if r >= 0 {
		s.lastReply = t
	}
	if s.firstSeen == 0 {
		s.firstSeen = t
	}
	m := t / minMs
	if s.acc.n > 0 && m != s.acc.minute {
		s.pendMin = append(s.pendMin, s.acc.rec())
		s.acc = minuteAcc{}
	}
	s.acc.add(m, r)
	s.count++
}

// Last returns up to n of the newest samples, oldest first.
func (st *Store) Last(id string, n int) []Sample {
	s := st.get(id)
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n = min(n, s.mem.n)
	out := make([]Sample, 0, n)
	for i := s.mem.n - n; i < s.mem.n; i++ {
		t, r := s.mem.at(i)
		out = append(out, Sample{t, r})
	}
	return out
}

// Seen returns the time of the first sample and of the last reply (0 if none).
func (st *Store) Seen(id string) (first, lastReply int64) {
	s := st.get(id)
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstSeen, s.lastReply
}

// Count returns how many samples are stored for a target.
func (st *Store) Count(id string) int64 {
	s := st.get(id)
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// Total returns the number of samples stored for all targets.
func (st *Store) Total() int64 {
	st.mu.RLock()
	defer st.mu.RUnlock()
	var n int64
	for _, s := range st.series {
		s.mu.Lock()
		n += s.count
		s.mu.Unlock()
	}
	return n
}

// SetMemory changes how many samples per target stay in memory.
func (st *Store) SetMemory(capacity int) {
	capacity = max(capacity, 16)
	st.mu.Lock()
	defer st.mu.Unlock()
	if capacity == st.memCap {
		return
	}
	st.memCap = capacity
	for _, s := range st.series {
		s.mu.Lock()
		if s.mem.n > capacity {
			s.memAll = false
		}
		s.mem = s.mem.resized(capacity)
		s.mu.Unlock()
	}
}

// Remove forgets a target and deletes its files.
func (st *Store) Remove(id string) error {
	st.mu.Lock()
	s := st.series[id]
	delete(st.series, id)
	st.mu.Unlock()
	if s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.pending, s.pendMin = nil, nil
	}
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return errors.New("invalid id")
	}
	return os.RemoveAll(filepath.Join(st.dir, id))
}

// Flush writes pending samples to disk. With final set, the unfinished
// current minute is written too (used on shutdown).
func (st *Store) Flush(final bool) error {
	st.mu.RLock()
	list := make([]*series, 0, len(st.series))
	for _, s := range st.series {
		list = append(list, s)
	}
	st.mu.RUnlock()
	var firstErr error
	for _, s := range list {
		if err := s.flush(final); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func dayName(t int64) string {
	return time.UnixMilli(t).UTC().Format("20060102")
}

func (s *series) flush(final bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if final && s.acc.n > 0 {
		s.pendMin = append(s.pendMin, s.acc.rec())
		s.acc = minuteAcc{}
	}
	if len(s.pending) == 0 && len(s.pendMin) == 0 {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	raw := map[string][]byte{}
	var order []string
	for _, x := range s.pending {
		day := dayName(x.T)
		if _, ok := raw[day]; !ok {
			order = append(order, day)
		}
		var rec [rawSize]byte
		binary.LittleEndian.PutUint32(rec[0:], uint32(x.T-x.T/dayMs*dayMs))
		us := int32(-1)
		if x.R >= 0 {
			us = int32(math.Round(float64(x.R) * 1000))
		}
		binary.LittleEndian.PutUint32(rec[4:], uint32(us))
		raw[day] = append(raw[day], rec[:]...)
	}
	for _, day := range order {
		if err := appendFile(filepath.Join(s.dir, day+".raw"), raw[day], rawSize); err != nil {
			return err
		}
	}
	s.pending = s.pending[:0]

	mins := map[string][]byte{}
	order = order[:0]
	for _, m := range s.pendMin {
		t := m.Minute * minMs
		day := dayName(t)
		if _, ok := mins[day]; !ok {
			order = append(order, day)
		}
		var rec [minSize]byte
		binary.LittleEndian.PutUint16(rec[0:], uint16((t-t/dayMs*dayMs)/minMs))
		binary.LittleEndian.PutUint16(rec[2:], uint16(min(m.N, math.MaxUint16)))
		binary.LittleEndian.PutUint16(rec[4:], uint16(min(m.Lost, math.MaxUint16)))
		binary.LittleEndian.PutUint32(rec[8:], math.Float32bits(m.Min))
		binary.LittleEndian.PutUint32(rec[12:], math.Float32bits(m.Avg))
		binary.LittleEndian.PutUint32(rec[16:], math.Float32bits(m.Max))
		mins[day] = append(mins[day], rec[:]...)
	}
	for _, day := range order {
		if err := appendFile(filepath.Join(s.dir, day+".min"), mins[day], minSize); err != nil {
			return err
		}
	}
	s.pendMin = s.pendMin[:0]
	return nil
}

// appendFile adds whole records of size rec to the end of a file. A record
// cut short by a power failure is dropped first; otherwise every record after
// it would be read shifted and come out as garbage.
func appendFile(name string, b []byte, rec int64) error {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	end, err := f.Seek(0, io.SeekEnd)
	if err == nil && end%rec != 0 {
		end -= end % rec
		if err = f.Truncate(end); err == nil {
			_, err = f.Seek(end, io.SeekStart)
		}
	}
	if err == nil {
		_, err = f.Write(b)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func dayStart(name string) (int64, bool) {
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(name), ".raw"), ".min")
	d, err := time.Parse("20060102", base)
	if err != nil {
		return 0, false
	}
	return d.UnixMilli(), true
}

func readRaw(name string, from, to int64) ([]Sample, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	base, ok := dayStart(name)
	if !ok {
		return nil, errors.New("bad file name")
	}
	out := make([]Sample, 0, len(b)/rawSize)
	sorted := true
	for i := 0; i+rawSize <= len(b); i += rawSize {
		t := base + int64(binary.LittleEndian.Uint32(b[i:]))
		if t < from || t > to {
			continue
		}
		us := int32(binary.LittleEndian.Uint32(b[i+4:]))
		r := Lost
		if us >= 0 {
			r = float32(us) / 1000
		}
		if n := len(out); n > 0 && out[n-1].T > t {
			sorted = false
		}
		out = append(out, Sample{t, r})
	}
	if !sorted {
		slices.SortStableFunc(out, func(a, b Sample) int { return cmp.Compare(a.T, b.T) })
	}
	return out, nil
}

func readMin(name string, from, to int64) ([]minuteRec, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	base, ok := dayStart(name)
	if !ok {
		return nil, errors.New("bad file name")
	}
	var out []minuteRec
	for i := 0; i+minSize <= len(b); i += minSize {
		t := base + int64(binary.LittleEndian.Uint16(b[i:]))*minMs
		if t+minMs <= from || t > to {
			continue
		}
		out = append(out, minuteRec{
			Minute: t / minMs,
			N:      int(binary.LittleEndian.Uint16(b[i+2:])),
			Lost:   int(binary.LittleEndian.Uint16(b[i+4:])),
			Min:    math.Float32frombits(binary.LittleEndian.Uint32(b[i+8:])),
			Avg:    math.Float32frombits(binary.LittleEndian.Uint32(b[i+12:])),
			Max:    math.Float32frombits(binary.LittleEndian.Uint32(b[i+16:])),
		})
	}
	return out, nil
}

// daysBetween lists UTC day names from..to inclusive.
func daysBetween(from, to int64) []string {
	var out []string
	for d := from / dayMs * dayMs; d <= to; d += dayMs {
		out = append(out, dayName(d))
	}
	return out
}

// Cleanup deletes raw files older than rawDays and minute files older than
// sumDays. Zero keeps that kind forever.
func (st *Store) Cleanup(rawDays, sumDays int) {
	now := time.Now().UTC()
	cut := func(days int) string {
		if days <= 0 {
			return ""
		}
		return now.AddDate(0, 0, -days).Format("20060102")
	}
	rawCut, minCut := cut(rawDays), cut(sumDays)
	dirs, _ := os.ReadDir(st.dir)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(st.dir, d.Name()))
		for _, f := range files {
			name := f.Name()
			day := strings.TrimSuffix(strings.TrimSuffix(name, ".raw"), ".min")
			if (strings.HasSuffix(name, ".raw") && rawCut != "" && day < rawCut) ||
				(strings.HasSuffix(name, ".min") && minCut != "" && day < minCut) {
				p := filepath.Join(st.dir, d.Name(), name)
				if info, err := os.Stat(p); err == nil && strings.HasSuffix(name, ".raw") {
					if s := st.get(d.Name()); s != nil {
						s.mu.Lock()
						s.count -= info.Size() / rawSize
						s.mu.Unlock()
					}
				}
				os.Remove(p)
			}
		}
	}
}

// DiskUsage returns the bytes used by all history files.
func (st *Store) DiskUsage() int64 {
	var n int64
	filepath.WalkDir(st.dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
