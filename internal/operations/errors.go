package operations

import (
	"errors"
	"fmt"
	"slices"
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
	// ErrTooManyTasks means the task's limit group already runs its maximum.
	ErrTooManyTasks = errors.New("operations: too many running tasks")
	// ErrDuplicateTask means an identical task (same profile, action and
	// subject) is already running.
	ErrDuplicateTask = errors.New("operations: identical task already running")
	// ErrTaskNotRun is wrapped by a task that failed before the work it names
	// began (colima could not be started). The job is reported as not run, so
	// no log row claims the work was attempted.
	ErrTaskNotRun = errors.New("operations: task did not run")
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

// TaskBlockedError means a task refused to start because a colima job of one of
// its TaskRequest.RefuseWhileRunning actions is running. JobID is read from the
// profile slot, which exists a moment before the job is readable, so
// GET /api/jobs/{id} can answer 404 for it briefly.
type TaskBlockedError struct {
	Action  Action
	Profile string
	JobID   string
}

func (e *TaskBlockedError) Error() string {
	return fmt.Sprintf("operations: task blocked by running %s job %s", e.Action, e.JobID)
}

// TaskBusyError wraps ErrDuplicateTask or ErrTooManyTasks with the id of the
// running job that holds the limit, when known (always for a cache prune).
type TaskBusyError struct {
	Err   error
	JobID string
}

func (e *TaskBusyError) Error() string { return e.Err.Error() }
func (e *TaskBusyError) Unwrap() error { return e.Err }

// CachePruneRunningError means Submit refused a job that downloads assets
// because a cache prune is running (it removes the cache folder regardless of
// what is using it). JobID is the prune job.
type CachePruneRunningError struct{ JobID string }

func (e *CachePruneRunningError) Error() string {
	return "operations: cache prune " + e.JobID + " is running"
}

// DownloadsAssets reports whether a job of this action may download cached
// assets (VM images, Kubernetes binaries): create, start, restart and
// kubernetes-start. update refreshes the runtime inside the VM, not the host
// cache, so it is not included.
func DownloadsAssets(a Action) bool { return slices.Contains(AssetDownloadActions, a) }

// AssetDownloadActions is the list DownloadsAssets reads. A cache prune is
// refused while one of these runs, and these are refused while a prune runs.
var AssetDownloadActions = []Action{ActionCreate, ActionStart, ActionRestart, ActionKubernetesStart}
