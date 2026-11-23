package operations

import (
	"fmt"
	"log/slog"
)

// JobLine is one output line as seen by an Observer.
type JobLine struct {
	JobID   string
	Profile string
	Seq     int
	Line    string
}

// Observer is told about job output and completion. It is the production seam
// for the event stream hub and the action log.
//
// Calls come from the job's pump goroutine, in order for a given job, outside
// every manager lock. JobOutput and JobFinished MUST NOT block: while an
// observer blocks, that job's pump stops reading output (so the child
// eventually stalls on its pipe) and the job's final state is not recorded.
// Other jobs are unaffected unless they share the blocked observer. Cancel
// still kills the child but returns its context error until the observer
// returns. JobFinished runs after the state is recorded and the profile slot
// is released, so slow work there delays nothing visible. Panics are
// recovered and dropped.
type Observer interface {
	JobOutput(JobLine)
	JobFinished(JobSummary)
}

func safely(f func()) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("operations: observer panicked", "panic", fmt.Sprintf("%T", p))
		}
	}()
	f()
}

func (m *Manager) notifyOutput(l JobLine) {
	for _, o := range m.opts.Observers {
		safely(func() { o.JobOutput(l) })
	}
}

func (m *Manager) notifyFinished(s JobSummary) {
	for _, o := range m.opts.Observers {
		safely(func() { o.JobFinished(s) })
	}
}
