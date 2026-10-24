package history

import (
	"math"
	"os"
	"path/filepath"
)

// Result is the answer to a Query. Small ranges come back as raw samples;
// larger ones as fixed-width buckets (one per pixel, roughly).
type Result struct {
	ID   string    `json:"id"`
	Mode string    `json:"mode"` // "raw" or "buckets"
	T    []int64   `json:"t,omitempty"`
	R    []float32 `json:"r,omitempty"`
	B0   int64     `json:"b0,omitempty"`   // start of the first bucket
	Step int64     `json:"step,omitempty"` // bucket width in ms
	N    []int32   `json:"n,omitempty"`    // samples per bucket (0 = no data)
	Lost []int32   `json:"lost,omitempty"`
	Min  []float32 `json:"min,omitempty"` // -1 when every sample was lost
	Avg  []float32 `json:"avg,omitempty"`
	Max  []float32 `json:"max,omitempty"`
}

var niceSteps = []int64{
	1_000, 2_000, 5_000, 10_000, 15_000, 30_000,
	60_000, 120_000, 300_000, 600_000, 900_000, 1_800_000,
	3_600_000, 7_200_000, 10_800_000, 21_600_000, 43_200_000, 86_400_000,
}

// NiceStep rounds a bucket width up to a value that lines up with clock time,
// so buckets from different requests match.
func NiceStep(ms int64) int64 {
	for _, s := range niceSteps {
		if s >= ms {
			return s
		}
	}
	return (ms + 86_400_000 - 1) / 86_400_000 * 86_400_000
}

// Query returns the samples of one target between from and to (unix ms).
// When there are more than maxPoints samples they are grouped into buckets.
// intervalMs is used to estimate how many samples a range holds.
func (st *Store) Query(id string, from, to int64, maxPoints int, intervalMs int64) Result {
	res := Result{ID: id, Mode: "raw"}
	s := st.get(id)
	if s == nil || to < from {
		return res
	}
	maxPoints = max(maxPoints, 10)
	intervalMs = max(intervalMs, 50)

	// Copy what is in memory under the lock and read files without it, so a
	// slow SD card never holds up recording new samples.
	s.mu.Lock()
	memFrom := int64(math.MaxInt64)
	if s.mem.n > 0 {
		memFrom = s.mem.first()
	}
	memCovers := s.mem.n > 0 && (s.memAll || memFrom <= from)
	var count int64
	if memCovers {
		count = int64(s.mem.search(to+1) - s.mem.search(from))
	} else {
		count = (to - from) / intervalMs
	}
	raw := count <= int64(maxPoints)
	step := NiceStep((to - from + int64(maxPoints) - 1) / int64(maxPoints))
	minutes := !raw && !memCovers && step%minMs == 0
	var mem []Sample
	var pend []minuteRec
	if minutes {
		pend = s.unwrittenMinutes(from, to)
	} else {
		mem = s.memRange(from, to)
	}
	dir := s.dir
	s.mu.Unlock()

	if minutes {
		b0 := from / step * step
		agg := newBuckets(int((to-b0)/step + 1))
		for _, m := range minuteRange(dir, from, to, pend) {
			agg.addMinute(int((m.Minute*minMs-b0)/step), m)
		}
		res.Mode, res.B0, res.Step = "buckets", b0, step
		res.N, res.Lost, res.Min, res.Avg, res.Max = agg.finish()
		return res
	}
	samples := mem
	if !memCovers && from < memFrom {
		// Everything older than memory was written to disk long ago.
		samples = append(rawFiles(dir, from, min(to, memFrom-1)), mem...)
	}
	if raw {
		res.T, res.R = make([]int64, len(samples)), make([]float32, len(samples))
		for i, x := range samples {
			res.T[i], res.R[i] = x.T, x.R
		}
		return res
	}
	b0 := from / step * step
	agg := newBuckets(int((to-b0)/step + 1))
	for _, x := range samples {
		agg.add(int((x.T-b0)/step), x.R)
	}
	res.Mode, res.B0, res.Step = "buckets", b0, step
	res.N, res.Lost, res.Min, res.Avg, res.Max = agg.finish()
	return res
}

// memRange copies the samples in memory between from and to. The caller
// holds s.mu.
func (s *series) memRange(from, to int64) []Sample {
	var out []Sample
	for i := s.mem.search(from); i < s.mem.n; i++ {
		t, r := s.mem.at(i)
		if t > to {
			break
		}
		out = append(out, Sample{t, r})
	}
	return out
}

// unwrittenMinutes copies the one-minute summaries not on disk yet: the
// queue and the minute in progress. The caller holds s.mu.
func (s *series) unwrittenMinutes(from, to int64) []minuteRec {
	var out []minuteRec
	for _, m := range s.pendMin {
		if t := m.Minute * minMs; t+minMs > from && t <= to {
			out = append(out, m)
		}
	}
	if s.acc.n > 0 {
		if t := s.acc.minute * minMs; t+minMs > from && t <= to {
			out = append(out, s.acc.rec())
		}
	}
	return out
}

// rawFiles reads the samples between from and to from the day files in dir.
func rawFiles(dir string, from, to int64) []Sample {
	var out []Sample
	for _, day := range daysBetween(from, to) {
		if smp, err := readRaw(filepath.Join(dir, day+".raw"), from, to); err == nil {
			out = append(out, smp...)
		}
	}
	return out
}

// minuteRange reads one-minute summaries from disk and adds the unwritten
// ones. A minute written to disk after the copy was taken is in both; the
// copy wins.
func minuteRange(dir string, from, to int64, unwritten []minuteRec) []minuteRec {
	have := map[int64]bool{}
	for _, m := range unwritten {
		have[m.Minute] = true
	}
	var out []minuteRec
	for _, day := range daysBetween(from, to) {
		name := filepath.Join(dir, day+".min")
		if _, err := os.Stat(name); err != nil {
			continue
		}
		recs, err := readMin(name, from, to)
		if err != nil {
			continue
		}
		for _, m := range recs {
			if !have[m.Minute] {
				out = append(out, m)
			}
		}
	}
	return append(out, unwritten...)
}

type buckets struct {
	n, lost  []int32
	min, max []float32
	sum      []float64
	got      []int32
}

func newBuckets(nb int) *buckets {
	b := &buckets{
		n: make([]int32, nb), lost: make([]int32, nb), got: make([]int32, nb),
		min: make([]float32, nb), max: make([]float32, nb), sum: make([]float64, nb),
	}
	for i := range b.min {
		b.min[i] = float32(math.Inf(1))
		b.max[i] = float32(math.Inf(-1))
	}
	return b
}

func (b *buckets) add(i int, r float32) {
	if i < 0 || i >= len(b.n) {
		return
	}
	b.n[i]++
	if r < 0 {
		b.lost[i]++
		return
	}
	b.got[i]++
	b.sum[i] += float64(r)
	b.min[i] = min(b.min[i], r)
	b.max[i] = max(b.max[i], r)
}

func (b *buckets) addMinute(i int, m minuteRec) {
	if i < 0 || i >= len(b.n) {
		return
	}
	b.n[i] += int32(m.N)
	b.lost[i] += int32(m.Lost)
	if got := m.N - m.Lost; got > 0 && m.Avg >= 0 {
		b.got[i] += int32(got)
		b.sum[i] += float64(m.Avg) * float64(got)
		b.min[i] = min(b.min[i], m.Min)
		b.max[i] = max(b.max[i], m.Max)
	}
}

func (b *buckets) finish() (n, lost []int32, mn, avg, mx []float32) {
	avg = make([]float32, len(b.n))
	for i := range b.n {
		if b.got[i] == 0 {
			b.min[i], b.max[i], avg[i] = -1, -1, -1
			continue
		}
		avg[i] = float32(math.Round(b.sum[i]/float64(b.got[i])*1000) / 1000)
	}
	return b.n, b.lost, b.min, avg, b.max
}
