package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// sshConfigTimeout bounds the whole request, the colima call included.
var sshConfigTimeout = 15 * time.Second

// Errors an SSHConfigSource reports. Anything else means Colima failed.
var (
	// ErrSSHConfigRefused means Colima ran and refused (it exited non-zero).
	ErrSSHConfigRefused = errors.New("httpapi: colima refused ssh-config")
	// ErrSSHConfigUnparseable means Colima's output was not an SSH config.
	ErrSSHConfigUnparseable = errors.New("httpapi: unrecognised ssh-config output")
)

// SSHConfigEntry is one key and value line of the config.
type SSHConfigEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SSHConfigDoc is the SSH config as the route answers it.
type SSHConfigDoc struct {
	Raw     string           `json:"raw"`
	Entries []SSHConfigEntry `json:"entries"`
}

// SSHConfigSource reads a profile's SSH config. The wiring adapts the colima
// package to it, so this layer stays free of Colima knowledge.
type SSHConfigSource interface {
	SSHConfig(ctx context.Context, name string) (SSHConfigDoc, error)
}

// WithSSHConfig registers the SSH config route:
//
//	GET /api/profiles/{name}/ssh-config
//
// It is a read: no busy check and no action log row. A profile that is not
// running answers 409 invalid_state when Colima refuses to print a config.
func WithSSHConfig(gate ProfileGate, src SSHConfigSource) Option {
	s := &sshConfigRoutes{gate: gate, src: src}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/ssh-config", s.get)
	}
}

type sshConfigRoutes struct {
	gate ProfileGate
	src  SSHConfigSource
}

func (s *sshConfigRoutes) get(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), sshConfigTimeout)
	defer cancel()

	gctx, gcancel := context.WithTimeout(ctx, gateTimeout)
	status, err := s.gate.ProfileStatus(gctx, name)
	gcancel()
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "route", "ssh-config", "error", err)
		return errColimaFailed()
	}
	status = strings.ToLower(status)

	doc, err := s.src.SSHConfig(ctx, name)
	switch {
	case errors.Is(err, ErrSSHConfigRefused) && status != "running":
		return notRunning(status)
	case err != nil:
		slog.Error("ssh-config failed", "route", "ssh-config", "error", err)
		return errColimaFailed()
	}
	if doc.Entries == nil {
		doc.Entries = []SSHConfigEntry{}
	}
	writeJSON(w, http.StatusOK, doc)
	return nil
}
