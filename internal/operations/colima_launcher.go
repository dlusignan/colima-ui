package operations

import (
	"context"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// ColimaLauncher runs requests through the colima adapter. Start uses empty
// options and limits (flags omitted, so the profile YAML values stand); stop
// and restart are not forced.
type ColimaLauncher struct{ S colima.Streamer }

// Launch implements Launcher.
func (l ColimaLauncher) Launch(ctx context.Context, r Request) (colima.Process, error) {
	switch r.Action {
	case ActionStart:
		return colima.Start(ctx, l.S, r.Profile, colima.StartOptions{}, colima.Limits{})
	case ActionStop:
		return colima.Stop(ctx, l.S, r.Profile, false)
	case ActionRestart:
		return colima.Restart(ctx, l.S, r.Profile, false)
	}
	return nil, ErrUnknownAction
}
