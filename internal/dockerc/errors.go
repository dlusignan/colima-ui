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

// ErrLogsUnavailable means the container's log driver cannot be read back
// (Docker answers 501), for example the none or syslog drivers.
var ErrLogsUnavailable = errors.New("dockerc: container logs are not readable")

// Errors of Docker write calls (post and delete helpers). Like the sentinels
// above they carry no paths and no Docker error text: the daemon's message is
// read and dropped, because it can echo request values and host paths.
var (
	// ErrObjectNotFound means Docker answered 404: the image, volume or
	// network does not exist.
	ErrObjectNotFound = errors.New("dockerc: object not found")
	// ErrConflict means Docker refused because of the object's state: it is in
	// use (409), or predefined and protected (403, for example the bridge
	// network).
	ErrConflict = errors.New("dockerc: object is in use or protected")
	// ErrInvalidRequest means Docker answered 400: it rejected a value that
	// passed our own validation.
	ErrInvalidRequest = errors.New("dockerc: docker rejected the request")
	// ErrDaemon means Docker answered 5xx or another status that has no
	// better meaning. The status is in the wrapped text, nothing else.
	ErrDaemon = errors.New("dockerc: docker engine error")
	// ErrBadName means a name or reference failed validation. It is returned
	// before any request is made.
	ErrBadName = errors.New("dockerc: invalid object name or reference")
)
