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

// imageLayerJSON is one layer of an image: time and size only. The command that
// built it is never returned.
type imageLayerJSON struct {
	CreatedAt *string `json:"createdAt"`
	SizeBytes int64   `json:"sizeBytes"`
}

// imageDetailJSON is the inspect answer. It has no environment, labels,
// command, entrypoint or layer command text.
type imageDetailJSON struct {
	ID             string           `json:"id"`
	Tags           []string         `json:"tags"`
	Digests        []string         `json:"digests"`
	SizeBytes      int64            `json:"sizeBytes"`
	CreatedAt      *string          `json:"createdAt"`
	Architecture   string           `json:"architecture"`
	OS             string           `json:"os"`
	ExposedPorts   []int            `json:"exposedPorts"`
	Containers     int              `json:"containers"`
	ContainerNames []string         `json:"containerNames"`
	Layers         []imageLayerJSON `json:"layers"`
	LayerCount     int              `json:"layerCount"`
}

// imageRemovalJSON is the answer of a remove. RemovedCount is null, and
// ResultComplete false, when Docker acted but its answer could not be read.
// Truncated is true when the lists hold fewer entries than RemovedCount.
type imageRemovalJSON struct {
	Untagged       []string `json:"untagged"`
	Deleted        []string `json:"deleted"`
	RemovedCount   *int     `json:"removedCount"`
	Truncated      bool     `json:"truncated"`
	ResultComplete bool     `json:"resultComplete"`
}

// imagePruneJSON is the answer of a prune: the removal fields plus the space
// Docker says it freed (null when unknown) and the mode that ran.
type imagePruneJSON struct {
	Untagged       []string `json:"untagged"`
	Deleted        []string `json:"deleted"`
	RemovedCount   *int     `json:"removedCount"`
	Truncated      bool     `json:"truncated"`
	ReclaimedBytes *int64   `json:"reclaimedBytes"`
	All            bool     `json:"all"`
	ResultComplete bool     `json:"resultComplete"`
}

// imagePruneConfirmation is the fixed string an image prune must carry.
const imagePruneConfirmation = "delete-unused-images"

const (
	tagImageBodyLimit = 4 << 10
	invalidRepoMsg    = "Not a valid repository: use a name such as myapp or registry.example.com/team/myapp, without a tag or digest."
	invalidTagMsg     = "Not a valid tag: 1 to 128 letters, digits, underscores, dots or dashes, not starting with a dot or dash."
)

// WithImages registers the image routes:
//
//	GET    /api/profiles/{name}/images
//	POST   /api/profiles/{name}/images/{image}/tag
//	GET    /api/profiles/{name}/images/{image}
//	DELETE /api/profiles/{name}/images/{image}
//	POST   /api/profiles/{name}/images/prune
//
// The list only reads. The other four go through the shared Docker write rules
// (dockerwrite.go): busy check, strict body, validation, action log row
// (inspect is a read: no busy check and no row). The busy lookup is required; a
// nil audit skips the row. {image} is an image ID or a reference; a reference
// with slashes must be sent with the slashes escaped as %2F.
func WithImages(gate ProfileGate, docker dockerc.Provider, busy ConfigBusy, audit ActionRecorder) Option {
	r := &imageRoutes{gate: gate, docker: docker, w: newDockerWriter(gate, docker, busy, audit)}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/images", r.list)
		rt.Handle("POST /api/profiles/{name}/images/{image}/tag", r.tag)
		rt.Handle("GET /api/profiles/{name}/images/{image}", r.inspect)
		rt.Handle("DELETE /api/profiles/{name}/images/{image}", r.remove)
		rt.Handle("POST /api/profiles/{name}/images/prune", r.prune)
	}
}

type imageRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
	w      *dockerWriter
}

func rfc3339OrNil(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// tag: POST /api/profiles/{name}/images/{image}/tag, body
// {"repository": "...", "tag": "..."}, both required. Answers 204.
func (i *imageRoutes) tag(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Repository, Tag string }
	return i.w.serve(w, r, writeSpec{
		Action: "docker.image.tag", BodyLimit: tagImageBodyLimit,
		Body: map[string]any{"repository": &in.Repository, "tag": &in.Tag},
		Plan: func() (writePlan, error) {
			ref, err := requireImageRef("image", r.PathValue("image"))
			if err != nil {
				return writePlan{}, err
			}
			fields := map[string]string{}
			if !dockerc.ValidImageRepository(in.Repository) {
				fields["repository"] = invalidRepoMsg
			}
			if !dockerc.ValidImageTag(in.Tag) {
				fields["tag"] = invalidTagMsg
			}
			if len(fields) == 0 && !dockerc.ValidImageTarget(in.Repository, in.Tag) {
				fields["repository"] = invalidRepoMsg
			}
			if len(fields) > 0 {
				return writePlan{}, errInvalidRequest(fields)
			}
			repo, tag := in.Repository, in.Tag
			return writePlan{Object: ref, AuditDetail: ref + " -> " + repo + ":" + tag, Run: func(ctx context.Context, c dockerc.Client) error {
				return c.TagImage(ctx, ref, repo, tag)
			}}, nil
		},
	})
}

// inspect: GET /api/profiles/{name}/images/{image}. A read: no busy check and
// no action log row.
func (i *imageRoutes) inspect(w http.ResponseWriter, r *http.Request) error {
	var out imageDetailJSON
	return i.w.serve(w, r, writeSpec{
		Action: "docker.image.inspect", ReadOnly: true,
		Plan: func() (writePlan, error) {
			ref, err := requireImageRef("image", r.PathValue("image"))
			if err != nil {
				return writePlan{}, err
			}
			return writePlan{Object: ref, Result: &out, Run: func(ctx context.Context, c dockerc.Client) error {
				d, err := c.InspectImage(ctx, ref)
				if err != nil {
					return err
				}
				out = imageDetailJSON{
					ID: d.ID, Tags: nonNilStrings(d.Tags), Digests: nonNilStrings(d.Digests), SizeBytes: d.SizeBytes,
					CreatedAt: rfc3339OrNil(d.Created), Architecture: d.Architecture, OS: d.OS,
					ExposedPorts: d.ExposedPorts, Containers: len(d.ContainerNames), ContainerNames: nonNilStrings(d.ContainerNames),
					Layers: make([]imageLayerJSON, 0, len(d.Layers)), LayerCount: d.LayerCount,
				}
				if out.ExposedPorts == nil {
					out.ExposedPorts = []int{}
				}
				for _, l := range d.Layers {
					out.Layers = append(out.Layers, imageLayerJSON{CreatedAt: rfc3339OrNil(l.Created), SizeBytes: l.SizeBytes})
				}
				return nil
			}}, nil
		},
	})
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// remove: DELETE /api/profiles/{name}/images/{image}. Never forced. An ID
// removes the image and all its references, a reference untags.
func (i *imageRoutes) remove(w http.ResponseWriter, r *http.Request) error {
	var out imageRemovalJSON
	return i.w.serve(w, r, writeSpec{
		Action: "docker.image.remove", Timeout: pruneRouteTimeout,
		Plan: func() (writePlan, error) {
			ref, err := requireImageRef("image", r.PathValue("image"))
			if err != nil {
				return writePlan{}, err
			}
			return writePlan{Object: ref, Result: &out, Run: func(ctx context.Context, c dockerc.Client) error {
				res, err := c.RemoveImage(ctx, ref)
				if err != nil {
					return err
				}
				out = imageRemovalJSON{
					Untagged: nonNilStrings(res.Untagged), Deleted: nonNilStrings(res.Deleted),
					RemovedCount: res.Count, Truncated: res.Truncated, ResultComplete: res.Complete,
				}
				return nil
			}}, nil
		},
	})
}

// prune: POST /api/profiles/{name}/images/prune, body
// {"confirm":"delete-unused-images","all":false}. The confirmation is required;
// all defaults to false (dangling images only).
func (i *imageRoutes) prune(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Confirm string
		All     bool
	}
	var out imagePruneJSON
	return i.w.serve(w, r, writeSpec{
		Action: "docker.image.prune", Timeout: pruneRouteTimeout, EmptyBodyIsObject: true,
		BodyLimit: 1 << 10, Body: map[string]any{"confirm": &in.Confirm, "all": &in.All},
		Plan: func() (writePlan, error) {
			if in.Confirm != imagePruneConfirmation {
				return writePlan{}, errInvalidRequest(map[string]string{
					"confirm": "Send the confirmation text to delete unused images.",
				})
			}
			all := in.All
			return writePlan{Result: &out, Run: func(ctx context.Context, c dockerc.Client) error {
				res, err := c.PruneImages(ctx, all)
				if err != nil {
					return err
				}
				out = imagePruneJSON{
					Untagged: nonNilStrings(res.Untagged), Deleted: nonNilStrings(res.Deleted),
					RemovedCount: res.Count, Truncated: res.Truncated, ReclaimedBytes: res.ReclaimedBytes,
					All: all, ResultComplete: res.Complete,
				}
				return nil
			}}, nil
		},
	})
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
