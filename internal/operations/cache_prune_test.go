package operations

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// fakeStreamer hands out one fakeProc like the real adapter: the process ends
// when the context does.
type fakeStreamer struct {
	mu    sync.Mutex
	calls [][]string
	proc  *fakeProc
	err   error
}

func (f *fakeStreamer) Stream(ctx context.Context, args []string) (colima.Process, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	go func() {
		select {
		case <-ctx.Done():
			f.proc.Cancel()
		case <-f.proc.done:
		}
	}()
	return f.proc, nil
}

func (f *fakeStreamer) argv() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[0]
}

func pruneReq(run TaskFunc, all bool) TaskRequest {
	subj := PruneSubjectCache
	if all {
		subj = PruneSubjectAll
	}
	return TaskRequest{Action: ActionCachePrune, Subject: subj, MaxPerProfile: 1,
		RefuseWhileRunning: []Action{ActionCreate, ActionStart}, Run: run}
}

func texts(s JobSummary) string {
	var out []string
	for _, l := range s.Output {
		out = append(out, l.Text)
	}
	return strings.Join(out, "|")
}

func TestCachePruneRunsFixedVectorAndSucceeds(t *testing.T) {
	for _, c := range []struct {
		all  bool
		want string
	}{{false, "prune --force"}, {true, "prune --force --all"}} {
		m, _, _ := newTestManager(t, nil)
		fs := &fakeStreamer{proc: newFakeProc()}
		fs.proc.emit("removing")
		fs.proc.exitWith(0)
		s, err := m.SubmitTask(pruneReq(CachePruneTask(fs, c.all), c.all))
		if err != nil {
			t.Fatal(err)
		}
		got := waitState(t, m, s.ID, StateSucceeded)
		if strings.Join(fs.argv(), " ") != c.want || texts(got) != "removing" || got.Profile != "" {
			t.Fatalf("argv %v job %+v", fs.argv(), got)
		}
	}
}

func TestCachePruneNonZeroExitFailsWithFixedLine(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	fs := &fakeStreamer{proc: newFakeProc()}
	fs.proc.emit("boom")
	fs.proc.exitWith(3)
	s, _ := m.SubmitTask(pruneReq(CachePruneTask(fs, false), false))
	got := waitState(t, m, s.ID, StateFailed)
	if texts(got) != "boom|error: colima prune exited with status 3." || got.NotRun {
		t.Fatalf("job %+v", got)
	}
}

func TestCachePruneThatCannotStartIsNotRun(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	fs := &fakeStreamer{err: errors.New("exec: /home/user/bin/colima: not found")}
	s, _ := m.SubmitTask(pruneReq(CachePruneTask(fs, true), true))
	got := waitState(t, m, s.ID, StateFailed)
	if !got.NotRun || strings.Contains(texts(got), "/home") {
		t.Fatalf("job %+v", got)
	}
}

func TestCachePruneCancelAwaitsTheChildAndRecordsClosingLine(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	fs := &fakeStreamer{proc: newFakeProc()}
	childGone := make(chan struct{})
	var once sync.Once
	fs.proc.onCancel = func(p *fakeProc) {
		once.Do(func() {
			time.Sleep(30 * time.Millisecond) // the child takes a moment to die
			p.kill()
			close(childGone)
		})
	}
	s, _ := m.SubmitTask(pruneReq(CachePruneTask(fs, false), false))
	got, err := m.Cancel(ctxTimeout(t, 3*time.Second), s.ID)
	if err != nil || got.State != StateCancelled {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	select {
	case <-childGone:
	default:
		t.Fatal("cancel returned before the child was gone")
	}
	if !strings.HasSuffix(texts(got), PruneCancelledLine) {
		t.Fatalf("closing line missing: %q", texts(got))
	}
}

func TestCachePruneShutdownCancelsAndAwaits(t *testing.T) {
	leakCheck(t)
	m := New(Options{Launcher: &fakeLauncher{}})
	fs := &fakeStreamer{proc: newFakeProc()}
	s, err := m.SubmitTask(pruneReq(CachePruneTask(fs, false), false))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(ctxTimeout(t, 3*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(s.ID)
	if got.State != StateCancelled || !fs.proc.wasCancelled() || !strings.HasSuffix(texts(got), PruneCancelledLine) {
		t.Fatalf("job %+v", got)
	}
}

func TestCachePruneIsGlobalOneAtATimeAndHoldsNoSlot(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	fs := &fakeStreamer{proc: newFakeProc()}
	s, err := m.SubmitTask(pruneReq(CachePruneTask(fs, false), false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitTask(pruneReq(CachePruneTask(&fakeStreamer{proc: newFakeProc()}, false), false)); !errors.Is(err, ErrDuplicateTask) {
		t.Fatalf("same variant: %v", err)
	}
	if _, err := m.SubmitTask(pruneReq(CachePruneTask(&fakeStreamer{proc: newFakeProc()}, true), true)); !errors.Is(err, ErrTooManyTasks) {
		t.Fatalf("other variant: %v", err)
	}
	// Every profile slot stays free, including the empty-name key.
	if _, running := m.Busy(""); running {
		t.Fatal("prune holds a slot")
	}
	// A stop on any profile is not blocked by a prune (a start is: see below).
	cj, err := m.Submit(Request{Action: ActionStop, Profile: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	l.proc(0).exitWith(0)
	waitState(t, m, cj.ID, StateSucceeded)
	fs.proc.exitWith(0)
	waitState(t, m, s.ID, StateSucceeded)
	// Released: a new prune is accepted.
	fs2 := &fakeStreamer{proc: newFakeProc()}
	fs2.proc.exitWith(0)
	eventually(t, "released", func() bool {
		s2, err := m.SubmitTask(pruneReq(CachePruneTask(fs2, false), false))
		if err != nil {
			return false
		}
		waitState(t, m, s2.ID, StateSucceeded)
		return true
	})
}

func TestCachePruneRefusedWhileCreateOrStartRuns(t *testing.T) {
	for _, act := range []Action{ActionStart, ActionCreate} {
		m, l, _ := newTestManager(t, nil)
		cj, err := m.Submit(Request{Action: act, Profile: "dev"})
		if err != nil {
			t.Fatal(err)
		}
		fs := &fakeStreamer{proc: newFakeProc()}
		_, err = m.SubmitTask(pruneReq(CachePruneTask(fs, false), false))
		var blocked *TaskBlockedError
		if !errors.As(err, &blocked) || blocked.JobID != cj.ID || blocked.Action != act {
			t.Fatalf("%s: err = %v", act, err)
		}
		if len(fs.calls) != 0 {
			t.Fatal("colima was reached")
		}
		l.proc(0).exitWith(0)
		waitState(t, m, cj.ID, StateSucceeded)
	}
	// Stop and delete do not block a prune.
	m, l, _ := newTestManager(t, nil)
	cj, _ := m.Submit(Request{Action: ActionStop, Profile: "dev"})
	fs := &fakeStreamer{proc: newFakeProc()}
	fs.proc.exitWith(0)
	s, err := m.SubmitTask(pruneReq(CachePruneTask(fs, false), false))
	if err != nil {
		t.Fatalf("prune during stop: %v", err)
	}
	waitState(t, m, s.ID, StateSucceeded)
	l.proc(0).exitWith(0)
	waitState(t, m, cj.ID, StateSucceeded)
}

func TestRunningPruneRefusesJobsThatDownloadAndAllowsTheRest(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	fs := &fakeStreamer{proc: newFakeProc()}
	pj, err := m.SubmitTask(pruneReq(CachePruneTask(fs, false), false))
	if err != nil {
		t.Fatal(err)
	}
	for _, act := range []Action{ActionCreate, ActionStart, ActionRestart, ActionKubernetesStart} {
		_, err := m.Submit(Request{Action: act, Profile: "dev"})
		var pr *CachePruneRunningError
		if !errors.As(err, &pr) || pr.JobID != pj.ID {
			t.Fatalf("%s: err = %v", act, err)
		}
	}
	// Nothing was launched and no slot was left behind.
	if _, running := m.Busy("dev"); running {
		t.Fatal("a refused job holds a slot")
	}
	for _, act := range []Action{ActionStop, ActionDelete, ActionUpdate, ActionKubernetesStop, ActionKubernetesReset, ActionKubernetesDelete} {
		s, err := m.Submit(Request{Action: act, Profile: "p-" + string(act)})
		if err != nil {
			t.Fatalf("%s refused during a prune: %v", act, err)
		}
		_ = s
	}
	// A second prune is busy and names the running one.
	_, err = m.SubmitTask(pruneReq(CachePruneTask(&fakeStreamer{proc: newFakeProc()}, true), true))
	var tb *TaskBusyError
	if !errors.As(err, &tb) || tb.JobID != pj.ID || !errors.Is(err, ErrTooManyTasks) {
		t.Fatalf("second prune: %v", err)
	}
	fs.proc.exitWith(0)
	waitState(t, m, pj.ID, StateSucceeded)
	// Once the prune is over the same actions are accepted again.
	eventually(t, "downloading job accepted", func() bool {
		s, err := m.Submit(Request{Action: ActionStart, Profile: "after"})
		if err != nil {
			return false
		}
		_ = s
		return true
	})
}
