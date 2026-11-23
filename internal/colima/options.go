package colima

import (
	"math"
	"regexp"
	"strconv"
)

// Typed enums for the start flags. The value lists are duplicated from
// internal/config on purpose (the adapter must not import the Config Service);
// a test pins the two together.
type (
	Runtime   string
	VMType    string
	Arch      string
	MountType string
)

const (
	RuntimeDocker     Runtime = "docker"
	RuntimeContainerd Runtime = "containerd"
	RuntimeIncus      Runtime = "incus"

	VMTypeQEMU    VMType = "qemu"
	VMTypeVZ      VMType = "vz"
	VMTypeKrunkit VMType = "krunkit"

	ArchAarch64 Arch = "aarch64"
	ArchX8664   Arch = "x86_64"

	MountSSHFS    MountType = "sshfs"
	MountNineP    MountType = "9p"
	MountVirtiofs MountType = "virtiofs"
)

var (
	validRuntimes   = []Runtime{RuntimeDocker, RuntimeContainerd, RuntimeIncus}
	validVMTypes    = []VMType{VMTypeQEMU, VMTypeVZ, VMTypeKrunkit}
	validArchs      = []Arch{ArchAarch64, ArchX8664}
	validMountTypes = []MountType{MountSSHFS, MountNineP, MountVirtiofs}
)

// k8sVersionRE is deliberately narrow: it is the only free-text start option.
// It mirrors the pattern in internal/config. The first character is never a
// dash, so the value cannot be read as a flag.
var k8sVersionRE = regexp.MustCompile(`^v?[0-9]+\.[0-9]+(\.[0-9]+)?(-[0-9A-Za-z.]+)?(\+k3s[0-9]+)?$`)

const maxK8sVersionLen = 64

// Limits are the host caps for resource options, injected so the adapter does
// not depend on the Config Service and tests stay deterministic. The zero
// value permits no resource option at all.
type Limits struct {
	MaxCPU       int
	MaxMemoryGiB float64
	MaxDiskGiB   int
}

// StartOptions are the flags `colima start` may be given. A nil pointer or an
// empty value means the flag is omitted, so Colima keeps its default or the
// value already stored in the profile.
//
// Only these fields exist. There is deliberately no way to pass --edit (opens
// an interactive editor) or free-text flags.
//
// Colima's --save-config defaults to on and this package does not override
// it: a start with options persists them to the profile's YAML file.
type StartOptions struct {
	CPUs              *int     // --cpus
	MemoryGiB         *float64 // --memory (GiB, fractional allowed)
	DiskGiB           *int     // --disk
	RootDiskGiB       *int     // --root-disk
	Runtime           Runtime  // --runtime
	VMType            VMType   // --vm-type
	Arch              Arch     // --arch
	MountType         MountType
	Kubernetes        bool   // --kubernetes (omitted when false)
	KubernetesVersion string // --kubernetes-version
}

func bad(field, reason string) error { return &InvalidOptionError{Field: field, Reason: reason} }

// Validate checks every set option against lim and the allowed values. It
// returns an *InvalidOptionError for the first problem, or nil.
func (o StartOptions) Validate(lim Limits) error {
	if o.CPUs != nil && (*o.CPUs < 1 || *o.CPUs > lim.MaxCPU) {
		return bad("cpus", "out of range")
	}
	if o.MemoryGiB != nil {
		// Written so NaN fails too.
		if m := *o.MemoryGiB; !(m >= 1 && m <= lim.MaxMemoryGiB) || math.IsInf(m, 0) {
			return bad("memory", "out of range")
		}
	}
	if o.DiskGiB != nil && (*o.DiskGiB < 1 || *o.DiskGiB > lim.MaxDiskGiB) {
		return bad("disk", "out of range")
	}
	if o.RootDiskGiB != nil && (*o.RootDiskGiB < 1 || *o.RootDiskGiB > lim.MaxDiskGiB) {
		return bad("root-disk", "out of range")
	}
	if !oneOf(o.Runtime, validRuntimes) {
		return bad("runtime", "not a supported value")
	}
	if !oneOf(o.VMType, validVMTypes) {
		return bad("vm-type", "not a supported value")
	}
	if !oneOf(o.Arch, validArchs) {
		return bad("arch", "not a supported value")
	}
	if !oneOf(o.MountType, validMountTypes) {
		return bad("mount-type", "not a supported value")
	}
	if v := o.KubernetesVersion; v != "" && (len(v) > maxK8sVersionLen || !k8sVersionRE.MatchString(v)) {
		return bad("kubernetes-version", "unrecognised version format")
	}
	return nil
}

// oneOf accepts the empty value (flag omitted) or a member of allowed.
func oneOf[T ~string](v T, allowed []T) bool {
	if v == "" {
		return true
	}
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// args renders the flags in a fixed order, one argv element per flag and per
// value. It must only be called on validated options.
func (o StartOptions) args() []string {
	var a []string
	if o.CPUs != nil {
		a = append(a, "--cpus", strconv.Itoa(*o.CPUs))
	}
	if o.MemoryGiB != nil {
		a = append(a, "--memory", strconv.FormatFloat(*o.MemoryGiB, 'f', -1, 64))
	}
	if o.DiskGiB != nil {
		a = append(a, "--disk", strconv.Itoa(*o.DiskGiB))
	}
	if o.RootDiskGiB != nil {
		a = append(a, "--root-disk", strconv.Itoa(*o.RootDiskGiB))
	}
	if o.Runtime != "" {
		a = append(a, "--runtime", string(o.Runtime))
	}
	if o.VMType != "" {
		a = append(a, "--vm-type", string(o.VMType))
	}
	if o.Arch != "" {
		a = append(a, "--arch", string(o.Arch))
	}
	if o.MountType != "" {
		a = append(a, "--mount-type", string(o.MountType))
	}
	if o.Kubernetes {
		a = append(a, "--kubernetes")
	}
	if o.KubernetesVersion != "" {
		a = append(a, "--kubernetes-version", o.KubernetesVersion)
	}
	return a
}
