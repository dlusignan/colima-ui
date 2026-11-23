package main

import (
	"time"

	"github.com/dlusignan/colima-ui/internal/events"
	"github.com/dlusignan/colima-ui/internal/operations"
)

// hubObserver turns job output and completion into the job.output and
// job.finished events of docs/api.md section 5.3. Hub.Publish never blocks, so
// no queue is needed and the job's pump is never held back.
type hubObserver struct {
	pub publisher
	now func() time.Time
}

// publisher is the part of *events.Hub the observer uses.
type publisher interface{ Publish(events.Event) }

var _ operations.Observer = (*hubObserver)(nil)

func newHubObserver(p publisher) *hubObserver {
	return &hubObserver{pub: p, now: time.Now}
}

func (o *hubObserver) JobOutput(l operations.JobLine) {
	o.pub.Publish(events.JobOutput{JobID: l.JobID, Profile: l.Profile, Seq: l.Seq, Line: l.Line})
}

func (o *hubObserver) JobFinished(s operations.JobSummary) {
	at := o.now()
	if s.FinishedAt != nil {
		at = *s.FinishedAt
	}
	o.pub.Publish(events.JobFinished{
		JobID: s.ID, Profile: s.Profile, State: string(s.State), FinishedAt: at, ExitCode: s.ExitCode,
	})
}

// refreshObserver asks the sampler to re-read the profile list when a job
// finishes, so a start or stop shows up within seconds instead of at the next
// periodic refresh. Refresh never blocks.
type refreshObserver struct{ refresh func() }

var _ operations.Observer = refreshObserver{}

func (refreshObserver) JobOutput(operations.JobLine) {}

func (o refreshObserver) JobFinished(operations.JobSummary) { o.refresh() }
