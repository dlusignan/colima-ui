package colima

import (
	"math"
	"net/netip"
	"regexp"
	"runtime"
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

// Patterns and bounds for the advanced settings. They mirror internal/config
// (a test pins the two together). Each pattern starts with a letter or digit,
// so a value is never read as a flag, and none admits whitespace, commas,
// equals signs or control characters.
var (
	hostnameRE  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	interfaceRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)
	cpuTypeRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

const (
	maxDNS      = 8
	maxDNSHosts = 16
	maxHostName = 253
)

// PortForwarder, NetworkMode are the allow-listed values of their flags.
type (
	PortForwarder string
	NetworkMode   string
)

const (
	PortForwarderSSH  PortForwarder = "ssh"
	PortForwarderGRPC PortForwarder = "grpc"
	PortForwarderNone PortForwarder = "none"

	NetworkModeShared  NetworkMode = "shared"
	NetworkModeBridged NetworkMode = "bridged"
)

var (
	validPortForwarders = []PortForwarder{PortForwarderSSH, PortForwarderGRPC, PortForwarderNone}
	validNetworkModes   = []NetworkMode{NetworkModeShared, NetworkModeBridged}
)

// DNSHost is one --dns-host entry. Colima takes it as name=address, so args
// builds that single element from these two validated parts.
type DNSHost struct {
	Name    string
	Address string
}

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

	// Advanced settings. Each maps to one fixed flag; an empty value, a nil
	// slice or false omits it. Arrays become repeated flags.
	Hostname         string        // --hostname
	DNS              []string      // --dns, repeated, at most 8
	DNSHosts         []DNSHost     // --dns-host name=address, repeated, at most 16
	SSHAgent         bool          // --ssh-agent
	VZRosetta        bool          // --vz-rosetta, only for vm type vz on arch aarch64
	PortForwarder    PortForwarder // --port-forwarder
	MountInotify     bool          // --mount-inotify
	NetworkAddress   bool          // --network-address
	NetworkMode      NetworkMode   // --network-mode
	NetworkInterface string        // --network-interface, only with network mode bridged
	CPUType          string        // --cpu-type

	// Host folders and VM environment. Mounts become repeated --mount path or
	// path:w, Env repeated --env KEY=value, in slice order. At most 16 and 32.
	// Giving --mount may replace Colima's default home mount (see docs/api.md).
	Mounts []Mount
	Env    []EnvVar
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
	if err := o.validateAdvanced(); err != nil {
		return err
	}
	return o.validateMountsEnv()
}

func (o StartOptions) validateAdvanced() error {
	if o.Hostname != "" && !hostnameRE.MatchString(o.Hostname) {
		return bad("hostname", "not a DNS label")
	}
	if len(o.DNS) > maxDNS {
		return bad("dns", "too many addresses")
	}
	for _, d := range o.DNS {
		if _, ok := canonicalAddr(d); !ok {
			return bad("dns", "not an IP address")
		}
	}
	if len(o.DNSHosts) > maxDNSHosts {
		return bad("dns-host", "too many entries")
	}
	for _, h := range o.DNSHosts {
		if !validHostName(h.Name) {
			return bad("dns-host", "not a host name")
		}
		if _, ok := canonicalAddr(h.Address); !ok {
			return bad("dns-host", "not an IP address")
		}
	}
	if !oneOf(o.PortForwarder, validPortForwarders) {
		return bad("port-forwarder", "not a supported value")
	}
	if !oneOf(o.NetworkMode, validNetworkModes) {
		return bad("network-mode", "not a supported value")
	}
	if o.NetworkInterface != "" {
		if !interfaceRE.MatchString(o.NetworkInterface) {
			return bad("network-interface", "not an interface name")
		}
		if o.NetworkMode != NetworkModeBridged {
			return bad("network-interface", "needs network mode bridged")
		}
	}
	if o.CPUType != "" && !cpuTypeRE.MatchString(o.CPUType) {
		return bad("cpu-type", "not a CPU model name")
	}
	// An empty vm type is Colima's default (vz, per `colima start --help`) and
	// an empty arch is the host's.
	arch := o.Arch
	if arch == "" {
		arch = defaultArch()
	}
	if o.VZRosetta && ((o.VMType != "" && o.VMType != VMTypeVZ) || arch != ArchAarch64) {
		return bad("vz-rosetta", "needs vm type vz and arch aarch64")
	}
	return nil
}

// hostArch is the host's Go architecture. Colima's default arch follows it, so
// it decides what a left-out arch means. Tests replace it.
var hostArch = runtime.GOARCH

// defaultArch is the arch Colima picks when none is given: aarch64 on an arm64
// host, x86_64 on an amd64 host, "" (unknown) elsewhere.
func defaultArch() Arch {
	switch hostArch {
	case "arm64":
		return ArchAarch64
	case "amd64":
		return ArchX8664
	}
	return ""
}

// canonicalAddr parses one IPv4 or IPv6 address without a zone and returns its
// canonical text. args emits that text, never the caller's.
func canonicalAddr(s string) (string, bool) {
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return "", false
	}
	return a.String(), true
}

// validHostName accepts dot-separated labels, at most 253 characters in all.
func validHostName(s string) bool {
	if s == "" || len(s) > maxHostName {
		return false
	}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			if !hostnameRE.MatchString(s[start:i]) {
				return false
			}
			start = i + 1
		}
	}
	return true
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
	if o.Hostname != "" {
		a = append(a, "--hostname", o.Hostname)
	}
	for _, d := range o.DNS {
		if c, ok := canonicalAddr(d); ok {
			a = append(a, "--dns", c)
		}
	}
	for _, h := range o.DNSHosts {
		if c, ok := canonicalAddr(h.Address); ok && validHostName(h.Name) {
			a = append(a, "--dns-host", h.Name+"="+c)
		}
	}
	if o.SSHAgent {
		a = append(a, "--ssh-agent")
	}
	if o.VZRosetta {
		a = append(a, "--vz-rosetta")
	}
	if o.PortForwarder != "" {
		a = append(a, "--port-forwarder", string(o.PortForwarder))
	}
	if o.MountInotify {
		a = append(a, "--mount-inotify")
	}
	if o.NetworkAddress {
		a = append(a, "--network-address")
	}
	if o.NetworkMode != "" {
		a = append(a, "--network-mode", string(o.NetworkMode))
	}
	if o.NetworkInterface != "" {
		a = append(a, "--network-interface", o.NetworkInterface)
	}
	if o.CPUType != "" {
		a = append(a, "--cpu-type", o.CPUType)
	}
	return append(a, o.mountEnvArgs()...)
}
