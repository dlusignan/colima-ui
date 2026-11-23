package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "colima-ui", "history.db")
}

func openAt(t *testing.T, path string, clk *fakeClock, mod func(*Options)) *Store {
	t.Helper()
	o := Options{Path: path, Clock: clk.Now, SlicePause: -1, CleanupInitialDelay: -1}
	if mod != nil {
		mod(&o)
	}
	s, err := Open(context.Background(), o)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func openTemp(t *testing.T, clk *fakeClock, mod func(*Options)) *Store {
	t.Helper()
	return openAt(t, dbPath(t), clk, mod)
}

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }
func str(v string) *string   { return &v }

func smp(profile, scope string, at time.Time, cpu float64, mem int64) Sample {
	return Sample{Profile: profile, Scope: scope, TakenAt: at, CPUPercent: f64(cpu), MemoryBytes: i64(mem)}
}

func mustAppend(t *testing.T, s *Store, in ...Sample) {
	t.Helper()
	if err := s.AppendSamples(context.Background(), in); err != nil {
		t.Fatalf("AppendSamples: %v", err)
	}
}

func query(t *testing.T, s *Store, q MetricQuery) []Point {
	t.Helper()
	pts, err := s.QueryMetrics(context.Background(), q)
	if err != nil {
		t.Fatalf("QueryMetrics: %v", err)
	}
	if pts == nil {
		t.Fatal("QueryMetrics returned a nil slice")
	}
	return pts
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.rdb.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
