package operations

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
)

func TestCancelRunningJob(t *testing.T) {
	m, l, clk := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	l.last().emit("working")
	clk.Advance(2 * time.Second)

	got, err := m.Cancel(ctxTimeout(t, 3*time.Second), s.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !l.last().wasCancelled() {
		t.Fatal("child process was not cancelled")
	}
	if got.State != StateCancelled || got.ExitCode != nil || got.FinishedAt == nil {
		t.Fatalf("returned job = %+v", got)
	}
	// Cancel returns only after the state is recorded: no polling here.
	now, _ := m.Get(s.ID)
	if now.State != StateCancelled {
		t.Fatalf("Get after Cancel = %s", now.State)
	}
	if len(now.Output) != 1 {
		t.Fatalf("output kept after cancel: %+v", now.Output)
	}
	eventually(t, "slot freed", func() bool { _, r := m.Busy("default"); return !r })
	mustSubmit(t, m, "default")
}

func TestCancelRacingCleanExitReturnsTrueFinalJob(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	// The process exits cleanly just as Cancel reaches it.
	l.last().onCancel = func(p *fakeProc) { p.exitWith(0) }

	got, err := m.Cancel(ctxTimeout(t, 3*time.Second), s.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got.State != StateSucceeded || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("final job = %+v, want succeeded with code 0", got)
	}
}

func TestCancelRaceStress(t *testing.T) {
	m, l, _ := newTestManager(t, func(o *Options) { o.RetainFinished = 1000 })
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		s := mustSubmit(t, m, fmt.Sprintf("p%d", i))
		p := l.last()
		wg.Add(2)
		go func() { defer wg.Done(); p.exitWith(0) }()
		go func() {
			defer wg.Done()
			got, err := m.Cancel(ctxTimeout(t, 5*time.Second), s.ID)
			var nr *NotRunningError
			switch {
			case err == nil:
				if got.State != StateSucceeded && got.State != StateCancelled {
					t.Errorf("state %s", got.State)
				}
			case errors.As(err, &nr):
				if nr.State == StateRunning {
					t.Errorf("NotRunningError with state running")
				}
			default:
				t.Errorf("Cancel: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestCancelFinishedJobIsNotRunningError(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	l.last().exitWith(0)
	waitState(t, m, s.ID, StateSucceeded)

	_, err := m.Cancel(ctxTimeout(t, time.Second), s.ID)
	var nr *NotRunningError
	if !errors.As(err, &nr) || nr.State != StateSucceeded || nr.JobID != s.ID {
		t.Fatalf("err = %v, want NotRunningError succeeded", err)
	}
	if _, err := m.Cancel(ctxTimeout(t, time.Second), "j_unknown"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("unknown job err = %v", err)
	}
}

func TestCancelSecondTimeIsNotRunning(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	if _, err := m.Cancel(ctxTimeout(t, 3*time.Second), s.ID); err != nil {
		t.Fatal(err)
	}
	_, err := m.Cancel(ctxTimeout(t, 3*time.Second), s.ID)
	var nr *NotRunningError
	if !errors.As(err, &nr) || nr.State != StateCancelled {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelHonoursCallerContext(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	l.last().onCancel = func(p *fakeProc) {} // process ignores the cancel for now
	_, err := m.Cancel(ctxTimeout(t, 30*time.Millisecond), s.ID)
	if err == nil {
		t.Fatal("want context error while the process lingers")
	}
	l.last().kill()
	got := waitState(t, m, s.ID, StateCancelled)
	if got.ExitCode != nil {
		t.Fatal("exit code should be nil")
	}
}

func TestKilledProcessWithoutCancelStaysDistinctFromCancel(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	l.last().end(colima.Exit{ExitCode: -1, Killed: true, Tail: []string{"x"}}, nil)
	got := waitState(t, m, s.ID, StateFailed)
	if len(got.Tail) != 1 || got.Tail[0] != "x" {
		t.Fatalf("tail = %v", got.Tail)
	}
}
