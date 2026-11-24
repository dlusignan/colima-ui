package operations

import (
	"context"
	"errors"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// ColimaLauncher runs requests through the colima adapter. Start uses empty
// options (flags omitted, so the profile YAML values stand); create passes the
// request's options, checked against Limits (the zero value permits no
// resource option); stop and restart are not forced.
//
// R is optional. When set, a Kubernetes action re-reads `colima status` once it
// holds the profile slot and refuses (ErrProfileNotRunning,
// ErrKubernetesNotEnabled) unless the VM is running with a cluster, because the
// route's earlier checks ran outside the slot.
type ColimaLauncher struct {
	S      colima.Streamer
	R      colima.Runner
	Limits colima.Limits
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
	case ActionUpdate:
		return colima.Update(ctx, l.S, r.Profile)
	case ActionKubernetesStart, ActionKubernetesStop, ActionKubernetesReset, ActionKubernetesDelete:
		if err := l.checkKubernetes(ctx, r.Profile); err != nil {
			return nil, err
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
	return nil, ErrUnknownAction
}

// statusRecheckTimeout bounds the in-slot status read, which Submit waits for.
const statusRecheckTimeout = 10 * time.Second

// checkKubernetes is the in-slot recheck. Colima exits non-zero from status for
// a VM that is not running, which is the only failure read as "not running".
func (l ColimaLauncher) checkKubernetes(ctx context.Context, profile string) error {
	if l.R == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, statusRecheckTimeout)
	defer cancel()
	d, err := colima.Status(ctx, l.R, profile)
	var exit *colima.ExitError
	switch {
	case errors.As(err, &exit):
		return ErrProfileNotRunning
	case err != nil:
		return err
	case !d.Kubernetes:
		return ErrKubernetesNotEnabled
	}
	return nil
}
