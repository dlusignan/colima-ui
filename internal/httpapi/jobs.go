package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/operations"
)

// Sentinel errors a ProfileGate returns for a profile name that cannot be used.
var (
	// ErrInvalidProfileName means the name is malformed.
	ErrInvalidProfileName = errors.New("httpapi: invalid profile name")
	// ErrProfileNotFound means the name is valid but no such profile exists.
	ErrProfileNotFound = errors.New("httpapi: profile not found")
)

// ProfileGate validates a profile name and reports its state. It is the only
// place a name is checked before it reaches the operation manager, and it keeps
// this package free of Colima knowledge.
//
// ProfileStatus returns ErrInvalidProfileName for a malformed name and
// ErrProfileNotFound for a valid name that does not exist (a start must never
// create a profile). Any other error means Colima failed. status is lowercase
// ("running", "stopped"); other values pass through.
type ProfileGate interface {
	ProfileStatus(ctx context.Context, name string) (status string, err error)
}

// JobsDeps is the operation manager as the job routes see it.
// *operations.Manager satisfies it.
type JobsDeps interface {
	Submit(r operations.Request) (operations.JobSummary, error)
	Get(id string) (operations.JobSummary, error)
	Cancel(ctx context.Context, id string) (operations.JobSummary, error)
	Busy(profile string) (jobID string, running bool)
}

// Timeouts are variables only so tests can shorten them.
var (
	gateTimeout   = 10 * time.Second
	cancelTimeout = 10 * time.Second
)

const maxJobIDLen = 64

// jobIDPattern is a loose check so a hostile id is never echoed back.
var jobIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// WithJobs registers the operation routes:
//
//	POST /api/profiles/{name}/start|stop|restart|update
//	GET  /api/jobs/{id}
//	POST /api/jobs/{id}/cancel
func WithJobs(mgr JobsDeps, gate ProfileGate) Option {
	j := &jobRoutes{mgr: mgr, gate: gate}
	return func(rt *Router) {
		rt.Handle("POST /api/profiles/{name}/start", j.action(operations.ActionStart))
		rt.Handle("POST /api/profiles/{name}/stop", j.action(operations.ActionStop))
		rt.Handle("POST /api/profiles/{name}/restart", j.action(operations.ActionRestart))
		rt.Handle("POST /api/profiles/{name}/update", j.action(operations.ActionUpdate))
		rt.Handle("GET /api/jobs/{id}", j.get)
		rt.Handle("POST /api/jobs/{id}/cancel", j.cancel)
	}
}

type jobRoutes struct {
	mgr  JobsDeps
	gate ProfileGate
}

// action serves start, stop and restart. The order matters: the body check,
// then the gate (which validates the name before anything else sees it), then
// Busy before the state check (while a start is in flight the list still says
// stopped, and a stop must answer profile_busy), then Submit, which stays
// authoritative because the earlier steps are reads outside the slot.
func (j *jobRoutes) action(act operations.Action) apiHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := emptyBody(w, r); err != nil {
			return err
		}
		name := r.PathValue("name")

		ctx, cancel := context.WithTimeout(r.Context(), gateTimeout)
		defer cancel()
		status, err := j.gate.ProfileStatus(ctx, name)
		switch {
		case errors.Is(err, ErrInvalidProfileName):
			return profileNotFound("")
		case errors.Is(err, ErrProfileNotFound):
			return profileNotFound(name)
		case err != nil:
			slog.Error("profile lookup failed", "action", act, "error", err)
			return errColimaFailed()
		}
		status = strings.ToLower(status)

		if id, running := j.mgr.Busy(name); running {
			return busyError(name, id)
		}
		if (act == operations.ActionStart && status == "running") ||
			(act == operations.ActionStop && status == "stopped") {
			return newAPIError(http.StatusConflict, "invalid_state",
				"The profile is already "+status+".", map[string]any{"status": status})
		}

		if act == operations.ActionUpdate && status != "running" {
			return notRunning(status)
		}

		sum, err := j.mgr.Submit(operations.Request{Action: act, Profile: name})
		if err != nil {
			return submitError(name, act, err)
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": sum.ID})
		return nil
	}
}

func profileNotFound(name string) *APIError {
	details := map[string]any{}
	if name != "" {
		details["name"] = name
	}
	return newAPIError(http.StatusNotFound, "profile_not_found", "No such profile.", details)
}

func busyError(profile, jobID string) *APIError {
	details := map[string]any{}
	if jobID != "" {
		details["jobId"] = jobID
	}
	return newAPIError(http.StatusConflict, "profile_busy", "A job is already running on this profile.", details)
}

// submitError maps what Submit returns.
func submitError(profile string, act operations.Action, err error) error {
	var busy *operations.BusyError
	switch {
	case errors.As(err, &busy):
		return busyError(profile, busy.JobID)
	case errors.Is(err, operations.ErrClosed):
		return newAPIError(http.StatusServiceUnavailable, "shutting_down", "The backend is shutting down.", nil)
	case errors.Is(err, operations.ErrIDExhausted):
		slog.Error("job id generation failed", "action", act, "error", err)
		return errInternal()
	}
	slog.Error("job launch failed", "action", act, "error", err)
	return errColimaFailed()
}

func validJobID(id string) bool {
	return len(id) <= maxJobIDLen && jobIDPattern.MatchString(id)
}

func jobNotFound(id string) *APIError {
	details := map[string]any{}
	if id != "" {
		details["jobId"] = id
	}
	return newAPIError(http.StatusNotFound, "job_not_found", "No such job.", details)
}

func (j *jobRoutes) get(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if !validJobID(id) {
		return jobNotFound("")
	}
	sum, err := j.mgr.Get(id)
	if errors.Is(err, operations.ErrJobNotFound) {
		return jobNotFound(id)
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toJobJSON(sum))
	return nil
}

// cancel returns 200 only with state cancelled. A cancel that raced a clean
// exit returns whatever the job ended as, which is a 409 job_not_running, the
// same answer a cancel a millisecond later would get.
func (j *jobRoutes) cancel(w http.ResponseWriter, r *http.Request) error {
	if err := emptyBody(w, r); err != nil {
		return err
	}
	id := r.PathValue("id")
	if !validJobID(id) {
		return jobNotFound("")
	}
	ctx, stop := context.WithTimeout(r.Context(), cancelTimeout)
	defer stop()
	sum, err := j.mgr.Cancel(ctx, id)
	var notRunning *operations.NotRunningError
	switch {
	case errors.Is(err, operations.ErrJobNotFound):
		return jobNotFound(id)
	case errors.As(err, &notRunning):
		return jobNotRunning(id, notRunning.State)
	case errors.Is(err, context.DeadlineExceeded):
		slog.Error("cancel timed out", "job", id)
		return errInternal()
	case errors.Is(err, context.Canceled):
		return nil // the client went away; there is nobody to answer
	case err != nil:
		return err
	}
	if sum.State != operations.StateCancelled {
		return jobNotRunning(id, sum.State)
	}
	writeJSON(w, http.StatusOK, toJobJSON(sum))
	return nil
}

func jobNotRunning(id string, state operations.State) *APIError {
	return newAPIError(http.StatusConflict, "job_not_running", "The job already finished.",
		map[string]any{"jobId": id, "state": string(state)})
}

type lineJSON struct {
	Seq  int    `json:"seq"`
	Line string `json:"line"`
}

// jobJSON is the wire form of a job. It deliberately has no Tail: that field
// can hold absolute paths.
type jobJSON struct {
	ID           string     `json:"id"`
	Profile      string     `json:"profile"`
	Action       string     `json:"action"`
	State        string     `json:"state"`
	StartedAt    string     `json:"startedAt"`
	FinishedAt   *string    `json:"finishedAt"`
	ExitCode     *int       `json:"exitCode"`
	Output       []lineJSON `json:"output"`
	Truncated    bool       `json:"truncated"`
	DroppedLines int        `json:"droppedLines"`
}

func toJobJSON(s operations.JobSummary) jobJSON {
	out := jobJSON{
		ID:           s.ID,
		Profile:      s.Profile,
		Action:       string(s.Action),
		State:        string(s.State),
		StartedAt:    s.StartedAt.UTC().Format(time.RFC3339),
		ExitCode:     s.ExitCode,
		Output:       make([]lineJSON, 0, len(s.Output)), // never null
		Truncated:    s.Truncated,
		DroppedLines: s.DroppedLines,
	}
	if s.FinishedAt != nil {
		f := s.FinishedAt.UTC().Format(time.RFC3339)
		out.FinishedAt = &f
	}
	for _, l := range s.Output {
		out.Output = append(out.Output, lineJSON{Seq: l.Seq, Line: l.Text})
	}
	return out
}
