package operations

import (
	"context"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// ColimaLauncher runs requests through the colima adapter. Start uses empty
// options (flags omitted, so the profile YAML values stand); create passes the
// request's options, checked against Limits (the zero value permits no
// resource option); stop and restart are not forced; delete always
// carries --force and adds --data only when the request asks for it.
type ColimaLauncher struct {
	S      colima.Streamer
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
	case ActionDelete:
		return colima.Delete(ctx, l.S, r.Profile, r.DeleteData)
	}
	return nil, ErrUnknownAction
}
