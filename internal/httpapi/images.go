package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

// imagesTimeout bounds the whole request.
var imagesTimeout = 15 * time.Second

// imageJSON is one entry of the images response.
type imageJSON struct {
	ID         string   `json:"id"`
	Tags       []string `json:"tags"`
	SizeBytes  int64    `json:"sizeBytes"`
	CreatedAt  string   `json:"createdAt"`
	Containers int      `json:"containers"`
}

// WithImages registers the images route:
//
//	GET /api/profiles/{name}/images
//
// The route is stateless and only reads from the Docker engine.
func WithImages(gate ProfileGate, docker dockerc.Provider) Option {
	r := &imageRoutes{gate: gate, docker: docker}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/images", r.list)
	}
}

type imageRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
}

// list checks the profile through the gate first, because the provider cannot
// tell a stopped VM from an unknown profile.
func (i *imageRoutes) list(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), imagesTimeout)
	defer cancel()

	gctx, gcancel := context.WithTimeout(ctx, gateTimeout)
	status, err := i.gate.ProfileStatus(gctx, name)
	gcancel()
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "route", "images", "error", err)
		return errColimaFailed()
	}
	if status = strings.ToLower(status); status != "running" {
		return notRunning(status)
	}

	client, err := i.docker.For(ctx, name)
	switch {
	case errors.Is(err, dockerc.ErrVMNotRunning):
		return notRunning("stopped")
	case errors.Is(err, dockerc.ErrRuntimeNotDocker):
		writeJSON(w, http.StatusOK, map[string][]imageJSON{"images": {}})
		return nil
	case err != nil:
		return dockerError("docker client", err)
	}

	list, err := client.Images(ctx)
	if err != nil {
		return dockerError("list images", err)
	}
	out := make([]imageJSON, 0, len(list))
	for _, img := range list {
		tags := append([]string{}, img.Tags...)
		out = append(out, imageJSON{
			ID: img.ID, Tags: tags, SizeBytes: img.SizeBytes,
			CreatedAt: img.Created.UTC().Format(time.RFC3339), Containers: img.Containers,
		})
	}
	sort.SliceStable(out, func(a, b int) bool {
		x, y := out[a], out[b]
		if (len(x.Tags) == 0) != (len(y.Tags) == 0) {
			return len(x.Tags) > 0
		}
		if len(x.Tags) > 0 && x.Tags[0] != y.Tags[0] {
			return x.Tags[0] < y.Tags[0]
		}
		return x.ID < y.ID
	})
	writeJSON(w, http.StatusOK, map[string][]imageJSON{"images": out})
	return nil
}
