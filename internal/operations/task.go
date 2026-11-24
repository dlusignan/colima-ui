package operations

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// TaskFunc is the work of a task job. It must stop when ctx ends. emit adds one
// output line to the job; it never blocks past ctx. A nil return means the task
// succeeded, an error from a task whose ctx ended means cancelled, any other
// error means failed. A panic is recovered and fails the task.
type TaskFunc func(ctx context.Context, emit func(line string)) error

// TaskRequest starts a job that is not a colima process, such as an image pull.
// Task jobs share the job table, output capture, cancel, subscriptions,
// observers and shutdown of colima jobs, but they do NOT take the profile's
// slot: a pull must not block start, stop or restart, and the slot is not meant
// for Docker work. They are limited on their own instead.
type TaskRequest struct {
	Action  Action
	Profile string
	// Subject names what the task works on. The submitter validates it. At
	// most one running task per profile, action and subject exists.
	Subject string
	// MaxPerProfile is how many tasks of this action may run on this profile at
	// once. Zero or negative means no limit.
	MaxPerProfile int
	Run           TaskFunc
}

func taskGroupKey(profile string, a Action) string {
	return slotKey(profile) + "\x00" + string(a)
}

// SubmitTask starts a task job. It fails with ErrClosed after Shutdown,
// ErrDuplicateTask when the same subject is already running on the profile, and
// ErrTooManyTasks when the profile already runs MaxPerProfile tasks of the
// action. The limits are reserved under the manager mutex and released when the
// job's final state is recorded, whatever way it ended. The task runs under the
// manager's own context, never the caller's.
func (m *Manager) SubmitTask(r TaskRequest) (JobSummary, error) {
	if r.Run == nil {
		return JobSummary{}, fmt.Errorf("operations: task has no function")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return JobSummary{}, ErrClosed
	}
	group := taskGroupKey(r.Profile, r.Action)
	dup := group + "\x00" + r.Subject
	if _, ok := m.taskKeys[dup]; ok {
		m.mu.Unlock()
		return JobSummary{}, ErrDuplicateTask
	}
	if r.MaxPerProfile > 0 && m.tasks[group] >= r.MaxPerProfile {
		m.mu.Unlock()
		return JobSummary{}, ErrTooManyTasks
	}
	id := m.allocIDLocked()
	if id == "" {
		m.mu.Unlock()
		return JobSummary{}, ErrIDExhausted
	}
	ctx, cancel := context.WithCancel(m.base)
	j := &job{
		id:        id,
		profile:   r.Profile,
		action:    r.Action,
		subject:   r.Subject,
		groupKey:  group,
		dupKey:    dup,
		cancel:    cancel,
		recorded:  make(chan struct{}),
		state:     StateRunning,
		startedAt: m.opts.Now(),
		ring:      newOutputRing(m.opts.MaxLines, m.opts.MaxBytes),
	}
	m.tasks[group]++
	m.taskKeys[dup] = struct{}{}
	m.jobs[id] = j
	m.wg.Add(1)
	m.mu.Unlock()

	// The snapshot is taken before the task can run, so a task that ends at
	// once is still reported as running to the caller that started it.
	sum := j.summary()
	proc := startTaskProc(ctx, cancel, r.Run)
	j.setProc(proc)
	go m.pump(j, proc)
	return sum, nil
}

// releaseTaskLocked gives back the limits a task job held. The manager mutex
// must be held. It is a no-op for colima jobs and safe to call twice.
func (m *Manager) releaseTaskLocked(j *job) {
	if j.groupKey == "" {
		return
	}
	if m.tasks[j.groupKey] > 0 {
		m.tasks[j.groupKey]--
		if m.tasks[j.groupKey] == 0 {
			delete(m.tasks, j.groupKey)
		}
	}
	delete(m.taskKeys, j.dupKey)
	j.groupKey, j.dupKey = "", ""
}

// taskProc adapts a TaskFunc to colima.Process so the colima pump runs it.
type taskProc struct {
	lines  chan string
	cancel context.CancelFunc
	once   sync.Once
	done   chan struct{}
	exit   colima.Exit
}

func startTaskProc(ctx context.Context, cancel context.CancelFunc, run TaskFunc) *taskProc {
	p := &taskProc{lines: make(chan string), cancel: cancel, done: make(chan struct{})}
	emit := func(line string) {
		// A task that outruns the pump waits for it; a cancelled one does not.
		select {
		case p.lines <- strings.TrimRight(line, "\r\n"):
		case <-ctx.Done():
		}
	}
	go func() {
		err := runRecovered(ctx, emit, run)
		switch {
		case err == nil:
			p.exit = colima.Exit{ExitCode: 0}
		case ctx.Err() != nil:
			p.exit = colima.Exit{ExitCode: -1, Killed: true}
		default:
			p.exit = colima.Exit{ExitCode: 1}
		}
		close(p.lines)
		close(p.done)
	}()
	return p
}

func runRecovered(ctx context.Context, emit func(string), run TaskFunc) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("operations: task panicked", "panic", fmt.Sprintf("%T", rec))
			err = fmt.Errorf("operations: task panicked")
		}
	}()
	return run(ctx, emit)
}

func (p *taskProc) Lines() <-chan string { return p.lines }

func (p *taskProc) Wait() (colima.Exit, error) {
	<-p.done
	return p.exit, nil
}

func (p *taskProc) Cancel() { p.once.Do(p.cancel) }
