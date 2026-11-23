package store

import (
	"math"
	"time"
)

// wpoint is a point with a weight: the number of raw samples it stands for
// (1 for a raw row, the folded count for a 1 minute row). t is unix seconds.
type wpoint struct {
	t    int64
	cpu  *float64
	mem  *int64
	disk *int64
	w    int64
}

// downsample returns at most maxPoints points covering [from, to] (unix
// seconds). When pts already fits it is converted unchanged. Otherwise the range
// is cut into equal-width buckets and each non-empty bucket becomes one point
// at the bucket start holding the weighted average of its members. Averages are
// NULL-aware: a field averages only over members that have a value, and stays
// nil when none do. pts must be in time order. This one function serves ring,
// raw and 1 minute sources.
func downsample(pts []wpoint, from, to int64, maxPoints int) []Point {
	out := make([]Point, 0, min(len(pts), maxPoints))
	if len(pts) <= maxPoints {
		for _, p := range pts {
			out = append(out, Point{TakenAt: time.Unix(p.t, 0).UTC(), CPUPercent: p.cpu, MemoryBytes: p.mem, DiskBytes: p.disk})
		}
		return out
	}
	span := to - from + 1
	if span < 1 {
		span = 1
	}
	width := (span + int64(maxPoints) - 1) / int64(maxPoints) // ceil, so buckets <= maxPoints
	if width < 1 {
		width = 1
	}
	var acc bucketAcc
	cur := int64(-1)
	flush := func() {
		if cur >= 0 {
			out = append(out, acc.point(from+cur*width))
		}
		acc = bucketAcc{}
	}
	for _, p := range pts {
		idx := (p.t - from) / width
		if p.t < from {
			idx = 0
		}
		if idx != cur {
			flush()
			cur = idx
		}
		acc.add(p)
	}
	flush()
	return out
}

type bucketAcc struct {
	cpuSum, memSum, diskSum float64
	cpuW, memW, diskW       int64
}

func (a *bucketAcc) add(p wpoint) {
	w := p.w
	if w < 1 {
		w = 1
	}
	if p.cpu != nil {
		a.cpuSum += *p.cpu * float64(w)
		a.cpuW += w
	}
	if p.mem != nil {
		a.memSum += float64(*p.mem) * float64(w)
		a.memW += w
	}
	if p.disk != nil {
		a.diskSum += float64(*p.disk) * float64(w)
		a.diskW += w
	}
}

func (a *bucketAcc) point(t int64) Point {
	pt := Point{TakenAt: time.Unix(t, 0).UTC()}
	if a.cpuW > 0 {
		v := a.cpuSum / float64(a.cpuW)
		pt.CPUPercent = &v
	}
	if a.memW > 0 {
		v := int64(math.Round(a.memSum / float64(a.memW)))
		pt.MemoryBytes = &v
	}
	if a.diskW > 0 {
		v := int64(math.Round(a.diskSum / float64(a.diskW)))
		pt.DiskBytes = &v
	}
	return pt
}
