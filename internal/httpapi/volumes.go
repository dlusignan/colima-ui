package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
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

// volumeDetailJSON is the inspect answer. It has no mountpoint or path field.
type volumeDetailJSON struct {
	Name           string            `json:"name"`
	Driver         string            `json:"driver"`
	CreatedAt      string            `json:"createdAt"`
	SizeBytes      *int64            `json:"sizeBytes"`
	Labels         map[string]string `json:"labels"`
	Containers     int               `json:"containers"`
	ContainerNames []string          `json:"containerNames"`
}

// volumePruneJSON is the prune answer. IncludesNamedVolumes is always true on
// the pinned API v1.41: every unused volume goes, not only anonymous ones.
type volumePruneJSON struct {
	Removed              []string `json:"removed"`
	ReclaimedBytes       int64    `json:"reclaimedBytes"`
	IncludesNamedVolumes bool     `json:"includesNamedVolumes"`
}

const (
	// pruneRouteTimeout covers a prune of many or large volumes.
	pruneRouteTimeout = 2 * time.Minute
	// createVolumeBodyLimit is far above 16 labels at their longest.
	createVolumeBodyLimit = 16 << 10
	invalidLabelsMessage  = "Labels must be an object of at most 16 string pairs with valid keys and values."
)

// WithVolumes registers the volume routes:
//
//	GET    /api/profiles/{name}/volumes
//	POST   /api/profiles/{name}/volumes
//	GET    /api/profiles/{name}/volumes/{volume}
//	DELETE /api/profiles/{name}/volumes/{volume}
//	POST   /api/profiles/{name}/volumes/prune
//
// The list only reads. The other four go through the shared Docker write rules
// (dockerwrite.go): busy check, strict body, validation, action log row. The
// busy lookup and the audit recorder are required for them; a nil audit skips
// the row.
func WithVolumes(gate ProfileGate, docker dockerc.Provider, busy ConfigBusy, audit ActionRecorder) Option {
	r := &volumesRoutes{gate: gate, docker: docker, w: newDockerWriter(gate, docker, busy, audit)}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/volumes", r.list)
		rt.Handle("POST /api/profiles/{name}/volumes", r.create)
		rt.Handle("GET /api/profiles/{name}/volumes/{volume}", r.inspect)
		rt.Handle("DELETE /api/profiles/{name}/volumes/{volume}", r.remove)
		rt.Handle("POST /api/profiles/{name}/volumes/prune", r.prune)
	}
}

type volumesRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
	w      *dockerWriter
}

func volumeCreated(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// parseLabels reads the labels object of a create body by hand: encoding/json
// would let a repeated key win silently. Absent or null means no labels. It
// refuses anything but an object of string values, a repeated key, more than
// dockerc.MaxLabels pairs, and any key or value dockerc rejects. The error
// names the field only, never a key or a value.
func parseLabels(raw json.RawMessage) (map[string]string, error) {
	bad := errInvalidRequest(map[string]string{"labels": invalidLabelsMessage})
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, bad
	}
	out := map[string]string{}
	for dec.More() {
		kt, err := dec.Token()
		k, ok := kt.(string)
		if err != nil || !ok {
			return nil, bad
		}
		var v string
		if err := dec.Decode(&v); err != nil {
			return nil, bad
		}
		if _, dup := out[k]; dup || len(out) >= dockerc.MaxLabels {
			return nil, bad
		}
		out[k] = v
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, bad
	}
	if !dockerc.ValidLabels(out) {
		return nil, bad
	}
	return out, nil
}

// create: POST /api/profiles/{name}/volumes, body {"name": "...", "labels": {...}}.
func (v *volumesRoutes) create(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name   string
		Labels json.RawMessage
	}
	var out volumeJSON
	return v.w.serve(w, r, writeSpec{
		Action: "docker.volume.create", BodyLimit: createVolumeBodyLimit,
		Body: map[string]any{"name": &in.Name, "labels": &in.Labels},
		Plan: func() (writePlan, error) {
			name, err := requireObjectName("name", in.Name)
			if err != nil {
				return writePlan{}, err
			}
			labels, err := parseLabels(in.Labels)
			if err != nil {
				return writePlan{}, err
			}
			return writePlan{
				Object: name, Status: http.StatusCreated, Result: &out,
				Run: func(ctx context.Context, c dockerc.Client) error {
					vol, err := c.CreateVolume(ctx, name, labels)
					if err != nil {
						return err
					}
					// Docker's create answer has no usage figures.
					out = volumeJSON{Name: vol.Name, Driver: vol.Driver, CreatedAt: volumeCreated(vol.Created)}
					return nil
				},
			}, nil
		},
	})
}

// inspect: GET /api/profiles/{name}/volumes/{volume}. It goes through the
// write rules like the other volume routes, so it also leaves an action row.
func (v *volumesRoutes) inspect(w http.ResponseWriter, r *http.Request) error {
	var out volumeDetailJSON
	return v.w.serve(w, r, writeSpec{
		Action: "docker.volume.inspect",
		Plan: func() (writePlan, error) {
			name, err := requireObjectName("volume", r.PathValue("volume"))
			if err != nil {
				return writePlan{}, err
			}
			return writePlan{
				Object: name, Result: &out,
				Run: func(ctx context.Context, c dockerc.Client) error {
					d, err := c.InspectVolume(ctx, name)
					if err != nil {
						return err
					}
					labels := d.Labels
					if labels == nil {
						labels = map[string]string{}
					}
					names := d.ContainerNames
					if names == nil {
						names = []string{}
					}
					out = volumeDetailJSON{
						Name: d.Name, Driver: d.Driver, CreatedAt: volumeCreated(d.Created),
						SizeBytes: d.SizeBytes, Labels: labels,
						Containers: len(names), ContainerNames: names,
					}
					return nil
				},
			}, nil
		},
	})
}

// remove: DELETE /api/profiles/{name}/volumes/{volume}. Never forced.
func (v *volumesRoutes) remove(w http.ResponseWriter, r *http.Request) error {
	return v.w.serve(w, r, writeSpec{
		Action: "docker.volume.remove",
		Plan: func() (writePlan, error) {
			name, err := requireObjectName("volume", r.PathValue("volume"))
			if err != nil {
				return writePlan{}, err
			}
			return writePlan{Object: name, Run: func(ctx context.Context, c dockerc.Client) error {
				return c.RemoveVolume(ctx, name)
			}}, nil
		},
	})
}

// prune: POST /api/profiles/{name}/volumes/prune. No body, no filters.
func (v *volumesRoutes) prune(w http.ResponseWriter, r *http.Request) error {
	var out volumePruneJSON
	return v.w.serve(w, r, writeSpec{
		Action: "docker.volume.prune", Timeout: pruneRouteTimeout,
		Plan: func() (writePlan, error) {
			return writePlan{Result: &out, Run: func(ctx context.Context, c dockerc.Client) error {
				res, err := c.PruneVolumes(ctx)
				if err != nil {
					return err
				}
				removed := res.Removed
				if removed == nil {
					removed = []string{}
				}
				out = volumePruneJSON{
					Removed: removed, ReclaimedBytes: res.ReclaimedBytes,
					IncludesNamedVolumes: dockerc.PruneIncludesNamedVolumes,
				}
				return nil
			}}, nil
		},
	})
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
