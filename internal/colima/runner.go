package colima

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Result is the captured outcome of one colima invocation.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner executes colima with a fixed argument vector. The binary is bound
// when the Runner is built, so callers (and fakes) never see or choose it.
//
// A non-zero exit is reported through Result.ExitCode with a nil error. A
// non-nil error means the process could not run (BinaryNotFoundError) or the
// context ended (the error wraps ctx.Err()).
type Runner interface {
	Run(ctx context.Context, args []string) (Result, error)
}

const (
	// DefaultMaxOutput caps captured stdout and stderr, each, in bytes.
	DefaultMaxOutput = 4 << 20
	// killWaitDelay bounds how long Run waits for pipes to close after the
	// process group has been killed.
	killWaitDelay = 2 * time.Second
	binaryName    = "colima"
)

// ExecRunner is the only Runner that starts a real process. It never uses a
// shell.
type ExecRunner struct {
	binary string
	// MaxOutput caps each of stdout and stderr; excess is discarded.
	MaxOutput int
}

var _ Runner = (*ExecRunner)(nil)

// NewExecRunner binds a runner to a colima binary. An empty binary means
// "look up colima on PATH"; otherwise binary must be an absolute path.
func NewExecRunner(binary string) (*ExecRunner, error) {
	if binary == "" {
		p, err := exec.LookPath(binaryName)
		if err != nil {
			return nil, &BinaryNotFoundError{Err: exec.ErrNotFound}
		}
		binary = p
	} else if !filepath.IsAbs(binary) {
		return nil, errors.New("colima binary path must be absolute")
	}
	return &ExecRunner{binary: binary, MaxOutput: DefaultMaxOutput}, nil
}

// Run implements Runner.
func (r *ExecRunner) Run(ctx context.Context, args []string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("colima: %w", err)
	}
	cmd := exec.CommandContext(ctx, r.binary, args...)
	stdout := &cappedBuffer{max: r.MaxOutput}
	stderr := &cappedBuffer{max: r.MaxOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = killWaitDelay
	configureProcessGroup(cmd)

	err := cmd.Run()
	res := Result{Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes()}
	if cerr := ctx.Err(); cerr != nil {
		return res, fmt.Errorf("colima: %w", cerr)
	}
	if err == nil {
		return res, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	// Strip the path that exec embeds in its errors.
	cause := err
	var pe *fs.PathError
	if errors.As(err, &pe) {
		cause = pe.Err
	}
	if errors.Is(cause, fs.ErrNotExist) || errors.Is(cause, exec.ErrNotFound) || errors.Is(cause, syscall.ENOENT) {
		return res, &BinaryNotFoundError{Err: exec.ErrNotFound}
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return res, fmt.Errorf("colima: %w", exec.ErrWaitDelay)
	}
	return res, fmt.Errorf("colima: cannot run process: %w", cause)
}

// cappedBuffer keeps at most max bytes and silently drops the rest, always
// reporting a full write so the child is never blocked on a full pipe.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		c.buf.Write(p[:room])
	}
	return len(p), nil
}
