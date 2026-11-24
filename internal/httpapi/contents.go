package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

// contentsTimeout bounds the whole contents request.
var contentsTimeout = 20 * time.Second

// contentsJSON is the answer of the contents route. Counts only: never a name
// or an id. Applicable is false (and the counts are left out) when the profile
// does not use the Docker runtime.
type contentsJSON struct {
	Applicable bool `json:"applicable"`
	Containers *int `json:"containers,omitempty"`
	Images     *int `json:"images,omitempty"`
	Volumes    *int `json:"volumes,omitempty"`
}

// WithContents registers the contents route:
//
//	GET /api/profiles/{name}/contents
//
// It is a read: no busy check and no action log row. The caller (the UI) asks
// once when a delete dialog opens and must not poll it, because it makes three
// Docker list calls.
func WithContents(gate ProfileGate, docker dockerc.Provider) Option {
	c := &contentsRoutes{gate: gate, docker: docker}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/contents", c.get)
	}
}

type contentsRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
}

func (c *contentsRoutes) get(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), contentsTimeout)
	defer cancel()

	gctx, gcancel := context.WithTimeout(ctx, gateTimeout)
	// The delete gate also matches a listed profile that differs only in
	// letter case and reports its own status, so a running VM is never
	// reported as stopped.
	statusOf := c.gate.ProfileStatus
	if dg, ok := c.gate.(DeleteGate); ok {
		statusOf = dg.ProfileStatusForDelete
	}
	status, err := statusOf(gctx, name)
	gcancel()
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "route", "contents", "error", err)
		return errColimaFailed()
	}
	if status = strings.ToLower(status); status != "running" {
		return notRunning(status)
	}

	counts, applicable, apiErr := countContents(ctx, c.docker, name, "contents")
	if apiErr != nil {
		return apiErr
	}
	if !applicable {
		writeJSON(w, http.StatusOK, contentsJSON{Applicable: false})
		return nil
	}
	writeJSON(w, http.StatusOK, contentsJSON{
		Applicable: true, Containers: &counts.Containers, Images: &counts.Images, Volumes: &counts.Volumes,
	})
	return nil
}

// countContents reads a running profile's Docker counts. It fails closed: a nil
// provider or any read failure (the daemon text is logged, never returned)
// becomes a 502 docker_unavailable. That includes a VM the resolver no longer
// finds running although the status read said running (a stop in between, or a
// name that differs from the listed one in letter case): it is never reported
// as stopped, because that could be false.
func countContents(ctx context.Context, p dockerc.Provider, name, route string) (dockerc.Contents, bool, *APIError) {
	if p == nil {
		return dockerc.Contents{}, false, dockerUnavailable()
	}
	counts, applicable, err := dockerc.ProfileContents(ctx, p, name)
	switch {
	case err == nil:
		return counts, applicable, nil
	}
	slog.Error("docker contents read failed", "route", route, "error", err)
	return dockerc.Contents{}, false, dockerUnavailable()
}

func dockerUnavailable() *APIError {
	return newAPIError(http.StatusBadGateway, "docker_unavailable",
		"The Docker socket is missing or not answering.", nil)
}
