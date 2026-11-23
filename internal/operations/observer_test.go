package operations

import (
	"testing"
	"time"
)

func TestObserverSeesOrderedOutputAndFinishAfterRecord(t *testing.T) {
	obs := &recObserver{}
	m, l, _ := newTestManager(t, func(o *Options) { o.Observers = []Observer{obs} })
	type probe struct {
		busy  bool
		state State
	}
	probed := make(chan probe, 1)
	obs.onFinish = func(s JobSummary) {
		_, r := m.Busy(s.Profile)
		g, _ := m.Get(s.ID)
		probed <- probe{r, g.State}
	}
	s := mustSubmit(t, m, "default")
	for _, text := range []string{"a", "b", "c"} {
		l.last().emit(text)
	}
	l.last().end(colimaExit(5, []string{"tail1", "tail2"}), nil)
	var pr probe
	select {
	case pr = <-probed:
	case <-time.After(3 * time.Second):
		t.Fatal("no JobFinished")
	}

	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.lines) != 3 {
		t.Fatalf("lines = %+v", obs.lines)
	}
	for i, ln := range obs.lines {
		if ln.Seq != i+1 || ln.JobID != s.ID || ln.Profile != "default" {
			t.Fatalf("line %d = %+v", i, ln)
		}
	}
	f := obs.finished[0]
	if f.State != StateFailed || f.ExitCode == nil || *f.ExitCode != 5 || f.FinishedAt == nil {
		t.Fatalf("finished = %+v", f)
	}
	if len(f.Tail) != 2 || f.Tail[1] != "tail2" {
		t.Fatalf("tail = %v", f.Tail)
	}
	if pr.busy || pr.state != StateFailed {
		t.Fatalf("in JobFinished: busy=%v state=%s, want slot released and state recorded", pr.busy, pr.state)
	}
}

func TestObserverPanicIsRecovered(t *testing.T) {
	bad := &recObserver{
		onLine:   func(JobLine) { panic("bad line") },
		onFinish: func(JobSummary) { panic("bad finish") },
	}
	good := &recObserver{}
	m, l, _ := newTestManager(t, func(o *Options) { o.Observers = []Observer{bad, good} })
	s := mustSubmit(t, m, "default")
	l.last().emit("one")
	l.last().emit("two")
	l.last().exitWith(0)
	waitState(t, m, s.ID, StateSucceeded)
	eventually(t, "good observer finish", func() bool { return good.finishedCount() == 1 })
	good.mu.Lock()
	n := len(good.lines)
	good.mu.Unlock()
	if n != 2 {
		t.Fatalf("good observer saw %d lines, want 2", n)
	}
	// The pump survived: the profile is usable again.
	eventually(t, "slot free", func() bool { _, r := m.Busy("default"); return !r })
	mustSubmit(t, m, "default")
}

func TestBlockedObserverStallsOnlyItsOwnJob(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var blockedJob string
	obs := &recObserver{}
	obs.onLine = func(l JobLine) {
		if l.JobID == blockedJob {
			entered <- struct{}{}
			<-release
		}
	}
	m, l, _ := newTestManager(t, func(o *Options) { o.Observers = []Observer{obs} })
	t.Cleanup(func() { // runs before the manager's own cleanup
		select {
		case <-release:
		default:
			close(release)
		}
	})
	a := mustSubmit(t, m, "a")
	blockedJob = a.ID // safe: nothing has been emitted yet
	b := mustSubmit(t, m, "b")
	pa, pb := l.proc(0), l.proc(1)

	pa.emit("stuck")
	<-entered
	// Job b is unaffected, end to end.
	pb.emit("b1")
	pb.exitWith(0)
	waitState(t, m, b.ID, StateSucceeded)

	// Cancel kills the child, but the state cannot be recorded while the
	// observer holds the pump, so Cancel gives up with its own context error.
	_, err := m.Cancel(ctxTimeout(t, 50*time.Millisecond), a.ID)
	if err == nil {
		t.Fatal("Cancel returned success while the observer blocks the pump")
	}
	if !pa.wasCancelled() {
		t.Fatal("child must be cancelled even though the observer is blocked")
	}
	if g, _ := m.Get(a.ID); g.State != StateRunning {
		t.Fatalf("state = %s, should still read running until the observer returns", g.State)
	}
	close(release)
	waitState(t, m, a.ID, StateCancelled)
}
