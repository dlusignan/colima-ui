package colima

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/config"
)

var testLimits = Limits{MaxCPU: 8, MaxMemoryGiB: 32, MaxDiskGiB: 500}

func ip(i int) *int           { return &i }
func fp(f float64) *float64   { return &f }

func TestStartOptionsArgsAllFields(t *testing.T) {
	o := StartOptions{
		CPUs: ip(4), MemoryGiB: fp(7.5), DiskGiB: ip(100), RootDiskGiB: ip(20),
		Runtime: RuntimeDocker, VMType: VMTypeVZ, Arch: ArchAarch64, MountType: MountVirtiofs,
		Kubernetes: true, KubernetesVersion: "v1.31.2+k3s1",
	}
	if err := o.Validate(testLimits); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(o.args(), " ")
	want := "--cpus 4 --memory 7.5 --disk 100 --root-disk 20 --runtime docker --vm-type vz --arch aarch64 --mount-type virtiofs --kubernetes --kubernetes-version v1.31.2+k3s1"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestStartOptionsZeroValueEmitsNothing(t *testing.T) {
	var o StartOptions
	if err := o.Validate(Limits{}); err != nil {
		t.Fatal(err)
	}
	if a := o.args(); len(a) != 0 {
		t.Fatalf("got %q", a)
	}
}

func TestStartOptionsNeverEmitsEditOrSaveConfig(t *testing.T) {
	o := StartOptions{CPUs: ip(1), Kubernetes: true}
	for _, a := range o.args() {
		if a == "--edit" || strings.HasPrefix(a, "--save-config") || a == "--foreground" {
			t.Fatalf("unexpected flag %s", a)
		}
	}
}

func TestStartOptionsValidate(t *testing.T) {
	cases := []struct {
		name  string
		opts  StartOptions
		field string
	}{
		{"cpus zero", StartOptions{CPUs: ip(0)}, "cpus"},
		{"cpus negative", StartOptions{CPUs: ip(-1)}, "cpus"},
		{"cpus over limit", StartOptions{CPUs: ip(9)}, "cpus"},
		{"memory low", StartOptions{MemoryGiB: fp(0.5)}, "memory"},
		{"memory high", StartOptions{MemoryGiB: fp(33)}, "memory"},
		{"memory nan", StartOptions{MemoryGiB: fp(math.NaN())}, "memory"},
		{"memory inf", StartOptions{MemoryGiB: fp(math.Inf(1))}, "memory"},
		{"disk zero", StartOptions{DiskGiB: ip(0)}, "disk"},
		{"disk high", StartOptions{DiskGiB: ip(501)}, "disk"},
		{"root disk zero", StartOptions{RootDiskGiB: ip(0)}, "root-disk"},
		{"root disk high", StartOptions{RootDiskGiB: ip(501)}, "root-disk"},
		{"runtime", StartOptions{Runtime: "podman"}, "runtime"},
		{"runtime flag injection", StartOptions{Runtime: "docker --edit"}, "runtime"},
		{"vm type", StartOptions{VMType: "hyperv"}, "vm-type"},
		{"arch", StartOptions{Arch: "arm64"}, "arch"},
		{"mount type", StartOptions{MountType: "nfs"}, "mount-type"},
		{"k8s version junk", StartOptions{KubernetesVersion: "latest; rm -rf /"}, "kubernetes-version"},
		{"k8s version flag", StartOptions{KubernetesVersion: "--edit"}, "kubernetes-version"},
		{"k8s version newline", StartOptions{KubernetesVersion: "v1.31.2\n--edit"}, "kubernetes-version"},
		{"k8s version long", StartOptions{KubernetesVersion: "v1." + strings.Repeat("1", 100)}, "kubernetes-version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.opts.Validate(testLimits)
			var ioe *InvalidOptionError
			if !errors.As(err, &ioe) {
				t.Fatalf("got %v, want InvalidOptionError", err)
			}
			if ioe.Field != c.field {
				t.Errorf("field %q, want %q", ioe.Field, c.field)
			}
			if strings.Contains(err.Error(), "rm -rf") || strings.Contains(err.Error(), "--edit") {
				t.Errorf("error echoes the rejected value: %v", err)
			}
		})
	}
}

func TestStartOptionsBoundariesAccepted(t *testing.T) {
	o := StartOptions{CPUs: ip(1), MemoryGiB: fp(1), DiskGiB: ip(500), RootDiskGiB: ip(1)}
	if err := o.Validate(testLimits); err != nil {
		t.Fatal(err)
	}
	o = StartOptions{CPUs: ip(8), MemoryGiB: fp(32), DiskGiB: ip(1)}
	if err := o.Validate(testLimits); err != nil {
		t.Fatal(err)
	}
}

func TestZeroLimitsRejectResources(t *testing.T) {
	if err := (StartOptions{CPUs: ip(1)}).Validate(Limits{}); err == nil {
		t.Fatal("zero Limits must reject resource options")
	}
}

// The enum lists are duplicated from internal/config on purpose; this keeps
// the two in step without a production import.
func TestEnumsMatchConfig(t *testing.T) {
	lim := config.Limits{MaxCPU: 8, MaxMemoryGiB: 32, MaxDiskGiB: 500}
	accepted := func(set func(*config.Profile, string), v string) bool {
		p := &config.Profile{}
		set(p, v)
		return len(config.Validate(p, lim, nil)) == 0
	}
	fields := []struct {
		name string
		set  func(*config.Profile, string)
		ours []string
	}{
		{"runtime", func(p *config.Profile, v string) { p.Runtime = v }, enumStrings(validRuntimes)},
		{"vmType", func(p *config.Profile, v string) { p.VMType = v }, enumStrings(validVMTypes)},
		{"arch", func(p *config.Profile, v string) { p.Arch = v }, enumStrings(validArchs)},
		{"mountType", func(p *config.Profile, v string) { p.MountType = v }, enumStrings(validMountTypes)},
	}
	candidates := []string{"docker", "containerd", "incus", "qemu", "vz", "krunkit", "aarch64", "x86_64", "sshfs", "9p", "virtiofs", "podman", "hyperv", "arm64", "nfs", "kata"}
	for _, f := range fields {
		for _, v := range f.ours {
			if !accepted(f.set, v) {
				t.Errorf("%s: config rejects %q which colima accepts", f.name, v)
			}
		}
		for _, v := range candidates {
			inOurs := false
			for _, o := range f.ours {
				inOurs = inOurs || o == v
			}
			if accepted(f.set, v) != inOurs {
				t.Errorf("%s: %q accepted by config=%v but by colima=%v", f.name, v, !inOurs, inOurs)
			}
		}
	}
	// Kubernetes version pattern: same verdicts as config for a sample.
	for _, v := range []string{"v1.31.2+k3s1", "1.31", "v1.31.2-rc1+k3s2", "latest", "--edit", "v1.31.2\n", "", "v1."} {
		cfg := len(config.Validate(&config.Profile{Kubernetes: config.Kubernetes{Version: v}}, lim, nil)) == 0
		ours := (StartOptions{KubernetesVersion: v}).Validate(testLimits) == nil
		if cfg != ours {
			t.Errorf("kubernetes version %q: config=%v colima=%v", v, cfg, ours)
		}
	}
}

func enumStrings[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}
