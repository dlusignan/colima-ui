package config

import (
	"fmt"
	"strings"
	"testing"
)

func advFields(p *Profile, a NewAdvanced) map[string]string {
	if p == nil {
		p = &Profile{}
	}
	out := map[string]string{}
	for _, e := range ValidateNew(p, a, testLimits) {
		out[e.Field] = e.Message
	}
	return out
}

func TestValidateNewAdvancedAcceptsGoodValues(t *testing.T) {
	a := NewAdvanced{
		Hostname: "vm-1", DNS: []string{"1.1.1.1", "2001:db8::1"},
		DNSHosts: []DNSHost{{"example.com", "192.0.2.1"}, {"single", "::1"}},
		SSHAgent: true, VZRosetta: true, PortForwarder: "none", MountInotify: true, NetworkAddress: true,
		NetworkMode: "bridged", NetworkInterface: "en0", CPUType: "cortex-a72",
	}
	if got := advFields(&Profile{VMType: "vz", Arch: "aarch64"}, a); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := advFields(nil, NewAdvanced{}); len(got) != 0 {
		t.Fatalf("zero value: %v", got)
	}
}

func TestValidateNewAdvancedNamesEveryBadField(t *testing.T) {
	a := NewAdvanced{
		Hostname: "-x", DNS: []string{"ok.example", "1.1.1.1", "--edit"},
		DNSHosts:      []DNSHost{{"a;b", "x"}, {"ok.test", "192.0.2.1"}},
		PortForwarder: "tcp", NetworkMode: "nat", NetworkInterface: "--force", CPUType: "-cpu",
	}
	got := advFields(nil, a)
	for _, k := range []string{"hostname", "dns[0]", "dns[2]", "dnsHosts[0].name", "dnsHosts[0].address",
		"portForwarder", "networkMode", "networkInterface", "cpuType"} {
		if got[k] == "" {
			t.Errorf("missing %s in %v", k, got)
		}
	}
	if len(got) != 9 {
		t.Errorf("fields %v", got)
	}
	for k, m := range got {
		for _, bad := range []string{"--edit", "--force", "a;b", "-cpu", "ok.example"} {
			if strings.Contains(m, bad) {
				t.Errorf("%s echoes %q: %s", k, bad, m)
			}
		}
	}
}

func TestValidateNewAdvancedCounts(t *testing.T) {
	mk := func(n int) []string {
		s := make([]string, n)
		for i := range s {
			s[i] = "1.1.1.1"
		}
		return s
	}
	if got := advFields(nil, NewAdvanced{DNS: mk(8)}); len(got) != 0 {
		t.Fatal(got)
	}
	if got := advFields(nil, NewAdvanced{DNS: mk(9)}); len(got) != 1 || got["dns"] == "" {
		t.Fatal(got)
	}
	// A huge array reports one field, not one per element.
	if got := advFields(nil, NewAdvanced{DNS: mk(100000)}); len(got) != 1 || got["dns"] == "" {
		t.Fatal(len(got))
	}
	hosts := func(n int) []DNSHost {
		h := make([]DNSHost, n)
		for i := range h {
			h[i] = DNSHost{fmt.Sprintf("h%d.test", i), "192.0.2.1"}
		}
		return h
	}
	if got := advFields(nil, NewAdvanced{DNSHosts: hosts(16)}); len(got) != 0 {
		t.Fatal(got)
	}
	if got := advFields(nil, NewAdvanced{DNSHosts: hosts(17)}); len(got) != 1 || got["dnsHosts"] == "" {
		t.Fatal(got)
	}
	if got := advFields(nil, NewAdvanced{DNSHosts: hosts(100000)}); len(got) != 1 {
		t.Fatal(len(got))
	}
}

func TestValidateNewRosettaRule(t *testing.T) {
	cases := []struct {
		vmType, arch string
		ok           bool
	}{
		{"", "", true}, {"vz", "", true}, {"", "aarch64", true}, {"vz", "aarch64", true},
		{"qemu", "", false}, {"krunkit", "aarch64", false}, {"vz", "x86_64", false}, {"", "x86_64", false},
		{"qemu", "x86_64", false},
	}
	for _, c := range cases {
		got := advFields(&Profile{VMType: c.vmType, Arch: c.arch}, NewAdvanced{VZRosetta: true})
		if (len(got) == 0) != c.ok || (!c.ok && got["vzRosetta"] == "") {
			t.Errorf("%q/%q: %v", c.vmType, c.arch, got)
		}
		// Off means no rule applies.
		if got := advFields(&Profile{VMType: c.vmType, Arch: c.arch}, NewAdvanced{}); len(got) != 0 {
			t.Errorf("%q/%q without rosetta: %v", c.vmType, c.arch, got)
		}
	}
}

func TestValidateNewNetworkInterfaceNeedsBridged(t *testing.T) {
	for mode, ok := range map[string]bool{"": false, "shared": false, "bridged": true} {
		got := advFields(nil, NewAdvanced{NetworkMode: mode, NetworkInterface: "en0"})
		if (len(got) == 0) != ok {
			t.Errorf("mode %q: %v", mode, got)
		}
	}
}

// Rules the help text does not state are not enforced here.
func TestValidateNewAdvancedDropsUnconfirmedRules(t *testing.T) {
	for _, a := range []NewAdvanced{
		{CPUType: "max"}, // with the default vz VM type
		{NetworkMode: "bridged"},
		{NetworkAddress: false, NetworkMode: "shared"},
		{PortForwarder: "grpc", NetworkMode: "bridged"},
		{DNS: []string{"1.1.1.1", "1.1.1.1"}},
		{DNSHosts: []DNSHost{{"a.test", "192.0.2.1"}, {"a.test", "192.0.2.2"}}},
	} {
		if got := advFields(&Profile{VMType: "vz"}, a); len(got) != 0 {
			t.Errorf("%+v: %v", a, got)
		}
	}
}

func TestAddressAndHostNameRules(t *testing.T) {
	for _, s := range []string{"1.1.1.1", "255.255.255.255", "::", "::1", "2001:db8::1", "::ffff:192.0.2.1"} {
		if !validAddress(s) {
			t.Errorf("rejected %q", s)
		}
	}
	for _, s := range []string{"", "1.1.1", "256.1.1.1", "01.1.1.1", "1.1.1.1/8", "1.1.1.1:53", "[::1]", "fe80::1%en0", " 1.1.1.1",
		"1.1.1.1 ", "1.1.1.1\n", "1.1.1.1,8.8.8.8", "localhost", "0x7f.1", "-1.1.1.1"} {
		if validAddress(s) {
			t.Errorf("accepted %q", s)
		}
	}
	for _, s := range []string{"a", "a.b", "A-1.example.com", "1.2.3.4", strings.Repeat("a", 63)} {
		if !validHostName(s) {
			t.Errorf("rejected %q", s)
		}
	}
	for _, s := range []string{"", ".", "a.", ".a", "a..b", "-a", "a-", "a_b", "a b", "a*", "*.a", "a=b", "a\x00", "a\n", strings.Repeat("a", 64)} {
		if validHostName(s) {
			t.Errorf("accepted %q", s)
		}
	}
}
