package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/store"
)

// This file holds the shared rules of every Docker write route (volume,
// network and image changes, issues #117 to #120). A route supplies what is
// specific to it, a writeSpec, and dockerWriter.serve does the rest in one
// fixed order:
//
//  1. Profile lookup through the ProfileGate: 404 profile_not_found for an
//     unknown or malformed name, 409 invalid_state when the VM is not running.
//  2. Busy check: 409 profile_busy while a start, stop or restart job runs on
//     the profile, like the config route. The check is advisory: it sees only
//     this backend's own jobs, not a colima run from a terminal or a config
//     write in progress, and a job may start a moment later; Docker then
//     fails or succeeds on its own. For the same reason a slow route such as
//     pull (#120) must NOT hold the profile slot for its whole run: that would
//     block start, stop and restart for minutes, and the slot is not meant
//     for Docker work.
//  3. Body: the route's keys are decoded strictly (decodeStrictObject: unknown,
//     duplicate and wrong-case keys, null as a struct, trailing data and a body
//     over the limit are 400 bad_request); a route without keys takes an empty
//     body or {}.
//  4. spec.Plan validates the decoded values and path parameters and returns
//     what to run. A failure is a 422 invalid_request with field names (use
//     requireObjectName and requireImageRef). Nothing has touched Docker yet.
//  5. The Docker client for the profile, then the call itself, then one action
//     log row for the attempt, then the answer. From here on the work runs on a
//     context that ignores a client hang-up (bounded by the spec timeout), so
//     a write that has started finishes and is recorded with its real outcome.
//
// Two spec flags bend the order: ReadOnly (volume inspect) skips the busy check
// and the action log row, EmptyBodyIsObject (volume prune) reads an empty body as
// {} so a missing key is the route's own 422.
//
// How to add a write route: build a dockerWriter once with newDockerWriter in
// the route group's WithX option, and in the handler declare the body struct,
// then return d.serve(w, r, writeSpec{Action: "docker.volume.remove", Plan: ...}).
// Docker error text never reaches the browser: dockerc errors map to fixed
// messages in dockerWriteError.

const (
	// defaultDockerWriteTimeout bounds the whole request, gate included.
	defaultDockerWriteTimeout = 30 * time.Second
	// auditTimeout bounds the action log append. It runs on a context that
	// outlives the request, so a client that hangs up after Docker has
	// answered still leaves its row.
	auditTimeout = 5 * time.Second

	// Outcomes of an action log row of a Docker write.
	auditSuccess = "success"
	auditFailed  = "failed"

	invalidNameMessage = "Not a valid Docker name."
	invalidRefMessage  = "Not a valid image reference."
)

// ActionRecorder appends one action log row. *store.Store satisfies it.
type ActionRecorder interface {
	AppendAction(ctx context.Context, e store.ActionEntry) error
}

var _ ActionRecorder = (*store.Store)(nil)

// dockerWriter carries the dependencies of the write routes.
type dockerWriter struct {
	gate   ProfileGate
	docker dockerc.Provider
	busy   ConfigBusy
	audit  ActionRecorder
}

// newDockerWriter panics on a nil gate, provider or busy lookup: a write route
// without them would skip the checks that make it safe, so the mistake must
// show at start-up, not on the first request. A nil audit is allowed (the row
// is skipped).
func newDockerWriter(gate ProfileGate, docker dockerc.Provider, busy ConfigBusy, audit ActionRecorder) *dockerWriter {
	if gate == nil || docker == nil || busy == nil {
		panic("httpapi: newDockerWriter needs a gate, a Docker provider and a busy lookup")
	}
	return &dockerWriter{gate: gate, docker: docker, busy: busy, audit: audit}
}

// writeSpec describes one request. Build it per request: Body holds pointers
// into the handler's own variables.
type writeSpec struct {
	// Action is the action log name, "docker.<resource>.<verb>".
	Action string
	// Body maps the exact JSON keys the route accepts to their decode
	// targets. Nil means the route takes no body (empty or {} only).
	Body map[string]any
	// BodyLimit overrides defaultBodyLimit.
	BodyLimit int64
	// Timeout overrides the default of 30 seconds, for slow calls like pull.
	Timeout time.Duration
	// ReadOnly marks a call that only reads (volume inspect): it skips the
	// busy check and writes no action log row. Everything else, including the
	// profile lookup, the body rules and the error mapping, is unchanged.
	ReadOnly bool
	// EmptyBodyIsObject makes an empty body mean {} for a route with Body keys,
	// so that a missing key is the route's own 422 and not a 400. Any other
	// malformed body is still a 400.
	EmptyBodyIsObject bool
	// Plan validates the decoded values and returns the call to make. It must
	// not touch Docker. Return an *APIError (errInvalidRequest) to refuse.
	Plan func() (writePlan, error)
}

// writePlan is a validated call.
type writePlan struct {
	// Object is the validated name or reference the call is about. It is the
	// only detail of the action log row and the only value echoed in an error
	// answer, so it must already have passed ValidObjectName or
	// ValidImageRef. Empty when the call has no single object.
	Object string
	// Run makes the Docker call, normally one typed dockerc.Client method.
	Run func(ctx context.Context, c dockerc.Client) error
	// Status is the success status (default 200 with Result, 204 without).
	Status int
	// Result is encoded as the success body. Nil gives an empty 204.
	Result any
}

// serve runs the shared steps for one request and writes the answer.
func (d *dockerWriter) serve(w http.ResponseWriter, r *http.Request, spec writeSpec) error {
	name := r.PathValue("name")
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultDockerWriteTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	gctx, gcancel := context.WithTimeout(ctx, gateTimeout)
	status, err := d.gate.ProfileStatus(gctx, name)
	gcancel()
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "route", spec.Action, "error", err)
		return errColimaFailed()
	}
	if status = strings.ToLower(status); status != "running" {
		return notRunning(status)
	}
	if id, running := d.busy.Busy(name); running && !spec.ReadOnly {
		return busyError(name, id)
	}

	if spec.Body == nil {
		if err := emptyBody(w, r); err != nil {
			return err
		}
	} else {
		limit := spec.BodyLimit
		if limit <= 0 {
			limit = defaultBodyLimit
		}
		if spec.EmptyBodyIsObject {
			b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
			if err != nil {
				return errBadRequest("Request body is not valid.")
			}
			if len(bytes.TrimSpace(b)) == 0 {
				b = []byte("{}")
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		if err := decodeStrictObject(w, r, limit, spec.Body); err != nil {
			return err
		}
	}
	plan, err := spec.Plan()
	if err != nil {
		return err
	}
	// A plan that breaks its own contract is a bug, not user input: stop
	// before Docker, and keep the value out of the log row and the answer.
	if plan.Run == nil || (plan.Object != "" && !dockerc.ValidObjectName(plan.Object) && !dockerc.ValidImageRef(plan.Object)) {
		slog.Error("docker write plan is invalid", "action", spec.Action)
		return errInternal()
	}

	// Everything before this point only read state, so a client that left
	// simply gets nothing. From here a write may already be under way at
	// Docker, so it runs detached from the request and ends by its own
	// deadline, and the row below tells what happened.
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
	defer rcancel()
	client, err := d.docker.For(rctx, name)
	if err == nil {
		err = plan.Run(rctx, client)
	}
	if !spec.ReadOnly {
		d.record(r.Context(), name, spec.Action, plan.Object, err)
	}
	if err != nil {
		return dockerWriteError(spec.Action, plan.Object, err)
	}
	switch {
	case plan.Result != nil:
		code := plan.Status
		if code == 0 {
			code = http.StatusOK
		}
		writeJSON(w, code, plan.Result)
	default:
		code := plan.Status
		if code == 0 {
			code = http.StatusNoContent
		}
		w.WriteHeader(code)
	}
	return nil
}

// record appends the action log row of one attempt. A failed append is logged
// and never changes the answer. The row holds the action, the outcome and the
// object name, nothing from the request body.
func (d *dockerWriter) record(parent context.Context, profile, action, object string, runErr error) {
	if d.audit == nil {
		return
	}
	e := store.ActionEntry{Profile: profile, Action: action, Outcome: auditSuccess}
	if runErr != nil {
		e.Outcome = auditFailed
	}
	if object != "" {
		e.Detail = &object
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), auditTimeout)
	defer cancel()
	if err := d.audit.AppendAction(ctx, e); err != nil {
		slog.Error("action log append failed", "action", action, "error", err)
	}
}

// dockerWriteError maps a failure of the Docker call. The cause is logged
// where it is unexpected and never returned; the answer carries only fixed
// text and the validated object name.
func dockerWriteError(action, object string, err error) error {
	details := map[string]any{}
	if object != "" {
		details["name"] = object
	}
	switch {
	case errors.Is(err, dockerc.ErrVMNotRunning):
		return notRunning("stopped")
	case errors.Is(err, dockerc.ErrRuntimeNotDocker):
		return newAPIError(http.StatusConflict, "invalid_state",
			"The profile does not use the Docker runtime.", map[string]any{"runtime": "not_docker"})
	case errors.Is(err, dockerc.ErrObjectNotFound):
		return newAPIError(http.StatusNotFound, "not_found", "No such Docker object.", details)
	case errors.Is(err, dockerc.ErrConflict):
		// A volume still mounted says by how many containers (a count only).
		var inUse *dockerc.InUseError
		if errors.As(err, &inUse) {
			details["containers"] = inUse.Containers
		}
		return newAPIError(http.StatusConflict, "conflict",
			"The Docker object is in use or protected.", details)
	case errors.Is(err, dockerc.ErrInvalidRequest):
		return newAPIError(http.StatusUnprocessableEntity, "invalid_request",
			"Docker did not accept the request values.", map[string]any{"fields": map[string]any{}})
	case errors.Is(err, dockerc.ErrDaemon):
		slog.Warn("docker write failed", "action", action, "error", err)
		return newAPIError(http.StatusBadGateway, "docker_unavailable",
			"Docker answered with an error.", nil)
	case errors.Is(err, context.DeadlineExceeded):
		slog.Warn("docker write timed out", "action", action)
		return newAPIError(http.StatusBadGateway, "docker_unavailable",
			"Docker did not answer in time.", nil)
	case errors.Is(err, dockerc.ErrDockerUnavailable):
		slog.Warn("docker write failed", "action", action, "error", err)
		return newAPIError(http.StatusBadGateway, "docker_unavailable",
			"The Docker socket is missing or not answering.", nil)
	case errors.Is(err, context.Canceled):
		// Only a shutdown can cancel the detached context. Not worth an
		// error line; the row already says failed.
		slog.Debug("docker write cancelled", "action", action)
		return errInternal()
	}
	slog.Error("docker write failed", "action", action, "error", err)
	return errInternal()
}

// errInvalidRequest is the 422 answer for values that failed validation.
// fields maps each rejected body key (or path parameter) to a fixed message,
// never to request text.
func errInvalidRequest(fields map[string]string) *APIError {
	f := make(map[string]any, len(fields))
	for k, v := range fields {
		f[k] = v
	}
	return newAPIError(http.StatusUnprocessableEntity, "invalid_request",
		"Some values are not allowed.", map[string]any{"fields": f})
}

// requireObjectName returns v when it is a valid Docker object name and a 422
// naming field otherwise. Use it for volume and network names, from the body
// and from path parameters alike.
func requireObjectName(field, v string) (string, error) {
	if !dockerc.ValidObjectName(v) {
		return "", errInvalidRequest(map[string]string{field: invalidNameMessage})
	}
	return v, nil
}

// requireImageRef returns v when it is a valid image reference and a 422
// naming field otherwise.
func requireImageRef(field, v string) (string, error) {
	if !dockerc.ValidImageRef(v) {
		return "", errInvalidRequest(map[string]string{field: invalidRefMessage})
	}
	return v, nil
}
