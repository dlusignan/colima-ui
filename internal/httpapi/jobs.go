package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
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

// DeleteGate is an optional extension of ProfileGate. A gate that implements it
// is used by the delete route instead of ProfileStatus, so that delete can be
// stricter than the other routes: it answers ErrProfileNotFound unless the
// profile is listed by Colima or has an entry in Colima's folder (the
// `default` profile is not special).
type DeleteGate interface {
	ProfileStatusForDelete(ctx context.Context, name string) (status string, err error)
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
//	POST /api/profiles/{name}/delete
//	GET  /api/jobs/{id}
//	POST /api/jobs/{id}/cancel
//
// Without WithDeleteContents a delete of a running profile is refused (the
// route cannot check what Docker holds, so it fails closed).
func WithJobs(mgr JobsDeps, gate ProfileGate, opts ...JobsOption) Option {
	j := &jobRoutes{mgr: mgr, gate: gate}
	for _, o := range opts {
		o(j)
	}
	return func(rt *Router) {
		rt.Handle("POST /api/profiles/{name}/start", j.action(operations.ActionStart))
		rt.Handle("POST /api/profiles/{name}/stop", j.action(operations.ActionStop))
		rt.Handle("POST /api/profiles/{name}/restart", j.action(operations.ActionRestart))
		rt.Handle("POST /api/profiles/{name}/update", j.action(operations.ActionUpdate))
		rt.Handle("POST /api/profiles/{name}/delete", j.delete)
		rt.Handle("GET /api/jobs/{id}", j.get)
		rt.Handle("POST /api/jobs/{id}/cancel", j.cancel)
	}
}

// JobsOption customizes WithJobs.
type JobsOption func(*jobRoutes)

// WithDeleteContents gives the delete route the Docker provider it uses to
// count a running profile's containers, images and volumes.
func WithDeleteContents(p dockerc.Provider) JobsOption {
	return func(j *jobRoutes) { j.docker = p }
}

type jobRoutes struct {
	mgr    JobsDeps
	gate   ProfileGate
	docker dockerc.Provider // nil: a running profile cannot be deleted
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

// deleteBodyLimit bounds the delete body: one name (at most 64 bytes) and a flag.
const deleteBodyLimit = 1 << 10

// delete serves POST /api/profiles/{name}/delete. The order: the body shape
// (400), Busy (409 profile_busy, before the slow status read), the gate (it
// validates the name before anything else sees it, 404), the confirmation (422,
// must equal the path name exactly), the state allowlist (409 invalid_state;
// broken passes, stopped is refused as cannot_verify, running passes only
// when Docker holds nothing or is not the runtime, 502 docker_unavailable when
// it cannot be read), then Submit, which stays authoritative for the slot and
// whose launcher repeats the status and the count inside it. Nothing starts before every check has passed, and
// the launcher always passes --force, so the process never waits on a prompt.
func (j *jobRoutes) delete(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Confirm    *string
		DeleteData *bool
	}
	// An empty body means {}, so a missing confirm is the route's own 422.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, deleteBodyLimit))
	if err != nil {
		return errBadRequest("Request body is not valid.")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err := decodeStrictObject(w, r, deleteBodyLimit, map[string]any{
		"confirm": &in.Confirm, "deleteData": &in.DeleteData,
	}); err != nil {
		return err
	}
	name := r.PathValue("name")

	// Busy first: the status read below is a slow `colima list`, and a start
	// that began before it must be seen, not raced. The launcher re-checks the
	// status inside the slot, and Submit stays authoritative for the slot.
	if id, running := j.mgr.Busy(name); running {
		return busyError(name, id)
	}

	ctx, cancel := context.WithTimeout(r.Context(), gateTimeout)
	defer cancel()
	statusOf := j.gate.ProfileStatus
	if dg, ok := j.gate.(DeleteGate); ok {
		statusOf = dg.ProfileStatusForDelete
	}
	status, err := statusOf(ctx, name)
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "action", operations.ActionDelete, "error", err)
		return errColimaFailed()
	}

	if in.Confirm == nil || *in.Confirm != name {
		return newAPIError(http.StatusUnprocessableEntity, "confirmation_required",
			"Send the profile name in confirm to delete it.", map[string]any{})
	}
	// Allowlist by state. A broken VM is deleted unless its Docker answers and
	// holds something. A stopped one is refused: its Docker data cannot be read, so it cannot be verified empty.
	// A running one is deleted only when its Docker engine holds nothing (or it
	// does not use Docker). Anything else (starting, unknown) is refused.
	switch st := strings.ToLower(status); st {
	case "broken":
		// Best effort: a broken VM usually cannot be read, and then it is
		// deletable. Only a Docker that answers and holds something refuses.
		if j.docker != nil {
			cctx, ccancel := context.WithTimeout(r.Context(), contentsTimeout)
			defer ccancel()
			if counts, applicable, err := dockerc.ProfileContents(cctx, j.docker, name); err == nil && applicable && !counts.Empty() {
				return dockerNotEmpty(st, counts)
			}
		}
	case "stopped":
		return newAPIError(http.StatusConflict, "invalid_state",
			"Start the profile and empty its Docker data before deleting it.",
			map[string]any{"status": st, "reason": "cannot_verify"})
	case "running":
		cctx, ccancel := context.WithTimeout(r.Context(), contentsTimeout)
		defer ccancel()
		counts, applicable, apiErr := countContents(cctx, j.docker, name, "delete")
		if apiErr != nil {
			return apiErr
		}
		if applicable && !counts.Empty() {
			return dockerNotEmpty(st, counts)
		}
	default:
		return newAPIError(http.StatusConflict, "invalid_state",
			"Stop the profile before deleting it.", map[string]any{"status": st})
	}

	sum, err := j.mgr.Submit(operations.Request{
		Action: operations.ActionDelete, Profile: name,
		DeleteData: in.DeleteData != nil && *in.DeleteData,
	})
	if err != nil {
		return submitError(name, operations.ActionDelete, err)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": sum.ID})
	return nil
}

func dockerNotEmpty(status string, counts dockerc.Contents) *APIError {
	return newAPIError(http.StatusConflict, "invalid_state",
		"The profile still holds Docker containers, images or volumes. Remove them before deleting it.",
		map[string]any{
			"status": status, "reason": "docker_not_empty",
			"containers": counts.Containers, "images": counts.Images, "volumes": counts.Volumes,
		})
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
	var pruning *operations.CachePruneRunningError
	var task *operations.TaskRunningError
	switch {
	case errors.As(err, &task):
		return newAPIError(http.StatusConflict, "invalid_state",
			"A task such as an image pull is running on this profile. Wait for it to finish or cancel it.",
			map[string]any{"status": "busy", "reason": "task_running", "jobId": task.JobID})
	case errors.As(err, &pruning):
		return newAPIError(http.StatusConflict, "invalid_state",
			"The download cache is being cleared. Wait for it to finish.",
			map[string]any{"status": "busy", "reason": "cache_prune_running", "jobId": pruning.JobID})
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
