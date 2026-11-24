package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dlusignan/colima-ui/internal/operations"
)

// ErrKubernetesProbeRefused means Colima would not report the profile's status,
// which it does for a VM that is not running.
var ErrKubernetesProbeRefused = errors.New("httpapi: colima refused the status read")

// KubernetesProbe reports whether a running profile has a Kubernetes cluster
// enabled, from `colima status` (the live answer, never the YAML file).
// KubernetesEnabled returns ErrKubernetesProbeRefused when Colima refuses the
// read and any other error when it fails.
type KubernetesProbe interface {
	KubernetesEnabled(ctx context.Context, name string) (bool, error)
}

// Confirmation strings the destructive routes require in `confirm`, compared
// exactly.
const (
	kubernetesResetConfirmation  = "reset-kubernetes"
	kubernetesDeleteConfirmation = "delete-kubernetes"
)

// kubernetesBodyLimit bounds the confirm body.
const kubernetesBodyLimit int64 = 1 << 10

// WithKubernetes registers the Kubernetes routes:
//
//	GET  /api/profiles/{name}/kubernetes
//	POST /api/profiles/{name}/kubernetes/start|stop|reset|delete
//
// The POST routes are jobs on the operation manager (one slot per profile).
func WithKubernetes(mgr JobsDeps, gate ProfileGate, probe KubernetesProbe) Option {
	k := &kubernetesRoutes{mgr: mgr, gate: gate, probe: probe}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/kubernetes", k.get)
		rt.Handle("POST /api/profiles/{name}/kubernetes/start", k.action(operations.ActionKubernetesStart, ""))
		rt.Handle("POST /api/profiles/{name}/kubernetes/stop", k.action(operations.ActionKubernetesStop, ""))
		rt.Handle("POST /api/profiles/{name}/kubernetes/reset", k.action(operations.ActionKubernetesReset, kubernetesResetConfirmation))
		rt.Handle("POST /api/profiles/{name}/kubernetes/delete", k.action(operations.ActionKubernetesDelete, kubernetesDeleteConfirmation))
	}
}

type kubernetesRoutes struct {
	mgr   JobsDeps
	gate  ProfileGate
	probe KubernetesProbe
}

func kubernetesDisabled() *APIError {
	return newAPIError(http.StatusConflict, "invalid_state",
		"Kubernetes is not enabled on this profile.",
		map[string]any{"status": "running", "reason": "kubernetes_not_enabled"})
}

// running is the shared first half: the gate (which validates the name), then
// the allowlist check that the profile is exactly running.
func (k *kubernetesRoutes) running(ctx context.Context, route, name string) error {
	gctx, gcancel := context.WithTimeout(ctx, gateTimeout)
	defer gcancel()
	status, err := k.gate.ProfileStatus(gctx, name)
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "route", route, "error", err)
		return errColimaFailed()
	}
	if status = strings.ToLower(status); status != "running" {
		return notRunning(status)
	}
	return nil
}

// enabled reads the live Kubernetes flag.
func (k *kubernetesRoutes) enabled(ctx context.Context, route, name string) (bool, error) {
	pctx, pcancel := context.WithTimeout(ctx, gateTimeout)
	defer pcancel()
	on, err := k.probe.KubernetesEnabled(pctx, name)
	switch {
	case errors.Is(err, ErrKubernetesProbeRefused):
		return false, notRunning("stopped")
	case err != nil:
		slog.Error("kubernetes status read failed", "route", route, "error", err)
		return false, errColimaFailed()
	}
	return on, nil
}

func (k *kubernetesRoutes) get(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if err := k.running(r.Context(), "kubernetes", name); err != nil {
		return err
	}
	on, err := k.enabled(r.Context(), "kubernetes", name)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": on})
	return nil
}

// action serves the four POST routes. confirm is the exact text `confirm` must
// carry, or "" for the routes that take an empty body. The order: body (no
// process runs for a bad body), Busy, the gate and its running check, the
// Kubernetes read, then Submit, which stays authoritative; the launcher
// rechecks the state inside the slot.
func (k *kubernetesRoutes) action(act operations.Action, confirm string) apiHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		var err error
		if confirm == "" {
			err = emptyBody(w, r)
		} else {
			err = confirmBody(w, r, confirm)
		}
		if err != nil {
			return err
		}
		name := r.PathValue("name")
		route := string(act)

		// Busy first: it is a map lookup, and it must win over the slow status
		// reads below (while a start is in flight the list still says stopped).
		// A name that is not a profile can never hold a slot.
		if id, busy := k.mgr.Busy(name); busy {
			return busyError(name, id)
		}
		if err := k.running(r.Context(), route, name); err != nil {
			return err
		}
		on, err := k.enabled(r.Context(), route, name)
		if err != nil {
			return err
		}
		if !on {
			return kubernetesDisabled()
		}

		sum, err := k.mgr.Submit(operations.Request{Action: act, Profile: name})
		switch {
		case errors.Is(err, operations.ErrProfileNotRunning):
			return notRunning("stopped")
		case errors.Is(err, operations.ErrKubernetesNotEnabled):
			return kubernetesDisabled()
		case err != nil:
			return submitError(name, act, err)
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": sum.ID})
		return nil
	}
}

// confirmBody reads the flat body {"confirm": "<want>"}: at most 1 KiB, no other
// keys, nothing after the object. A malformed body is a 400; a missing,
// empty or different confirm (and an empty body) is a 422 on `confirm`.
func confirmBody(w http.ResponseWriter, r *http.Request, want string) error {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, kubernetesBodyLimit))
	if err != nil {
		return errBadRequest("Request body is not valid.")
	}
	var in struct {
		Confirm *string `json:"confirm"`
	}
	if b = bytes.TrimSpace(b); len(b) > 0 {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return errBadRequest("Request body is not valid.")
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return errBadRequest("Request body is not valid.")
		}
	}
	if in.Confirm == nil || *in.Confirm != want {
		return errInvalidRequest(map[string]string{
			"confirm": "Send the confirmation text to destroy the cluster's workloads and state.",
		})
	}
	return nil
}
