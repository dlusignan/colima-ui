package actionlog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/operations"
	"github.com/dlusignan/colima-ui/internal/store"
)

type fakeSink struct {
	mu      sync.Mutex
	rows    []store.ActionEntry
	err     error
	gate    chan struct{} // when non-nil, AppendAction blocks until closed
	entered chan struct{}
	calls   int
}

func (f *fakeSink) AppendAction(ctx context.Context, e store.ActionEntry) error {
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, e)
	return nil
}

func (f *fakeSink) snapshot() []store.ActionEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.ActionEntry(nil), f.rows...)
}

func (f *fakeSink) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeSink) setErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newLogger(b *logBuf) *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func intp(i int) *int { return &i }

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func summary(a operations.Action, st operations.State, code *int) operations.JobSummary {
	return operations.JobSummary{
		ID: "j1", Profile: "synthetic-dev", Action: a, State: st, StartedAt: t0,
		ExitCode: code, Tail: []string{"error: open /home/user/.colima/x: denied"},
	}
}

func TestJobFinishedMapping(t *testing.T) {
	cases := []struct {
		name    string
		state   operations.State
		code    *int
		outcome string
		detail  string // "" means nil
	}{
		{"success", operations.StateSucceeded, intp(0), "success", ""},
		{"failed with code", operations.StateFailed, intp(3), "failed", "colima exited with status 3"},
		{"failed no code", operations.StateFailed, nil, "failed", "colima did not exit normally"},
		{"cancelled", operations.StateCancelled, nil, "cancelled", "cancelled before it finished"},
	}
	for _, action := range []operations.Action{operations.ActionStart, operations.ActionStop, operations.ActionRestart, operations.ActionDelete} {
		for _, c := range cases {
			t.Run(string(action)+"/"+c.name, func(t *testing.T) {
				fs := &fakeSink{}
				r := New(fs)
				r.JobFinished(summary(action, c.state, c.code))
				if err := r.Close(ctxT(t)); err != nil {
					t.Fatal(err)
				}
				rows := fs.snapshot()
				if len(rows) != 1 {
					t.Fatalf("rows = %d, want exactly 1", len(rows))
				}
				e := rows[0]
				if e.Profile != "synthetic-dev" || e.Action != string(action) || e.Outcome != c.outcome || !e.StartedAt.Equal(t0) {
					t.Fatalf("row = %+v", e)
				}
				if c.detail == "" {
					if e.Detail != nil {
						t.Fatalf("detail = %q, want nil", *e.Detail)
					}
					return
				}
				if e.Detail == nil || *e.Detail != c.detail {
					t.Fatalf("detail = %v, want %q", e.Detail, c.detail)
				}
				if strings.Contains(*e.Detail, "/") {
					t.Fatalf("detail leaks a path: %q", *e.Detail)
				}
			})
		}
	}
}

func TestRunningStateWritesNothing(t *testing.T) {
	fs := &fakeSink{}
	var lb logBuf
	r := New(fs, WithLogger(newLogger(&lb)))
	r.JobFinished(summary(operations.ActionStart, operations.StateRunning, nil))
	_ = r.Close(ctxT(t))
	if n := len(fs.snapshot()); n != 0 {
		t.Fatalf("rows = %d", n)
	}
	if !strings.Contains(lb.String(), "non-final") {
		t.Fatalf("no log line: %q", lb.String())
	}
}

func TestJobOutputIsNoop(t *testing.T) {
	fs := &fakeSink{}
	r := New(fs)
	r.JobOutput(operations.JobLine{Profile: "synthetic-dev", Line: "x"})
	_ = r.Close(ctxT(t))
	if len(fs.snapshot()) != 0 {
		t.Fatal("output wrote a row")
	}
}

func TestConfigChange(t *testing.T) {
	fs := &fakeSink{}
	r := New(fs, WithClock(func() time.Time { return t0 }))
	r.RecordConfigChange("synthetic-dev", []string{"disk", "cpu", "bogus", "memory"})
	r.RecordConfigChange("synthetic-dev", nil)
	r.RecordConfigChange("synthetic-dev", []string{"bogus"})
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	rows := fs.snapshot()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	e := rows[0]
	if e.Action != "config_change" || e.Outcome != "success" || e.Profile != "synthetic-dev" || !e.StartedAt.Equal(t0) {
		t.Fatalf("row = %+v", e)
	}
	if e.Detail == nil || *e.Detail != "changed cpu, memory, disk" {
		t.Fatalf("detail = %v", e.Detail)
	}
}

func TestBlockedSinkNeverBlocksProducers(t *testing.T) {
	fs := &fakeSink{gate: make(chan struct{})}
	r := New(fs, WithLogger(newLogger(&logBuf{})))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("producers blocked on a stuck sink")
	}
	close(fs.gate)
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if got, dropped := len(fs.snapshot()), int(r.Dropped()); got+dropped != 200 || got == 0 {
		t.Fatalf("written %d + dropped %d != 200", got, dropped)
	}
}

func TestOverflowDropsAndWarns(t *testing.T) {
	fs := &fakeSink{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	var lb logBuf
	r := New(fs, WithQueueSize(2), WithLogger(newLogger(&lb)))
	r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
	<-fs.entered // writer holds one row, queue is empty
	for i := 0; i < 5; i++ {
		r.JobFinished(summary(operations.ActionStop, operations.StateSucceeded, intp(0)))
	}
	if r.Dropped() != 3 {
		t.Fatalf("dropped = %d, want 3", r.Dropped())
	}
	if n := strings.Count(lb.String(), "queue full"); n != 1 {
		t.Fatalf("warn lines = %d, want 1 per burst", n)
	}
	close(fs.gate)
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if n := len(fs.snapshot()); n != 3 {
		t.Fatalf("written = %d, want 3", n)
	}
}

func TestCloseDrains(t *testing.T) {
	fs := &fakeSink{}
	r := New(fs)
	for i := 0; i < 10; i++ {
		r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
	}
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if n := len(fs.snapshot()); n != 10 {
		t.Fatalf("written = %d, want 10", n)
	}
}

func TestCloseDeadlineWithBlockedSink(t *testing.T) {
	fs := &fakeSink{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	r := New(fs, WithLogger(newLogger(&logBuf{})))
	r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
	<-fs.entered
	r.JobFinished(summary(operations.ActionStop, operations.StateSucceeded, intp(0)))
	r.JobFinished(summary(operations.ActionStop, operations.StateSucceeded, intp(0)))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := r.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	close(fs.gate)
	<-r.done // writer exits and discards the abandoned rows
	if n := len(fs.snapshot()); n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}
	if r.Dropped() != 2 {
		t.Fatalf("dropped = %d, want 2", r.Dropped())
	}
}

func TestSendAfterCloseAndDoubleClose(t *testing.T) {
	fs := &fakeSink{}
	r := New(fs)
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
	r.RecordConfigChange("synthetic-dev", []string{"cpu"})
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if r.Dropped() != 2 || len(fs.snapshot()) != 0 {
		t.Fatalf("dropped=%d rows=%d", r.Dropped(), len(fs.snapshot()))
	}
}

func TestConcurrentSendAndClose(t *testing.T) {
	fs := &fakeSink{}
	r := New(fs)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
				r.RecordConfigChange("synthetic-dev", []string{"cpu"})
			}
		}()
	}
	_ = r.Close(ctxT(t))
	wg.Wait()
	if got := uint64(len(fs.snapshot())) + r.Dropped(); got != 1600 {
		t.Fatalf("written+dropped = %d, want 1600", got)
	}
}

func TestFailingSinkLogsOncePerBurst(t *testing.T) {
	fs := &fakeSink{err: errors.New("disk full")}
	var lb logBuf
	r := New(fs, WithLogger(newLogger(&lb)))
	for i := 0; i < 3; i++ {
		r.JobFinished(summary(operations.ActionStart, operations.StateFailed, intp(1)))
	}
	waitFor(t, func() bool { return fs.callCount() == 3 })
	fs.setErr(nil)
	r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	out := lb.String()
	if n := strings.Count(out, "write failed"); n != 1 {
		t.Fatalf("error lines = %d, want 1:\n%s", n, out)
	}
	if n := strings.Count(out, "recovered"); n != 1 {
		t.Fatalf("recovery lines = %d, want 1:\n%s", n, out)
	}
	if n := len(fs.snapshot()); n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWritesThroughRealStore(t *testing.T) {
	s, err := store.Open(ctxT(t), store.Options{Path: t.TempDir() + "/h.db"})
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := New(s)
	r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
	r.RecordConfigChange("synthetic-dev", []string{"cpu"})
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecentActions(ctxT(t), "synthetic-dev", 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
}

func TestSecondFailureBurstLogsAgain(t *testing.T) {
	fs := &fakeSink{err: errors.New("disk full")}
	var lb logBuf
	r := New(fs, WithLogger(newLogger(&lb)))
	send := func(n int) {
		for i := 0; i < n; i++ {
			r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
		}
	}
	send(2)
	waitFor(t, func() bool { return fs.callCount() == 2 })
	fs.setErr(nil)
	send(1)
	waitFor(t, func() bool { return fs.callCount() == 3 && strings.Contains(lb.String(), "recovered") })
	fs.setErr(errors.New("disk full again"))
	send(2)
	waitFor(t, func() bool { return fs.callCount() == 5 })
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	out := lb.String()
	if n := strings.Count(out, "write failed"); n != 2 {
		t.Fatalf("error lines = %d, want 2:\n%s", n, out)
	}
	if n := strings.Count(out, "recovered"); n != 1 {
		t.Fatalf("recovery lines = %d, want 1:\n%s", n, out)
	}
}

type ctxSink struct {
	mu    sync.Mutex
	calls int
}

func (c *ctxSink) AppendAction(ctx context.Context, _ store.ActionEntry) error {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func TestWriteTimeoutMovesOn(t *testing.T) {
	cs := &ctxSink{}
	var lb logBuf
	r := New(cs, WithWriteTimeout(20*time.Millisecond), WithLogger(newLogger(&lb)))
	r.JobFinished(summary(operations.ActionStart, operations.StateSucceeded, intp(0)))
	r.JobFinished(summary(operations.ActionStop, operations.StateSucceeded, intp(0)))
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	cs.mu.Lock()
	calls := cs.calls
	cs.mu.Unlock()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if n := strings.Count(lb.String(), "write failed"); n != 1 {
		t.Fatalf("error lines = %d, want 1:\n%s", n, lb.String())
	}
}

func TestImagePullRowsUseTheDockerActionNameAndTheReference(t *testing.T) {
	cases := map[operations.State]string{
		operations.StateSucceeded: "success", operations.StateFailed: "failed", operations.StateCancelled: "cancelled",
	}
	for state, outcome := range cases {
		fs := &fakeSink{}
		r := New(fs)
		s := summary(operations.ActionImagePull, state, intp(1))
		s.Subject = "postgres:18-alpine"
		r.JobFinished(s)
		if err := r.Close(ctxT(t)); err != nil {
			t.Fatal(err)
		}
		rows := fs.snapshot()
		if len(rows) != 1 || rows[0].Action != "docker.image.pull" || rows[0].Outcome != outcome ||
			rows[0].Detail == nil || *rows[0].Detail != "postgres:18-alpine" || rows[0].Profile != "synthetic-dev" {
			t.Fatalf("%s: rows = %+v", state, rows)
		}
	}
}

func TestImagePullRowDropsAnUnprintableSubject(t *testing.T) {
	for _, subj := range []string{"a\nb", "a\x1b[31m", "é", strings.Repeat("a", 300), ""} {
		fs := &fakeSink{}
		r := New(fs)
		s := summary(operations.ActionImagePull, operations.StateSucceeded, intp(0))
		s.Subject = subj
		r.JobFinished(s)
		if err := r.Close(ctxT(t)); err != nil {
			t.Fatal(err)
		}
		if rows := fs.snapshot(); len(rows) != 1 || rows[0].Detail != nil {
			t.Fatalf("subject %q: rows = %+v", subj, rows)
		}
	}
}

func TestImagePullRunningWritesNothing(t *testing.T) {
	fs := &fakeSink{}
	r := New(fs)
	r.JobFinished(summary(operations.ActionImagePull, operations.StateRunning, nil))
	if err := r.Close(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if len(fs.snapshot()) != 0 {
		t.Fatal("row written for a running pull")
	}
}
