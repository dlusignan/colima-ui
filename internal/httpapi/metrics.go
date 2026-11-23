package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/dlusignan/colima-ui/internal/store"
)

const (
	defaultMaxPoints = 300
	maxScopeLen      = 128
)

// scopePattern is a loose check on the scope (vm or a container id). A bad
// scope is rejected without being echoed.
var scopePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// WithMetrics registers the metric history route:
//
//	GET /api/profiles/{name}/metrics
//
// Ring-versus-disk selection and downsampling happen in the store.
func WithMetrics(reader store.MetricReader, gate ProfileGate) Option {
	m := &metricRoutes{reader: reader, gate: gate}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/metrics", m.query)
	}
}

type metricRoutes struct {
	reader store.MetricReader
	gate   ProfileGate
}

type metricPointJSON struct {
	TakenAt     string   `json:"takenAt"`
	CPUPercent  *float64 `json:"cpuPercent"`
	MemoryBytes *int64   `json:"memoryBytes"`
	DiskBytes   *int64   `json:"diskBytes"`
}

type metricsResponse struct {
	Profile string            `json:"profile"`
	Scope   string            `json:"scope"`
	From    string            `json:"from"`
	To      string            `json:"to"`
	Points  []metricPointJSON `json:"points"`
}

func errInvalidRange(parameter, message string) *APIError {
	return newAPIError(http.StatusBadRequest, "invalid_range", message, map[string]any{"parameter": parameter})
}

// query runs the name gate first so a hostile name never reaches the store and
// a missing profile is a 404 even with bad parameters. The status is ignored: a
// stopped profile still has history.
func (m *metricRoutes) query(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")

	ctx, cancel := context.WithTimeout(r.Context(), gateTimeout)
	defer cancel()
	if _, err := m.gate.ProfileStatus(ctx, name); err != nil {
		switch {
		case errors.Is(err, ErrInvalidProfileName):
			return profileNotFound("")
		case errors.Is(err, ErrProfileNotFound):
			return profileNotFound(name)
		}
		return errColimaFailed()
	}

	q, perr := parseMetricQuery(r.URL.Query(), time.Now())
	if perr != nil {
		return perr
	}
	q.Profile = name

	pts, err := m.reader.QueryMetrics(r.Context(), q)
	switch {
	case errors.Is(err, store.ErrClosed):
		return newAPIError(http.StatusServiceUnavailable, "shutting_down", "The backend is shutting down.", nil)
	case errors.Is(err, store.ErrInvalidQuery):
		return errInvalidRange("from", "The requested range is not valid.")
	case err != nil:
		return err
	}

	out := make([]metricPointJSON, 0, len(pts))
	for _, p := range pts {
		out = append(out, metricPointJSON{
			TakenAt:     p.TakenAt.UTC().Format(time.RFC3339),
			CPUPercent:  p.CPUPercent,
			MemoryBytes: p.MemoryBytes,
			DiskBytes:   p.DiskBytes,
		})
	}
	writeJSON(w, http.StatusOK, metricsResponse{
		Profile: name,
		Scope:   q.Scope,
		From:    q.From.UTC().Format(time.RFC3339),
		To:      q.To.UTC().Format(time.RFC3339),
		Points:  out,
	})
	return nil
}

// parseMetricQuery applies the contract defaults and validates every
// parameter. A repeated parameter is rejected rather than guessed.
func parseMetricQuery(v url.Values, now time.Time) (store.MetricQuery, *APIError) {
	q := store.MetricQuery{Scope: "vm", MaxPoints: defaultMaxPoints}
	for _, k := range []string{"scope", "from", "to", "maxPoints"} {
		if len(v[k]) > 1 {
			return q, errInvalidRange(k, k+" must be given at most once.")
		}
	}
	if v.Has("scope") {
		s := v.Get("scope")
		if s == "" || len(s) > maxScopeLen || !scopePattern.MatchString(s) {
			return q, errInvalidRange("scope", "scope must be vm or a container id.")
		}
		q.Scope = s
	}

	q.To = now.UTC().Truncate(time.Second)
	if v.Has("to") {
		t, err := time.Parse(time.RFC3339, v.Get("to"))
		if err != nil {
			return q, errInvalidRange("to", "to must be an RFC 3339 time.")
		}
		q.To = t.UTC()
	}
	q.From = q.To.Add(-time.Hour)
	if v.Has("from") {
		t, err := time.Parse(time.RFC3339, v.Get("from"))
		if err != nil {
			return q, errInvalidRange("from", "from must be an RFC 3339 time.")
		}
		q.From = t.UTC()
	}
	if q.From.Unix() <= 0 {
		return q, errInvalidRange("from", "from is out of range.")
	}
	if q.From.After(q.To) {
		return q, errInvalidRange("from", "from must not be after to.")
	}

	if v.Has("maxPoints") {
		n, err := strconv.ParseInt(v.Get("maxPoints"), 10, 64)
		if err != nil {
			var ne *strconv.NumError
			if !errors.As(err, &ne) || !errors.Is(ne.Err, strconv.ErrRange) {
				return q, errInvalidRange("maxPoints", "maxPoints must be an integer of at least 1.")
			}
		}
		if n < 1 {
			return q, errInvalidRange("maxPoints", "maxPoints must be an integer of at least 1.")
		}
		q.MaxPoints = int(min(n, store.MaxPointsLimit))
	}
	return q, nil
}
