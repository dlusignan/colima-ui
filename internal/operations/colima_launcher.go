package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/dockerc"
)

// ColimaLauncher runs requests through the colima adapter. Start uses empty
// options (flags omitted, so the profile YAML values stand); create passes the
// request's options, checked against Limits (the zero value permits no
// resource option); stop and restart are not forced; delete always
// carries --force and adds --data only when the request asks for it.
//
// VMStatus is the live status read for a profile ("running", "stopped", ...).
// A delete re-reads it inside the profile slot, after the route's own check,
// because the route's read can be slow and the VM may have changed meanwhile.
// A running VM whose Docker engine holds nothing (or that does not use
// Docker) may be deleted, and so may a broken one unless its Docker answers
// and holds something (a broken VM whose Docker cannot be read is deletable);
// anything else ends as a failed job with a closing line and colima is never
// run. A nil VMStatus skips the
// check (tests of the argument vector); production wiring always sets it.
//
// DockerContents counts the Docker objects of a running profile (applicable
// is false for another runtime). A delete of a running profile counts again
// inside the slot, just before colima runs, and fails closed: a nil hook, an
// error (a VM that is no longer running included) means colima is not run. The count
// and `colima delete` are not atomic, so a container created in between is
// not seen.
type ColimaLauncher struct {
	S        colima.Streamer
	Limits   colima.Limits
	VMStatus func(ctx context.Context, profile string) (string, error)
	// DockerContents is the in-slot count of a delete (see above).
	DockerContents func(ctx context.Context, profile string) (c dockerc.Contents, applicable bool, err error)
	// KubernetesEnabled reports whether the profile's configuration enables
	// Kubernetes (the config service, never a YAML parse here). The Kubernetes
	// actions fail closed: with VMStatus or KubernetesEnabled nil they never
	// run colima.
	KubernetesEnabled func(profile string) (bool, error)
}

// statusTimeout bounds the in-slot status read of a delete, and contentsTimeout
// its Docker count.
const (
	statusTimeout   = 15 * time.Second
	contentsTimeout = 20 * time.Second
)

// Reasons a delete refused inside the slot (JobSummary.NotRunReason).
const (
	// NotRunState: the status no longer allowed a delete (for example starting).
	NotRunState = "state"
	// NotRunCannotVerify: the profile is stopped, so its Docker data cannot be read.
	NotRunCannotVerify = "cannot_verify"
	// NotRunDockerNotEmpty: the engine holds containers, images or volumes.
	NotRunDockerNotEmpty = "docker_not_empty"
	// NotRunDockerUnreadable: the count failed (or no count source was wired).
	NotRunDockerUnreadable = "docker_unreadable"
)

// refusedProcess is a finished process that printed one line and failed, so a
// refusal inside the slot shows up as a failed job with a readable log.
type refusedProcess struct {
	lines  chan string
	reason string
}

func newRefusedProcess(line string) *refusedProcess {
	ch := make(chan string, 1)
	ch <- line
	close(ch)
	return &refusedProcess{lines: ch}
}

// newRefusedDelete is a refusal with a reason token for the action log.
func newRefusedDelete(reason, line string) *refusedProcess {
	p := newRefusedProcess(line)
	p.reason = reason
	return p
}

// NotRunReason is the fixed reason token, copied to JobSummary.NotRunReason.
func (p *refusedProcess) NotRunReason() string { return p.reason }

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
		if p := l.refuseDelete(ctx, r.Profile, status); p != nil {
			return p, nil
		}
	}
	return colima.Delete(ctx, l.S, r.Profile, r.DeleteData)
}

// refuseDelete returns a refusal for a delete that must not run, or nil when it
// may. The allowlist: broken passes; running passes only when Docker holds
// nothing (or is not the runtime); stopped, starting, unknown and anything
// else is refused.
func (l ColimaLauncher) refuseDelete(ctx context.Context, profile, status string) *refusedProcess {
	switch strings.ToLower(status) {
	case "broken":
		// Best effort: a broken VM usually cannot be read, and then it is
		// deletable (that is how a damaged profile is cleaned up). When Docker
		// does answer and holds something, the delete is refused like any other.
		if l.DockerContents == nil {
			return nil
		}
		cctx, cancel := context.WithTimeout(ctx, contentsTimeout)
		defer cancel()
		if counts, applicable, err := l.DockerContents(cctx, profile); err == nil && applicable && !counts.Empty() {
			return newRefusedDelete(NotRunDockerNotEmpty,
				"Not deleted: Docker in this profile still holds containers, images or volumes. Remove them, then delete it again.")
		}
		return nil
	case "stopped":
		return newRefusedDelete(NotRunCannotVerify,
			"Not deleted: the profile is stopped, so what Docker holds cannot be checked. Start it, remove its containers, images and volumes, then delete it again.")
	case "running":
	default:
		return newRefusedDelete(NotRunState,
			"Not deleted: the profile is not running or broken. Wait for it to settle, then delete it again.")
	}
	unreadable := newRefusedDelete(NotRunDockerUnreadable,
		"Not deleted: could not check what Docker holds in this profile. Try again.")
	if l.DockerContents == nil {
		return unreadable
	}
	cctx, cancel := context.WithTimeout(ctx, contentsTimeout)
	defer cancel()
	counts, applicable, err := l.DockerContents(cctx, profile)
	switch {
	case err != nil:
		return unreadable
	case applicable && !counts.Empty():
		return newRefusedDelete(NotRunDockerNotEmpty,
			"Not deleted: Docker in this profile still holds containers, images or volumes. Remove them, then delete it again.")
	}
	return nil
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
