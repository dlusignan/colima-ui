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
// once when a delete dialog opens and must not poll it, because it makes four
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
	status, err := c.gate.ProfileStatus(gctx, name)
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
// becomes a 502 docker_unavailable, and a VM that stopped meanwhile a 409.
func countContents(ctx context.Context, p dockerc.Provider, name, route string) (dockerc.Contents, bool, *APIError) {
	if p == nil {
		return dockerc.Contents{}, false, dockerUnavailable()
	}
	counts, applicable, err := dockerc.ProfileContents(ctx, p, name)
	switch {
	case err == nil:
		return counts, applicable, nil
	case errors.Is(err, dockerc.ErrVMNotRunning):
		return dockerc.Contents{}, false, notRunning("stopped")
	}
	slog.Error("docker contents read failed", "route", route, "error", err)
	return dockerc.Contents{}, false, dockerUnavailable()
}

func dockerUnavailable() *APIError {
	return newAPIError(http.StatusBadGateway, "docker_unavailable",
		"The Docker socket is missing or not answering.", nil)
}
