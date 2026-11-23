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

// networksTimeout bounds the whole request.
var networksTimeout = 15 * time.Second

// networkJSON is one entry of the networks response.
type networkJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Scope      string `json:"scope"`
	Internal   bool   `json:"internal"`
	Containers int    `json:"containers"`
}

// WithNetworks registers the networks route:
//
//	GET /api/profiles/{name}/networks
//
// The route is stateless and only reads from the Docker engine.
func WithNetworks(gate ProfileGate, docker dockerc.Provider) Option {
	r := &networksRoutes{gate: gate, docker: docker}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/networks", r.list)
	}
}

type networksRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
}

// list checks the profile through the gate first, because the provider cannot
// tell a stopped VM from an unknown profile.
func (v *networksRoutes) list(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), networksTimeout)
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
		slog.Error("profile lookup failed", "route", "networks", "error", err)
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
		writeJSON(w, http.StatusOK, map[string][]networkJSON{"networks": {}})
		return nil
	case err != nil:
		return dockerError("docker client", err)
	}

	list, err := client.Networks(ctx)
	if err != nil {
		return dockerError("list networks", err)
	}
	out := make([]networkJSON, 0, len(list))
	for _, x := range list {
		out = append(out, networkJSON{
			ID: x.ID, Name: x.Name, Driver: x.Driver, Scope: x.Scope,
			Internal: x.Internal, Containers: x.Containers,
		})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Name != out[b].Name {
			return out[a].Name < out[b].Name
		}
		return out[a].ID < out[b].ID
	})
	writeJSON(w, http.StatusOK, map[string][]networkJSON{"networks": out})
	return nil
}
