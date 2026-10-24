package history

// ring holds the most recent samples of one target in memory, oldest first.
type ring struct {
	t    []int64
	r    []float32
	head int // index of the oldest sample
	n    int
}

func newRing(capacity int) *ring {
	capacity = max(capacity, 16)
	return &ring{t: make([]int64, capacity), r: make([]float32, capacity)}
}

func (g *ring) capacity() int { return len(g.t) }

func (g *ring) push(t int64, r float32) {
	c := len(g.t)
	i := (g.head + g.n) % c
	g.t[i], g.r[i] = t, r
	if g.n == c {
		g.head = (g.head + 1) % c
	} else {
		g.n++
	}
}

func (g *ring) at(i int) (int64, float32) {
	j := (g.head + i) % len(g.t)
	return g.t[j], g.r[j]
}

func (g *ring) first() int64 {
	t, _ := g.at(0)
	return t
}

func (g *ring) last() int64 {
	t, _ := g.at(g.n - 1)
	return t
}

// search returns the index of the first sample at or after t.
func (g *ring) search(t int64) int {
	lo, hi := 0, g.n
	for lo < hi {
		m := (lo + hi) / 2
		if mt, _ := g.at(m); mt < t {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// resized returns a ring with a new capacity that keeps the newest samples.
func (g *ring) resized(capacity int) *ring {
	ng := newRing(capacity)
	keep := min(g.n, ng.capacity())
	for i := g.n - keep; i < g.n; i++ {
		ng.push(g.at(i))
	}
	return ng
}
