package colima

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProcess is a scripted Process. Cancel finishes it as killed.
type fakeProcess struct {
	lines  chan string
	exit   Exit
	err    error
	once   sync.Once
	cancel chan struct{}
	done   chan struct{}
}

func newFakeProcess(lines []string, exit Exit, err error) *fakeProcess {
	p := &fakeProcess{lines: make(chan string, len(lines)), exit: exit, err: err,
		cancel: make(chan struct{}), done: make(chan struct{})}
	for _, l := range lines {
		p.lines <- l
	}
	close(p.lines)
	close(p.done)
	return p
}

// newBlockingFakeProcess runs until Cancel, then closes as killed.
func newBlockingFakeProcess() *fakeProcess {
	p := &fakeProcess{lines: make(chan string), cancel: make(chan struct{}), done: make(chan struct{})}
	go func() {
		<-p.cancel
		p.exit = Exit{ExitCode: -1, Killed: true}
		p.err = context.Canceled
		close(p.lines)
		close(p.done)
	}()
	return p
}

func (p *fakeProcess) Lines() <-chan string { return p.lines }
func (p *fakeProcess) Cancel()              { p.once.Do(func() { close(p.cancel) }) }
func (p *fakeProcess) Wait() (Exit, error)  { <-p.done; return p.exit, p.err }

// fakeStreamer records argument vectors and hands out a prepared process.
type fakeStreamer struct {
	calls [][]string
	proc  Process
	err   error
}

func (f *fakeStreamer) Stream(_ context.Context, args []string) (Process, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.err != nil {
		return nil, f.err
	}
	return f.proc, nil
}

func (f *fakeStreamer) argv(t *testing.T) string {
	t.Helper()
	if len(f.calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(f.calls))
	}
	return strings.Join(f.calls[0], " ")
}

func TestStartBuildsArgvFromTypedFields(t *testing.T) {
	f := &fakeStreamer{proc: newFakeProcess([]string{"starting"}, Exit{}, nil)}
	p, err := Start(context.Background(), f, "synthetic-dev", StartOptions{CPUs: ip(2), Runtime: RuntimeContainerd, Kubernetes: true}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.argv(t); got != "start -p synthetic-dev --cpus 2 --runtime containerd --kubernetes" {
		t.Fatalf("argv = %s", got)
	}
	if lines := collectFake(p); len(lines) != 1 || lines[0] != "starting" {
		t.Fatalf("lines = %q", lines)
	}
}

func TestStartMinimal(t *testing.T) {
	f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
	if _, err := Start(context.Background(), f, DefaultProfile, StartOptions{}, Limits{}); err != nil {
		t.Fatal(err)
	}
	if got := f.argv(t); got != "start -p default" {
		t.Fatalf("argv = %s", got)
	}
}

func TestStopAndRestartArgv(t *testing.T) {
	cases := []struct {
		name string
		call func(Streamer) (Process, error)
		want string
	}{
		{"stop", func(s Streamer) (Process, error) { return Stop(context.Background(), s, "synthetic-a", false) }, "stop -p synthetic-a"},
		{"stop force", func(s Streamer) (Process, error) { return Stop(context.Background(), s, "synthetic-a", true) }, "stop -p synthetic-a --force"},
		{"restart", func(s Streamer) (Process, error) { return Restart(context.Background(), s, "synthetic-a", false) }, "restart -p synthetic-a"},
		{"update", func(s Streamer) (Process, error) { return Update(context.Background(), s, "synthetic-a") }, "update -p synthetic-a"},
		{"restart force", func(s Streamer) (Process, error) { return Restart(context.Background(), s, "synthetic-a", true) }, "restart -p synthetic-a --force"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
			if _, err := c.call(f); err != nil {
				t.Fatal(err)
			}
			if got := f.argv(t); got != c.want {
				t.Fatalf("argv = %s, want %s", got, c.want)
			}
		})
	}
}

func TestProfileIsOneArgvElement(t *testing.T) {
	f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
	if _, err := Stop(context.Background(), f, "a.b_c-1", false); err != nil {
		t.Fatal(err)
	}
	if len(f.calls[0]) != 3 || f.calls[0][1] != "-p" || f.calls[0][2] != "a.b_c-1" {
		t.Fatalf("argv = %q", f.calls[0])
	}
}

func TestInvalidInputNeverStartsAProcess(t *testing.T) {
	bad := []string{"", "-rf", "--edit", "a b", "a;b", "../x", strings.Repeat("a", 65), "a\n"}
	for _, name := range bad {
		f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
		var ipe *InvalidProfileError
		if _, err := Start(context.Background(), f, name, StartOptions{}, testLimits); !errors.As(err, &ipe) {
			t.Errorf("Start(%q): got %v", name, err)
		}
		if _, err := Stop(context.Background(), f, name, false); !errors.As(err, &ipe) {
			t.Errorf("Stop(%q): got %v", name, err)
		}
		if _, err := Restart(context.Background(), f, name, false); !errors.As(err, &ipe) {
			t.Errorf("Restart(%q): got %v", name, err)
		}
		if len(f.calls) != 0 {
			t.Errorf("%q: process started for an invalid profile", name)
		}
	}
	f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
	var ioe *InvalidOptionError
	if _, err := Start(context.Background(), f, "ok", StartOptions{CPUs: ip(99)}, testLimits); !errors.As(err, &ioe) {
		t.Fatalf("got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("process started for invalid options")
	}
}

func TestNilStreamerIsAnError(t *testing.T) {
	if _, err := Stop(context.Background(), nil, "ok", false); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStreamErrorsPropagate(t *testing.T) {
	f := &fakeStreamer{err: &BinaryNotFoundError{Err: errors.New("x")}}
	var bnf *BinaryNotFoundError
	if _, err := Restart(context.Background(), f, "ok", false); !errors.As(err, &bnf) {
		t.Fatalf("got %v", err)
	}
}

func TestSuccessAndNonZeroExitThroughFake(t *testing.T) {
	ok := &fakeStreamer{proc: newFakeProcess([]string{"a", "b"}, Exit{Tail: []string{"a", "b"}}, nil)}
	p, _ := Stop(context.Background(), ok, "ok", false)
	collectFake(p)
	if ex, err := p.Wait(); err != nil || ex.ExitCode != 0 || ex.Killed {
		t.Fatalf("exit = %+v err = %v", ex, err)
	}
	bad := &fakeStreamer{proc: newFakeProcess([]string{"fail"}, Exit{ExitCode: 1, Tail: []string{"fail"}}, nil)}
	p, _ = Stop(context.Background(), bad, "ok", false)
	collectFake(p)
	ex, err := p.Wait()
	if err != nil || ex.ExitCode != 1 || ex.Killed || ex.Code() == nil || len(ex.Tail) != 1 {
		t.Fatalf("exit = %+v err = %v", ex, err)
	}
}

func TestCancellationThroughFake(t *testing.T) {
	f := &fakeStreamer{proc: newBlockingFakeProcess()}
	p, err := Start(context.Background(), f, "ok", StartOptions{}, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	p.Cancel()
	p.Cancel()
	done := make(chan struct{})
	go func() { collectFake(p); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("channel not closed")
	}
	ex, err := p.Wait()
	if !errors.Is(err, context.Canceled) || !ex.Killed {
		t.Fatalf("exit = %+v err = %v", ex, err)
	}
}

func collectFake(p Process) []string {
	var out []string
	for l := range p.Lines() {
		out = append(out, l)
	}
	return out
}

func TestDeleteArgv(t *testing.T) {
	for _, c := range []struct {
		data bool
		want string
	}{
		{false, "delete -p synthetic-a --force"},
		{true, "delete -p synthetic-a --force --data"},
	} {
		f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
		if _, err := Delete(context.Background(), f, "synthetic-a", c.data); err != nil {
			t.Fatal(err)
		}
		if got := f.argv(t); got != c.want {
			t.Fatalf("argv = %s, want %s", got, c.want)
		}
	}
}

func TestDeleteRejectsHostileNamesAndNilStreamer(t *testing.T) {
	for _, name := range []string{"", "../x", "a b", "-p", "--force", "a;b", "$(x)", "a\nb"} {
		f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
		var ipe *InvalidProfileError
		if _, err := Delete(context.Background(), f, name, true); !errors.As(err, &ipe) {
			t.Errorf("Delete(%q): got %v", name, err)
		}
		if len(f.calls) != 0 {
			t.Errorf("Delete(%q) reached the streamer", name)
		}
	}
	if _, err := Delete(context.Background(), nil, "ok", false); err == nil {
		t.Error("nil streamer: want error")
	}
}

func TestPruneArgvAlwaysForcesAndAddsAllOnlyWhenAsked(t *testing.T) {
	for _, c := range []struct {
		all  bool
		want string
	}{{false, "prune --force"}, {true, "prune --force --all"}} {
		f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
		if _, err := Prune(context.Background(), f, c.all); err != nil {
			t.Fatal(err)
		}
		if got := f.argv(t); got != c.want {
			t.Fatalf("all=%v argv = %q, want %q", c.all, got, c.want)
		}
	}
}

func TestPruneWithoutStreamerFails(t *testing.T) {
	if _, err := Prune(context.Background(), nil, false); err == nil {
		t.Fatal("want an error")
	}
}

func TestPruneReturnsStreamError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Prune(context.Background(), &fakeStreamer{err: boom}, false); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
