package operations

import (
	"context"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// Action is what a job does to a profile.
type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	// ActionCreate starts a profile that does not exist yet, with the options
	// in Request.Options. The route that submits it checks existence.
	ActionCreate Action = "create"
)

// Request is a job request. Start, stop and restart take no other input: a
// start reuses the values stored in the profile YAML. Only ActionCreate reads
// Options.
type Request struct {
	Action  Action
	Profile string
	Options colima.StartOptions
}

// Launcher starts the process for a request. The context bounds the process:
// cancelling it must end the process. Errors (invalid profile, binary missing)
// come back from Submit before any job exists. Launch runs with no manager
// mutex held.
type Launcher interface {
	Launch(ctx context.Context, r Request) (colima.Process, error)
}
