package colima

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

// Streamer starts a colima command and hands back a live handle instead of
// buffering until exit. It is separate from Runner so the read-side fakes stay
// untouched. Fake it in tests of callers (the Operation Manager does).
type Streamer interface {
	Stream(ctx context.Context, args []string) (Process, error)
}

// Process is a running colima command.
//
// Lines delivers merged stdout and stderr, in the order the child wrote them,
// split on \n, \r and \r\n (so \r progress updates arrive as separate lines).
// Lines are raw: ANSI escapes are kept. A line longer than MaxLineBytes is
// truncated. The channel is closed exactly once, after the process has been
// reaped and its output drained.
//
// The channel is bounded, so a consumer that stops reading eventually holds
// the child back. Cancel always releases it: once cancelled, or once the
// output cap is reached, remaining lines are dropped instead of sent. A
// caller that abandons a Process must therefore call Cancel.
//
// Wait blocks until the process has ended and Lines is closed, then returns
// the same result on every call, from any goroutine.
//
// Cancel is safe to call from any goroutine, any number of times, before or
// after the process ends. It has the same effect as cancelling the context
// given to Stream.
type Process interface {
	Lines() <-chan string
	Wait() (Exit, error)
	Cancel()
}

// Exit is how a process ended.
//
// A non-zero exit is not an error: it is reported here with a nil error from
// Wait. Killed is true when the process was ended by a signal or by
// cancellation, in which case ExitCode is -1 and Code returns nil.
type Exit struct {
	ExitCode int
	Killed   bool
	// Tail is the last TailLines lines of output. Unlike the channel it is
	// kept current past the output cap. It can contain absolute paths, so it
	// is for logs, never for error text.
	Tail []string
}

// Code returns the exit code, or nil if the process was killed.
func (e Exit) Code() *int {
	if e.Killed {
		return nil
	}
	c := e.ExitCode
	return &c
}

const (
	// TailLines is how many trailing output lines Exit.Tail keeps.
	TailLines = 50
	// MaxLineBytes caps one delivered line; the rest of a longer line is
	// discarded up to its terminator.
	MaxLineBytes = 64 << 10
	// linesBuffer is the bounded channel capacity.
	linesBuffer = 256
)

var _ Streamer = (*ExecRunner)(nil)

// Stream implements Streamer. Errors from starting the process are typed
// (BinaryNotFoundError) or wrap ctx.Err(); a non-zero exit is reported by
// Process.Wait. The context bounds the process: cancelling it kills the whole
// process group.
func (r *ExecRunner) Stream(ctx context.Context, args []string) (Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("colima: %w", err)
	}
	// One pipe for both stdout and stderr: the kernel keeps their order.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("colima: cannot run process: %w", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cctx, r.binary, args...)
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.WaitDelay = killWaitDelay
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		cancel()
		return nil, mapProcessError(err)
	}
	_ = pw.Close() // the child holds its own copy

	p := &streamProc{
		lines:  make(chan string, linesBuffer),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	pm := &pump{ctx: cctx, pr: pr, lines: p.lines, max: r.MaxOutput}
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		pm.run()
	}()
	go func() {
		werr := cmd.Wait()
		// The process is gone. Drain what is buffered, but do not wait on a
		// daemonised grandchild that still holds the pipe open.
		pm.exited.Store(true)
		pm.armDeadline()
		<-pumpDone
		_ = pr.Close()
		p.exit, p.err = classifyWait(cctx, werr)
		p.exit.Tail = pm.tail()
		cancel()
		close(p.lines)
		close(p.done)
	}()
	return p, nil
}

func classifyWait(ctx context.Context, werr error) (Exit, error) {
	if cerr := ctx.Err(); cerr != nil {
		return Exit{ExitCode: -1, Killed: true}, fmt.Errorf("colima: %w", cerr)
	}
	if werr == nil {
		return Exit{}, nil
	}
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return Exit{ExitCode: code}, nil
		}
		return Exit{ExitCode: -1, Killed: true}, nil
	}
	return Exit{ExitCode: -1, Killed: true}, mapProcessError(werr)
}

type streamProc struct {
	lines  chan string
	cancel context.CancelFunc
	done   chan struct{}
	// exit and err are written once before done is closed.
	exit Exit
	err  error
}

func (p *streamProc) Lines() <-chan string { return p.lines }
func (p *streamProc) Cancel()              { p.cancel() }

func (p *streamProc) Wait() (Exit, error) {
	<-p.done
	ex := p.exit
	ex.Tail = append([]string(nil), p.exit.Tail...)
	return ex, p.err
}

// pump splits the merged output into lines, keeps the tail and forwards lines
// until cancelled or the byte cap is reached. It always keeps reading so the
// child never blocks on a full pipe.
type pump struct {
	ctx    context.Context
	pr     *os.File
	lines  chan<- string
	max    int
	sent   int
	capped bool
	exited atomic.Bool

	ring []string
	next int
	full bool
}

func (m *pump) armDeadline() {
	// Best effort: where the pipe does not support deadlines the reader just
	// relies on EOF.
	_ = m.pr.SetReadDeadline(time.Now().Add(killWaitDelay))
}

func (m *pump) run() {
	br := bufio.NewReaderSize(m.pr, 32<<10)
	var buf []byte
	sawCR := false
	for {
		b, err := br.ReadByte()
		if err != nil { // EOF, deadline after exit, or closed
			if len(buf) > 0 {
				m.emit(string(buf))
			}
			return
		}
		switch b {
		case '\r':
			sawCR = true
			if len(buf) > 0 {
				m.emit(string(buf))
				buf = buf[:0]
			}
		case '\n':
			if sawCR {
				sawCR = false
				continue
			}
			m.emit(string(buf))
			buf = buf[:0]
		default:
			sawCR = false
			if len(buf) < MaxLineBytes {
				buf = append(buf, b)
			}
		}
	}
}

func (m *pump) emit(line string) {
	if m.ring == nil {
		m.ring = make([]string, TailLines)
	}
	m.ring[m.next] = line
	m.next = (m.next + 1) % TailLines
	if m.next == 0 {
		m.full = true
	}

	if m.capped || m.ctx.Err() != nil {
		return
	}
	if m.sent+len(line)+1 > m.max {
		m.capped = true
		return
	}
	m.sent += len(line) + 1
	select {
	case m.lines <- line:
		return
	default:
	}
	// The consumer is behind: wait for room, but never past cancellation.
	select {
	case m.lines <- line:
	case <-m.ctx.Done():
	}
	if m.exited.Load() {
		m.armDeadline() // time spent blocked on the consumer is not the child's
	}
}

// tail returns the retained lines, oldest first. Call only after run returned.
func (m *pump) tail() []string {
	if m.ring == nil {
		return nil
	}
	if !m.full {
		return append([]string(nil), m.ring[:m.next]...)
	}
	out := make([]string, 0, TailLines)
	out = append(out, m.ring[m.next:]...)
	return append(out, m.ring[:m.next]...)
}
