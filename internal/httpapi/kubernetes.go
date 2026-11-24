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

// KubernetesProbe reports whether a profile's configuration enables
// Kubernetes (`kubernetes.enabled`, read through the config service). It is the
// configured decision, not whether the cluster is running: `colima status`
// reports false for a stopped cluster, which would make Start unreachable.
// A profile with no configuration is not enabled.
type KubernetesProbe interface {
	KubernetesEnabled(name string) (bool, error)
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

// enabled reads the configured flag.
func (k *kubernetesRoutes) enabled(route, name string) (bool, error) {
	on, err := k.probe.KubernetesEnabled(name)
	if err != nil {
		slog.Error("kubernetes config read failed", "route", route, "error", err)
		return false, errInternal()
	}
	return on, nil
}

func (k *kubernetesRoutes) get(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if err := k.running(r.Context(), "kubernetes", name); err != nil {
		return err
	}
	on, err := k.enabled("kubernetes", name)
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
		on, err := k.enabled(route, name)
		if err != nil {
			return err
		}
		if !on {
			return kubernetesDisabled()
		}

		sum, err := k.mgr.Submit(operations.Request{Action: act, Profile: name})
		switch {
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
	var got string
	ok := false
	if b = bytes.TrimSpace(b); len(b) > 0 {
		var m map[string]json.RawMessage
		dec := json.NewDecoder(bytes.NewReader(b))
		if err := dec.Decode(&m); err != nil || m == nil {
			return errBadRequest("Request body is not valid.")
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return errBadRequest("Request body is not valid.")
		}
		if dupKeys(b) {
			return errBadRequest("Request body is not valid.")
		}
		for k := range m {
			if k != "confirm" {
				return errBadRequest("Request body is not valid.")
			}
		}
		if raw, present := m["confirm"]; present {
			if err := json.Unmarshal(raw, &got); err != nil {
				// null is not text: it counts as a missing confirm (422).
				if string(bytes.TrimSpace(raw)) != "null" {
					return errBadRequest("Request body is not valid.")
				}
			} else {
				ok = true
			}
		}
	}
	if !ok || got != want {
		return errInvalidRequest(map[string]string{
			"confirm": "Send the confirmation text to destroy the cluster's workloads and state.",
		})
	}
	return nil
}

// dupKeys reports whether the top-level object repeats a key (case-sensitive).
// The input is already known to be one valid JSON object.
func dupKeys(b []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil { // the opening brace
		return false
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return false
		}
		key, _ := t.(string)
		if seen[key] {
			return true
		}
		seen[key] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return false
		}
	}
	return false
}
