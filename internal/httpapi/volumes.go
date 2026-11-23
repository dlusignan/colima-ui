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

// volumesTimeout bounds the whole request.
var volumesTimeout = 15 * time.Second

// volumeJSON is one entry of the volumes response. It has no path field.
type volumeJSON struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	CreatedAt  string `json:"createdAt"`
	SizeBytes  *int64 `json:"sizeBytes"`
	Containers *int   `json:"containers"`
}

// WithVolumes registers the volumes route:
//
//	GET /api/profiles/{name}/volumes
//
// The route is stateless and only reads from the Docker engine.
func WithVolumes(gate ProfileGate, docker dockerc.Provider) Option {
	r := &volumesRoutes{gate: gate, docker: docker}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/volumes", r.list)
	}
}

type volumesRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
}

// list checks the profile through the gate first, because the provider cannot
// tell a stopped VM from an unknown profile.
func (v *volumesRoutes) list(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), volumesTimeout)
	defer cancel()

	gctx, gcancel := context.WithTimeout(ctx, gateTimeout)
	status, err := v.gate.ProfileStatus(gctx, name)
	gcancel()
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "route", "volumes", "error", err)
		return errColimaFailed()
	}
	if status = strings.ToLower(status); status != "running" {
		return notRunning(status)
	}

	client, err := v.docker.For(ctx, name)
	switch {
	case errors.Is(err, dockerc.ErrVMNotRunning):
		return notRunning("stopped")
	case errors.Is(err, dockerc.ErrRuntimeNotDocker):
		writeJSON(w, http.StatusOK, map[string][]volumeJSON{"volumes": {}})
		return nil
	case err != nil:
		return dockerError("docker client", err)
	}

	list, err := client.Volumes(ctx)
	if err != nil {
		return dockerError("list volumes", err)
	}
	out := make([]volumeJSON, 0, len(list))
	for _, x := range list {
		created := ""
		if !x.Created.IsZero() {
			created = x.Created.UTC().Format(time.RFC3339)
		}
		out = append(out, volumeJSON{
			Name: x.Name, Driver: x.Driver, CreatedAt: created,
			SizeBytes: x.SizeBytes, Containers: x.Containers,
		})
	}
	sort.SliceStable(out, func(a, b int) bool {
		return out[a].Name < out[b].Name
	})
	writeJSON(w, http.StatusOK, map[string][]volumeJSON{"volumes": out})
	return nil
}
