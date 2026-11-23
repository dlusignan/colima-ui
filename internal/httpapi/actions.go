package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/dlusignan/colima-ui/internal/store"
)

const (
	defaultActionLimit = 20
	maxActionLimit     = 100
)

// ActionReader lists recent action log entries. *store.Store satisfies it.
type ActionReader interface {
	RecentActions(ctx context.Context, profile string, limit int) ([]store.ActionEntry, error)
}

// WithActions registers the action log route:
//
//	GET /api/profiles/{name}/actions
func WithActions(reader ActionReader, gate ProfileGate) Option {
	a := &actionRoutes{reader: reader, gate: gate}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/actions", a.list)
	}
}

type actionRoutes struct {
	reader ActionReader
	gate   ProfileGate
}

type actionJSON struct {
	StartedAt string  `json:"startedAt"`
	Action    string  `json:"action"`
	Outcome   string  `json:"outcome"`
	Detail    *string `json:"detail"`
}

// list runs the name gate first so a hostile name never reaches the store and
// a missing profile is a 404 even with a bad limit.
func (a *actionRoutes) list(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")

	ctx, cancel := context.WithTimeout(r.Context(), gateTimeout)
	defer cancel()
	if _, err := a.gate.ProfileStatus(ctx, name); err != nil {
		switch {
		case errors.Is(err, ErrInvalidProfileName):
			return profileNotFound("")
		case errors.Is(err, ErrProfileNotFound):
			return profileNotFound(name)
		}
		return errColimaFailed()
	}

	limit := defaultActionLimit
	if q := r.URL.Query(); q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 {
			return newAPIError(http.StatusBadRequest, "invalid_range",
				"limit must be an integer of at least 1.", map[string]any{"parameter": "limit"})
		}
		limit = min(n, maxActionLimit)
	}

	entries, err := a.reader.RecentActions(r.Context(), name, limit)
	if errors.Is(err, store.ErrClosed) {
		return newAPIError(http.StatusServiceUnavailable, "shutting_down", "The backend is shutting down.", nil)
	}
	if err != nil {
		return err
	}
	out := make([]actionJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, actionJSON{
			StartedAt: e.StartedAt.UTC().Format(time.RFC3339),
			Action:    e.Action,
			Outcome:   e.Outcome,
			Detail:    e.Detail,
		})
	}
	writeJSON(w, http.StatusOK, map[string][]actionJSON{"entries": out})
	return nil
}
