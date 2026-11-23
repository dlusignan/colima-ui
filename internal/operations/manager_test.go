package operations

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
)

func TestBusyRejectionCarriesRunningJobID(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	first := mustSubmit(t, m, "default")

	_, err := m.Submit(Request{Action: ActionStop, Profile: "default"})
	var busy *BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("err = %v, want *BusyError", err)
	}
	if busy.JobID != first.ID || busy.Profile != "default" {
		t.Fatalf("busy = %+v, want job %s on default", busy, first.ID)
	}
	if l.count() != 1 {
		t.Fatalf("launcher called %d times, want 1", l.count())
	}
	if id, running := m.Busy("default"); !running || id != first.ID {
		t.Fatalf("Busy = %q, %v", id, running)
	}

	l.last().exitWith(0)
	waitState(t, m, first.ID, StateSucceeded)
	eventually(t, "slot freed", func() bool { _, r := m.Busy("default"); return !r })
	mustSubmit(t, m, "default")
}

func TestTwoProfilesRunAtOnce(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	a := mustSubmit(t, m, "a")
	b := mustSubmit(t, m, "b")
	if a.ID == b.ID {
		t.Fatal("jobs share an ID")
	}
	l.proc(0).emit("from a")
	l.proc(1).emit("from b")
	eventually(t, "both lines", func() bool {
		sa, _ := m.Get(a.ID)
		sb, _ := m.Get(b.ID)
		return len(sa.Output) == 1 && len(sb.Output) == 1
	})
	sa, _ := m.Get(a.ID)
	sb, _ := m.Get(b.ID)
	if sa.State != StateRunning || sb.State != StateRunning {
		t.Fatalf("states %s %s, both should be running at once", sa.State, sb.State)
	}
	if sa.Output[0].Text != "from a" || sb.Output[0].Text != "from b" {
		t.Fatalf("outputs mixed: %+v %+v", sa.Output, sb.Output)
	}
	l.proc(0).exitWith(0)
	l.proc(1).exitWith(0)
	waitState(t, m, a.ID, StateSucceeded)
	waitState(t, m, b.ID, StateSucceeded)
}

func TestJobRecordsIDStatusTimestampsAndOutput(t *testing.T) {
	m, l, clk := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	start := clk.Now()
	if s.State != StateRunning || !s.StartedAt.Equal(start) || s.FinishedAt != nil || s.ExitCode != nil {
		t.Fatalf("submit summary = %+v", s)
	}
	if s.Action != ActionStart || s.Profile != "default" {
		t.Fatalf("summary = %+v", s)
	}
	l.last().emit("one")
	l.last().emit("two")
	eventually(t, "lines", func() bool { g, _ := m.Get(s.ID); return len(g.Output) == 2 })
	clk.Advance(7 * time.Second)
	l.last().exitWith(0)
	done := waitState(t, m, s.ID, StateSucceeded)
	if done.FinishedAt == nil || !done.FinishedAt.Equal(start.Add(7*time.Second)) {
		t.Fatalf("FinishedAt = %v", done.FinishedAt)
	}
	if done.ExitCode == nil || *done.ExitCode != 0 {
		t.Fatalf("ExitCode = %v", done.ExitCode)
	}
	want := []Line{{1, "one"}, {2, "two"}}
	if !reflect.DeepEqual(done.Output, want) {
		t.Fatalf("output = %+v", done.Output)
	}
	// The snapshot is a copy.
	done.Output[0].Text = "mutated"
	again, _ := m.Get(s.ID)
	if again.Output[0].Text != "one" {
		t.Fatal("Get must return a copy")
	}
	if _, err := m.Get("j_nope"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("Get unknown = %v", err)
	}
}

func TestFinalStateMapping(t *testing.T) {
	three := 3
	zero := 0
	cases := []struct {
		name      string
		exit      colima.Exit
		err       error
		cancelReq bool
		state     State
		code      *int
	}{
		{"clean exit", colima.Exit{}, nil, false, StateSucceeded, &zero},
		{"non-zero exit", colima.Exit{ExitCode: 3}, nil, false, StateFailed, &three},
		{"killed, cancel requested", colima.Exit{ExitCode: -1, Killed: true}, fmt.Errorf("x: %w", context.Canceled), true, StateCancelled, nil},
		{"killed, ctx error only", colima.Exit{ExitCode: -1, Killed: true}, fmt.Errorf("x: %w", context.Canceled), false, StateCancelled, nil},
		{"killed, flag only", colima.Exit{ExitCode: -1, Killed: true}, nil, true, StateCancelled, nil},
		{"killed by someone else", colima.Exit{ExitCode: -1, Killed: true}, nil, false, StateFailed, nil},
		{"unexpected wait error", colima.Exit{}, errors.New("boom"), false, StateFailed, nil},
		{"cancel requested but exited clean", colima.Exit{}, nil, true, StateSucceeded, &zero},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, code := classify(tc.exit, tc.err, tc.cancelReq)
			if st != tc.state || !reflect.DeepEqual(code, tc.code) {
				t.Fatalf("got %s %v, want %s %v", st, code, tc.state, tc.code)
			}
		})
	}
}

func TestFailedWithExitCode(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	l.last().exitWith(3)
	got := waitState(t, m, s.ID, StateFailed)
	if got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("ExitCode = %v, want 3", got.ExitCode)
	}
}

func TestKilledWithoutCancelRequestIsFailedWithNullCode(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	l.last().end(colima.Exit{ExitCode: -1, Killed: true}, nil) // e.g. kill -9 from outside
	got := waitState(t, m, s.ID, StateFailed)
	if got.ExitCode != nil {
		t.Fatalf("ExitCode = %v, want nil", *got.ExitCode)
	}
}

func TestOutputSeqAndEvictionThroughManager(t *testing.T) {
	m, l, _ := newTestManager(t, func(o *Options) { o.MaxLines = 3 })
	s := mustSubmit(t, m, "default")
	for i := 1; i <= 5; i++ {
		l.last().emit(fmt.Sprintf("l%d", i))
	}
	l.last().exitWith(0)
	got := waitState(t, m, s.ID, StateSucceeded)
	if !got.Truncated || got.DroppedLines != 2 {
		t.Fatalf("Truncated %v Dropped %d", got.Truncated, got.DroppedLines)
	}
	if len(got.Output) != 3 || got.Output[0].Seq != got.DroppedLines+1 || got.Output[2].Seq != 5 {
		t.Fatalf("output = %+v", got.Output)
	}
}

func TestOutputByteBoundThroughManager(t *testing.T) {
	m, l, _ := newTestManager(t, func(o *Options) { o.MaxBytes = 10 })
	s := mustSubmit(t, m, "default")
	l.last().emit("aaaa")
	l.last().emit("bbbb")
	l.last().emit("cccc")
	l.last().exitWith(0)
	got := waitState(t, m, s.ID, StateSucceeded)
	if len(got.Output) != 2 || got.Output[0].Text != "bbbb" || got.Output[0].Seq != 2 || !got.Truncated {
		t.Fatalf("output = %+v truncated %v", got.Output, got.Truncated)
	}
}

func TestLaunchErrorCreatesNoJobAndFreesSlot(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	boom := errors.New("no binary")
	l.err = boom
	if _, err := m.Submit(Request{Action: ActionStart, Profile: "p"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, running := m.Busy("p"); running {
		t.Fatal("slot leaked after launch error")
	}
	l.err = nil
	mustSubmit(t, m, "p")
}

func TestLaunchPanicFreesSlot(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	l.panicV = "kaboom"
	if _, err := m.Submit(Request{Action: ActionStart, Profile: "p"}); err == nil {
		t.Fatal("want error from panicking launcher")
	}
	if _, running := m.Busy("p"); running {
		t.Fatal("slot leaked after launcher panic")
	}
	l.panicV = nil
	mustSubmit(t, m, "p")
}

func TestNoManagerMutexHeldDuringLaunch(t *testing.T) {
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
	// Launch is blocked. The manager must still answer, and the slot is taken.
	id, running := m.Busy("p")
	if !running || id == "" {
		t.Fatalf("Busy during launch = %q, %v", id, running)
	}
	var busy *BusyError
	if _, err := m.Submit(Request{Profile: "p"}); !errors.As(err, &busy) || busy.JobID != id {
		t.Fatalf("second submit err = %v, want busy with job %s", err, id)
	}
	if held, _ := m.TryExclusive("p", func() error { return nil }); held {
		t.Fatal("TryExclusive must not hold a slot reserved by a launching job")
	}
	if held, err := m.TryExclusive("other", func() error { return nil }); !held || err != nil {
		t.Fatalf("other profile: held %v err %v", held, err)
	}
	close(l.gate)
	r := <-out
	if r.err != nil || r.s.ID != id {
		t.Fatalf("submit result = %+v", r)
	}
	l.last().exitWith(0)
}

var idFormat = regexp.MustCompile(`^j_[0-9a-f]{6}$`)

func TestDefaultJobIDFormatAndUniqueness(t *testing.T) {
	leakCheck(t)
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := randomID()
		if !idFormat.MatchString(id) {
			t.Fatalf("bad id %q", id)
		}
		seen[id] = true
	}
	if len(seen) < 495 { // 24 bits: a handful of birthday collisions is possible, hundreds is not
		t.Fatalf("only %d unique ids of 500", len(seen))
	}

	l := &fakeLauncher{}
	m := New(Options{Launcher: l})
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	s := mustSubmit(t, m, "p")
	if !idFormat.MatchString(s.ID) {
		t.Fatalf("manager id %q", s.ID)
	}
}

func TestIDCollisionRetriesAndExhausts(t *testing.T) {
	seq := []string{"j_aaaaaa", "j_aaaaaa", "j_bbbbbb"}
	i := 0
	m, l, _ := newTestManager(t, func(o *Options) {
		o.NewID = func() string { v := seq[i%len(seq)]; i++; return v }
	})
	a := mustSubmit(t, m, "a")
	b := mustSubmit(t, m, "b") // generator offers a duplicate first
	if a.ID != "j_aaaaaa" || b.ID != "j_bbbbbb" {
		t.Fatalf("ids %s %s", a.ID, b.ID)
	}
	_ = l

	m2, _, _ := newTestManager(t, func(o *Options) { o.NewID = func() string { return "j_cccccc" } })
	mustSubmit(t, m2, "a")
	if _, err := m2.Submit(Request{Profile: "b"}); !errors.Is(err, ErrIDExhausted) {
		t.Fatalf("err = %v, want ErrIDExhausted", err)
	}
	if _, running := m2.Busy("b"); running {
		t.Fatal("slot leaked after id exhaustion")
	}
}

func TestRetentionEvictsOldestFinishedJob(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	var ids []string
	for i := 0; i < DefaultRetainFinished+1; i++ {
		s := mustSubmit(t, m, fmt.Sprintf("p%d", i))
		ids = append(ids, s.ID)
		l.last().exitWith(0)
		waitState(t, m, s.ID, StateSucceeded)
		// Retention runs right after the state is recorded; wait for the slot.
		p := fmt.Sprintf("p%d", i)
		eventually(t, "slot free", func() bool { _, r := m.Busy(p); return !r })
	}
	eventually(t, "oldest evicted", func() bool { _, err := m.Get(ids[0]); return errors.Is(err, ErrJobNotFound) })
	if _, err := m.Get(ids[1]); err != nil {
		t.Fatalf("second oldest should be retained: %v", err)
	}
	if _, err := m.Get(ids[len(ids)-1]); err != nil {
		t.Fatalf("newest should be retained: %v", err)
	}
}

func TestRunningJobsAreNeverEvicted(t *testing.T) {
	m, l, _ := newTestManager(t, func(o *Options) { o.RetainFinished = 1 })
	long := mustSubmit(t, m, "long")
	for i := 0; i < 3; i++ {
		p := fmt.Sprintf("q%d", i)
		s := mustSubmit(t, m, p)
		l.last().exitWith(0)
		waitState(t, m, s.ID, StateSucceeded)
		eventually(t, "slot free", func() bool { _, r := m.Busy(p); return !r })
	}
	if got, err := m.Get(long.ID); err != nil || got.State != StateRunning {
		t.Fatalf("running job: %v %v", got.State, err)
	}
}
