package dockerc

import (
	"context"
	"errors"
	"sync"
	"time"
)

// maxStatsInFlight caps concurrent per-container stats calls in Snapshot.
const maxStatsInFlight = 4

// Container is one entry of the container list.
type Container struct {
	ID    string // 12-char short form
	Name  string // without the leading "/"
	Image string
	State string // "running", "exited", ...

	Status  string    // Docker's status text, "Up 2 hours"
	Created time.Time // UTC
	Ports   []Port    // nil when Docker reports none
}

// Image is one entry of the image list.
type Image struct {
	ID         string    // 12-char short form, without the "sha256:" prefix
	Tags       []string  // never nil; empty for an untagged image
	SizeBytes  int64     // image size, shared layers counted in every image
	Created    time.Time // UTC
	Containers int       // containers, running or not, whose ImageID is this image
}

// Volume is one entry of the volume list. It has no mountpoint field on
// purpose, so a host path cannot leak into a response.
type Volume struct {
	Name       string
	Driver     string
	Created    time.Time // UTC; zero when Docker's value could not be parsed
	SizeBytes  *int64    // nil when Docker has no usage figure (absent or negative)
	Containers *int      // containers, running or not, that mount it; nil when unknown
}

// Network is one entry of the network list.
type Network struct {
	ID         string // 12-char short form
	Name       string
	Driver     string
	Scope      string
	Internal   bool
	Containers int // containers, running or not, attached to it
}

// Port is one port entry of a container. An empty HostIP and a zero HostPort
// mean the port is exposed but not published.
type Port struct {
	ContainerPort uint16
	Protocol      string // "tcp", "udp", "sctp"
	HostIP        string
	HostPort      uint16
}

// Reading is the raw counters of one stats call.
type Reading struct {
	At            time.Time
	CPUTotalNanos uint64 // cpu_usage.total_usage
	SystemNanos   uint64 // system_cpu_usage
	OnlineCPUs    uint32
	MemoryBytes   uint64 // usage minus page cache, clamped at 0
	MemoryLimit   uint64
}

// DiskUsage is the Docker engine's disk accounting in bytes.
type DiskUsage struct {
	ImagesBytes     int64
	ContainersBytes int64
	VolumesBytes    int64
	BuildCacheBytes int64
	LayersBytes     int64
}

// ContainerCounts is the engine's own container tally from /info.
type ContainerCounts struct {
	Total   int
	Running int
	Paused  int
	Stopped int
}

// Engine is the small set of engine facts the engine route returns. It has
// no host name and no path by construction.
type Engine struct {
	Version     string
	APIVersion  string // the engine's highest supported API version
	OS          string
	Arch        string
	CPUs        int
	MemoryBytes int64
	Containers  ContainerCounts
	Images      int
}

// Client reads from the Docker engine of one profile.
type Client interface {
	// Containers lists all containers, running or not.
	Containers(ctx context.Context) ([]Container, error)
	// Stats takes one one-shot reading. A container that is gone or not
	// running gives ErrContainerNotFound.
	Stats(ctx context.Context, id string) (Reading, error)
	DiskUsage(ctx context.Context) (DiskUsage, error)
	// Info reads the engine facts from /info and /version. Only the fields
	// of Engine are decoded.
	Info(ctx context.Context) (Engine, error)
	// Images lists all images. The in-use count comes from the container
	// list, not from Docker's images Containers field, which is -1 on API
	// v1.41.
	Images(ctx context.Context) ([]Image, error)
	// Volumes lists all volumes from /system/df, the only call that carries
	// sizes and reference counts.
	Volumes(ctx context.Context) ([]Volume, error)
	// Networks lists all networks. The attached count comes from the
	// container list, because the network list has no Containers map.
	Networks(ctx context.Context) ([]Network, error)
	// Logs reads the last tail lines (1 to MaxLogTail) of a container's
	// stdout and stderr, running or stopped. An unknown container gives
	// ErrContainerNotFound and an unreadable log driver ErrLogsUnavailable.
	Logs(ctx context.Context, id string, tail int) (LogResult, error)

	// CreateVolume creates a local volume. name and labels are validated here
	// (ValidObjectName, ValidLabels); an invalid one gives ErrBadName before
	// any request. An existing name gives ErrConflict.
	CreateVolume(ctx context.Context, name string, labels map[string]string) (Volume, error)
	// InspectVolume reads one volume with its labels and the containers that
	// mount it. An unknown name gives ErrObjectNotFound.
	InspectVolume(ctx context.Context, name string) (VolumeDetail, error)
	// RemoveVolume removes a volume, never with force. A mounted volume gives
	// *InUseError (an ErrConflict) and an unknown one ErrObjectNotFound.
	RemoveVolume(ctx context.Context, name string) error
	// PruneVolumes removes unused volumes. On API v1.41 that includes named
	// ones (PruneIncludesNamedVolumes).
	PruneVolumes(ctx context.Context) (PruneResult, error)

	// CreateNetwork creates a bridge network (never another driver). name and
	// subnet are validated here (ValidObjectName, NormalizeSubnet; an empty
	// subnet lets Docker choose); an invalid one gives ErrBadName before any
	// request. An existing name gives ErrConflict.
	CreateNetwork(ctx context.Context, name string, internal bool, subnet string) (Network, error)
	// InspectNetwork reads one network by name or ID with its subnets and
	// attached containers. An unknown one gives ErrObjectNotFound.
	InspectNetwork(ctx context.Context, ref string) (NetworkDetail, error)
	// RemoveNetwork removes a network, never with force. A predefined network
	// gives ErrConflict without a request to remove it, one with containers
	// *InUseError (an ErrConflict) and an unknown one ErrObjectNotFound.
	RemoveNetwork(ctx context.Context, ref string) error
	// PruneNetworks removes unused networks; Docker keeps the predefined ones.
	PruneNetworks(ctx context.Context) (NetworkPruneResult, error)

	// TagImage adds repository:tag to the image found by ID or reference. All
	// arguments are validated here (ValidImageRef, ValidImageTarget); an
	// invalid one gives ErrBadName before any request. A target tag that is on
	// another image gives ErrConflict (Docker would silently move it).
	TagImage(ctx context.Context, ref, repository, tag string) error
	// InspectImage reads one image by ID or reference: no environment, labels,
	// commands or layer command text. An unknown one gives ErrObjectNotFound.
	InspectImage(ctx context.Context, ref string) (ImageDetail, error)
	// RemoveImage removes an image (by ID: with all its references) or untags
	// one reference, never with force. An image used by any container gives
	// *InUseError (an ErrConflict) and an unknown one ErrObjectNotFound.
	RemoveImage(ctx context.Context, ref string) (ImageRemoval, error)
	// PruneImages removes unused images: dangling only, or with all every
	// unused image (PruneImagesIncludesTagged).
	PruneImages(ctx context.Context, all bool) (ImagePruneResult, error)
	// PullImage pulls ref (a validated reference that carries a tag or a
	// digest, see ValidPullRef; anything else gives ErrBadName before any
	// request) from a public registry, with no credentials. emit receives the
	// compact progress lines: one per layer state change, never raw Docker
	// output. It blocks until the pull ends. A failure the registry or Docker
	// reported is a *PullError whose Message is safe to show; cancelling ctx
	// stops the HTTP stream and returns an error wrapping ctx.Err().
	PullImage(ctx context.Context, ref string, emit func(line string)) error
}

// Provider returns a Client for a profile, or ErrVMNotRunning,
// ErrRuntimeNotDocker or ErrDockerUnavailable.
type Provider interface {
	For(ctx context.Context, profile string) (Client, error)
}

// Usage is the per-container result of Snapshot.
type Usage struct {
	Reading     Reading // keep as prev for the next Snapshot; zero if not running
	CPUPercent  float64
	MemoryBytes uint64
}

// Snapshot reads stats for the running containers (at most maxStatsInFlight at
// a time) and computes CPU percent against prev, keyed by container ID. Not
// running containers get a zero Usage. A container that vanished between list
// and stats is left out. The first other error cancels the rest and is
// returned.
func Snapshot(ctx context.Context, c Client, containers []Container, prev map[string]Reading) (map[string]Usage, error) {
	out := make(map[string]Usage, len(containers))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		sem      = make(chan struct{}, maxStatsInFlight)
	)
	for _, ct := range containers {
		if ct.State != "running" {
			mu.Lock()
			out[ct.ID] = Usage{}
			mu.Unlock()
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(ct Container) {
			defer wg.Done()
			defer func() { <-sem }()
			cur, err := c.Stats(ctx, ct.ID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				u := Usage{Reading: cur, MemoryBytes: cur.MemoryBytes}
				if p, ok := prev[ct.ID]; ok {
					u.CPUPercent = CPUPercent(p, cur)
				}
				out[ct.ID] = u
			case isGone(err):
			default:
				if firstErr == nil {
					firstErr = err
					cancel()
				}
			}
		}(ct)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func isGone(err error) bool { return errors.Is(err, ErrContainerNotFound) }
