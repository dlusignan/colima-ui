package colima

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/config"
)

func TestAdvancedFlagTable(t *testing.T) {
	cases := []struct {
		name string
		opts StartOptions
		want []string
	}{
		{"hostname", StartOptions{Hostname: "vm-1"}, []string{"--hostname", "vm-1"}},
		{"dns repeated", StartOptions{DNS: []string{"1.1.1.1", "2001:db8::1"}},
			[]string{"--dns", "1.1.1.1", "--dns", "2001:db8::1"}},
		{"dns canonical form", StartOptions{DNS: []string{"2001:DB8:0:0:0:0:0:1"}}, []string{"--dns", "2001:db8::1"}},
		{"dns host repeated", StartOptions{DNSHosts: []DNSHost{{"example.com", "192.0.2.10"}, {"b", "2001:db8::2"}}},
			[]string{"--dns-host", "example.com=192.0.2.10", "--dns-host", "b=2001:db8::2"}},
		{"ssh agent", StartOptions{SSHAgent: true}, []string{"--ssh-agent"}},
		{"rosetta", StartOptions{VZRosetta: true}, []string{"--vz-rosetta"}},
		{"rosetta explicit vz aarch64", StartOptions{VZRosetta: true, VMType: VMTypeVZ, Arch: ArchAarch64},
			[]string{"--vm-type", "vz", "--arch", "aarch64", "--vz-rosetta"}},
		{"port forwarder ssh", StartOptions{PortForwarder: PortForwarderSSH}, []string{"--port-forwarder", "ssh"}},
		{"port forwarder grpc", StartOptions{PortForwarder: PortForwarderGRPC}, []string{"--port-forwarder", "grpc"}},
		{"port forwarder none", StartOptions{PortForwarder: PortForwarderNone}, []string{"--port-forwarder", "none"}},
		{"mount inotify", StartOptions{MountInotify: true}, []string{"--mount-inotify"}},
		{"network address", StartOptions{NetworkAddress: true}, []string{"--network-address"}},
		{"network mode shared", StartOptions{NetworkMode: NetworkModeShared}, []string{"--network-mode", "shared"}},
		{"network bridged with interface", StartOptions{NetworkMode: NetworkModeBridged, NetworkInterface: "en0"},
			[]string{"--network-mode", "bridged", "--network-interface", "en0"}},
		{"cpu type", StartOptions{CPUType: "cortex-a72"}, []string{"--cpu-type", "cortex-a72"}},
		{"cpu type with qemu", StartOptions{VMType: VMTypeQEMU, CPUType: "max"}, []string{"--vm-type", "qemu", "--cpu-type", "max"}},
		{"false and empty add nothing", StartOptions{DNS: []string{}, DNSHosts: []DNSHost{}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.opts.Validate(testLimits); err != nil {
				t.Fatal(err)
			}
			if got := c.opts.args(); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestAdvancedAllFieldsOrder(t *testing.T) {
	o := StartOptions{
		Hostname: "vm", DNS: []string{"1.1.1.1"}, DNSHosts: []DNSHost{{"a.test", "192.0.2.1"}},
		SSHAgent: true, VZRosetta: true, PortForwarder: PortForwarderGRPC, MountInotify: true,
		NetworkAddress: true, NetworkMode: NetworkModeBridged, NetworkInterface: "en0", CPUType: "max",
	}
	if err := o.Validate(testLimits); err != nil {
		t.Fatal(err)
	}
	want := "--hostname vm --dns 1.1.1.1 --dns-host a.test=192.0.2.1 --ssh-agent --vz-rosetta --port-forwarder grpc " +
		"--mount-inotify --network-address --network-mode bridged --network-interface en0 --cpu-type max"
	if got := strings.Join(o.args(), " "); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// hostile is the corpus every free-text advanced field must refuse.
var hostile = []string{
	"--edit", "-x", "-", "--", "--hostname=evil", "a;b", "a b", " a", "a ", "a\nb", "a\n--edit", "a\r\nb", "a\x00b",
	"$(id)", "`id`", "a|b", "a&b", "a=b", "a,b", "a\tb", "ünï", "a/b", `a\b`, "../x", "a'b", `a"b`, "*", "a*", "%",
	".", ".a", "", // "" is the omitted value and is checked separately
}

func TestAdvancedStringFieldsRejectHostileValues(t *testing.T) {
	fields := []struct {
		name string
		set  func(*StartOptions, string)
	}{
		{"hostname", func(o *StartOptions, v string) { o.Hostname = v }},
		{"dns", func(o *StartOptions, v string) { o.DNS = []string{v} }},
		{"dns-host", func(o *StartOptions, v string) { o.DNSHosts = []DNSHost{{v, "192.0.2.1"}} }},
		{"dns-host", func(o *StartOptions, v string) { o.DNSHosts = []DNSHost{{"a.test", v}} }},
		{"network-interface", func(o *StartOptions, v string) { o.NetworkMode = NetworkModeBridged; o.NetworkInterface = v }},
		{"cpu-type", func(o *StartOptions, v string) { o.CPUType = v }},
		{"port-forwarder", func(o *StartOptions, v string) { o.PortForwarder = PortForwarder(v) }},
		{"network-mode", func(o *StartOptions, v string) { o.NetworkMode = NetworkMode(v) }},
	}
	for _, f := range fields {
		for _, v := range hostile {
			if v == "" {
				continue
			}
			var o StartOptions
			f.set(&o, v)
			err := o.Validate(testLimits)
			var ioe *InvalidOptionError
			if !errors.As(err, &ioe) || ioe.Field != f.name {
				t.Errorf("%s=%q: got %v", f.name, v, err)
				continue
			}
			if len(v) > 3 && strings.Contains(err.Error(), v) {
				t.Errorf("%s=%q echoed in %v", f.name, v, err)
			}
		}
	}
}

func TestAdvancedInvalidValues(t *testing.T) {
	long := strings.Repeat("a", 64)
	cases := []struct {
		name  string
		opts  StartOptions
		field string
	}{
		{"hostname 64 chars", StartOptions{Hostname: long}, "hostname"},
		{"hostname leading hyphen", StartOptions{Hostname: "-a"}, "hostname"},
		{"hostname trailing hyphen", StartOptions{Hostname: "a-"}, "hostname"},
		{"hostname dotted", StartOptions{Hostname: "a.b"}, "hostname"},
		{"dns zone", StartOptions{DNS: []string{"fe80::1%eth0"}}, "dns"},
		{"dns port", StartOptions{DNS: []string{"1.1.1.1:53"}}, "dns"},
		{"dns prefix", StartOptions{DNS: []string{"10.0.0.0/8"}}, "dns"},
		{"dns leading zero", StartOptions{DNS: []string{"01.1.1.1"}}, "dns"},
		{"dns name", StartOptions{DNS: []string{"example.com"}}, "dns"},
		{"dns csv", StartOptions{DNS: []string{"1.1.1.1,8.8.8.8"}}, "dns"},
		{"dns empty element", StartOptions{DNS: []string{""}}, "dns"},
		{"dns nine", StartOptions{DNS: strings.Split(strings.Repeat("1.1.1.1,", 9)[:71], ",")}, "dns"},
		{"dns-host name label too long", StartOptions{DNSHosts: []DNSHost{{long + ".test", "192.0.2.1"}}}, "dns-host"},
		{"dns-host name too long", StartOptions{DNSHosts: []DNSHost{{strings.Repeat("a.", 127) + "a", "192.0.2.1"}}}, "dns-host"},
		{"dns-host empty name", StartOptions{DNSHosts: []DNSHost{{"", "192.0.2.1"}}}, "dns-host"},
		{"dns-host empty address", StartOptions{DNSHosts: []DNSHost{{"a.test", ""}}}, "dns-host"},
		{"dns-host address csv", StartOptions{DNSHosts: []DNSHost{{"a.test", "192.0.2.1,192.0.2.2"}}}, "dns-host"},
		{"dns-host name with equals", StartOptions{DNSHosts: []DNSHost{{"a=1.1.1.1", "192.0.2.1"}}}, "dns-host"},
		{"dns-host seventeen", StartOptions{DNSHosts: make17()}, "dns-host"},
		{"port forwarder case", StartOptions{PortForwarder: "SSH"}, "port-forwarder"},
		{"network mode case", StartOptions{NetworkMode: "Bridged"}, "network-mode"},
		{"interface 16 chars", StartOptions{NetworkMode: NetworkModeBridged, NetworkInterface: strings.Repeat("a", 16)}, "network-interface"},
		{"interface without bridged", StartOptions{NetworkInterface: "en0"}, "network-interface"},
		{"interface with shared", StartOptions{NetworkMode: NetworkModeShared, NetworkInterface: "en0"}, "network-interface"},
		{"cpu type 65 chars", StartOptions{CPUType: strings.Repeat("a", 65)}, "cpu-type"},
		{"cpu type feature list", StartOptions{CPUType: "host,+x2apic"}, "cpu-type"},
		{"rosetta with qemu", StartOptions{VZRosetta: true, VMType: VMTypeQEMU}, "vz-rosetta"},
		{"rosetta with krunkit", StartOptions{VZRosetta: true, VMType: VMTypeKrunkit}, "vz-rosetta"},
		{"rosetta with x86_64", StartOptions{VZRosetta: true, Arch: ArchX8664}, "vz-rosetta"},
		{"rosetta vz x86_64", StartOptions{VZRosetta: true, VMType: VMTypeVZ, Arch: ArchX8664}, "vz-rosetta"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.opts.Validate(testLimits)
			var ioe *InvalidOptionError
			if !errors.As(err, &ioe) || ioe.Field != c.field {
				t.Fatalf("got %v, want field %s", err, c.field)
			}
		})
	}
}

func make17() []DNSHost {
	h := make([]DNSHost, 17)
	for i := range h {
		h[i] = DNSHost{"a.test", "192.0.2.1"}
	}
	return h
}

func TestAdvancedBoundariesAccepted(t *testing.T) {
	dns := make([]string, 8)
	hosts := make([]DNSHost, 16)
	for i := range dns {
		dns[i] = "192.0.2.1" // repeats are Colima's business
	}
	for i := range hosts {
		hosts[i] = DNSHost{strings.Repeat("a", 63) + ".test", "2001:db8::1"}
	}
	o := StartOptions{
		Hostname: strings.Repeat("a", 63), DNS: dns, DNSHosts: hosts,
		NetworkMode: NetworkModeBridged, NetworkInterface: strings.Repeat("a", 15), CPUType: strings.Repeat("a", 64),
	}
	if err := o.Validate(testLimits); err != nil {
		t.Fatal(err)
	}
	// 253 characters in all.
	l := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 253-192)
	if len(l) != 253 {
		t.Fatalf("len %d", len(l))
	}
	if err := (StartOptions{DNSHosts: []DNSHost{{l, "192.0.2.1"}}}).Validate(testLimits); err != nil {
		t.Fatal(err)
	}
	if err := (StartOptions{DNSHosts: []DNSHost{{l + "a", "192.0.2.1"}}}).Validate(testLimits); err == nil {
		t.Fatal("254 characters accepted")
	}
}

// No value is ever concatenated into a flag string: every user value is its
// own argv element, except the name=address pair of --dns-host.
func TestAdvancedValuesAreSeparateArgvElements(t *testing.T) {
	o := StartOptions{Hostname: "vm", DNS: []string{"1.1.1.1"}, DNSHosts: []DNSHost{{"a.test", "192.0.2.1"}},
		NetworkMode: NetworkModeBridged, NetworkInterface: "en0", CPUType: "max", PortForwarder: PortForwarderSSH}
	a := o.args()
	for i, el := range a {
		if strings.HasPrefix(el, "--") && strings.Contains(el, "=") {
			t.Errorf("flag %q carries its value", el)
		}
		if !strings.HasPrefix(el, "--") && (i == 0 || !strings.HasPrefix(a[i-1], "--")) {
			t.Errorf("value %q does not follow a flag", el)
		}
	}
}

// Start refuses a hostile value before any process exists.
func TestStartRefusesHostileAdvancedValues(t *testing.T) {
	for _, o := range []StartOptions{
		{Hostname: "--edit"}, {DNS: []string{"1.1.1.1; rm -rf /"}}, {DNSHosts: []DNSHost{{"a;b", "192.0.2.1"}}},
		{CPUType: "-cpu"}, {NetworkMode: NetworkModeBridged, NetworkInterface: "--force"}, {PortForwarder: "ssh --edit"},
	} {
		fs := &fakeStreamer{}
		if _, err := Start(t.Context(), fs, "dev", o, testLimits); err == nil {
			t.Errorf("%+v accepted", o)
		}
		if len(fs.calls) != 0 {
			t.Errorf("%+v launched %v", o, fs.calls)
		}
	}
}

func TestStartBuildsFullAdvancedArgv(t *testing.T) {
	fs := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
	o := StartOptions{Hostname: "vm", DNSHosts: []DNSHost{{"a.test", "192.0.2.1"}}, SSHAgent: true}
	if _, err := Start(t.Context(), fs, "dev", o, testLimits); err != nil {
		t.Fatal(err)
	}
	want := []string{"start", "-p", "dev", "--hostname", "vm", "--dns-host", "a.test=192.0.2.1", "--ssh-agent"}
	if len(fs.calls) != 1 || !reflect.DeepEqual(fs.calls[0], want) {
		t.Fatalf("calls %q", fs.calls)
	}
}

// The adapter's backstop and the Config Service's validator must agree on the
// advanced values, since the creator runs both.
func TestAdvancedValidatorsAgreeWithConfig(t *testing.T) {
	values := append([]string{
		"vm-1", "a", "A1", "a-b", "1.1.1.1", "2001:db8::1", "::", "::ffff:1.2.3.4", "fe80::1%eth0", "1.1.1.1:53",
		"example.com", "a.b.c", "en0", "bond0.100", "max", "cortex-a72", "Skylake-Client-v1", "host,+x2apic",
		strings.Repeat("a", 15), strings.Repeat("a", 16), strings.Repeat("a", 63), strings.Repeat("a", 64),
		strings.Repeat("a", 65), strings.Repeat("a.", 126) + "a", strings.Repeat("a.", 127) + "a", "a.", ".a", "a_b", "a..b",
	}, hostile...)
	check := func(label, v string, ours, theirs bool) {
		if ours != theirs {
			t.Errorf("%s %q: adapter %v, config %v", label, v, ours, theirs)
		}
	}
	for _, v := range values {
		if v == "" {
			continue
		}
		cfg := func(a config.NewAdvanced) bool {
			return len(config.ValidateNew(&config.Profile{}, a, config.Limits{})) == 0
		}
		check("hostname", v, StartOptions{Hostname: v}.Validate(testLimits) == nil, cfg(config.NewAdvanced{Hostname: v}))
		check("dns", v, StartOptions{DNS: []string{v}}.Validate(testLimits) == nil, cfg(config.NewAdvanced{DNS: []string{v}}))
		check("dns-host name", v, StartOptions{DNSHosts: []DNSHost{{v, "192.0.2.1"}}}.Validate(testLimits) == nil,
			cfg(config.NewAdvanced{DNSHosts: []config.DNSHost{{Name: v, Address: "192.0.2.1"}}}))
		check("dns-host address", v, StartOptions{DNSHosts: []DNSHost{{"a.test", v}}}.Validate(testLimits) == nil,
			cfg(config.NewAdvanced{DNSHosts: []config.DNSHost{{Name: "a.test", Address: v}}}))
		check("interface", v, StartOptions{NetworkMode: NetworkModeBridged, NetworkInterface: v}.Validate(testLimits) == nil,
			cfg(config.NewAdvanced{NetworkMode: "bridged", NetworkInterface: v}))
		check("cpu type", v, StartOptions{CPUType: v}.Validate(testLimits) == nil, cfg(config.NewAdvanced{CPUType: v}))
		check("port forwarder", v, StartOptions{PortForwarder: PortForwarder(v)}.Validate(testLimits) == nil,
			cfg(config.NewAdvanced{PortForwarder: v}))
		check("network mode", v, StartOptions{NetworkMode: NetworkMode(v)}.Validate(testLimits) == nil,
			cfg(config.NewAdvanced{NetworkMode: v}))
	}
	// Cross-field rules and count caps.
	for _, vt := range []string{"", "vz", "qemu", "krunkit"} {
		for _, ar := range []string{"", "aarch64", "x86_64"} {
			ours := StartOptions{VZRosetta: true, VMType: VMType(vt), Arch: Arch(ar)}.Validate(testLimits) == nil
			theirs := len(config.ValidateNew(&config.Profile{VMType: vt, Arch: ar}, config.NewAdvanced{VZRosetta: true}, config.Limits{})) == 0
			check("rosetta "+vt+"/"+ar, "", ours, theirs)
		}
	}
	for _, n := range []int{0, 1, 8, 9, 100} {
		dns := make([]string, n)
		for i := range dns {
			dns[i] = "1.1.1.1"
		}
		check("dns count", "", StartOptions{DNS: dns}.Validate(testLimits) == nil,
			len(config.ValidateNew(&config.Profile{}, config.NewAdvanced{DNS: dns}, config.Limits{})) == 0)
	}
	for _, n := range []int{0, 1, 16, 17, 100} {
		h, ch := make([]DNSHost, n), make([]config.DNSHost, n)
		for i := range h {
			h[i], ch[i] = DNSHost{"a.test", "192.0.2.1"}, config.DNSHost{Name: "a.test", Address: "192.0.2.1"}
		}
		check("dns-host count", "", StartOptions{DNSHosts: h}.Validate(testLimits) == nil,
			len(config.ValidateNew(&config.Profile{}, config.NewAdvanced{DNSHosts: ch}, config.Limits{})) == 0)
	}
}
