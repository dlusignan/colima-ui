package colima

import (
	"context"
	"encoding/json"
	"errors"
)

// Details describes a running profile as reported by `colima status --json`.
// Note that status uses "cpu" where list uses "cpus".
type Details struct {
	DisplayName      string `json:"display_name"`
	Driver           string `json:"driver"`
	Arch             string `json:"arch"`
	Runtime          string `json:"runtime"`
	MountType        string `json:"mount_type"`
	DockerSocket     string `json:"docker_socket"`
	ContainerdSocket string `json:"containerd_socket"`
	Kubernetes       bool   `json:"kubernetes"`
	CPUs             int    `json:"cpu"`
	Memory           int64  `json:"memory"` // bytes
	Disk             int64  `json:"disk"`   // bytes
}

// Status runs `colima status --json -p <profile>`. Colima exits 1 for a
// profile that is stopped or does not exist and cannot tell the two apart, so
// callers should use List for existence and state and call Status only for a
// running profile. The profile is validated before any process starts.
func Status(ctx context.Context, r Runner, profile string) (Details, error) {
	if err := ValidateProfile(profile); err != nil {
		return Details{}, err
	}
	res, err := run(ctx, r, []string{"status", "--json", "-p", profile})
	if err != nil {
		return Details{}, err
	}
	var d Details
	if err := json.Unmarshal(res.Stdout, &d); err != nil {
		return Details{}, &ParseError{Op: "status", Raw: string(res.Stdout), Err: err}
	}
	if d.Runtime == "" {
		return Details{}, &ParseError{Op: "status", Raw: string(res.Stdout), Err: errors.New("missing runtime")}
	}
	return d, nil
}
