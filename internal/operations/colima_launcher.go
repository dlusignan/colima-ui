package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// ColimaLauncher runs requests through the colima adapter. Start uses empty
// options (flags omitted, so the profile YAML values stand); create passes the
// request's options, checked against Limits (the zero value permits no
// resource option); stop and restart are not forced; delete always
// carries --force and adds --data only when the request asks for it.
//
// VMStatus is the live status read for a profile ("running", "stopped", ...).
// A delete re-reads it inside the profile slot, after the route's own check,
// because the route's read can be slow and the VM may have started meanwhile:
// only "stopped" and "broken" pass, anything else ends as a failed job with a
// closing line and colima is never run. A nil VMStatus skips the check (tests
// of the argument vector); production wiring always sets it.
type ColimaLauncher struct {
	S        colima.Streamer
	Limits   colima.Limits
	VMStatus func(ctx context.Context, profile string) (string, error)
	// KubernetesEnabled reports whether the profile's configuration enables
	// Kubernetes (the config service, never a YAML parse here). The Kubernetes
	// actions fail closed: with VMStatus or KubernetesEnabled nil they never
	// run colima.
	KubernetesEnabled func(profile string) (bool, error)
}

// statusTimeout bounds the in-slot status read of a delete.
const statusTimeout = 15 * time.Second

// deletableStatus is the allowlist of statuses a delete may start from.
func deletableStatus(status string) bool {
	switch strings.ToLower(status) {
	case "stopped", "broken":
		return true
	}
	return false
}

// refusedProcess is a finished process that printed one line and failed, so a
// refusal inside the slot shows up as a failed job with a readable log.
type refusedProcess struct{ lines chan string }

func newRefusedProcess(line string) *refusedProcess {
	ch := make(chan string, 1)
	ch <- line
	close(ch)
	return &refusedProcess{lines: ch}
}

func (p *refusedProcess) Lines() <-chan string { return p.lines }
func (p *refusedProcess) Wait() (colima.Exit, error) {
	return colima.Exit{ExitCode: 1}, nil
}
func (p *refusedProcess) Cancel() {}

// NotRun marks the process as one that never ran colima; the manager copies it
// to JobSummary.NotRun.
func (p *refusedProcess) NotRun() bool { return true }

func (l ColimaLauncher) launchDelete(ctx context.Context, r Request) (colima.Process, error) {
	if err := colima.ValidateProfile(r.Profile); err != nil {
		return nil, err
	}
	if l.VMStatus != nil {
		sctx, cancel := context.WithTimeout(ctx, statusTimeout)
		status, err := l.VMStatus(sctx, r.Profile)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("operations: status before delete: %w", err)
		}
		if !deletableStatus(status) {
			return newRefusedProcess("Not deleted: the profile is not stopped. Stop it, then delete it again."), nil
		}
	}
	return colima.Delete(ctx, l.S, r.Profile, r.DeleteData)
}

// Launch implements Launcher.
func (l ColimaLauncher) Launch(ctx context.Context, r Request) (colima.Process, error) {
	switch r.Action {
	case ActionStart:
		return colima.Start(ctx, l.S, r.Profile, colima.StartOptions{}, colima.Limits{})
	case ActionCreate:
		return colima.Start(ctx, l.S, r.Profile, r.Options, l.Limits)
	case ActionStop:
		return colima.Stop(ctx, l.S, r.Profile, false)
	case ActionRestart:
		return colima.Restart(ctx, l.S, r.Profile, false)
	case ActionDelete:
		return l.launchDelete(ctx, r)
	case ActionUpdate:
		return colima.Update(ctx, l.S, r.Profile)
	case ActionKubernetesStart, ActionKubernetesStop, ActionKubernetesReset, ActionKubernetesDelete:
		return l.launchKubernetes(ctx, r)
	}
	return nil, ErrUnknownAction
}

// launchKubernetes runs one Kubernetes verb after the in-slot recheck: the VM
// must be exactly running and Kubernetes must be enabled in the profile
// configuration. A refusal is a failed job with a closing line (colima never
// runs), like a refused delete. The checks fail closed: a missing hook or a
// failed read means colima is not run.
func (l ColimaLauncher) launchKubernetes(ctx context.Context, r Request) (colima.Process, error) {
	if err := colima.ValidateProfile(r.Profile); err != nil {
		return nil, err
	}
	if l.VMStatus == nil || l.KubernetesEnabled == nil {
		return nil, errors.New("operations: kubernetes actions need a status and a config source")
	}
	sctx, cancel := context.WithTimeout(ctx, statusTimeout)
	status, err := l.VMStatus(sctx, r.Profile)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("operations: status before kubernetes action: %w", err)
	}
	if strings.ToLower(status) != "running" {
		return newRefusedProcess("Not run: the profile is not running. Start it, then try again."), nil
	}
	on, err := l.KubernetesEnabled(r.Profile)
	if err != nil {
		return nil, fmt.Errorf("operations: kubernetes config before action: %w", err)
	}
	if !on {
		return newRefusedProcess("Not run: Kubernetes is not enabled in this profile's configuration."), nil
	}
	switch r.Action {
	case ActionKubernetesStart:
		return colima.KubernetesStart(ctx, l.S, r.Profile)
	case ActionKubernetesStop:
		return colima.KubernetesStop(ctx, l.S, r.Profile)
	case ActionKubernetesReset:
		return colima.KubernetesReset(ctx, l.S, r.Profile)
	default:
		return colima.KubernetesDelete(ctx, l.S, r.Profile)
	}
}
