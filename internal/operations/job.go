package operations

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// State is a job's lifecycle state, as named in docs/api.md.
type State string

const (
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// JobSummary is an immutable snapshot of a job. Output holds the retained
// lines; Truncated and DroppedLines describe what the bound evicted. Tail is
// the last lines of the process output as colima reported them (set once the
// job ended). It can contain absolute paths: use it for logs, not for API
// responses.
type JobSummary struct {
	ID      string
	Profile string
	Action  Action
	// Subject is what a task job works on (an image reference for a pull),
	// validated by whoever submitted it. Empty for colima jobs.
	Subject string
	// DeleteData is set on a delete job that also removes the runtime data.
	DeleteData bool
	// NotRun is set on a job whose process refused to run (a delete whose
	// in-slot status check failed): colima never started, so exit codes in the
	// summary say nothing about colima.
	NotRun       bool
	State        State
	StartedAt    time.Time
	FinishedAt   *time.Time
	ExitCode     *int
	Output       []Line
	Truncated    bool
	DroppedLines int
	Tail         []string
}

type job struct {
	id      string
	profile string
	action  Action
	subject string
	// deleteData is Request.DeleteData of a delete job.
	deleteData bool
	// notRun mirrors JobSummary.NotRun, set before the final state.
	notRun bool
	cancel context.CancelFunc
	// groupKey and dupKey are the task limits this job holds ("" for none).
	groupKey string
	dupKey   string

	// cancelReq is set by Cancel and Shutdown before the context is cancelled.
	cancelReq atomic.Bool
	// recorded is closed once the final state is stored.
	recorded chan struct{}

	mu         sync.Mutex
	proc       colima.Process
	state      State
	startedAt  time.Time
	finishedAt *time.Time
	exitCode   *int
	tail       []string
	ring       *outputRing
	subs       map[*Subscription]struct{}
}

// setProc stores the process and closes the race with an earlier cancel.
func (j *job) setProc(p colima.Process) {
	j.mu.Lock()
	j.proc = p
	j.mu.Unlock()
	if j.cancelReq.Load() {
		p.Cancel()
	}
}

// requestCancel marks the job as cancel-requested and stops the process.
func (j *job) requestCancel() {
	j.cancelReq.Store(true)
	j.cancel()
	j.mu.Lock()
	p := j.proc
	j.mu.Unlock()
	if p != nil {
		p.Cancel()
	}
}

// appendLine stores a line and hands it to subscribers without blocking; a
// subscriber whose buffer is full is dropped.
func (j *job) appendLine(text string) JobLine {
	j.mu.Lock()
	defer j.mu.Unlock()
	seq := j.ring.add(text)
	l := Line{Seq: seq, Text: text}
	for s := range j.subs {
		select {
		case s.ch <- l:
		default:
			s.dropLocked(ErrSlowSubscriber)
		}
	}
	return JobLine{JobID: j.id, Profile: j.profile, Seq: seq, Line: text}
}

// finish records the final state and releases every subscriber.
func (j *job) finish(state State, code *int, tail []string, at time.Time) {
	j.mu.Lock()
	j.state = state
	j.exitCode = code
	j.finishedAt = &at
	j.tail = tail
	for s := range j.subs {
		s.dropLocked(nil)
	}
	j.subs = nil
	j.mu.Unlock()
	close(j.recorded)
}

func (j *job) summary() JobSummary {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := JobSummary{
		ID:           j.id,
		Profile:      j.profile,
		Action:       j.action,
		Subject:      j.subject,
		DeleteData:   j.deleteData,
		NotRun:       j.notRun,
		State:        j.state,
		StartedAt:    j.startedAt,
		Output:       j.ring.after(0),
		Truncated:    j.ring.dropped > 0,
		DroppedLines: j.ring.dropped,
		Tail:         append([]string(nil), j.tail...),
	}
	if j.finishedAt != nil {
		t := *j.finishedAt
		s.FinishedAt = &t
	}
	if j.exitCode != nil {
		c := *j.exitCode
		s.ExitCode = &c
	}
	return s
}

// classify maps how the process ended to a final state and exit code.
func classify(exit colima.Exit, err error, cancelRequested bool) (State, *int) {
	if exit.Killed {
		if cancelRequested || errors.Is(err, context.Canceled) {
			return StateCancelled, nil
		}
		return StateFailed, nil
	}
	if err != nil {
		return StateFailed, nil
	}
	if exit.ExitCode == 0 {
		return StateSucceeded, exit.Code()
	}
	return StateFailed, exit.Code()
}
