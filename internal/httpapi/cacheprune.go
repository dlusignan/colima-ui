package httpapi

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/dlusignan/colima-ui/internal/operations"
)

const (
	// CachePruneConfirmation is the fixed string a cache prune must carry. It
	// is compared exactly: no trimming, no case folding.
	CachePruneConfirmation = "delete-cached-assets"
	cachePruneBodyLimit    = 1 << 10
)

// PruneJobs is the job manager as the cache prune route sees it.
// *operations.Manager satisfies it.
type PruneJobs interface {
	SubmitTask(r operations.TaskRequest) (operations.JobSummary, error)
}

// PruneTaskFunc builds the body of a cache-prune job. The wiring supplies it
// from the colima adapter, so this package stays free of Colima knowledge.
type PruneTaskFunc func(includeLima bool) operations.TaskFunc

// WithCachePrune registers the cache prune route:
//
//	POST /api/colima/cache/prune   {"confirm":"delete-cached-assets","includeLima":false}
//
// It answers 202 with the job, which GET /api/jobs/{id}, POST
// /api/jobs/{id}/cancel and the job.* events then follow. The body is a flat
// strict object (decodeStrictObject) of at most 1 KiB with exactly the keys
// confirm and optional includeLima. The job is a task job
// (operations.Manager.SubmitTask): it holds no profile slot, at most one runs at
// a time across the whole backend, and it is refused while a create or start
// job runs on any profile, because those download the assets the prune would
// remove. The action log row is written by the manager's observer when the job
// ends. A cancelled prune can leave a partly cleared cache.
func WithCachePrune(jobs PruneJobs, task PruneTaskFunc) Option {
	if jobs == nil || task == nil {
		panic("httpapi: WithCachePrune needs a job manager and a task builder")
	}
	c := &cachePruneRoutes{jobs: jobs, task: task}
	return func(rt *Router) {
		rt.Handle("POST /api/colima/cache/prune", c.prune)
	}
}

type cachePruneRoutes struct {
	jobs PruneJobs
	task PruneTaskFunc
}

func (c *cachePruneRoutes) prune(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Confirm     *string
		IncludeLima *bool
	}
	// An empty body means {}, so a missing confirm is the route's own 422.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, cachePruneBodyLimit))
	if err != nil {
		return errBadRequest("Request body is not valid.")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err := decodeStrictObject(w, r, cachePruneBodyLimit, map[string]any{
		"confirm": &in.Confirm, "includeLima": &in.IncludeLima,
	}); err != nil {
		return err
	}
	if in.Confirm == nil || *in.Confirm != CachePruneConfirmation {
		return newAPIError(http.StatusUnprocessableEntity, "confirmation_required",
			"Send the confirmation text in confirm to clear the download cache.", map[string]any{})
	}
	all := in.IncludeLima != nil && *in.IncludeLima
	subject := operations.PruneSubjectCache
	if all {
		subject = operations.PruneSubjectAll
	}
	sum, err := c.jobs.SubmitTask(operations.TaskRequest{
		Action: operations.ActionCachePrune, Subject: subject, MaxPerProfile: 1,
		RefuseWhileRunning: []operations.Action{operations.ActionCreate, operations.ActionStart},
		Run:                c.task(all),
	})
	var blocked *operations.TaskBlockedError
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, pullAcceptedJSON{
			JobID: sum.ID, Action: string(sum.Action), Profile: sum.Profile, State: string(sum.State)})
		return nil
	case errors.As(err, &blocked):
		return newAPIError(http.StatusConflict, "invalid_state",
			"A profile is being created or started and may be downloading these files. Wait for it to finish.",
			map[string]any{"status": "starting", "reason": "assets_in_use", "jobId": blocked.JobID})
	case errors.Is(err, operations.ErrDuplicateTask), errors.Is(err, operations.ErrTooManyTasks):
		return newAPIError(http.StatusConflict, "conflict",
			"The download cache is already being cleared.", map[string]any{})
	case errors.Is(err, operations.ErrClosed):
		return newAPIError(http.StatusServiceUnavailable, "shutting_down", "The backend is shutting down.", nil)
	}
	slog.Error("cache prune submit failed", "error", err)
	return errInternal()
}
