package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type minuteRow struct {
	profile, scope string
	start          int64
	cpu            *float64
	mem, disk      *int64
	samples        int64
}

func minuteRows(t *testing.T, s *Store) []minuteRow {
	t.Helper()
	rows, err := s.rdb.Query("SELECT profile, scope, bucket_start, cpu_percent, memory_bytes, disk_bytes, samples FROM metric_sample_1m ORDER BY profile, scope, bucket_start")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []minuteRow
	for rows.Next() {
		var r minuteRow
		var cpu *float64
		var mem, disk *int64
		if err := rows.Scan(&r.profile, &r.scope, &r.start, &cpu, &mem, &disk, &r.samples); err != nil {
			t.Fatal(err)
		}
		r.cpu, r.mem, r.disk = cpu, mem, disk
		out = append(out, r)
	}
	return out
}

func cleanup(t *testing.T, s *Store) {
	t.Helper()
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}

func TestCleanupFoldsAndIsIdempotent(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	base := t0.Add(-48 * time.Hour) // minute aligned
	var in []Sample
	for i := 0; i < 12; i++ { // 12 samples, 5s apart, in one minute: cpu 0..11
		in = append(in, Sample{Profile: "p", Scope: "vm", TakenAt: base.Add(time.Duration(i) * 5 * time.Second),
			CPUPercent: f64(float64(i)), MemoryBytes: i64(int64(100 + i))})
	}
	in = append(in, Sample{Profile: "p", Scope: "vm", TakenAt: base.Add(time.Minute + time.Second), CPUPercent: f64(50)})
	in = append(in, Sample{Profile: "p", Scope: "c1", TakenAt: base.Add(10 * time.Second)}) // all NULL
	mustAppend(t, s, in...)
	mustAppend(t, s, smp("p", "vm", t0.Add(-time.Hour), 7, 7)) // recent, untouched

	cleanup(t, s)
	rows := minuteRows(t, s)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	// ordered by profile, scope, start: c1, vm@base, vm@base+60
	if rows[0].scope != "c1" || rows[0].cpu != nil || rows[0].mem != nil || rows[0].disk != nil || rows[0].samples != 1 {
		t.Errorf("NULL-only bucket = %+v", rows[0])
	}
	if rows[1].start != base.Unix() || rows[1].samples != 12 || *rows[1].cpu != 5.5 || *rows[1].mem != 106 /* round(105.5) */ || rows[1].disk != nil {
		t.Errorf("vm bucket = %+v cpu=%v mem=%v", rows[1], *rows[1].cpu, *rows[1].mem)
	}
	if rows[2].start != base.Unix()+60 || rows[2].samples != 1 {
		t.Errorf("second bucket = %+v", rows[2])
	}
	if n := countRows(t, s, "metric_sample"); n != 1 {
		t.Fatalf("raw rows left = %d, want 1", n)
	}

	cleanup(t, s) // rerun is a no-op
	again := minuteRows(t, s)
	if fmt.Sprint(rows) != fmt.Sprint(again) && len(rows) != len(again) {
		t.Fatalf("second run changed rows")
	}
	for i := range rows {
		if rows[i].samples != again[i].samples || rows[i].start != again[i].start {
			t.Fatalf("row %d changed: %+v -> %+v", i, rows[i], again[i])
		}
	}
	if n := countRows(t, s, "metric_sample"); n != 1 {
		t.Fatalf("raw rows after rerun = %d", n)
	}
}

func TestCleanupFoldNeverOverwritesExistingBucket(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	base := t0.Add(-48 * time.Hour)
	mustAppend(t, s, smp("p", "vm", base, 10, 1))
	cleanup(t, s)
	// A late raw sample for the already folded minute (clock skew).
	mustAppend(t, s, smp("p", "vm", base.Add(20*time.Second), 90, 1))
	cleanup(t, s)
	rows := minuteRows(t, s)
	if len(rows) != 1 || *rows[0].cpu != 10 || rows[0].samples != 1 {
		t.Fatalf("existing bucket was modified: %+v", rows)
	}
	if n := countRows(t, s, "metric_sample"); n != 0 {
		t.Fatalf("late raw row not removed: %d", n)
	}
}

func TestRetentionBoundaryAt24Hours(t *testing.T) {
	// now is 30s past a minute, so the cutoff rounds down to the minute.
	now := t0.Add(30 * time.Second)
	clk := newClock(now)
	s := openTemp(t, clk, nil)
	cutoff := t0.Add(-RawRetention) // floor(now-24h to the minute)
	mustAppend(t, s,
		smp("p", "vm", cutoff.Add(-time.Second), 1, 1), // minute before the cutoff: folded
		smp("p", "vm", cutoff, 2, 1),                   // exactly at the cutoff: stays raw
		smp("p", "vm", cutoff.Add(25*time.Second), 3, 1),
		smp("p", "vm", now.Add(-RawRetention), 4, 1), // exactly now-24h (30s after cutoff): raw
	)
	cleanup(t, s)
	if n := countRows(t, s, "metric_sample"); n != 3 {
		t.Fatalf("raw rows = %d, want 3", n)
	}
	rows := minuteRows(t, s)
	if len(rows) != 1 || rows[0].start != cutoff.Add(-time.Minute).Unix() || rows[0].samples != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	// Nothing in the minute starting at the cutoff was half folded.
	var inMinute int
	_ = s.rdb.QueryRow("SELECT COUNT(*) FROM metric_sample_1m WHERE bucket_start >= ?", cutoff.Unix()).Scan(&inMinute)
	if inMinute != 0 {
		t.Fatal("cutoff minute was folded early")
	}
}

func TestRetentionBoundaryAt30Days(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	edge := t0.Add(-MinuteRetention).Unix() // minute aligned: t0 is
	for _, start := range []int64{edge - 120, edge - 60, edge, edge + 60} {
		if _, err := s.wdb.Exec("INSERT INTO metric_sample_1m (profile, scope, bucket_start, samples) VALUES ('p','vm',?,12)", start); err != nil {
			t.Fatal(err)
		}
	}
	cleanup(t, s)
	rows := minuteRows(t, s)
	if len(rows) != 2 || rows[0].start != edge || rows[1].start != edge+60 {
		t.Fatalf("rows = %+v (a bucket exactly 30 days old must stay)", rows)
	}
}

func TestCleanupBatching(t *testing.T) {
	oldSlice, oldChunk := foldSlice, deleteChunk
	t.Cleanup(func() { foldSlice, deleteChunk = oldSlice, oldChunk })

	build := func() *Store {
		clk := newClock(t0)
		s := openTemp(t, clk, nil)
		base := t0.Add(-60 * time.Hour)
		var in []Sample
		for i := 0; i < 3*60; i++ { // three hours, one sample a minute
			in = append(in, smp("p", "vm", base.Add(time.Duration(i)*time.Minute), float64(i), 1))
		}
		mustAppend(t, s, in...)
		return s
	}

	foldSlice = time.Hour
	s := build()
	before := s.sqlCalls.Load()
	cleanup(t, s)
	small := s.sqlCalls.Load() - before
	if got := len(minuteRows(t, s)); got != 180 {
		t.Fatalf("1h slices: %d minute rows", got)
	}

	foldSlice = 10 * time.Hour
	s2 := build()
	before = s2.sqlCalls.Load()
	cleanup(t, s2)
	big := s2.sqlCalls.Load() - before
	if got := len(minuteRows(t, s2)); got != 180 {
		t.Fatalf("10h slice: %d minute rows", got)
	}
	if small <= big {
		t.Fatalf("expected more transactions with 1h slices (%d) than one 10h slice (%d)", small, big)
	}

	// Chunked deletes: 25 old rows with chunk 10 need 3 transactions.
	deleteChunk = 10
	clk := newClock(t0)
	s3 := openTemp(t, clk, nil)
	edge := t0.Add(-MinuteRetention).Unix()
	for i := int64(1); i <= 25; i++ {
		if _, err := s3.wdb.Exec("INSERT INTO metric_sample_1m (profile, scope, bucket_start, samples) VALUES ('p','vm',?,1)", edge-60*i); err != nil {
			t.Fatal(err)
		}
	}
	before = s3.sqlCalls.Load()
	if err := s3.deleteOldMinutes(context.Background(), edge); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s3, "metric_sample_1m"); n != 0 {
		t.Fatalf("rows left = %d", n)
	}
	if calls := s3.sqlCalls.Load() - before; calls != 3 {
		t.Fatalf("delete transactions = %d, want 3", calls)
	}
}

func TestCleanupSkipsEmptyStretchesBetweenSlices(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	// Two clusters ten days apart: the loop must jump the gap, not walk it.
	a := t0.Add(-20 * 24 * time.Hour)
	b := t0.Add(-10 * 24 * time.Hour)
	mustAppend(t, s, smp("p", "vm", a, 1, 1), smp("p", "vm", b, 2, 1))
	before := s.sqlCalls.Load()
	cleanup(t, s)
	if calls := s.sqlCalls.Load() - before; calls > 20 {
		t.Fatalf("walked the gap: %d calls", calls)
	}
	if len(minuteRows(t, s)) != 2 || countRows(t, s, "metric_sample") != 0 {
		t.Fatal("clusters not folded")
	}
}

func TestActionLogCapAndOrder(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, func(o *Options) { o.ActionLogCap = 5 })
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if err := s.AppendAction(ctx, ActionEntry{Profile: "a", StartedAt: t0.Add(time.Duration(i) * time.Second),
			Action: "start", Outcome: "success", Detail: str(fmt.Sprint("n", i))}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		_ = s.AppendAction(ctx, ActionEntry{Profile: "b", StartedAt: t0.Add(time.Duration(i) * time.Second), Action: "stop", Outcome: "failed"})
	}
	cleanup(t, s)
	got, err := s.RecentActions(ctx, "a", 100)
	if err != nil || len(got) != 5 {
		t.Fatalf("a: %d %v", len(got), err)
	}
	for i, e := range got { // newest first: n7..n3
		if want := fmt.Sprint("n", 7-i); *e.Detail != want {
			t.Errorf("entry %d detail = %q want %q", i, *e.Detail, want)
		}
	}
	b, _ := s.RecentActions(ctx, "b", 100)
	if len(b) != 3 || b[0].Detail != nil || b[0].Outcome != "failed" {
		t.Fatalf("b = %+v", b)
	}
	cleanup(t, s) // idempotent
	if again, _ := s.RecentActions(ctx, "a", 100); len(again) != 5 {
		t.Fatalf("a after rerun = %d", len(again))
	}
}

func TestRecentActionsLimitClampAndValidation(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	ctx := context.Background()
	for i := 0; i < 120; i++ {
		if err := s.AppendAction(ctx, ActionEntry{Profile: "a", StartedAt: t0.Add(time.Duration(i) * time.Second), Action: "start", Outcome: "success"}); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.RecentActions(ctx, "a", 1000); len(got) != MaxRecentActions {
		t.Fatalf("len = %d", len(got))
	}
	if got, _ := s.RecentActions(ctx, "a", 0); len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got, err := s.RecentActions(ctx, "nobody", 10); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty = %v %v", got, err)
	}
	if _, err := s.RecentActions(ctx, "", 10); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("err = %v", err)
	}
	for _, e := range []ActionEntry{{Action: "a", Outcome: "b"}, {Profile: "p", Outcome: "b"}, {Profile: "p", Action: "a"}} {
		if err := s.AppendAction(ctx, e); !errors.Is(err, ErrInvalidAction) {
			t.Errorf("%+v: %v", e, err)
		}
	}
	// A zero StartedAt means now (the injected clock).
	if err := s.AppendAction(ctx, ActionEntry{Profile: "z", Action: "start", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.RecentActions(ctx, "z", 1); !got[0].StartedAt.Equal(t0) {
		t.Fatalf("StartedAt = %v", got[0].StartedAt)
	}
}

func TestCleanupCancelledLeavesConsistentData(t *testing.T) {
	oldSlice := foldSlice
	t.Cleanup(func() { foldSlice = oldSlice })
	foldSlice = time.Minute

	clk := newClock(t0)
	s := openTemp(t, clk, func(o *Options) { o.SlicePause = 5 * time.Millisecond })
	base := t0.Add(-72 * time.Hour)
	var in []Sample
	total := 600
	for i := 0; i < total; i++ { // one sample every 10s over 100 minutes
		in = append(in, smp("p", "vm", base.Add(time.Duration(i)*10*time.Second), 1, 1))
	}
	mustAppend(t, s, in...)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	err := s.Cleanup(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	check := func() {
		var folded, raw int
		_ = s.rdb.QueryRow("SELECT COALESCE(SUM(samples),0) FROM metric_sample_1m").Scan(&folded)
		raw = countRows(t, s, "metric_sample")
		if folded+raw != total {
			t.Fatalf("folded %d + raw %d != %d", folded, raw, total)
		}
	}
	check()
	if countRows(t, s, "metric_sample") == 0 || countRows(t, s, "metric_sample") == total {
		t.Log("cancel landed at an extreme; consistency still verified")
	}
	cleanup(t, s) // finishing the job later works
	check()
	if n := countRows(t, s, "metric_sample"); n != 0 {
		t.Fatalf("raw left = %d", n)
	}
}

func TestCleanupReturnsFreePages(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	base := t0.Add(-48 * time.Hour)
	var in []Sample
	for i := 0; i < 20000; i++ {
		in = append(in, smp("p", "vm", base.Add(time.Duration(i)*time.Second), 1, 1))
	}
	mustAppend(t, s, in...)
	pages := func() int {
		var n int
		_ = s.rdb.QueryRow("PRAGMA page_count").Scan(&n)
		return n
	}
	before := pages()
	cleanup(t, s)
	if after := pages(); after >= before {
		t.Fatalf("page_count %d -> %d: incremental vacuum did nothing", before, after)
	}
}

func TestCleanupDoesNotTouchRings(t *testing.T) {
	clk := newClock(t0)
	s := openTemp(t, clk, nil)
	mustAppend(t, s, smp("p", "vm", t0.Add(-time.Hour), 1, 1))
	cleanup(t, s)
	b := s.sqlCalls.Load()
	if got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-2 * time.Hour), To: t0}); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	if s.sqlCalls.Load() != b {
		t.Fatal("ring query touched SQLite after cleanup")
	}
}
