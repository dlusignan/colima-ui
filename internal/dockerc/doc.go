// Package dockerc reads container and disk data from the Docker Engine inside
// a Colima VM. It is a small hand-rolled HTTP client over the Unix socket that
// Colima exposes per profile, pinned to API version 1.41 (one-shot stats needs
// 1.41 or newer, and the pin keeps the /system/df JSON shape stable). It is
// mostly read: list containers, read one stats sample, read disk usage. Writes
// go through the unexported post and delete helpers (write.go), which map
// Docker's status to typed errors (ErrObjectNotFound, ErrConflict,
// ErrInvalidRequest, ErrDaemon) and drop the daemon's error text. Every write
// URL is built with ObjectPath or RefPath, after ValidObjectName or
// ValidImageRef, and the helpers refuse any path with a "." or ".." segment.
// The HTTP client never follows a redirect.
//
// The exported interface (Client, Provider) uses only plain value types, so
// consumers such as the metrics sampler and the containers route can fake it.
// The package never runs colima itself: the socket path comes from an injected
// SocketResolver. NewColimaResolver builds one on top of the colima adapter.
//
// Errors are sentinels checked with errors.Is (ErrVMNotRunning,
// ErrDockerUnavailable, ErrRuntimeNotDocker, ErrContainerNotFound). Their text
// never contains the socket path or any other absolute path.
//
// Stats are raw counters: a one-shot stats call returns a zeroed precpu_stats,
// so CPU percent comes from two Readings via CPUPercent, and the caller keeps
// the previous reading. DiskUsage is heavy on a large image store; callers
// should poll it far less often than Stats.
package dockerc
