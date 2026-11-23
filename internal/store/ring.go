package store

import (
	"sort"
	"time"
)

type scopeKey struct{ profile, scope string }

// entry is one raw sample in a ring (40 bytes).
type entry struct {
	t    int64 // unix seconds
	cpu  float64
	mem  int64
	disk int64
	has  uint8 // bit 0 cpu, bit 1 mem, bit 2 disk
}

const (
	hasCPU  = 1 << 0
	hasMem  = 1 << 1
	hasDisk = 1 << 2
)

func entryOf(s Sample) entry {
	e := entry{t: s.TakenAt.Unix()}
	if s.CPUPercent != nil {
		e.cpu, e.has = *s.CPUPercent, e.has|hasCPU
	}
	if s.MemoryBytes != nil {
		e.mem, e.has = *s.MemoryBytes, e.has|hasMem
	}
	if s.DiskBytes != nil {
		e.disk, e.has = *s.DiskBytes, e.has|hasDisk
	}
	return e
}

func (e entry) wpoint() wpoint {
	p := wpoint{t: e.t, w: 1}
	if e.has&hasCPU != 0 {
		v := e.cpu
		p.cpu = &v
	}
	if e.has&hasMem != 0 {
		v := e.mem
		p.mem = &v
	}
	if e.has&hasDisk != 0 {
		v := e.disk
		p.disk = &v
	}
	return p
}

// ring holds the newest samples of one scope in time order. The backing slice
// grows lazily up to cap and then wraps, overwriting the oldest. It is not
// safe for concurrent use: the Store guards it.
type ring struct {
	cap  int
	buf  []entry
	head int // index of the oldest entry once len(buf) == cap, else 0
	// minComplete: the ring is known complete only for times >= minComplete.
	// It rises when the oldest entries are overwritten.
	minComplete int64
	lastSeen    time.Time // store clock at the last push
}

func newRing(capacity int) *ring { return &ring{cap: capacity} }

func (r *ring) len() int { return len(r.buf) }

func (r *ring) at(i int) entry { return r.buf[(r.head+i)%len(r.buf)] }

func (r *ring) markLost(t int64) {
	if t+1 > r.minComplete {
		r.minComplete = t + 1
	}
}

// push adds an entry keeping time order. The common case (newest) is O(1); an
// out-of-order entry rebuilds the ring (rare).
func (r *ring) push(e entry) {
	n := len(r.buf)
	if n == 0 || e.t >= r.at(n-1).t {
		if n < r.cap {
			r.buf = append(r.buf, e)
			return
		}
		r.markLost(r.buf[r.head].t)
		r.buf[r.head] = e
		r.head = (r.head + 1) % r.cap
		return
	}
	lin := make([]entry, 0, min(n+1, r.cap))
	for i := 0; i < n; i++ {
		lin = append(lin, r.at(i))
	}
	p := sort.Search(len(lin), func(i int) bool { return lin[i].t > e.t })
	lin = append(lin, entry{})
	copy(lin[p+1:], lin[p:])
	lin[p] = e
	for len(lin) > r.cap {
		r.markLost(lin[0].t)
		lin = lin[1:]
	}
	r.buf, r.head = lin, 0
}

// collect returns the entries with from <= t <= to as weighted points.
func (r *ring) collect(from, to int64) []wpoint {
	n := len(r.buf)
	lo := sort.Search(n, func(i int) bool { return r.at(i).t >= from })
	var out []wpoint
	for i := lo; i < n; i++ {
		e := r.at(i)
		if e.t > to {
			break
		}
		out = append(out, e.wpoint())
	}
	return out
}
