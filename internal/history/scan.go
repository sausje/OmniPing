package history

import (
	"math"
	"os"
	"path/filepath"
)

// Minute is a one-minute summary: N samples starting at T, Lost of them
// lost, and the round trips of the others (-1 when all were lost).
type Minute struct {
	T             int64
	N, Lost       int
	Min, Avg, Max float32
}

// Chunk is a run of consecutive history: raw samples, or one-minute
// summaries for days whose raw samples are no longer kept.
type Chunk struct {
	Raw     []Sample
	Minutes []Minute
}

// Scan calls fn with the history of one target between from and to, oldest
// first, a day (or the part in memory) at a time, until fn returns false.
// Files are read without holding the target's lock.
func (st *Store) Scan(id string, from, to int64, fn func(Chunk) bool) {
	s := st.get(id)
	if s == nil || to < from {
		return
	}
	s.mu.Lock()
	memFrom := int64(math.MaxInt64)
	if s.mem.n > 0 {
		memFrom = s.mem.first()
	}
	memCovers := s.mem.n > 0 && (s.memAll || memFrom <= from)
	var mem []Sample
	if s.mem.n > 0 && to >= memFrom {
		mem = s.memRange(from, to)
	}
	dir := s.dir
	s.mu.Unlock()

	if !memCovers && from < memFrom {
		diskTo := min(to, memFrom-1)
		for _, day := range daysBetween(from, diskTo) {
			var c Chunk
			if smp, err := readRaw(filepath.Join(dir, day+".raw"), from, diskTo); err == nil {
				c.Raw = smp
			} else if os.IsNotExist(err) {
				recs, _ := readMin(filepath.Join(dir, day+".min"), from, diskTo)
				for _, m := range recs {
					c.Minutes = append(c.Minutes, Minute{T: m.Minute * minMs, N: m.N, Lost: m.Lost, Min: m.Min, Avg: m.Avg, Max: m.Max})
				}
			}
			if (len(c.Raw) > 0 || len(c.Minutes) > 0) && !fn(c) {
				return
			}
		}
	}
	if len(mem) > 0 {
		fn(Chunk{Raw: mem})
	}
}
