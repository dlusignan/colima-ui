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
	// ActionUpdate refreshes the container runtime of a running profile.
	ActionUpdate Action = "update"
	// The four Kubernetes actions run `colima kubernetes <verb>` on a running
	// profile whose cluster is enabled. Reset and delete destroy workloads and
	// cluster state; a cancelled one can leave the cluster half done.
	ActionKubernetesStart  Action = "kubernetes-start"
	ActionKubernetesStop   Action = "kubernetes-stop"
	ActionKubernetesReset  Action = "kubernetes-reset"
	ActionKubernetesDelete Action = "kubernetes-delete"
	// ActionCreate starts a profile that does not exist yet, with the options
	// in Request.Options. The route that submits it checks existence.
	ActionCreate Action = "create"
	// ActionImagePull is a Docker image pull. It is a task job (SubmitTask):
	// it never holds the profile slot and no Launcher sees it.
	ActionImagePull Action = "image-pull"
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

// IsKubernetes reports whether a is one of the four Kubernetes actions.
func (a Action) IsKubernetes() bool {
	switch a {
	case ActionKubernetesStart, ActionKubernetesStop, ActionKubernetesReset, ActionKubernetesDelete:
		return true
	}
	return false
}
