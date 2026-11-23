package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

const (
	logsDefaultTail = 200
	// logsMaxBody caps the encoded response body. The slack covers the
	// wrapper object around the lines.
	logsMaxBody  = 512 << 10
	logsBodySlop = 64
)

// logsTimeout bounds the whole request: the inspect (5 seconds) plus the
// logs read (15 seconds) and the gate.
var logsTimeout = 25 * time.Second

// logIDPattern is the only id shape the route accepts. A name or a prefix
// never reaches the Docker URL.
var logIDPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

type logsJSON struct {
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

// WithLogs registers the container logs route:
//
//	GET /api/profiles/{name}/containers/{id}/logs?tail=200
//
// It returns a bounded snapshot, never a stream. The route only reads, and it
// never logs or echoes container output outside the response body.
func WithLogs(gate ProfileGate, docker dockerc.Provider) Option {
	r := &logRoutes{gate: gate, docker: docker}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/containers/{id}/logs", r.get)
	}
}

type logRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
}

func containerNotFound() *APIError {
	return newAPIError(http.StatusNotFound, "container_not_found", "No such container.", nil)
}

func errInvalidTail() *APIError {
	return errInvalidRange("tail", "tail must be an integer of 1 or more.")
}

// parseTail is strict: at most one value, ASCII digits only (no sign, no
// space, no hex), at least 1. Values above the maximum are clamped.
func parseTail(rawQuery string) (int, *APIError) {
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return 0, errInvalidTail()
	}
	vals, ok := q["tail"]
	if !ok {
		return logsDefaultTail, nil
	}
	if len(vals) != 1 || vals[0] == "" {
		return 0, errInvalidTail()
	}
	v := vals[0]
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, errInvalidTail()
		}
	}
	v = strings.TrimLeft(v, "0")
	switch {
	case v == "":
		return 0, errInvalidTail()
	case len(v) > 6:
		return dockerc.MaxLogTail, nil
	}
	n, _ := strconv.Atoi(v)
	if n > dockerc.MaxLogTail {
		n = dockerc.MaxLogTail
	}
	return n, nil
}

func (l *logRoutes) get(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), logsTimeout)
	defer cancel()

	gctx, gcancel := context.WithTimeout(ctx, gateTimeout)
	status, err := l.gate.ProfileStatus(gctx, name)
	gcancel()
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "route", "logs", "error", err)
		return errColimaFailed()
	}
	if status = strings.ToLower(status); status != "running" {
		return notRunning(status)
	}

	id := r.PathValue("id")
	if !logIDPattern.MatchString(id) {
		return containerNotFound()
	}
	tail, perr := parseTail(r.URL.RawQuery)
	if perr != nil {
		return perr
	}

	client, err := l.docker.For(ctx, name)
	switch {
	case errors.Is(err, dockerc.ErrVMNotRunning):
		return notRunning("stopped")
	case errors.Is(err, dockerc.ErrRuntimeNotDocker):
		return containerNotFound()
	case err != nil:
		return dockerError("docker client", err)
	}

	res, err := client.Logs(ctx, id, tail)
	switch {
	case errors.Is(err, dockerc.ErrContainerNotFound):
		return containerNotFound()
	case errors.Is(err, dockerc.ErrLogsUnavailable):
		return newAPIError(http.StatusConflict, "invalid_state",
			"This container's log driver does not keep readable logs.",
			map[string]any{"reason": "logs_unavailable"})
	case err != nil:
		return dockerError("read logs", err)
	}

	lines, dropped := fitBody(res.Lines, tail)
	writeJSON(w, http.StatusOK, logsJSON{
		Lines:     lines,
		Truncated: res.Older || res.Cut || dropped,
	})
	return nil
}

// fitBody keeps the newest lines whose encoded size fits the body cap and
// reports whether any older line was dropped. It also enforces tail, so the
// handler stays correct if a client returns more lines than asked.
func fitBody(lines []string, tail int) ([]string, bool) {
	dropped := false
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
		dropped = true
	}
	budget := logsMaxBody - logsBodySlop
	keep := 0
	for i := len(lines) - 1; i >= 0; i-- {
		enc, err := json.Marshal(lines[i])
		if err != nil {
			enc = nil
		}
		cost := len(enc) + 1
		if cost > budget {
			break
		}
		budget -= cost
		keep++
	}
	if keep < len(lines) {
		dropped = true
		lines = lines[len(lines)-keep:]
	}
	out := make([]string, len(lines))
	copy(out, lines)
	return out, dropped
}
