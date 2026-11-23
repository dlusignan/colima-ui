package dockerc

import "errors"

// Sentinel errors. They carry no paths; wrap them with %w and check with
// errors.Is.
var (
	// ErrVMNotRunning means the profile is stopped (or unknown to colima).
	ErrVMNotRunning = errors.New("dockerc: vm is not running")
	// ErrDockerUnavailable means the profile runs but its Docker socket is
	// missing, refuses connections, or answers with something unusable.
	ErrDockerUnavailable = errors.New("dockerc: docker is unavailable")
	// ErrRuntimeNotDocker means the profile uses another container runtime, so
	// Docker data does not apply.
	ErrRuntimeNotDocker = errors.New("dockerc: profile runtime is not docker")
	// ErrContainerNotFound means the container is gone or no longer running.
	ErrContainerNotFound = errors.New("dockerc: container not found")
)
