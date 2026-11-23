package colima

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeRunner records the argument vectors it receives and returns canned results.
type fakeRunner struct {
	calls  [][]string
	result Result
	err    error
}

func (f *fakeRunner) Run(_ context.Context, args []string) (Result, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	return f.result, f.err
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakecolima")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecRunnerCapturesOutputAndExitCode(t *testing.T) {
	bin := writeScript(t, `echo "out $1"; echo "err" >&2; exit 3`)
	r, err := NewExecRunner(bin)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("non-zero exit must be reported via Result, got %v", err)
	}
	if res.ExitCode != 3 || strings.TrimSpace(string(res.Stdout)) != "out hello" || strings.TrimSpace(string(res.Stderr)) != "err" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestExecRunnerNoShellInterpretation(t *testing.T) {
	bin := writeScript(t, `printf '%s' "$1"`)
	r, _ := NewExecRunner(bin)
	res, err := r.Run(context.Background(), []string{"a;touch pwned $(id)"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "a;touch pwned $(id)" {
		t.Errorf("argument was altered: %q", res.Stdout)
	}
}

func TestExecRunnerCapsOutput(t *testing.T) {
	bin := writeScript(t, `i=0; while [ $i -lt 2000 ]; do echo xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx; i=$((i+1)); done`)
	r, _ := NewExecRunner(bin)
	r.MaxOutput = 1024
	res, err := r.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != 1024 {
		t.Errorf("stdout len = %d, want 1024", len(res.Stdout))
	}
}

func TestNewExecRunnerMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := NewExecRunner("")
	var bnf *BinaryNotFoundError
	if !errors.As(err, &bnf) || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("got %v, want BinaryNotFoundError wrapping exec.ErrNotFound", err)
	}
}

func TestNewExecRunnerRejectsRelativePath(t *testing.T) {
	if _, err := NewExecRunner("./colima"); err == nil {
		t.Fatal("relative path must be rejected")
	}
}

func TestExecRunnerBinaryVanished(t *testing.T) {
	r, err := NewExecRunner(writeScript(t, "exit 0"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(r.binary)
	_, err = r.Run(context.Background(), nil)
	var bnf *BinaryNotFoundError
	if !errors.As(err, &bnf) {
		t.Fatalf("got %v, want BinaryNotFoundError", err)
	}
	if strings.Contains(err.Error(), os.TempDir()) {
		t.Errorf("error leaks path: %v", err)
	}
}

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// Proves that cancelling kills the whole process group: the script backgrounds
// a grandchild that would otherwise outlive it and hold the pipes open.
func TestExecRunnerCancelKillsProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	bin := writeScript(t, `sleep 300 &
echo $! > "`+pidFile+`"
wait`)
	r, _ := NewExecRunner(bin)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := r.Run(ctx, nil); done <- err }()

	var grandchild int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(b)) != "" {
			grandchild, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if grandchild == 0 {
		t.Fatal("grandchild never started")
	}
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	for i := 0; i < 100 && pidAlive(grandchild); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	// A killed child may linger as a zombie until reaped by init; kill(0) can
	// succeed for zombies, so also accept that state.
	if pidAlive(grandchild) {
		out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(grandchild)).Output()
		if !strings.HasPrefix(strings.TrimSpace(string(out)), "Z") && strings.TrimSpace(string(out)) != "" {
			t.Fatalf("grandchild %d survived cancel (stat %q)", grandchild, out)
		}
	}
}

func TestExecRunnerDeadline(t *testing.T) {
	r, _ := NewExecRunner(writeScript(t, "sleep 300"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := r.Run(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}
}

func TestExecRunnerAlreadyCancelled(t *testing.T) {
	r, _ := NewExecRunner(writeScript(t, "exit 0"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Run(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want Canceled", err)
	}
}
