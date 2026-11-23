package httpapi

import (
	"context"
	"errors"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/operations"
)

// failingLauncher never starts a process; it stands in for the colima adapter.
type failingLauncher struct{}

func (failingLauncher) Launch(context.Context, operations.Request) (colima.Process, error) {
	return nil, errors.New("launcher not available in tests")
}

var _ JobsDeps = (*operations.Manager)(nil)
