package profiles

import (
	"context"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/config"
)

// ColimaSource is the live side: what the colima binary reports.
type ColimaSource interface {
	List(ctx context.Context) ([]colima.Instance, error)
	Version(ctx context.Context) (colima.VersionInfo, error)
}

// ConfigSource is the file side. *config.Service satisfies it.
type ConfigSource interface {
	ListProfiles() ([]string, error)
	Read(name string) (*config.Profile, error)
}

type runnerSource struct{ r colima.Runner }

// NewColimaSource wraps a Runner as a ColimaSource.
func NewColimaSource(r colima.Runner) ColimaSource { return runnerSource{r: r} }

func (s runnerSource) List(ctx context.Context) ([]colima.Instance, error) {
	return colima.List(ctx, s.r)
}

func (s runnerSource) Version(ctx context.Context) (colima.VersionInfo, error) {
	return colima.Version(ctx, s.r)
}
