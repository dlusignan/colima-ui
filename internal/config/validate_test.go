package config

import (
	"math"
	"strings"
	"testing"
)

var testLimits = Limits{MaxCPU: 8, MaxMemoryGiB: 32, MaxDiskGiB: 500}

func fields(errs []FieldError) string {
	var f []string
	for _, e := range errs {
		f = append(f, e.Field)
	}
	return strings.Join(f, ",")
}

func TestValidateBadFixture(t *testing.T) {
	errs := Validate(readFixture(t, "bad_values.yaml"), testLimits, nil)
	want := "cpu,memory,disk,rootDisk,arch,runtime,vmType,mountType,network.mode,kubernetes.version," +
		"mounts[0].location,mounts[1].location,mounts[1].mountPoint"
	if got := fields(errs); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, e := range errs {
		if e.Message == "" {
			t.Errorf("empty message for %s", e.Field)
		}
	}
}

func TestValidateRanges(t *testing.T) {
	i := func(v int) *int { return &v }
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		name string
		p    Profile
		bad  string
	}{
		{"cpu ok", Profile{CPU: i(8)}, ""},
		{"cpu low", Profile{CPU: i(0)}, "cpu"},
		{"cpu high", Profile{CPU: i(9)}, "cpu"},
		{"cpu negative", Profile{CPU: i(-1)}, "cpu"},
		{"mem ok", Profile{Memory: f(1.5)}, ""},
		{"mem edge", Profile{Memory: f(32)}, ""},
		{"mem low", Profile{Memory: f(0.99)}, "memory"},
		{"mem high", Profile{Memory: f(32.1)}, "memory"},
		{"mem nan", Profile{Memory: f(math.NaN())}, "memory"},
		{"mem inf", Profile{Memory: f(math.Inf(1))}, "memory"},
		{"disk ok", Profile{Disk: i(500)}, ""},
		{"disk low", Profile{Disk: i(0)}, "disk"},
		{"disk high", Profile{Disk: i(501)}, "disk"},
		{"rootDisk low", Profile{RootDisk: i(0)}, "rootDisk"},
		{"rootDisk ok", Profile{RootDisk: i(1)}, ""},
		{"unset all", Profile{}, ""},
	}
	for _, c := range cases {
		if got := fields(Validate(&c.p, testLimits, nil)); got != c.bad {
			t.Errorf("%s: got %q want %q", c.name, got, c.bad)
		}
	}
}

func TestValidateDiskCannotShrink(t *testing.T) {
	d100, d50, d200 := 100, 50, 200
	cur := &Profile{Disk: &d100}
	if got := fields(Validate(&Profile{Disk: &d50}, testLimits, cur)); got != "disk" {
		t.Errorf("shrink: %q", got)
	}
	if got := fields(Validate(&Profile{Disk: &d200}, testLimits, cur)); got != "" {
		t.Errorf("grow: %q", got)
	}
	if got := fields(Validate(&Profile{Disk: &d50}, testLimits, nil)); got != "" {
		t.Errorf("no current: %q", got)
	}
}

func TestValidateEnums(t *testing.T) {
	for field, vals := range enums {
		for _, v := range vals {
			p := &Profile{}
			setEnum(p, field, v)
			if errs := Validate(p, testLimits, nil); errs != nil {
				t.Errorf("%s=%s rejected: %v", field, v, errs)
			}
		}
		p := &Profile{}
		setEnum(p, field, "bogus; rm")
		if got := fields(Validate(p, testLimits, nil)); got != field {
			t.Errorf("%s bogus: %q", field, got)
		}
	}
}

func setEnum(p *Profile, field, v string) {
	switch field {
	case "arch":
		p.Arch = v
	case "runtime":
		p.Runtime = v
	case "vmType":
		p.VMType = v
	case "mountType":
		p.MountType = v
	case "network.mode":
		p.Network.Mode = v
	}
}

func TestValidateKubernetesVersion(t *testing.T) {
	for _, v := range []string{"v1.31.2+k3s1", "1.30", "v1.29.4", "v1.31.0-rc1+k3s2"} {
		p := &Profile{Kubernetes: Kubernetes{Version: v}}
		if errs := Validate(p, testLimits, nil); errs != nil {
			t.Errorf("%q rejected: %v", v, errs)
		}
	}
	for _, v := range []string{"latest", "v1", "1.2.3 --x", "$(id)", "1.2;ls", "v1.2.3\n", strings.Repeat("1", 70)} {
		p := &Profile{Kubernetes: Kubernetes{Version: v}}
		if got := fields(Validate(p, testLimits, nil)); got != "kubernetes.version" {
			t.Errorf("%q: %q", v, got)
		}
	}
}

func TestValidateMounts(t *testing.T) {
	bad := []Mount{
		{Location: ""},
		{Location: "relative"},
		{Location: "~/work"},
		{Location: "/home/user/../etc"},
		{Location: "/home/user/./x"},
		{Location: "/home//user"},
		{Location: "/home/user/"},
		{Location: "/home/user\nx"},
		{Location: "/home/user\x00"},
	}
	for _, m := range bad {
		p := &Profile{Mounts: []Mount{m}}
		if got := fields(Validate(p, testLimits, nil)); got != "mounts[0].location" {
			t.Errorf("%q: %q", m.Location, got)
		}
	}
	for _, mp := range []string{"rel", "/a/../b", "/a\nb"} {
		p := &Profile{Mounts: []Mount{{Location: "/home/user", MountPoint: mp}}}
		if got := fields(Validate(p, testLimits, nil)); got != "mounts[0].mountPoint" {
			t.Errorf("%q: %q", mp, got)
		}
	}
	ok := &Profile{Mounts: []Mount{{Location: "/home/user/work", MountPoint: "/work"}, {Location: "/"}}}
	if errs := Validate(ok, testLimits, nil); errs != nil {
		t.Errorf("valid mounts rejected: %v", errs)
	}
}

func TestValidateNilAndDefaults(t *testing.T) {
	if Validate(nil, testLimits, nil) == nil {
		t.Error("nil profile valid")
	}
	if l := DefaultLimits(); l.MaxCPU < 1 || l.MaxMemoryGiB < 1 || l.MaxDiskGiB < 1 {
		t.Errorf("%+v", l)
	}
}

func TestValidateNewReportsEveryBadField(t *testing.T) {
	cpu, disk, root, mem := 99, 0, -1, math.NaN()
	p := &Profile{CPU: &cpu, Memory: &mem, Disk: &disk, RootDisk: &root,
		Arch: "arm", Runtime: "podman", VMType: "xen", MountType: "nfs",
		Kubernetes: Kubernetes{Version: "--edit"}}
	got := fields(ValidateNew(p, testLimits))
	want := "cpu,memory,disk,rootDisk,arch,runtime,vmType,mountType,kubernetes.version"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if errs := ValidateNew(&Profile{}, testLimits); len(errs) != 0 {
		t.Fatalf("empty profile: %v", errs)
	}
}

func TestValidateNewHasNoShrinkRule(t *testing.T) {
	d := 10
	if errs := ValidateNew(&Profile{Disk: &d}, testLimits); len(errs) != 0 {
		t.Fatalf("%v", errs)
	}
}

func TestValidateNewKubernetesVersionNeedsKubernetes(t *testing.T) {
	on, off := true, false
	cases := map[string]struct {
		k    Kubernetes
		want string
	}{
		"version, unset":      {Kubernetes{Version: "v1.31.2+k3s1"}, "kubernetes.version"},
		"version, disabled":   {Kubernetes{Enabled: &off, Version: "v1.31.2+k3s1"}, "kubernetes.version"},
		"version, enabled":    {Kubernetes{Enabled: &on, Version: "v1.31.2+k3s1"}, ""},
		"enabled, no version": {Kubernetes{Enabled: &on}, ""},
		"nothing":             {Kubernetes{}, ""},
		"bad version, unset":  {Kubernetes{Version: "--edit"}, "kubernetes.version"},
	}
	for name, tc := range cases {
		errs := ValidateNew(&Profile{Kubernetes: tc.k}, testLimits)
		if got := fields(errs); got != tc.want {
			t.Errorf("%s: got %q want %q", name, got, tc.want)
		}
	}
	// Validate itself (the config PUT path) does not gain the rule.
	if errs := Validate(&Profile{Kubernetes: Kubernetes{Version: "v1.31.2+k3s1"}}, testLimits, nil); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}
}
