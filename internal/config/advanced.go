package config

import (
	"fmt"
	"net/netip"
	"regexp"
)

// Bounds and patterns for the advanced creation settings. Every pattern starts
// with a letter or digit, so a value can never be read as a flag, and none
// admits whitespace, commas, equals signs or control characters.
const (
	maxDNS      = 8
	maxDNSHosts = 16
	maxHostName = 253
)

var (
	// hostnameRE is one DNS label (at most 63 characters).
	hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	// interfaceRE fits Linux and macOS interface names (at most 15 characters).
	interfaceRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)
	// cpuTypeRE is a CPU model name such as max, host, cortex-a72 or Skylake-Client-v1.
	// A feature list (host,+x2apic) is not accepted.
	cpuTypeRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// DNSHost is one custom DNS name given to the VM's resolver.
type DNSHost struct {
	Name    string
	Address string
}

// NewAdvanced holds the optional creation settings that have no place in the
// profile view: networking, DNS, hostname, SSH agent, Rosetta and so on. The
// zero value of each field means the setting is left to Colima.
type NewAdvanced struct {
	Hostname         string
	DNS              []string
	DNSHosts         []DNSHost
	SSHAgent         bool
	VZRosetta        bool
	PortForwarder    string
	MountInotify     bool
	NetworkAddress   bool
	NetworkMode      string
	NetworkInterface string
	CPUType          string
}

// validAddress reports whether s is one IPv4 or IPv6 address in the textual
// form Colima's resolver settings take: no zone, no port, no prefix.
func validAddress(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.Zone() == ""
}

// validHostName reports whether s is a DNS name of dot-separated labels, at
// most 253 characters, without a trailing dot.
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

// validateAdvanced returns field errors named with the creation body's keys
// (the creator passes them through unchanged). vmType and arch come from the
// profile being created: an empty one means Colima's default, which `colima
// start --help` documents as vz and aarch64.
//
// Cross-field rules kept, with where each comes from:
//   - vzRosetta needs vmType vz and arch aarch64: the flag is "enable Rosetta
//     for amd64 emulation", which exists only for the vz VM type on an arm VM.
//   - networkInterface needs networkMode bridged: the flag is "host network
//     interface to use for bridged mode", and the mode defaults to shared.
//
// Rules deliberately not kept, because the help text does not state them:
// cpuType only with qemu, bridged needing networkAddress, and anything about
// the port forwarder. Colima refuses what it cannot run and the job fails with
// its output.
func validateAdvanced(a NewAdvanced, vmType, arch string) []FieldError {
	var errs []FieldError
	add := func(field, msg string) { errs = append(errs, FieldError{Field: field, Message: msg}) }

	if a.Hostname != "" && !hostnameRE.MatchString(a.Hostname) {
		add("hostname", "must be a DNS label: letters, digits and hyphens, starting and ending with a letter or digit, at most 63 characters")
	}
	if len(a.DNS) > maxDNS {
		add("dns", fmt.Sprintf("must list at most %d addresses", maxDNS))
	} else {
		for i, d := range a.DNS {
			if !validAddress(d) {
				add(fmt.Sprintf("dns[%d]", i), "must be an IPv4 or IPv6 address")
			}
		}
	}
	if len(a.DNSHosts) > maxDNSHosts {
		add("dnsHosts", fmt.Sprintf("must list at most %d entries", maxDNSHosts))
	} else {
		for i, h := range a.DNSHosts {
			if !validHostName(h.Name) {
				add(fmt.Sprintf("dnsHosts[%d].name", i), "must be a host name of letters, digits, hyphens and dots, at most 253 characters")
			}
			if !validAddress(h.Address) {
				add(fmt.Sprintf("dnsHosts[%d].address", i), "must be an IPv4 or IPv6 address")
			}
		}
	}
	if a.PortForwarder != "" && a.PortForwarder != "ssh" && a.PortForwarder != "grpc" && a.PortForwarder != "none" {
		add("portForwarder", "must be one of ssh, grpc, none")
	}
	if a.NetworkMode != "" && a.NetworkMode != "shared" && a.NetworkMode != "bridged" {
		add("networkMode", "must be one of shared, bridged")
	}
	if a.NetworkInterface != "" {
		switch {
		case !interfaceRE.MatchString(a.NetworkInterface):
			add("networkInterface", "must be an interface name of letters, digits, dots, underscores and hyphens, at most 15 characters")
		case a.NetworkMode != "bridged":
			add("networkInterface", "requires networkMode bridged")
		}
	}
	if a.CPUType != "" && !cpuTypeRE.MatchString(a.CPUType) {
		add("cpuType", "must be a CPU model name of letters, digits, dots, underscores and hyphens, at most 64 characters")
	}
	if a.VZRosetta && ((vmType != "" && vmType != "vz") || (arch != "" && arch != "aarch64")) {
		add("vzRosetta", "requires vmType vz and arch aarch64")
	}
	return errs
}
