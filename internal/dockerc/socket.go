package dockerc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// SocketResolver returns the Docker socket path for a profile, or
// ErrVMNotRunning, ErrRuntimeNotDocker or ErrDockerUnavailable.
type SocketResolver func(ctx context.Context, profile string) (string, error)

type provider struct{ resolve SocketResolver }

// NewProvider returns a Provider that resolves the socket on every call, so a
// restarted VM is picked up. The resulting Client is cheap to build.
func NewProvider(resolve SocketResolver) Provider { return &provider{resolve: resolve} }

func (p *provider) For(ctx context.Context, profile string) (Client, error) {
	sock, err := p.resolve(ctx, profile)
	if err != nil {
		return nil, err
	}
	return newHTTPClient(sock), nil
}

// NewColimaResolver builds a SocketResolver from the colima adapter: list for
// state and runtime, then status for the reported docker_socket. The status
// JSON is authoritative; there is no fallback to a conventional path.
func NewColimaResolver(r colima.Runner) SocketResolver {
	return func(ctx context.Context, profile string) (string, error) {
		if err := colima.ValidateProfile(profile); err != nil {
			return "", err
		}
		list, err := colima.List(ctx, r)
		if err != nil {
			return "", fmt.Errorf("dockerc: listing profiles: %w", err)
		}
		var inst *colima.Instance
		for i := range list {
			if list[i].Name == profile {
				inst = &list[i]
				break
			}
		}
		if inst == nil || inst.Status != colima.StatusRunning {
			return "", ErrVMNotRunning
		}
		d, err := colima.Status(ctx, r, profile)
		if err != nil {
			return "", fmt.Errorf("dockerc: reading status: %w", err)
		}
		// Both reads must name the same known runtime. Anything else (empty,
		// unknown, or the two disagreeing) fails closed as unavailable: only an
		// explicit containerd or incus is "not Docker".
		listed, reported := NormalizeRuntime(inst.Runtime), NormalizeRuntime(d.Runtime)
		switch {
		case listed != reported || !knownRuntime(listed):
			return "", fmt.Errorf("%w: runtime not recognised", ErrDockerUnavailable)
		case listed != "docker":
			return "", ErrRuntimeNotDocker
		}
		sock := strings.TrimPrefix(d.DockerSocket, "unix://")
		if sock == "" || !filepath.IsAbs(sock) {
			return "", fmt.Errorf("%w: no docker socket reported", ErrDockerUnavailable)
		}
		fi, err := os.Stat(sock)
		if err != nil || fi.Mode()&os.ModeSocket == 0 {
			return "", fmt.Errorf("%w: docker socket missing", ErrDockerUnavailable)
		}
		return sock, nil
	}
}

// NormalizeRuntime lowercases and trims a runtime name and drops a "+k3s"
// suffix (the form Colima may use when Kubernetes runs on the runtime).
func NormalizeRuntime(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.TrimSuffix(s, "+k3s")
}

// knownRuntime is the allowlist of runtimes the resolver can classify.
func knownRuntime(r string) bool {
	switch r {
	case "docker", "containerd", "incus":
		return true
	}
	return false
}
