package operations

import (
	"errors"
	"fmt"
)

var (
	// ErrJobNotFound means no job with that ID is known (never existed or
	// evicted by retention).
	ErrJobNotFound = errors.New("operations: job not found")
	// ErrClosed is returned once Shutdown has begun.
	ErrClosed = errors.New("operations: manager is shut down")
	// ErrSlowSubscriber is the Subscription.Err of a subscriber that fell
	// behind and was dropped.
	ErrSlowSubscriber = errors.New("operations: subscriber too slow, dropped")
	// ErrUnknownAction is returned by ColimaLauncher for an unknown Action.
	ErrUnknownAction = errors.New("operations: unknown action")
	// ErrIDExhausted means the ID generator kept returning IDs already in use.
	ErrIDExhausted = errors.New("operations: could not generate a unique job id")
)

// BusyError means the profile's slot is taken. JobID is the running job, or
// empty when the slot is an exclusive hold (a config write). HTTP maps it to
// 409 profile_busy and omits details.jobId when JobID is empty.
type BusyError struct {
	Profile string
	JobID   string
}

func (e *BusyError) Error() string {
	if e.JobID == "" {
		return fmt.Sprintf("operations: profile %q is busy (exclusive hold)", e.Profile)
	}
	return fmt.Sprintf("operations: profile %q is busy (job %s)", e.Profile, e.JobID)
}

// NotRunningError is returned by Cancel for a job that already finished.
// State is its final state. HTTP maps it to 409 job_not_running.
type NotRunningError struct {
	JobID string
	State State
}

func (e *NotRunningError) Error() string {
	return fmt.Sprintf("operations: job %s is not running (state %s)", e.JobID, e.State)
}
