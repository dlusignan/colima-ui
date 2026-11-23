package config

import (
	"fmt"
	"path"
	"regexp"
	"runtime"
	"strings"
)

// enums is the single table of allowed values (Colima 0.10.x). A version bump
// edits only this table.
var enums = map[string][]string{
	"arch":         {"aarch64", "x86_64"},
	"runtime":      {"docker", "containerd", "incus"},
	"vmType":       {"qemu", "vz", "krunkit"},
	"mountType":    {"sshfs", "9p", "virtiofs"},
	"network.mode": {"shared", "bridged"},
}

// k3sVersionRE is deliberately narrow: it is the only free-text value that
// could reach a command line.
var k3sVersionRE = regexp.MustCompile(`^v?[0-9]+\.[0-9]+(\.[0-9]+)?(-[0-9A-Za-z.]+)?(\+k3s[0-9]+)?$`)

// Limits are the host caps for resource fields, injected so tests are
// deterministic.
type Limits struct {
	MaxCPU       int
	MaxMemoryGiB float64
	MaxDiskGiB   int
}

// DefaultLimits returns caps from the host CPU count and fixed ceilings.
func DefaultLimits() Limits {
	return Limits{MaxCPU: runtime.NumCPU(), MaxMemoryGiB: 256, MaxDiskGiB: 8192}
}

// Validate checks a proposed profile and returns field-level errors (nil when
// valid). current, when non-nil, is the stored profile and enables the
// "disk cannot shrink" rule. Unlisted fields are not validated.
func Validate(p *Profile, lim Limits, current *Profile) []FieldError {
	var errs []FieldError
	add := func(field, format string, a ...any) {
		errs = append(errs, FieldError{Field: field, Message: fmt.Sprintf(format, a...)})
	}
	if p == nil {
		return []FieldError{{Field: "", Message: "no profile"}}
	}

	if p.CPU != nil && (*p.CPU < 1 || *p.CPU > lim.MaxCPU) {
		add("cpu", "must be between 1 and %d", lim.MaxCPU)
	}
	if p.Memory != nil {
		// Written so NaN fails too.
		if m := *p.Memory; !(m >= 1 && m <= lim.MaxMemoryGiB) {
			add("memory", "must be between 1 and %g GiB", lim.MaxMemoryGiB)
		}
	}
	if p.Disk != nil {
		switch {
		case *p.Disk < 1 || *p.Disk > lim.MaxDiskGiB:
			add("disk", "must be between 1 and %d GiB", lim.MaxDiskGiB)
		case current != nil && current.Disk != nil && *p.Disk < *current.Disk:
			add("disk", "cannot be smaller than the current %d GiB", *current.Disk)
		}
	}
	if p.RootDisk != nil && (*p.RootDisk < 1 || *p.RootDisk > lim.MaxDiskGiB) {
		add("rootDisk", "must be between 1 and %d GiB", lim.MaxDiskGiB)
	}

	enumCheck := func(field, value string) {
		if value == "" {
			return
		}
		for _, ok := range enums[field] {
			if value == ok {
				return
			}
		}
		add(field, "must be one of %s", strings.Join(enums[field], ", "))
	}
	enumCheck("arch", p.Arch)
	enumCheck("runtime", p.Runtime)
	enumCheck("vmType", p.VMType)
	enumCheck("mountType", p.MountType)
	enumCheck("network.mode", p.Network.Mode)

	if v := p.Kubernetes.Version; v != "" && (len(v) > 64 || !k3sVersionRE.MatchString(v)) {
		add("kubernetes.version", "must look like v1.31.2+k3s1")
	}

	for i, m := range p.Mounts {
		if msg := checkMountPath(m.Location, true); msg != "" {
			add(fmt.Sprintf("mounts[%d].location", i), "%s", msg)
		}
		if m.MountPoint != "" {
			if msg := checkMountPath(m.MountPoint, false); msg != "" {
				add(fmt.Sprintf("mounts[%d].mountPoint", i), "%s", msg)
			}
		}
	}
	return errs
}

// checkMountPath returns a problem description or "". Paths use forward
// slashes (they are host or guest POSIX paths).
func checkMountPath(p string, required bool) string {
	if p == "" {
		if required {
			return "is required"
		}
		return ""
	}
	if strings.ContainsAny(p, "\x00\n\r") {
		return "contains a control character"
	}
	if !path.IsAbs(p) {
		return "must be an absolute path"
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "must not contain .. segments"
		}
	}
	if path.Clean(p) != p {
		return "must be a clean path"
	}
	return ""
}
