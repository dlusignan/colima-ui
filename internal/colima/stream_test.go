package colima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var _ Streamer = (*ExecRunner)(nil)

func newStreamRunner(t *testing.T, body string) *ExecRunner {
	t.Helper()
	r, err := NewExecRunner(writeScript(t, body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// collect drains the channel, failing the test if it does not close in time.
func collect(t *testing.T, p Process) []string {
	t.Helper()
	var got []string
	timeout := time.After(20 * time.Second)
	for {
		select {
		case l, ok := <-p.Lines():
			if !ok {
				return got
			}
			got = append(got, l)
		case <-timeout:
			t.Fatal("Lines did not close")
		}
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStreamDeliversLinesWhileRunning(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	r := newStreamRunner(t, `echo first
while [ ! -f "`+gate+`" ]; do sleep 0.02; done
echo second`)
	p, err := r.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case l := <-p.Lines():
		if l != "first" {
			t.Fatalf("got %q", l)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first line did not arrive while the process was still running")
	}
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if rest := collect(t, p); len(rest) != 1 || rest[0] != "second" {
		t.Fatalf("rest = %q", rest)
	}
	ex, err := p.Wait()
	if err != nil || ex.ExitCode != 0 || ex.Killed {
		t.Fatalf("exit = %+v err = %v", ex, err)
	}
}

func TestStreamMergesStdoutAndStderrInOrder(t *testing.T) {
	r := newStreamRunner(t, `echo a; echo b >&2; echo c; echo d >&2`)
	p, err := r.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(collect(t, p), ","); got != "a,b,c,d" {
		t.Fatalf("got %s", got)
	}
}

func TestStreamPassesArgsWithoutShell(t *testing.T) {
	r := newStreamRunner(t, `printf '%s\n' "$1"`)
	p, err := r.Stream(context.Background(), []string{"a;touch pwned $(id)"})
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, p); len(got) != 1 || got[0] != "a;touch pwned $(id)" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamNonZeroExitIsNotKilled(t *testing.T) {
	r := newStreamRunner(t, `echo boom; exit 3`)
	p, _ := r.Stream(context.Background(), nil)
	collect(t, p)
	ex, err := p.Wait()
	if err != nil {
		t.Fatalf("non-zero exit must not be an error, got %v", err)
	}
	if ex.ExitCode != 3 || ex.Killed {
		t.Fatalf("exit = %+v", ex)
	}
	if c := ex.Code(); c == nil || *c != 3 {
		t.Fatalf("Code() = %v", c)
	}
	if len(ex.Tail) != 1 || ex.Tail[0] != "boom" {
		t.Fatalf("tail = %q", ex.Tail)
	}
}

func TestStreamSignalledProcessHasNoExitCode(t *testing.T) {
	r := newStreamRunner(t, `echo about to die; kill -9 $$`)
	p, _ := r.Stream(context.Background(), nil)
	collect(t, p)
	ex, err := p.Wait()
	if err != nil {
		t.Fatalf("got %v", err)
	}
	if !ex.Killed || ex.Code() != nil {
		t.Fatalf("exit = %+v, want killed with nil Code()", ex)
	}
}

func TestStreamTailKeepsLastLines(t *testing.T) {
	r := newStreamRunner(t, `i=1; while [ $i -le 120 ]; do echo "line $i"; i=$((i+1)); done`)
	p, _ := r.Stream(context.Background(), nil)
	got := collect(t, p)
	if len(got) != 120 {
		t.Fatalf("delivered %d lines", len(got))
	}
	ex, _ := p.Wait()
	if len(ex.Tail) != TailLines || ex.Tail[0] != "line 71" || ex.Tail[len(ex.Tail)-1] != "line 120" {
		t.Fatalf("tail = %d lines, first %q last %q", len(ex.Tail), ex.Tail[0], ex.Tail[len(ex.Tail)-1])
	}
}

func TestStreamCarriageReturnProgressLines(t *testing.T) {
	r := newStreamRunner(t, `printf 'p1\rp2\rdone\r\nnext\n\n'; printf 'tail-no-newline'`)
	p, _ := r.Stream(context.Background(), nil)
	got := strings.Join(collect(t, p), "|")
	if got != "p1|p2|done|next||tail-no-newline" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamLineLongerThan64KiB(t *testing.T) {
	r := newStreamRunner(t, `head -c 300000 /dev/zero | tr '\0' x; echo; echo next`)
	p, _ := r.Stream(context.Background(), nil)
	got := collect(t, p)
	if len(got) != 2 || got[1] != "next" {
		t.Fatalf("got %d lines, want the long line then next", len(got))
	}
	if len(got[0]) == 0 || len(got[0]) > MaxLineBytes || strings.Trim(got[0], "x") != "" {
		t.Fatalf("long line length %d", len(got[0]))
	}
	if MaxLineBytes < 64<<10 {
		t.Fatalf("MaxLineBytes %d is below 64 KiB", MaxLineBytes)
	}
}

func TestStreamOutputCap(t *testing.T) {
	r := newStreamRunner(t, `i=1; while [ $i -le 2000 ]; do echo "line $i"; i=$((i+1)); done`)
	r.MaxOutput = 1000
	p, _ := r.Stream(context.Background(), nil)
	got := collect(t, p)
	total := 0
	for _, l := range got {
		total += len(l) + 1
	}
	if total > 1000 || len(got) == 0 || len(got) >= 2000 {
		t.Fatalf("forwarded %d lines / %d bytes with cap 1000", len(got), total)
	}
	ex, err := p.Wait()
	if err != nil || ex.ExitCode != 0 {
		t.Fatalf("exit = %+v err = %v", ex, err)
	}
	if last := ex.Tail[len(ex.Tail)-1]; last != "line 2000" {
		t.Fatalf("tail must stay current past the cap, last = %q", last)
	}
}

func TestStreamBinaryNotFound(t *testing.T) {
	r := &ExecRunner{binary: filepath.Join(t.TempDir(), "missing"), MaxOutput: DefaultMaxOutput}
	_, err := r.Stream(context.Background(), nil)
	var bnf *BinaryNotFoundError
	if !errors.As(err, &bnf) {
		t.Fatalf("got %v, want BinaryNotFoundError", err)
	}
	if strings.Contains(err.Error(), os.TempDir()) {
		t.Errorf("error leaks path: %v", err)
	}
}

func TestStreamContextAlreadyDone(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	r := newStreamRunner(t, `touch "`+marker+`"`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Stream(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("process started despite a cancelled context")
	}
}

// startGrandchildScript backgrounds a sleep, records its pid and waits.
func startGrandchildScript(t *testing.T) (*ExecRunner, string) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	return newStreamRunner(t, `sleep 300 &
echo $! > "`+pidFile+`"
echo started
wait`), pidFile
}

func readPid(t *testing.T, pidFile string) int {
	t.Helper()
	var pid int
	waitFor(t, func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		pid = n
		return err == nil && n > 0
	}, "grandchild pid")
	return pid
}

func TestStreamCancelKillsProcessGroupAndClosesChannel(t *testing.T) {
	r, pidFile := startGrandchildScript(t)
	p, err := r.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pid := readPid(t, pidFile)
	t.Cleanup(func() { killPid(pid) })
	p.Cancel()
	done := make(chan struct{})
	go func() { collect(t, p); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("channel not closed after Cancel")
	}
	ex, err := p.Wait()
	if !errors.Is(err, context.Canceled) || !ex.Killed || ex.Code() != nil {
		t.Fatalf("exit = %+v err = %v", ex, err)
	}
	waitFor(t, func() bool { return !pidRunning(pid) }, "grandchild to die")
}

func TestStreamContextCancelKillsProcessGroup(t *testing.T) {
	r, pidFile := startGrandchildScript(t)
	ctx, cancel := context.WithCancel(context.Background())
	p, _ := r.Stream(ctx, nil)
	pid := readPid(t, pidFile)
	t.Cleanup(func() { killPid(pid) })
	cancel()
	collect(t, p)
	if _, err := p.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	waitFor(t, func() bool { return !pidRunning(pid) }, "grandchild to die")
}

func TestStreamDeadlineIsDistinguishable(t *testing.T) {
	r := newStreamRunner(t, `sleep 300`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	p, _ := r.Stream(ctx, nil)
	collect(t, p)
	if _, err := p.Wait(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestStreamCancelTwiceAndConcurrently(t *testing.T) {
	r := newStreamRunner(t, `sleep 300`)
	p, _ := r.Stream(context.Background(), nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.Cancel(); p.Cancel() }()
	}
	wg.Wait()
	collect(t, p)
	p.Cancel() // after the process is gone
	if _, err := p.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestStreamWaitAfterCloseAndRepeatable(t *testing.T) {
	r := newStreamRunner(t, `echo x; exit 2`)
	p, _ := r.Stream(context.Background(), nil)
	collect(t, p)
	a, errA := p.Wait()
	b, errB := p.Wait()
	if errA != nil || errB != nil || a.ExitCode != 2 || b.ExitCode != 2 {
		t.Fatalf("a=%+v/%v b=%+v/%v", a, errA, b, errB)
	}
	// The channel stays closed.
	if _, ok := <-p.Lines(); ok {
		t.Fatal("channel reopened")
	}
	// Wait from several goroutines at once.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = p.Wait() }()
	}
	wg.Wait()
}

func TestStreamExitTailIsACopy(t *testing.T) {
	r := newStreamRunner(t, `echo one`)
	p, _ := r.Stream(context.Background(), nil)
	collect(t, p)
	a, _ := p.Wait()
	a.Tail[0] = "mutated"
	b, _ := p.Wait()
	if b.Tail[0] != "one" {
		t.Fatalf("Wait result shares memory: %q", b.Tail)
	}
}

// A consumer that reads slowly sees every line in order: the producer is
// back-pressured by the bounded channel, never loses lines.
func TestStreamSlowConsumerGetsEverythingInOrder(t *testing.T) {
	r := newStreamRunner(t, `i=1; while [ $i -le 1500 ]; do echo "n $i"; i=$((i+1)); done`)
	p, _ := r.Stream(context.Background(), nil)
	time.Sleep(300 * time.Millisecond) // let the channel fill and the pipe back up
	var got []string
	for l := range p.Lines() {
		got = append(got, l)
	}
	if len(got) != 1500 {
		t.Fatalf("got %d lines", len(got))
	}
	for i, l := range got {
		if l != fmt.Sprintf("n %d", i+1) {
			t.Fatalf("line %d = %q", i, l)
		}
	}
}

// A consumer that stops reading must not be able to wedge Cancel or leak
// goroutines: sends give up once the process is cancelled.
func TestStreamStalledConsumerCannotDeadlockCancel(t *testing.T) {
	before := runtime.NumGoroutine()
	r := newStreamRunner(t, `while true; do echo spam; done`)
	p, _ := r.Stream(context.Background(), nil)
	<-p.Lines() // read one line, then stop reading
	time.Sleep(300 * time.Millisecond)
	cancelled := make(chan struct{})
	go func() { p.Cancel(); close(cancelled) }()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel blocked")
	}
	done := make(chan struct{})
	go func() { _, _ = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait blocked after Cancel with a stalled consumer")
	}
	// Remaining buffered lines are still drainable and the channel is closed.
	for range p.Lines() {
	}
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before }, "goroutines to exit")
}

func TestStreamNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	r := newStreamRunner(t, `echo hi`)
	slow := newStreamRunner(t, `sleep 300`)
	for i := 0; i < 5; i++ {
		p, _ := r.Stream(context.Background(), nil)
		collect(t, p)
		_, _ = p.Wait()
		q, _ := slow.Stream(context.Background(), nil)
		q.Cancel()
		collect(t, q)
		_, _ = q.Wait()
	}
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before }, "goroutines to exit")
}

// A daemonised grandchild that keeps the output pipe open must not keep the
// stream open once the main process has exited.
func TestStreamOrphanHoldingPipeDoesNotHangForever(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	r := newStreamRunner(t, `sleep 300 &
echo $! > "`+pidFile+`"
echo parent done`)
	p, _ := r.Stream(context.Background(), nil)
	pid := readPid(t, pidFile)
	t.Cleanup(func() { killPid(pid) })
	start := time.Now()
	got := collect(t, p)
	if len(got) != 1 || got[0] != "parent done" {
		t.Fatalf("got %q", got)
	}
	if d := time.Since(start); d > 3*killWaitDelay+2*time.Second {
		t.Fatalf("took %v", d)
	}
	if ex, err := p.Wait(); err != nil || ex.ExitCode != 0 {
		t.Fatalf("exit = %+v err = %v", ex, err)
	}
}
