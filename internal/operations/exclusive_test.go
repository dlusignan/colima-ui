package operations

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTryExclusiveHoldsSlotAndBlocksSubmit(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	ran := false
	held, err := m.TryExclusive("default", func() error {
		ran = true
		// No manager mutex is held here: these calls would deadlock otherwise.
		if id, running := m.Busy("default"); running || id != "" {
			t.Errorf("Busy during hold = %q, %v; a hold is not a job", id, running)
		}
		_, serr := m.Submit(Request{Action: ActionStart, Profile: "default"})
		var busy *BusyError
		if !errors.As(serr, &busy) || busy.JobID != "" || busy.Profile != "default" {
			t.Errorf("Submit during hold err = %v, want BusyError with empty JobID", serr)
		}
		if h, nerr := m.TryExclusive("default", func() error { t.Error("nested fn ran"); return nil }); h || nerr != nil {
			t.Errorf("nested TryExclusive = %v, %v", h, nerr)
		}
		// Other profiles stay available.
		s, oerr := m.Submit(Request{Action: ActionStart, Profile: "other"})
		if oerr != nil {
			t.Errorf("other profile: %v", oerr)
		} else {
			l.last().exitWith(0)
			_ = s
		}
		return nil
	})
	if !held || err != nil || !ran {
		t.Fatalf("held %v err %v ran %v", held, err, ran)
	}
	if l.count() != 1 {
		t.Fatalf("launcher count = %d, want only the other profile's job", l.count())
	}
	mustSubmit(t, m, "default") // slot released after fn
}

func TestTryExclusiveNotHeldWhileJobRuns(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	held, err := m.TryExclusive("default", func() error { t.Error("fn must not run"); return nil })
	if held || err != nil {
		t.Fatalf("held %v err %v", held, err)
	}
	l.last().exitWith(0)
	waitState(t, m, s.ID, StateSucceeded)
	eventually(t, "slot free", func() bool { _, r := m.Busy("default"); return !r })
	held, err = m.TryExclusive("default", func() error { return nil })
	if !held || err != nil {
		t.Fatalf("after job: held %v err %v", held, err)
	}
}

func TestTryExclusiveReturnsFnErrorAndReleasesOnErrorAndPanic(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	boom := errors.New("write failed")
	held, err := m.TryExclusive("p", func() error { return boom })
	if !held || !errors.Is(err, boom) {
		t.Fatalf("held %v err %v", held, err)
	}
	mustSubmit(t, m, "p")

	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic should propagate")
			}
		}()
		_, _ = m.TryExclusive("q", func() error { panic("bad fn") })
	}()
	mustSubmit(t, m, "q") // slot released despite the panic
}

func TestTryExclusiveAfterShutdown(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	if err := m.Shutdown(ctxTimeout(t, 3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if held, err := m.TryExclusive("p", func() error { return nil }); held || !errors.Is(err, ErrClosed) {
		t.Fatalf("held %v err %v", held, err)
	}
}

func TestSlotsAreCaseInsensitive(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "Dev")
	var busy *BusyError
	if _, err := m.Submit(Request{Action: ActionCreate, Profile: "dEV"}); !errors.As(err, &busy) || busy.JobID != s.ID || busy.Profile != "dEV" {
		t.Fatalf("Submit other case err = %v", err)
	}
	if id, running := m.Busy("DEV"); !running || id != s.ID {
		t.Fatalf("Busy = %q, %v", id, running)
	}
	if held, err := m.TryExclusive("dev", func() error { t.Error("fn ran"); return nil }); held || err != nil {
		t.Fatalf("TryExclusive held %v err %v", held, err)
	}
	if got := s.Profile; got != "Dev" {
		t.Fatalf("job profile = %q, want the requested spelling", got)
	}
	l.last().exitWith(0)
	waitState(t, m, s.ID, StateSucceeded)
	eventually(t, "slot free", func() bool { _, r := m.Busy("dev"); return !r })
	mustSubmit(t, m, "dev")
}

func TestExclusiveHoldBlocksOtherCaseSubmit(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	held, err := m.TryExclusive("Dev", func() error {
		var busy *BusyError
		if _, serr := m.Submit(Request{Action: ActionCreate, Profile: "dev"}); !errors.As(serr, &busy) {
			t.Errorf("Submit err = %v, want BusyError", serr)
		}
		return nil
	})
	if !held || err != nil {
		t.Fatalf("held %v err %v", held, err)
	}
}

func TestConcurrentCreatesOnCaseVariantsLaunchOnce(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	names := []string{"dev", "Dev", "DEV", "dEv"}
	var wg sync.WaitGroup
	var ok, busy atomic.Int32
	for _, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Submit(Request{Action: ActionCreate, Profile: n})
			var be *BusyError
			switch {
			case err == nil:
				ok.Add(1)
			case errors.As(err, &be):
				busy.Add(1)
			default:
				t.Errorf("err = %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || busy.Load() != int32(len(names)-1) || l.count() != 1 {
		t.Fatalf("ok %d busy %d launches %d", ok.Load(), busy.Load(), l.count())
	}
	l.last().exitWith(0)
}
