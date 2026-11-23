package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAppendQueryRoundTripWithNulls(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	at := t0.Add(-time.Minute)
	mustAppend(t, s,
		Sample{Profile: "default", Scope: "vm", TakenAt: at, CPUPercent: f64(12.5), MemoryBytes: i64(1 << 30), DiskBytes: i64(5)},
		Sample{Profile: "default", Scope: "abc123", TakenAt: at, CPUPercent: f64(1)}, // container: no mem, no disk
	)
	vm := query(t, s, MetricQuery{Profile: "default", Scope: "vm", From: at.Add(-time.Second), To: t0})
	if len(vm) != 1 || *vm[0].CPUPercent != 12.5 || *vm[0].MemoryBytes != 1<<30 || *vm[0].DiskBytes != 5 || !vm[0].TakenAt.Equal(at) {
		t.Fatalf("vm = %+v", vm)
	}
	c := query(t, s, MetricQuery{Profile: "default", Scope: "abc123", From: at, To: t0})
	if len(c) != 1 || *c[0].CPUPercent != 1 || c[0].MemoryBytes != nil || c[0].DiskBytes != nil {
		t.Fatalf("container = %+v", c)
	}
	// NULLs are stored as NULL, not zero.
	var nulls int
	if err := s.rdb.QueryRow("SELECT COUNT(*) FROM metric_sample WHERE scope='abc123' AND memory_bytes IS NULL AND disk_bytes IS NULL").Scan(&nulls); err != nil || nulls != 1 {
		t.Fatalf("NULL columns: %d %v", nulls, err)
	}
	// The disk path returns the same NULLs.
	s.ringMu.Lock()
	s.rings = map[scopeKey]*ring{}
	s.ringMu.Unlock()
	c = query(t, s, MetricQuery{Profile: "default", Scope: "abc123", From: at, To: t0})
	if len(c) != 1 || c[0].MemoryBytes != nil || c[0].DiskBytes != nil {
		t.Fatalf("disk container = %+v", c)
	}
}

func TestQueryFiltersByProfileScopeAndRange(t *testing.T) {
	s := openTemp(t, newClock(t0), nil)
	for i := 0; i < 10; i++ {
		at := t0.Add(-time.Duration(10-i) * time.Minute)
		mustAppend(t, s,
			smp("a", "vm", at, float64(i), 1), smp("b", "vm", at, 100, 1), smp("a", "c1", at, 200, 1))
	}
	got := query(t, s, MetricQuery{Profile: "a", Scope: "vm", From: t0.Add(-5 * time.Minute), To: t0.Add(-2 * time.Minute)})
	if len(got) != 4 { // inclusive both ends: -5,-4,-3,-2
		t.Fatalf("len = %d", len(got))
	}
	for i, p := range got {
		if *p.CPUPercent != float64(5+i) {
			t.Errorf("point %d cpu = %v", i, *p.CPUPercent)
		}
	}
	none := query(t, s, MetricQuery{Profile: "zzz", Scope: "vm", From: t0.Add(-time.Hour), To: t0})
	if len(none) != 0 {
		t.Fatal("unknown profile returned points")
	}
}

func TestQueryDefaultsAndLimits(t *testing.T) {
	s := openTemp(t, newClock(t0), nil)
	for _, q := range []MetricQuery{
		{Scope: "vm", From: t0},
		{Profile: "p", From: t0},
		{Profile: "p", Scope: "vm"},
		{Profile: "p", Scope: "vm", From: t0, To: t0.Add(-time.Second)},
	} {
		if _, err := s.QueryMetrics(context.Background(), q); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("%+v: err = %v", q, err)
		}
	}
	// Zero To means now; MaxPoints is clamped to 2000.
	var in []Sample
	for i := 0; i < 2500; i++ {
		in = append(in, smp("p", "vm", t0.Add(-time.Duration(2500-i)*time.Second), float64(i), 1))
	}
	mustAppend(t, s, in...)
	got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-time.Hour), MaxPoints: 99999})
	if len(got) > MaxPointsLimit || len(got) < 1000 {
		t.Fatalf("len = %d", len(got))
	}
	got = query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-time.Hour), MaxPoints: 50})
	if len(got) > 50 {
		t.Fatalf("len = %d > 50", len(got))
	}
	got = query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-time.Hour), MaxPoints: -3})
	if len(got) == 0 {
		t.Fatal("negative MaxPoints should fall back to the default")
	}
}

func TestAppendValidation(t *testing.T) {
	s := openTemp(t, newClock(t0), nil)
	bad := [][]Sample{
		{{Scope: "vm", TakenAt: t0}},
		{{Profile: "p", TakenAt: t0}},
		{{Profile: "p", Scope: "vm"}},
		{smp("p", "vm", t0, 1, 1), {Profile: "p"}}, // one bad sample rejects the whole batch
	}
	for i, b := range bad {
		if err := s.AppendSamples(context.Background(), b); !errors.Is(err, ErrInvalidSample) {
			t.Errorf("case %d: err = %v", i, err)
		}
	}
	if n := countRows(t, s, "metric_sample"); n != 0 {
		t.Fatalf("rows = %d, want 0", n)
	}
	if err := s.AppendSamples(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestOldRangeReadsMinuteTableAndRaw(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	// Three days ago: two samples in one minute. Then fold them.
	old := t0.Add(-72 * time.Hour).Truncate(time.Minute)
	mustAppend(t, s, smp("p", "vm", old.Add(5*time.Second), 10, 100), smp("p", "vm", old.Add(35*time.Second), 30, 300))
	mustAppend(t, s, smp("p", "vm", t0.Add(-time.Hour), 99, 1)) // inside the window
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: old.Add(-time.Hour), To: t0, MaxPoints: 100})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if !got[0].TakenAt.Equal(old) || *got[0].CPUPercent != 20 || *got[0].MemoryBytes != 200 {
		t.Fatalf("folded point = %+v", got[0])
	}
	if *got[1].CPUPercent != 99 {
		t.Fatalf("raw point = %+v", got[1])
	}
}
