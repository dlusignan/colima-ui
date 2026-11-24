package dockerc

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// containerActionTimeout bounds one start, stop or restart. It is longer than
// the default write deadline because Docker waits out the grace period (10
// seconds unless the container sets its own) before it kills a container.
const containerActionTimeout = 60 * time.Second

// containerTarget is /containers/{id}/{verb} for a validated id. It fails with
// ErrBadName unless id passes ValidContainerID, before any request.
func containerTarget(id string, verb fixedPath) (target, error) {
	if !ValidContainerID(id) {
		return target{}, ErrBadName
	}
	return target{path: "/containers/" + id + string(verb)}, nil
}

// containerAction posts one lifecycle verb. Docker answers 204 when it did the
// work and 304 when the container was already in that state; 304 is a success
// that changed nothing. No query is sent, so stop and restart use the
// container's own stop timeout (Docker's default grace period). Docker's 400 on
// an existing container means it refuses the container as it is (start answers
// 400 when its resource limits no longer fit the host), so it is an
// ErrConflict, like 409. The id is always a full id, so there is no ambiguity
// that could make a 400 mean "not found".
func (c *httpClient) containerAction(ctx context.Context, id string, verb fixedPath, alreadyOK bool) (bool, error) {
	t, err := containerTarget(id, verb)
	if err != nil {
		return false, err
	}
	ok := []int{http.StatusNoContent}
	if alreadyOK {
		ok = append(ok, http.StatusNotModified)
	}
	status, err := c.postStatus(ctx, containerActionTimeout, t, nil, ok...)
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			return false, ErrConflict
		}
		return false, err
	}
	return status == http.StatusNoContent, nil
}

// StartContainer starts a stopped container. It reports whether anything
// changed: false when Docker says the container was already running. A paused
// container or any other state Docker refuses gives ErrConflict and an unknown
// one ErrObjectNotFound.
func (c *httpClient) StartContainer(ctx context.Context, id string) (bool, error) {
	return c.containerAction(ctx, id, "/start", true)
}

// StopContainer stops a running container with Docker's default grace period.
// It reports whether anything changed: false when it was already stopped.
func (c *httpClient) StopContainer(ctx context.Context, id string) (bool, error) {
	return c.containerAction(ctx, id, "/stop", true)
}

// RestartContainer restarts a container with Docker's default grace period.
// Docker has no "already" answer for a restart, so a success is always a
// change.
func (c *httpClient) RestartContainer(ctx context.Context, id string) (bool, error) {
	return c.containerAction(ctx, id, "/restart", false)
}
