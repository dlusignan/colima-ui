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

// Client reads from the Docker engine of one profile.
type Client interface {
	// Containers lists all containers, running or not.
	Containers(ctx context.Context) ([]Container, error)
	// Stats takes one one-shot reading. A container that is gone or not
	// running gives ErrContainerNotFound.
	Stats(ctx context.Context, id string) (Reading, error)
	DiskUsage(ctx context.Context) (DiskUsage, error)
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
