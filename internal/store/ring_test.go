package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRingWrapKeepsNewest(t *testing.T) {
	r := newRing(4)
	for i := int64(1); i <= 10; i++ {
		r.push(entry{t: i, cpu: float64(i), has: hasCPU})
	}
	pts := r.collect(0, 100)
	if len(pts) != 4 || pts[0].t != 7 || pts[3].t != 10 {
		t.Fatalf("%+v", pts)
	}
	if r.minComplete != 7 { // t=6 was the newest overwritten
		t.Fatalf("minComplete = %d, want 7", r.minComplete)
	}
	if got := r.collect(8, 9); len(got) != 2 || got[0].t != 8 {
		t.Fatalf("range: %+v", got)
	}
}

func TestRingOutOfOrderInsert(t *testing.T) {
	r := newRing(4)
	for _, tt := range []int64{10, 30, 40} {
		r.push(entry{t: tt})
	}
	r.push(entry{t: 20})
	var ts []int64
	for _, p := range r.collect(0, 100) {
		ts = append(ts, p.t)
	}
	if fmt.Sprint(ts) != "[10 20 30 40]" {
		t.Fatalf("%v", ts)
	}
	r.push(entry{t: 5}) // older than everything while full: dropped and marked lost
	if got := r.collect(0, 100); len(got) != 4 || got[0].t != 10 || r.minComplete != 6 {
		t.Fatalf("%+v min=%d", got, r.minComplete)
	}
	r.push(entry{t: 25}) // full, out of order: oldest evicted
	if got := r.collect(0, 100); len(got) != 4 || got[0].t != 20 || r.minComplete != 11 {
		t.Fatalf("%+v min=%d", got, r.minComplete)
	}
}

func TestInWindowQueryMakesNoSQLiteCall(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	var in []Sample
	for i := 0; i < 100; i++ {
		in = append(in, smp("p", "vm", t0.Add(-time.Duration(100-i)*5*time.Second), float64(i), 1))
	}
	mustAppend(t, s, in...)
	before := s.sqlCalls.Load()
	for i := 0; i < 20; i++ {
		got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-24 * time.Hour), To: t0, MaxPoints: 10})
		if len(got) == 0 || len(got) > 10 {
			t.Fatalf("len = %d", len(got))
		}
	}
	// Even with the reader pool closed, the ring answers.
	if err := s.rdb.Close(); err != nil {
		t.Fatal(err)
	}
	got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-time.Hour), To: t0})
	if len(got) != 100 {
		t.Fatalf("len = %d", len(got))
	}
	if after := s.sqlCalls.Load(); after != before {
		t.Fatalf("in-window queries made %d SQLite calls", after-before)
	}
	// A range reaching before the window must go to SQLite.
	s2 := openTemp(t, clk, nil)
	mustAppend(t, s2, in...)
	b2 := s2.sqlCalls.Load()
	query(t, s2, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-25 * time.Hour), To: t0})
	if s2.sqlCalls.Load() == b2 {
		t.Fatal("out-of-window query did not read SQLite")
	}
}

func TestWindowBoundaryExactly24Hours(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	mustAppend(t, s, smp("p", "vm", t0.Add(-RawRetention), 1, 1), smp("p", "vm", t0.Add(-time.Hour), 2, 1))
	b := s.sqlCalls.Load()
	got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-RawRetention), To: t0})
	if s.sqlCalls.Load() != b || len(got) != 2 {
		t.Fatalf("exactly 24h should be ring served: calls+%d len=%d", s.sqlCalls.Load()-b, len(got))
	}
	got = query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-RawRetention - time.Second), To: t0})
	if s.sqlCalls.Load() == b || len(got) != 2 {
		t.Fatalf("24h+1s should use SQLite: len=%d", len(got))
	}
}

func TestRehydrateAfterReopen(t *testing.T) {
	path := dbPath(t)
	clk := newClock(t0)
	s := openAt(t, path, clk, nil)
	mustAppend(t, s,
		smp("p", "vm", t0.Add(-2*time.Hour), 1, 1),
		smp("p", "vm", t0.Add(-time.Minute), 2, 2),
		smp("p", "gone", t0.Add(-time.Hour), 3, 3),  // idle for > 10 min: skipped
		smp("p", "vm", t0.Add(-30*time.Hour), 9, 9), // outside the window
		Sample{Profile: "p", Scope: "c", TakenAt: t0.Add(-time.Minute), CPUPercent: f64(4)})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := openAt(t, path, clk, nil)
	b := s2.sqlCalls.Load()
	got := query(t, s2, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-24 * time.Hour), To: t0})
	if len(got) != 2 || *got[0].CPUPercent != 1 || *got[1].CPUPercent != 2 {
		t.Fatalf("vm = %+v", got)
	}
	got = query(t, s2, MetricQuery{Profile: "p", Scope: "c", From: t0.Add(-24 * time.Hour), To: t0})
	if len(got) != 1 || got[0].MemoryBytes != nil {
		t.Fatalf("c = %+v", got)
	}
	if s2.sqlCalls.Load() != b {
		t.Fatal("rehydrated scopes should not hit SQLite")
	}
	s2.ringMu.RLock()
	_, hasGone := s2.rings[scopeKey{"p", "gone"}]
	n := len(s2.rings)
	s2.ringMu.RUnlock()
	if hasGone || n != 2 {
		t.Fatalf("rings = %d, gone present = %v", n, hasGone)
	}
	// The skipped scope is still answered, from disk.
	got = query(t, s2, MetricQuery{Profile: "p", Scope: "gone", From: t0.Add(-24 * time.Hour), To: t0})
	if len(got) != 1 {
		t.Fatalf("gone = %+v", got)
	}
}

func TestRingIdleEviction(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	mustAppend(t, s, smp("p", "c1", t0, 1, 1), smp("p", "vm", t0, 1, 1))
	clk.Advance(9 * time.Minute)
	mustAppend(t, s, smp("p", "vm", clk.Now(), 2, 2)) // vm stays warm
	clk.Advance(2 * time.Minute)                      // c1 idle for 11 minutes
	mustAppend(t, s, smp("p", "vm", clk.Now(), 3, 3))
	s.ringMu.RLock()
	_, c1 := s.rings[scopeKey{"p", "c1"}]
	_, vm := s.rings[scopeKey{"p", "vm"}]
	s.ringMu.RUnlock()
	if c1 || !vm {
		t.Fatalf("c1 present=%v vm present=%v", c1, vm)
	}
	// Evicted scope still readable (disk), and re-creating it restores a ring
	// that contains its earlier history.
	if got := query(t, s, MetricQuery{Profile: "p", Scope: "c1", From: t0.Add(-time.Hour), To: clk.Now()}); len(got) != 1 {
		t.Fatalf("evicted scope = %+v", got)
	}
	mustAppend(t, s, smp("p", "c1", clk.Now(), 5, 5))
	b := s.sqlCalls.Load()
	got := query(t, s, MetricQuery{Profile: "p", Scope: "c1", From: t0.Add(-time.Hour), To: clk.Now()})
	if len(got) != 2 || s.sqlCalls.Load() != b {
		t.Fatalf("rebuilt ring: len=%d calls+%d", len(got), s.sqlCalls.Load()-b)
	}
}

func TestRingScopeCap(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, func(o *Options) { o.MaxScopes = 3 })
	for i := 0; i < 6; i++ {
		mustAppend(t, s, smp("p", fmt.Sprintf("c%d", i), t0, float64(i), 1))
	}
	s.ringMu.RLock()
	n := len(s.rings)
	s.ringMu.RUnlock()
	if n != 3 {
		t.Fatalf("rings = %d, want 3", n)
	}
	// Scopes over the cap are served from SQLite and still correct.
	b := s.sqlCalls.Load()
	got := query(t, s, MetricQuery{Profile: "p", Scope: "c5", From: t0.Add(-time.Hour), To: t0})
	if len(got) != 1 || *got[0].CPUPercent != 5 || s.sqlCalls.Load() == b {
		t.Fatalf("capped scope: %+v calls+%d", got, s.sqlCalls.Load()-b)
	}
}

func TestRingWrapFallsBackToDiskForTheLostPart(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, func(o *Options) { o.RingSlots = 5 })
	var in []Sample
	for i := 0; i < 12; i++ {
		in = append(in, smp("p", "vm", t0.Add(-time.Duration(12-i)*time.Minute), float64(i), 1))
	}
	for _, sm := range in { // one at a time so the ring wraps
		mustAppend(t, s, sm)
	}
	// The ring holds only the newest 5, but a query for 12 minutes must be complete.
	got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-12 * time.Minute), To: t0})
	if len(got) != 12 {
		t.Fatalf("len = %d, want 12", len(got))
	}
	// A query inside the ring's complete part is still memory only.
	b := s.sqlCalls.Load()
	got = query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-4 * time.Minute), To: t0})
	if len(got) != 4 || s.sqlCalls.Load() != b {
		t.Fatalf("len=%d calls+%d", len(got), s.sqlCalls.Load()-b)
	}
}

func TestRingFullSizeMemoryBound(t *testing.T) {
	if DefaultRingSlots != 17280 {
		t.Fatalf("DefaultRingSlots = %d", DefaultRingSlots)
	}
	r := newRing(DefaultRingSlots)
	if r.len() != 0 || cap(r.buf) != 0 {
		t.Fatal("ring must start unallocated")
	}
	_ = context.Background()
}
