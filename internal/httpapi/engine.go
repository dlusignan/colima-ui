package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

// engineTimeout bounds the whole request, both Docker calls included.
var engineTimeout = 15 * time.Second

type engineContainersJSON struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Paused  int `json:"paused"`
	Stopped int `json:"stopped"`
}

type engineDiskJSON struct {
	ImagesBytes     int64 `json:"imagesBytes"`
	ContainersBytes int64 `json:"containersBytes"`
	VolumesBytes    int64 `json:"volumesBytes"`
	BuildCacheBytes int64 `json:"buildCacheBytes"`
	LayersBytes     int64 `json:"layersBytes"`
}

// engineJSON is the engine response. It is a hand-written subset: no host
// name, no path, no registry data.
type engineJSON struct {
	Version     string               `json:"version"`
	APIVersion  string               `json:"apiVersion"`
	OS          string               `json:"os"`
	Arch        string               `json:"arch"`
	CPUs        int                  `json:"cpus"`
	MemoryBytes int64                `json:"memoryBytes"`
	Containers  engineContainersJSON `json:"containers"`
	Images      int                  `json:"images"`
	DiskUsage   engineDiskJSON       `json:"diskUsage"`
}

// WithEngine registers the engine route:
//
//	GET /api/profiles/{name}/engine
//
// The route is stateless and only reads from the Docker engine.
func WithEngine(gate ProfileGate, docker dockerc.Provider) Option {
	r := &engineRoutes{gate: gate, docker: docker}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/engine", r.get)
	}
}

type engineRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
}

func notDockerRuntime() *APIError {
	return newAPIError(http.StatusConflict, "invalid_state",
		"The profile does not use the Docker runtime, so there is no engine to describe.",
		map[string]any{"runtime": "not_docker"})
}

func (e *engineRoutes) get(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
	defer cancel()

	gctx, gcancel := context.WithTimeout(ctx, gateTimeout)
	status, err := e.gate.ProfileStatus(gctx, name)
	gcancel()
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "route", "engine", "error", err)
		return errColimaFailed()
	}
	if status = strings.ToLower(status); status != "running" {
		return notRunning(status)
	}

	client, err := e.docker.For(ctx, name)
	switch {
	case errors.Is(err, dockerc.ErrVMNotRunning):
		return notRunning("stopped")
	case errors.Is(err, dockerc.ErrRuntimeNotDocker):
		return notDockerRuntime()
	case err != nil:
		return dockerError("docker client", err)
	}

	// Both calls share one context: the first failure cancels the other.
	callCtx, callCancel := context.WithCancel(ctx)
	defer callCancel()
	var (
		wg      sync.WaitGroup
		info    dockerc.Engine
		infoErr error
		disk    dockerc.DiskUsage
		diskErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		info, infoErr = client.Info(callCtx)
		if infoErr != nil {
			callCancel()
		}
	}()
	go func() {
		defer wg.Done()
		disk, diskErr = client.DiskUsage(callCtx)
		if diskErr != nil {
			callCancel()
		}
	}()
	wg.Wait()
	// A failure cancels the sibling, whose own error is then a cancellation
	// rather than the cause, so the cause is reported first.
	for _, c := range []struct {
		what string
		err  error
	}{{"engine info", infoErr}, {"disk usage", diskErr}} {
		if c.err != nil && !errors.Is(c.err, context.Canceled) {
			return dockerError(c.what, c.err)
		}
	}
	for _, c := range []error{infoErr, diskErr} {
		if c != nil {
			return dockerError("engine", c)
		}
	}

	writeJSON(w, http.StatusOK, engineJSON{
		Version:     info.Version,
		APIVersion:  info.APIVersion,
		OS:          info.OS,
		Arch:        info.Arch,
		CPUs:        info.CPUs,
		MemoryBytes: info.MemoryBytes,
		Containers: engineContainersJSON{
			Total: info.Containers.Total, Running: info.Containers.Running,
			Paused: info.Containers.Paused, Stopped: info.Containers.Stopped,
		},
		Images: info.Images,
		DiskUsage: engineDiskJSON{
			ImagesBytes: disk.ImagesBytes, ContainersBytes: disk.ContainersBytes,
			VolumesBytes: disk.VolumesBytes, BuildCacheBytes: disk.BuildCacheBytes,
			LayersBytes: disk.LayersBytes,
		},
	})
	return nil
}
