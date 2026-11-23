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

// Timing knobs are variables only so tests can change them.
var (
	// containersTimeout bounds the whole request, including both stats rounds.
	containersTimeout = 15 * time.Second
	// cpuSampleWait is the gap between the two stats rounds. CPU percent is a
	// difference between two readings, so one round alone would report 0.
	cpuSampleWait = time.Second
)

// containerJSON is one entry of the containers response.
type containerJSON struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Image       string     `json:"image"`
	State       string     `json:"state"`
	CPUPercent  float64    `json:"cpuPercent"`
	MemoryBytes uint64     `json:"memoryBytes"`
	Status      string     `json:"status"`
	CreatedAt   string     `json:"createdAt"`
	Ports       []portJSON `json:"ports"`
}

// portJSON is one port of a container. HostIP and HostPort are null for a port
// that is exposed but not published.
type portJSON struct {
	ContainerPort int     `json:"containerPort"`
	Protocol      string  `json:"protocol"`
	HostIP        *string `json:"hostIp"`
	HostPort      *int    `json:"hostPort"`
}

// portsJSON maps and sorts ports by container port, protocol, host port
// (unpublished first) and host IP. It never returns nil, so the wire shows [].
func portsJSON(in []dockerc.Port) []portJSON {
	out := make([]portJSON, 0, len(in))
	for _, p := range in {
		pj := portJSON{ContainerPort: int(p.ContainerPort), Protocol: p.Protocol}
		if p.HostIP != "" {
			ip := p.HostIP
			pj.HostIP = &ip
		}
		if p.HostPort != 0 {
			hp := int(p.HostPort)
			pj.HostPort = &hp
		}
		out = append(out, pj)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ContainerPort != b.ContainerPort {
			return a.ContainerPort < b.ContainerPort
		}
		if a.Protocol != b.Protocol {
			return a.Protocol < b.Protocol
		}
		ah, bh := 0, 0
		if a.HostPort != nil {
			ah = *a.HostPort
		}
		if b.HostPort != nil {
			bh = *b.HostPort
		}
		if ah != bh {
			return ah < bh
		}
		aip, bip := "", ""
		if a.HostIP != nil {
			aip = *a.HostIP
		}
		if b.HostIP != nil {
			bip = *b.HostIP
		}
		return aip < bip
	})
	return out
}

// WithContainers registers the container route:
//
//	GET /api/profiles/{name}/containers
//
// The route is stateless and reads usage live: it takes two stats rounds about
// a second apart so CPU percent has a baseline.
func WithContainers(gate ProfileGate, docker dockerc.Provider) Option {
	c := &containerRoutes{gate: gate, docker: docker}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/containers", c.list)
	}
}

type containerRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
}

// list checks the profile through the gate first, because the provider cannot
// tell a stopped VM from an unknown profile.
func (c *containerRoutes) list(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), containersTimeout)
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
		slog.Error("profile lookup failed", "route", "containers", "error", err)
		return errColimaFailed()
	}
	if status = strings.ToLower(status); status != "running" {
		return notRunning(status)
	}

	client, err := c.docker.For(ctx, name)
	switch {
	case errors.Is(err, dockerc.ErrVMNotRunning):
		return notRunning("stopped")
	case errors.Is(err, dockerc.ErrRuntimeNotDocker):
		writeJSON(w, http.StatusOK, map[string][]containerJSON{"containers": {}})
		return nil
	case err != nil:
		return dockerError("docker client", err)
	}

	list, err := client.Containers(ctx)
	if err != nil {
		return dockerError("list containers", err)
	}
	usage, err := c.usage(ctx, client, list)
	if err != nil {
		return dockerError("container stats", err)
	}

	out := make([]containerJSON, 0, len(list))
	for _, ct := range list {
		u := usage[ct.ID] // a container that vanished mid-request keeps zeros
		out = append(out, containerJSON{
			ID: ct.ID, Name: ct.Name, Image: ct.Image, State: ct.State,
			CPUPercent: u.CPUPercent, MemoryBytes: u.MemoryBytes,
			Status: ct.Status, CreatedAt: ct.Created.UTC().Format(time.RFC3339),
			Ports: portsJSON(ct.Ports),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string][]containerJSON{"containers": out})
	return nil
}

// usage takes two snapshots, the second against the first's readings. With no
// running container it skips the wait and the second round.
func (c *containerRoutes) usage(ctx context.Context, client dockerc.Client, list []dockerc.Container) (map[string]dockerc.Usage, error) {
	first, err := dockerc.Snapshot(ctx, client, list, nil)
	if err != nil {
		return nil, err
	}
	running := false
	prev := make(map[string]dockerc.Reading, len(first))
	for _, ct := range list {
		if ct.State == "running" {
			running = true
		}
		if u, ok := first[ct.ID]; ok && u.Reading != (dockerc.Reading{}) {
			prev[ct.ID] = u.Reading
		}
	}
	if !running {
		return first, nil
	}
	if cpuSampleWait > 0 {
		t := time.NewTimer(cpuSampleWait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		}
	}
	return dockerc.Snapshot(ctx, client, list, prev)
}

func notRunning(status string) *APIError {
	return newAPIError(http.StatusConflict, "invalid_state",
		"The profile is "+status+".", map[string]any{"status": status})
}

// dockerError maps a Docker failure. The cause is logged, never returned.
func dockerError(what string, err error) error {
	if errors.Is(err, dockerc.ErrDockerUnavailable) {
		return newAPIError(http.StatusBadGateway, "docker_unavailable",
			"The Docker socket is missing or not answering.", nil)
	}
	slog.Error("containers route failed", "step", what, "error", err)
	return errInternal()
}
