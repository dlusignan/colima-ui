package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/operations"
)

// MaxPullsPerProfile is how many image pulls may run on one profile at the
// same time. A third answers 409 conflict.
const MaxPullsPerProfile = 2

const (
	pullBodyLimit    = 1 << 10
	pullNeedsTagMsg  = "Add a tag or a digest, such as postgres:18-alpine."
	pullFailedText   = "The pull failed."
	pullUnreachable  = "Docker is not reachable."
	pullBadRefText   = "Not a valid image reference."
	pullCancelledMsg = "Pull cancelled."
)

// PullJobs is the job manager as the pull route sees it. *operations.Manager
// satisfies it.
type PullJobs interface {
	SubmitTask(r operations.TaskRequest) (operations.JobSummary, error)
}

// pullAcceptedJSON is the 202 answer: the job's id (the same key start, stop
// and restart answer with) plus what the UI needs to show it at once.
type pullAcceptedJSON struct {
	JobID   string `json:"jobId"`
	Action  string `json:"action"`
	Profile string `json:"profile"`
	State   string `json:"state"`
}

// WithImagePull registers the image pull route:
//
//	POST /api/profiles/{name}/images/pull   {"reference":"postgres:18-alpine"}
//
// It answers 202 with the job, which GET /api/jobs/{id}, POST
// /api/jobs/{id}/cancel and the job.* events then follow like a start or stop.
// The route goes through the shared Docker write rules (dockerwrite.go): profile
// gate, running Docker runtime, busy check, strict body. The pull is a task job
// (operations.Manager.SubmitTask) and does NOT hold the profile's colima slot,
// so a long pull never blocks start, stop or restart. The action log row is
// written by the manager's observer when the job ends, not here.
func WithImagePull(gate ProfileGate, docker dockerc.Provider, busy ConfigBusy, jobs PullJobs) Option {
	if jobs == nil {
		panic("httpapi: WithImagePull needs a job manager")
	}
	p := &pullRoutes{jobs: jobs, w: newDockerWriter(gate, docker, busy, nil)}
	return func(rt *Router) {
		rt.Handle("POST /api/profiles/{name}/images/pull", p.pull)
	}
}

type pullRoutes struct {
	jobs PullJobs
	w    *dockerWriter
}

func (p *pullRoutes) pull(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Reference string }
	var out pullAcceptedJSON
	profile := r.PathValue("name")
	return p.w.serve(w, r, writeSpec{
		Action: "docker.image.pull", NoAudit: true, EmptyBodyIsObject: true, BodyLimit: pullBodyLimit,
		Body: map[string]any{"reference": &in.Reference},
		Plan: func() (writePlan, error) {
			ref := in.Reference
			if !dockerc.ValidImageRef(ref) {
				return writePlan{}, errInvalidRequest(map[string]string{"reference": invalidRefMessage})
			}
			if !dockerc.ValidPullRef(ref) {
				return writePlan{}, errInvalidRequest(map[string]string{"reference": pullNeedsTagMsg})
			}
			return writePlan{Object: ref, Status: http.StatusAccepted, Result: &out,
				Run: func(_ context.Context, c dockerc.Client) error {
					// The pull outlives this request: the job runs on the
					// manager's context, with the client resolved above.
					sum, err := p.jobs.SubmitTask(operations.TaskRequest{
						Action: operations.ActionImagePull, Profile: profile, Subject: ref,
						MaxPerProfile: MaxPullsPerProfile, Run: pullTask(c, ref),
					})
					switch {
					case err == nil:
						out = pullAcceptedJSON{JobID: sum.ID, Action: string(sum.Action), Profile: sum.Profile, State: string(sum.State)}
						return nil
					case errors.Is(err, operations.ErrDuplicateTask):
						return newAPIError(http.StatusConflict, "conflict",
							"This image is already being pulled on this profile.", map[string]any{"name": ref})
					case errors.Is(err, operations.ErrTooManyTasks):
						return newAPIError(http.StatusConflict, "conflict",
							"Too many pulls are running on this profile. Wait for one to finish.",
							map[string]any{"limit": MaxPullsPerProfile})
					case errors.Is(err, operations.ErrClosed):
						return newAPIError(http.StatusServiceUnavailable, "shutting_down", "The backend is shutting down.", nil)
					}
					slog.Error("pull job submit failed", "error", err)
					return errInternal()
				}}, nil
		},
	})
}

// pullTask is the job body: it runs one pull and writes compact lines. The only
// registry or daemon text that reaches the output is a dockerc.PullError
// message, which dockerc has already cleaned and which is cleaned again here.
func pullTask(c dockerc.Client, ref string) operations.TaskFunc {
	return func(ctx context.Context, emit func(string)) error {
		safeEmit := func(line string) {
			if line = dockerc.CleanPullText(line); line != "" {
				emit(line)
			}
		}
		safeEmit("Pulling " + ref)
		err := c.PullImage(ctx, ref, safeEmit)
		switch {
		case err == nil:
			return nil
		case ctx.Err() != nil:
			emit(pullCancelledMsg)
			return err
		}
		safeEmit("error: " + pullFailureText(err))
		return err
	}
}

// pullFailureText is the line shown for a failed pull: the registry's own
// message when there is one, else a fixed text for the kind of failure.
func pullFailureText(err error) string {
	var pe *dockerc.PullError
	if errors.As(err, &pe) {
		if m := dockerc.CleanPullText(pe.Message); m != "" {
			return m
		}
	}
	switch {
	case errors.Is(err, dockerc.ErrBadName):
		return pullBadRefText
	case errors.Is(err, dockerc.ErrDockerUnavailable), errors.Is(err, dockerc.ErrVMNotRunning):
		return pullUnreachable
	case errors.Is(err, dockerc.ErrObjectNotFound):
		return "The image was not found, or it needs credentials."
	}
	slog.Warn("image pull failed", "error", err)
	return pullFailedText
}
