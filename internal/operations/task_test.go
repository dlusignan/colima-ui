package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func taskReq(profile, subject string, run TaskFunc) TaskRequest {
	return TaskRequest{Action: ActionImagePull, Profile: profile, Subject: subject, MaxPerProfile: 2, Run: run}
}

// blockUntilCancelled is a task that emits one line, then waits for its ctx.
func blockUntilCancelled(started chan<- struct{}) TaskFunc {
	return func(ctx context.Context, emit func(string)) error {
		emit("working")
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
}

// slotFreeAgain proves the limits of (profile, subject) are released: the same
// subject can be submitted again and the group is not full.
func slotFreeAgain(t *testing.T, m *Manager, profile, subject string) {
	t.Helper()
	eventually(t, "limits released", func() bool {
		s, err := m.SubmitTask(taskReq(profile, subject, func(context.Context, func(string)) error { return nil }))
		if err != nil {
			return false
		}
		waitState(t, m, s.ID, StateSucceeded)
		return true
	})
}

func TestTaskSucceedsAndRecordsOutput(t *testing.T) {
	obs := &recObserver{}
	m, _, _ := newTestManager(t, func(o *Options) { o.Observers = []Observer{obs} })
	s, err := m.SubmitTask(taskReq("dev", "postgres:18", func(_ context.Context, emit func(string)) error {
		emit("one")
		emit("two\r\n")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Action != ActionImagePull || s.Profile != "dev" || s.Subject != "postgres:18" || s.State != StateRunning {
		t.Fatalf("summary = %+v", s)
	}
	got := waitState(t, m, s.ID, StateSucceeded)
	if got.ExitCode == nil || *got.ExitCode != 0 || len(got.Output) != 2 || got.Output[1].Text != "two" {
		t.Fatalf("job = %+v", got)
	}
	eventually(t, "observer", func() bool { return obs.finishedCount() == 1 })
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.finished[0].Subject != "postgres:18" || obs.finished[0].Action != ActionImagePull || len(obs.lines) != 2 {
		t.Fatalf("observer saw %+v / %+v", obs.finished, obs.lines)
	}
}

func TestTaskFailureIsFailedWithExitCodeOne(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	s, _ := m.SubmitTask(taskReq("dev", "a:1", func(_ context.Context, emit func(string)) error {
		emit("error: nope")
		return errors.New("boom")
	}))
	got := waitState(t, m, s.ID, StateFailed)
	if got.ExitCode == nil || *got.ExitCode != 1 {
		t.Fatalf("exit = %v", got.ExitCode)
	}
	slotFreeAgain(t, m, "dev", "a:1")
}

func TestTaskDoesNotHoldTheProfileSlot(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	started := make(chan struct{}, 4)
	s, err := m.SubmitTask(taskReq("dev", "a:1", blockUntilCancelled(started)))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if id, running := m.Busy("dev"); running {
		t.Fatalf("Busy reports task job %s", id)
	}
	// A colima job and an exclusive hold both work while the task runs.
	cj := mustSubmit(t, m, "dev")
	l.proc(0).exitWith(0)
	waitState(t, m, cj.ID, StateSucceeded)
	// The colima slot is released just after the final state is recorded.
	eventually(t, "exclusive hold", func() bool {
		held, err := m.TryExclusive("DEV", func() error { return nil })
		return held && err == nil
	})
	// And a colima job does not block tasks.
	cj2 := mustSubmit(t, m, "dev")
	started2 := make(chan struct{}, 1)
	s2, err := m.SubmitTask(taskReq("dev", "b:1", blockUntilCancelled(started2)))
	if err != nil {
		t.Fatalf("task while colima job runs: %v", err)
	}
	<-started2
	l.proc(1).exitWith(0)
	waitState(t, m, cj2.ID, StateSucceeded)
	for _, id := range []string{s.ID, s2.ID} {
		if _, err := m.Cancel(ctxTimeout(t, 3*time.Second), id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTaskLimitsPerProfileAndDuplicates(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	started := make(chan struct{}, 8)
	var ids []string
	for _, ref := range []string{"a:1", "b:1"} {
		s, err := m.SubmitTask(taskReq("dev", ref, blockUntilCancelled(started)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
		<-started
	}
	if _, err := m.SubmitTask(taskReq("dev", "c:1", blockUntilCancelled(started))); !errors.Is(err, ErrTooManyTasks) {
		t.Fatalf("third pull: %v", err)
	}
	if _, err := m.SubmitTask(taskReq("DEV", "a:1", blockUntilCancelled(started))); !errors.Is(err, ErrDuplicateTask) {
		t.Fatalf("duplicate (case-folded profile): %v", err)
	}
	// Another profile has its own budget, and the same reference is fine there.
	s, err := m.SubmitTask(taskReq("other", "a:1", blockUntilCancelled(started)))
	if err != nil {
		t.Fatalf("other profile: %v", err)
	}
	ids = append(ids, s.ID)
	<-started
	// Cancelling one frees exactly one place.
	if _, err := m.Cancel(ctxTimeout(t, 3*time.Second), ids[0]); err != nil {
		t.Fatal(err)
	}
	var again JobSummary
	eventually(t, "place released", func() bool {
		again, err = m.SubmitTask(taskReq("dev", "c:1", blockUntilCancelled(started)))
		return err == nil
	})
	<-started
	if _, err := m.SubmitTask(taskReq("dev", "d:1", blockUntilCancelled(started))); !errors.Is(err, ErrTooManyTasks) {
		t.Fatalf("limit after refill: %v", err)
	}
	ids = append(ids, again.ID)
	for _, id := range ids[1:] {
		m.Cancel(ctxTimeout(t, 3*time.Second), id)
	}
}

func TestTaskCancelEndsCancelledAndReleases(t *testing.T) {
	obs := &recObserver{}
	m, _, _ := newTestManager(t, func(o *Options) { o.Observers = []Observer{obs} })
	started := make(chan struct{}, 1)
	s, _ := m.SubmitTask(taskReq("dev", "a:1", blockUntilCancelled(started)))
	<-started
	got, err := m.Cancel(ctxTimeout(t, 3*time.Second), s.ID)
	if err != nil || got.State != StateCancelled || got.ExitCode != nil {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	slotFreeAgain(t, m, "dev", "a:1")
	eventually(t, "observer", func() bool { return obs.finishedCount() >= 1 })
	obs.mu.Lock()
	first := obs.finished[0].State
	obs.mu.Unlock()
	if first != StateCancelled {
		t.Fatalf("observer state = %s", first)
	}
}

func TestTaskPanicFailsAndReleases(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	s, err := m.SubmitTask(taskReq("dev", "a:1", func(_ context.Context, emit func(string)) error {
		emit("before")
		panic("synthetic secret /home/user/x")
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := waitState(t, m, s.ID, StateFailed)
	for _, l := range got.Output {
		if strings.Contains(l.Text, "secret") {
			t.Fatalf("panic text in output: %q", l.Text)
		}
	}
	slotFreeAgain(t, m, "dev", "a:1")
}

func TestTaskShutdownCancelsAndAwaits(t *testing.T) {
	leakCheck(t)
	obs := &recObserver{}
	m := New(Options{Launcher: &fakeLauncher{}, Observers: []Observer{obs}})
	started := make(chan struct{}, 2)
	var ids []string
	for _, ref := range []string{"a:1", "b:1"} {
		s, err := m.SubmitTask(taskReq("dev", ref, blockUntilCancelled(started)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
		<-started
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		s, err := m.Get(id)
		if err != nil || s.State != StateCancelled {
			t.Fatalf("job %s = %+v, %v", id, s, err)
		}
	}
	if _, err := m.SubmitTask(taskReq("dev", "c:1", func(context.Context, func(string)) error { return nil })); !errors.Is(err, ErrClosed) {
		t.Fatalf("after shutdown: %v", err)
	}
	eventually(t, "observers", func() bool { return obs.finishedCount() == 2 })
}

func TestTaskShutdownWaitsForATaskThatIgnoresItsContext(t *testing.T) {
	m := New(Options{Launcher: &fakeLauncher{}})
	release := make(chan struct{})
	started := make(chan struct{})
	m.SubmitTask(taskReq("dev", "a:1", func(context.Context, func(string)) error {
		close(started)
		<-release
		return nil
	}))
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := m.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline (the task is still running)", err)
	}
	close(release)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if err := m.Shutdown(ctx2); err != nil {
		t.Fatal(err)
	}
}

func TestTaskOutputIsBounded(t *testing.T) {
	m, _, _ := newTestManager(t, func(o *Options) { o.MaxLines = 10; o.MaxBytes = 200 })
	s, _ := m.SubmitTask(taskReq("dev", "a:1", func(_ context.Context, emit func(string)) error {
		for i := 0; i < 1000; i++ {
			emit(fmt.Sprintf("line %d %s", i, strings.Repeat("x", 50)))
		}
		return nil
	}))
	got := waitState(t, m, s.ID, StateSucceeded)
	if !got.Truncated || got.DroppedLines < 990 || len(got.Output) > 10 {
		t.Fatalf("truncated=%v dropped=%d kept=%d", got.Truncated, got.DroppedLines, len(got.Output))
	}
}

func TestTaskSubscribeSeesLinesAndEnd(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	gate := make(chan struct{})
	s, _ := m.SubmitTask(taskReq("dev", "a:1", func(_ context.Context, emit func(string)) error {
		emit("first")
		<-gate
		emit("second")
		return nil
	}))
	var sub *Subscription
	eventually(t, "first line", func() bool {
		var err error
		sub, err = m.Subscribe(s.ID, 0)
		return err == nil && len(sub.Backlog) == 1
	})
	close(gate)
	var live []string
	for l := range sub.Lines() {
		live = append(live, l.Text)
	}
	if len(live) != 1 || live[0] != "second" || sub.Err() != nil {
		t.Fatalf("live = %q err = %v", live, sub.Err())
	}
}

func TestTaskConcurrentSubmitsRespectTheLimit(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	release := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, tooMany := 0, 0
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := m.SubmitTask(taskReq("dev", fmt.Sprintf("img:%d", i), func(ctx context.Context, _ func(string)) error {
				select {
				case <-release:
				case <-ctx.Done():
				}
				return nil
			}))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrTooManyTasks):
				tooMany++
			default:
				t.Errorf("err = %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(release)
	if ok != 2 || tooMany != 38 {
		t.Fatalf("accepted %d, refused %d", ok, tooMany)
	}
}

func TestTaskWithoutFunctionIsRefused(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	if _, err := m.SubmitTask(TaskRequest{Action: ActionImagePull, Profile: "dev"}); err == nil {
		t.Fatal("want error")
	}
}

func TestTaskClosingLineAfterCancelIsAlwaysRecorded(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	for i := 0; i < 200; i++ {
		started := make(chan struct{}, 1)
		s, err := m.SubmitTask(taskReq("dev", "a:1", func(ctx context.Context, emit func(string)) error {
			started <- struct{}{}
			<-ctx.Done()
			emit("closing")
			return ctx.Err()
		}))
		if err != nil {
			t.Fatal(err)
		}
		<-started
		got, err := m.Cancel(ctxTimeout(t, 3*time.Second), s.ID)
		if err != nil || len(got.Output) != 1 || got.Output[0].Text != "closing" {
			t.Fatalf("iteration %d: %+v, %v", i, got, err)
		}
		eventually(t, "released", func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.taskKeys) == 0 })
	}
}
