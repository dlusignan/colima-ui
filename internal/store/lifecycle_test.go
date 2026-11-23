package store

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func settleGoroutines(t *testing.T, baseline int) {
	t.Helper()
	var n int
	for i := 0; i < 100; i++ {
		runtime.GC()
		n = runtime.NumGoroutine()
		if n <= baseline {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	buf := make([]byte, 1<<16)
	buf = buf[:runtime.Stack(buf, true)]
	t.Fatalf("goroutines: %d after close, baseline %d\n%s", n, baseline, buf)
}

func TestCloseIdempotentAndErrClosed(t *testing.T) {
	clk := newClock(t0)
	path := dbPath(t)
	s := openAt(t, path, clk, nil)
	mustAppend(t, s, smp("p", "vm", t0, 1, 1))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
	ctx := context.Background()
	if err := s.AppendSamples(ctx, []Sample{smp("p", "vm", t0, 1, 1)}); !errors.Is(err, ErrClosed) {
		t.Errorf("AppendSamples: %v", err)
	}
	if _, err := s.QueryMetrics(ctx, MetricQuery{Profile: "p", Scope: "vm", From: t0}); !errors.Is(err, ErrClosed) {
		t.Errorf("QueryMetrics: %v", err)
	}
	if err := s.AppendAction(ctx, ActionEntry{Profile: "p", Action: "a", Outcome: "b"}); !errors.Is(err, ErrClosed) {
		t.Errorf("AppendAction: %v", err)
	}
	if _, err := s.RecentActions(ctx, "p", 1); !errors.Is(err, ErrClosed) {
		t.Errorf("RecentActions: %v", err)
	}
	if err := s.Cleanup(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("Cleanup: %v", err)
	}
	if err := s.Start(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("Start: %v", err)
	}
	// Empty batches are still refused after close.
	if err := s.AppendSamples(ctx, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("AppendSamples(nil): %v", err)
	}
}

func TestCloseTruncatesWAL(t *testing.T) {
	path := dbPath(t)
	s := openAt(t, path, newClock(t0), nil)
	var in []Sample
	for i := 0; i < 500; i++ {
		in = append(in, smp("p", "vm", t0.Add(-time.Duration(i)*time.Second), 1, 1))
	}
	mustAppend(t, s, in...)
	if st, err := os.Stat(path + "-wal"); err != nil || st.Size() == 0 {
		t.Fatalf("expected a non-empty WAL before close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path + "-wal"); err == nil && st.Size() != 0 {
		t.Fatalf("WAL has %d bytes after Close", st.Size())
	}
}

func TestNoGoroutineLeakAfterClose(t *testing.T) {
	baseline := runtime.NumGoroutine()
	clk := newClock(t0)
	s, err := Open(context.Background(), Options{Path: dbPath(t), Clock: clk.Now, SlicePause: -1,
		CleanupInitialDelay: -1, CleanupInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil { // second Start is a no-op
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		mustAppend(t, s, smp("p", "vm", t0.Add(-time.Duration(i)*time.Second), 1, 1))
		query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-time.Hour), To: t0})
		query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-72 * time.Hour), To: t0})
	}
	time.Sleep(30 * time.Millisecond)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	settleGoroutines(t, baseline)
}

func TestCleanupLoopRunsAndStops(t *testing.T) {
	clk := newClock(t0)
	var errs atomic.Int64
	s := openTemp(t, clk, func(o *Options) {
		o.CleanupInterval = 5 * time.Millisecond
		o.OnCleanupError = func(error) { errs.Add(1) }
	})
	mustAppend(t, s, smp("p", "vm", t0.Add(-48*time.Hour), 1, 1))
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for countRows(t, s, "metric_sample") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("background cleanup never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if errs.Load() != 0 {
		t.Fatalf("loop reported %d errors", errs.Load())
	}
}

func TestCleanupLoopStopsWithItsContext(t *testing.T) {
	baseline := runtime.NumGoroutine()
	clk := newClock(t0)
	s := openTemp(t, clk, func(o *Options) { o.CleanupInterval = time.Hour; o.CleanupInitialDelay = time.Hour })
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	settleGoroutines(t, baseline)
}

func TestContextCancelledCallsFailCleanly(t *testing.T) {
	s := openTemp(t, newClock(t0), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.AppendSamples(ctx, []Sample{smp("p", "vm", t0, 1, 1)}); err == nil {
		t.Fatal("expected error")
	}
	if n := countRows(t, s, "metric_sample"); n != 0 {
		t.Fatalf("rows = %d after cancelled append", n)
	}
	s.ringMu.RLock()
	defer s.ringMu.RUnlock()
	if len(s.rings) != 0 {
		t.Fatal("ring written although the append failed")
	}
}

func TestConcurrentAppendQueryCleanupClose(t *testing.T) {
	for _, mem := range []bool{false, true} {
		name := "file"
		if mem {
			name = "memory"
		}
		t.Run(name, func(t *testing.T) {
			baseline := runtime.NumGoroutine()
			clk := newClock(t0)
			path := MemoryPath
			if !mem {
				path = dbPath(t)
			}
			s, err := Open(context.Background(), Options{Path: path, Clock: clk.Now, SlicePause: -1,
				CleanupInitialDelay: -1, CleanupInterval: 2 * time.Millisecond, RingSlots: 50, MaxScopes: 5})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			ok := func(err error) bool {
				return err == nil || errors.Is(err, ErrClosed)
			}
			var wg sync.WaitGroup
			var bad atomic.Value
			fail := func(err error) {
				if !ok(err) {
					bad.CompareAndSwap(nil, err)
				}
			}
			ctx := context.Background()
			for w := 0; w < 4; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for i := 0; i < 150; i++ {
						at := t0.Add(-time.Duration(i%50*30) * time.Minute) // spans old and recent
						fail(s.AppendSamples(ctx, []Sample{
							smp("p", "vm", at, float64(i), 1), smp("p", "c"+string(rune('a'+w)), at, 1, 1)}))
						fail(s.AppendAction(ctx, ActionEntry{Profile: "p", Action: "start", Outcome: "success"}))
					}
				}(w)
			}
			for r := 0; r < 4; r++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 150; i++ {
						_, err := s.QueryMetrics(ctx, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-time.Duration(i%3) * 48 * time.Hour), To: t0, MaxPoints: 20})
						fail(err)
						_, err = s.RecentActions(ctx, "p", 5)
						fail(err)
					}
				}()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 20; i++ {
					fail(s.Cleanup(ctx))
				}
			}()
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(60 * time.Millisecond)
				fail(s.Close())
				fail(s.Close())
			}()
			wg.Wait()
			if v := bad.Load(); v != nil {
				t.Fatalf("unexpected error: %v", v)
			}
			settleGoroutines(t, baseline)
		})
	}
}

func TestConcurrentWritersKeepDataConsistent(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	ctx := context.Background()
	var wg sync.WaitGroup
	const workers, per = 6, 40
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				at := t0.Add(-time.Duration(w*per+i) * time.Second)
				if err := s.AppendSamples(ctx, []Sample{smp("p", "vm", at, 1, 1)}); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-time.Hour), To: t0, MaxPoints: 2000})
	if len(got) != workers*per {
		t.Fatalf("ring has %d points, want %d", len(got), workers*per)
	}
	for i := 1; i < len(got); i++ {
		if got[i].TakenAt.Before(got[i-1].TakenAt) {
			t.Fatal("points out of order")
		}
	}
	if n := countRows(t, s, "metric_sample"); n != workers*per {
		t.Fatalf("db has %d rows", n)
	}
}
