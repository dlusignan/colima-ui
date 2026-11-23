package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/dlusignan/colima-ui/internal/operations"
)

// createBodyLimit bounds the POST /api/profiles body. The route takes a handful
// of short values, so it is far below the default limit.
const createBodyLimit int64 = 64 << 10

// ErrProfileExists means the name is already in `colima list` or has a folder
// under the Colima home, in any letter case.
var ErrProfileExists = errors.New("httpapi: profile already exists")

// ValidationError carries a field-level answer. Fields maps wire field names
// to fixed messages: never request text.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "httpapi: validation failed" }

// NewProfile is the decoded creation body. Nil pointers, empty strings and
// false mean the setting is left to Colima's default.
type NewProfile struct {
	Name              string
	CPUs              *int
	MemoryGiB         *float64
	DiskGiB           *int
	RootDiskGiB       *int
	Runtime           string
	VMType            string
	Arch              string
	MountType         string
	Kubernetes        bool
	KubernetesVersion string
}

// ProfileCreator validates a creation request, refuses a profile that exists
// and starts the job that creates it. It keeps this package free of Colima
// knowledge. Create returns *ValidationError, ErrProfileExists, an error from
// operations.Manager.Submit (*operations.BusyError, ErrClosed, ErrIDExhausted)
// or any other error when Colima could not be asked or launched (a 502).
type ProfileCreator interface {
	Create(ctx context.Context, p NewProfile) (jobID string, err error)
}

// WithCreate registers the creation route:
//
//	POST /api/profiles
func WithCreate(c ProfileCreator) Option {
	cr := &createRoutes{creator: c}
	return func(rt *Router) {
		rt.Handle("POST /api/profiles", cr.post)
	}
}

type createRoutes struct{ creator ProfileCreator }

type profileCreated struct {
	JobID   string `json:"jobId"`
	Profile string `json:"profile"`
}

func (c *createRoutes) post(w http.ResponseWriter, r *http.Request) error {
	var p NewProfile
	err := decodeStrictObject(w, r, createBodyLimit, map[string]any{
		"name": &p.Name, "cpus": &p.CPUs, "memoryGiB": &p.MemoryGiB, "diskGiB": &p.DiskGiB,
		"rootDiskGiB": &p.RootDiskGiB, "runtime": &p.Runtime, "vmType": &p.VMType,
		"arch": &p.Arch, "mountType": &p.MountType, "kubernetes": &p.Kubernetes,
		"kubernetesVersion": &p.KubernetesVersion,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), gateTimeout)
	defer cancel()
	id, err := c.creator.Create(ctx, p)
	if err != nil {
		return createError(err)
	}
	writeJSON(w, http.StatusAccepted, profileCreated{JobID: id, Profile: p.Name})
	return nil
}

// createError maps what a ProfileCreator returns. Nothing from err reaches the
// client.
func createError(err error) error {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		fields := make(map[string]any, len(verr.Fields))
		keys := make([]string, 0, len(verr.Fields))
		for k := range verr.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fields[k] = verr.Fields[k]
		}
		return newAPIError(http.StatusUnprocessableEntity, "validation_failed",
			"Some values are not allowed.", map[string]any{"fields": fields})
	case errors.Is(err, ErrProfileExists):
		return newAPIError(http.StatusConflict, "profile_exists", "A profile with this name already exists.", nil)
	}
	// Busy, shutting down and an exhausted ID generator have their own answers;
	// anything else is Colima (the listing, the folder scan or the launch)
	// failing, a 502 with nothing from err in the body.
	return submitError("", operations.ActionCreate, err)
}
