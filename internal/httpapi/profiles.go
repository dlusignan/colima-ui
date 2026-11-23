package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/dlusignan/colima-ui/internal/profiles"
)

// ProfilesDeps is the part of the profile service the routes use.
// *profiles.Service satisfies it.
type ProfilesDeps interface {
	List(ctx context.Context) ([]profiles.Profile, error)
	Get(ctx context.Context, name string) (profiles.Profile, error)
}

// profilesTimeout bounds one request; it is a variable only so tests can
// shorten it.
var profilesTimeout = 15 * time.Second

const maxProfileNameLen = 64

// profileNamePattern is a loose check so a hostile name is never echoed back.
var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// WithProfiles registers the read routes:
//
//	GET /api/profiles
//	GET /api/profiles/{name}
func WithProfiles(svc ProfilesDeps) Option {
	p := &profileRoutes{svc: svc}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles", p.list)
		rt.Handle("GET /api/profiles/{name}", p.get)
	}
}

type profileRoutes struct{ svc ProfilesDeps }

func (p *profileRoutes) list(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), profilesTimeout)
	defer cancel()
	list, err := p.svc.List(ctx)
	if err != nil {
		return profilesError("", err)
	}
	if list == nil {
		list = []profiles.Profile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list})
	return nil
}

func (p *profileRoutes) get(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), profilesTimeout)
	defer cancel()
	prof, err := p.svc.Get(ctx, name)
	if err != nil {
		return profilesError(name, err)
	}
	writeJSON(w, http.StatusOK, prof)
	return nil
}

// profilesError maps the service's errors. The service returns ErrNotFound for
// a malformed name too, so the name is echoed only when it looks safe.
func profilesError(name string, err error) error {
	var ce *profiles.ColimaError
	switch {
	case errors.Is(err, profiles.ErrNotFound):
		if len(name) == 0 || len(name) > maxProfileNameLen || !profileNamePattern.MatchString(name) {
			return profileNotFound("")
		}
		return profileNotFound(name)
	case errors.As(err, &ce):
		slog.Error("profile query failed", "error", err)
		return errColimaFailed()
	default:
		return err
	}
}
