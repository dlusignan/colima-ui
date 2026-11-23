package operations

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShutdownCancelsRunningJobsAndWaits(t *testing.T) {
	obs := &recObserver{}
	m, l, _ := newTestManager(t, func(o *Options) { o.Observers = []Observer{obs} })
	var ids []string
	for _, p := range []string{"a", "b", "c"} {
		ids = append(ids, mustSubmit(t, m, p).ID)
	}
	if err := m.Shutdown(ctxTimeout(t, 3*time.Second)); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for i, id := range ids {
		if !l.proc(i).wasCancelled() {
			t.Fatalf("process %d not cancelled", i)
		}
		// Shutdown returned, so every pump has finished: no polling.
		g, err := m.Get(id)
		if err != nil || g.State != StateCancelled || g.ExitCode != nil {
			t.Fatalf("job %s: %+v %v", id, g, err)
		}
	}
	if obs.finishedCount() != 3 {
		t.Fatalf("observers told about %d finishes, want 3", obs.finishedCount())
	}
	if _, err := m.Submit(Request{Action: ActionStart, Profile: "a"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit after Shutdown = %v, want ErrClosed", err)
	}
	if err := m.Shutdown(ctxTimeout(t, time.Second)); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestShutdownWaitsForExclusiveHolder(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	inFn := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.TryExclusive("p", func() error { close(inFn); <-release; return nil })
	}()
	<-inFn
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := m.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown with a holder = %v, want deadline exceeded", err)
	}
	close(release)
	<-done
	if err := m.Shutdown(ctxTimeout(t, 3*time.Second)); err != nil {
		t.Fatalf("Shutdown after release: %v", err)
	}
}

func TestShutdownDuringLaunchStillEndsTheJob(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	l.entered = make(chan struct{})
	l.gate = make(chan struct{})
	type res struct {
		s   JobSummary
		err error
	}
	out := make(chan res, 1)
	go func() {
		s, err := m.Submit(Request{Action: ActionStart, Profile: "p"})
		out <- res{s, err}
	}()
	<-l.entered
	sd := make(chan error, 1)
	go func() { sd <- m.Shutdown(ctxTimeout(t, 5*time.Second)) }()
	time.Sleep(20 * time.Millisecond) // let Shutdown start waiting
	close(l.gate)
	r := <-out
	if r.err != nil {
		t.Fatalf("Submit: %v", r.err)
	}
	if err := <-sd; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if g, _ := m.Get(r.s.ID); g.State != StateCancelled {
		t.Fatalf("state = %s, want cancelled", g.State)
	}
}

func TestRequestContextIsNotWiredIn(t *testing.T) {
	// Submit takes no context, so a cancelled request cannot stop a job; the
	// job context hangs off the manager. Pin it: the job survives until
	// Cancel or Shutdown.
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "p")
	time.Sleep(20 * time.Millisecond)
	if l.last().wasCancelled() {
		t.Fatal("job cancelled without Cancel or Shutdown")
	}
	if g, _ := m.Get(s.ID); g.State != StateRunning {
		t.Fatalf("state = %s", g.State)
	}
}
