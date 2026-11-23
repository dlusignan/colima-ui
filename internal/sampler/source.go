package sampler

import (
	"context"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/events"
	"github.com/dlusignan/colima-ui/internal/store"
)

// VMScope is the scope name of the whole VM.
const VMScope = "vm"

// Target is one profile as the sampler sees it.
type Target struct {
	Name    string
	Status  string // colima status, for example "Running"
	Runtime string
	CPUs    int
	Memory  int64
	Disk    int64
}

func (t Target) running() bool { return t.Status == colima.StatusRunning }

// sampled reports whether the profile is read by the sources.
func (t Target) sampled() bool { return t.running() && t.Runtime == "docker" }

// ContainerObs is the usage of one running container.
type ContainerObs struct {
	ID          string
	CPUPercent  float64
	MemoryBytes int64
}

// Partial is what one source knows about a profile. Nil means unknown.
type Partial struct {
	VMCPU      *float64
	VMMemory   *int64
	VMDisk     *int64
	Containers []ContainerObs
	// Truncated is true when the source dropped containers to stay under its
	// cap.
	Truncated bool
}

// Source collects part of a tick for one profile. Collect may return data
// together with an error (the data is used). Sources are called from one
// goroutine per profile at a time, but different profiles run concurrently.
type Source interface {
	Name() string
	// Timeout bounds one Collect call.
	Timeout() time.Duration
	Collect(ctx context.Context, t Target) (Partial, error)
	// Forget drops all state held for a profile (baselines, caches).
	Forget(profile string)
}

// Sink receives one batch per profile and tick (satisfied by store.SampleSink).
type Sink interface {
	AppendSamples(ctx context.Context, samples []store.Sample) error
}

// Publisher receives live values (satisfied by *events.Hub). Publish must not
// block.
type Publisher interface {
	Publish(e events.Event)
}

// Lister returns every profile known to colima.
type Lister interface {
	Targets(ctx context.Context) ([]Target, error)
}

// ColimaLister is the production Lister over `colima list`.
type ColimaLister struct{ Runner colima.Runner }

// Targets lists the profiles.
func (l ColimaLister) Targets(ctx context.Context) ([]Target, error) {
	list, err := colima.List(ctx, l.Runner)
	if err != nil {
		return nil, err
	}
	out := make([]Target, 0, len(list))
	for _, in := range list {
		out = append(out, Target{Name: in.Name, Status: in.Status, Runtime: in.Runtime, CPUs: in.CPUs, Memory: in.Memory, Disk: in.Disk})
	}
	return out, nil
}

func ptr[T any](v T) *T { return &v }
