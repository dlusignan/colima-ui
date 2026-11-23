package operations

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// fakeProc is a colima.Process whose lines, exit and cancel behaviour the test
// controls. Cancel kills it like the real adapter unless onCancel says
// otherwise.
type fakeProc struct {
	lines     chan string
	done      chan struct{}
	cancelled chan struct{}
	endOnce   sync.Once
	cancelOne sync.Once

	mu   sync.Mutex
	exit colima.Exit
	err  error

	onCancel func(p *fakeProc)
}

func newFakeProc() *fakeProc {
	p := &fakeProc{
		lines:     make(chan string, 256),
		done:      make(chan struct{}),
		cancelled: make(chan struct{}),
	}
	p.onCancel = func(p *fakeProc) { p.kill() }
	return p
}

func (p *fakeProc) Lines() <-chan string { return p.lines }

func (p *fakeProc) Wait() (colima.Exit, error) {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exit, p.err
}

func (p *fakeProc) Cancel() {
	p.cancelOne.Do(func() { close(p.cancelled) })
	p.onCancel(p)
}

func (p *fakeProc) emit(s string) { p.lines <- s }

// end closes Lines and finishes Wait with the given result (first call wins).
func (p *fakeProc) end(exit colima.Exit, err error) {
	p.endOnce.Do(func() {
		p.mu.Lock()
		p.exit, p.err = exit, err
		p.mu.Unlock()
		close(p.lines)
		close(p.done)
	})
}

func (p *fakeProc) exitWith(code int) { p.end(colima.Exit{ExitCode: code}, nil) }

func (p *fakeProc) kill() {
	p.end(colima.Exit{ExitCode: -1, Killed: true}, fmt.Errorf("colima: %w", context.Canceled))
}

func (p *fakeProc) wasCancelled() bool {
	select {
	case <-p.cancelled:
		return true
	default:
		return false
	}
}

// fakeLauncher hands out fakeProcs. Like the real adapter it cancels the
// process when the launch context ends.
type fakeLauncher struct {
	mu      sync.Mutex
	reqs    []Request
	made    []*fakeProc
	err     error
	panicV  any
	entered chan struct{} // if set, signalled on entry to Launch
	gate    chan struct{} // if set, Launch blocks until it is closed
	setup   func(p *fakeProc)
}

func (l *fakeLauncher) Launch(ctx context.Context, r Request) (colima.Process, error) {
	l.mu.Lock()
	l.reqs = append(l.reqs, r)
	entered, gate, err, pv := l.entered, l.gate, l.err, l.panicV
	l.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if gate != nil {
		<-gate
	}
	if pv != nil {
		panic(pv)
	}
	if err != nil {
		return nil, err
	}
	p := newFakeProc()
	if l.setup != nil {
		l.setup(p)
	}
	l.mu.Lock()
	l.made = append(l.made, p)
	l.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			p.Cancel()
		case <-p.done:
		}
	}()
	return p, nil
}

func (l *fakeLauncher) proc(i int) *fakeProc {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.made[i]
}

func (l *fakeLauncher) last() *fakeProc {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.made[len(l.made)-1]
}

func (l *fakeLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.made)
}

// fakeClock is a controllable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// seqIDs returns a deterministic ID generator.
func seqIDs() func() string {
	var mu sync.Mutex
	n := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("j_%06x", n)
	}
}

// leakCheck fails the test if goroutines started during it are still alive
// shortly after it ends. Call it before creating anything.
func leakCheck(t *testing.T) {
	t.Helper()
	base := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for runtime.NumGoroutine() > base {
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<16)
				buf = buf[:runtime.Stack(buf, true)]
				t.Errorf("goroutine leak: %d now, %d before\n%s", runtime.NumGoroutine(), base, buf)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}

// newTestManager builds a Manager with a fake launcher, a fake clock and
// sequential IDs, and shuts it down at the end of the test.
func newTestManager(t *testing.T, mod func(*Options)) (*Manager, *fakeLauncher, *fakeClock) {
	t.Helper()
	leakCheck(t)
	l := &fakeLauncher{}
	clk := newFakeClock()
	opts := Options{Launcher: l, Now: clk.Now, NewID: seqIDs()}
	if mod != nil {
		mod(&opts)
	}
	m := New(opts)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.Shutdown(ctx); err != nil {
			t.Errorf("cleanup shutdown: %v", err)
		}
	})
	return m, l, clk
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitState(t *testing.T, m *Manager, id string, want State) JobSummary {
	t.Helper()
	var s JobSummary
	eventually(t, fmt.Sprintf("job %s to be %s", id, want), func() bool {
		var err error
		s, err = m.Get(id)
		return err == nil && s.State == want
	})
	return s
}

func mustSubmit(t *testing.T, m *Manager, profile string) JobSummary {
	t.Helper()
	s, err := m.Submit(Request{Action: ActionStart, Profile: profile})
	if err != nil {
		t.Fatalf("Submit(%s): %v", profile, err)
	}
	return s
}

func ctxTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// recObserver records everything it is told.
type recObserver struct {
	mu       sync.Mutex
	lines    []JobLine
	finished []JobSummary
	onLine   func(JobLine)
	onFinish func(JobSummary)
}

func (o *recObserver) JobOutput(l JobLine) {
	o.mu.Lock()
	o.lines = append(o.lines, l)
	f := o.onLine
	o.mu.Unlock()
	if f != nil {
		f(l)
	}
}

func (o *recObserver) JobFinished(s JobSummary) {
	o.mu.Lock()
	o.finished = append(o.finished, s)
	f := o.onFinish
	o.mu.Unlock()
	if f != nil {
		f(s)
	}
}

func (o *recObserver) finishedCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.finished)
}
