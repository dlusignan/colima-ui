package config

import (
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) *Profile {
	t.Helper()
	p, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDiffClassification(t *testing.T) {
	const base = `cpu: 2
memory: 4
disk: 50
rootDisk: 20
arch: aarch64
runtime: docker
vmType: vz
mountType: virtiofs
hostname: a
mounts:
  - location: /home/user/a
env:
  K: v
kubernetes:
  enabled: false
  k3sArgs: []
network:
  mode: shared
  gatewayAddress: 192.168.5.2
sshPort: 0
`
	r := func(f string) Change { return Change{f, ClassRestart} }
	c := func(f string) Change { return Change{f, ClassRecreate} }
	edit := func(old, repl string) string { return replaceOnce(t, base, old, repl) }
	table := []struct {
		name, next string
		running    bool
		want       []Change
	}{
		{"identical", base, true, nil},
		{"cpu", edit("cpu: 2", "cpu: 4"), true, []Change{r("cpu")}},
		{"memory", edit("memory: 4", "memory: 6.5"), true, []Change{r("memory")}},
		{"disk grow", edit("disk: 50", "disk: 80"), true, []Change{r("disk")}},
		{"disk shrink", edit("disk: 50", "disk: 10"), true, []Change{c("disk")}},
		{"rootDisk", edit("rootDisk: 20", "rootDisk: 30"), true, []Change{r("rootDisk")}},
		{"arch", edit("aarch64", "x86_64"), true, []Change{c("arch")}},
		{"runtime", edit("docker", "containerd"), true, []Change{c("runtime")}},
		{"vmType", edit("vz", "qemu"), true, []Change{c("vmType")}},
		{"mountType", edit("virtiofs", "sshfs"), true, []Change{c("mountType")}},
		{"hostname", edit("hostname: a", "hostname: b"), true, []Change{r("hostname")}},
		{"mounts", edit("/home/user/a", "/home/user/b"), true, []Change{r("mounts")}},
		{"env", edit("K: v", "K: w"), true, []Change{r("env")}},
		{"k8s typed", edit("enabled: false", "enabled: true"), true, []Change{r("kubernetes")}},
		{"k8s untyped", edit("k3sArgs: []", "k3sArgs: [--x]"), true, []Change{r("kubernetes")}},
		{"network untyped", edit("192.168.5.2", "192.168.5.9"), true, []Change{r("network")}},
		{"unknown key", edit("sshPort: 0", "sshPort: 22\nnewKey: 1"), true, []Change{r("newKey"), r("sshPort")}},
		{"removed key", edit("sshPort: 0\n", ""), true, []Change{r("sshPort")}},
		{"many", replaceOnce(t, edit("cpu: 2", "cpu: 3"), "memory: 4", "memory: 9"), true, []Change{r("cpu"), r("memory")}},
		// stopped: restart-class omitted, recreate-class kept.
		{"stopped cpu", edit("cpu: 2", "cpu: 4"), false, nil},
		{"stopped unknown", edit("sshPort: 0", "sshPort: 9"), false, nil},
		{"stopped vmType", edit("vz", "qemu"), false, []Change{c("vmType")}},
	}
	old := mustParse(t, base)
	for _, tc := range table {
		got := Diff(old, mustParse(t, tc.next), tc.running)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestDiffTypedChangeWithoutNodeEdit(t *testing.T) {
	old := mustParse(t, "cpu: 2\nruntime: docker\n")
	next := mustParse(t, "cpu: 2\nruntime: docker\n")
	cpu := 6
	next.CPU = &cpu
	next.Runtime = "incus"
	got := Diff(old, next, true)
	want := []Change{{"cpu", ClassRestart}, {"runtime", ClassRecreate}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestDiffNil(t *testing.T) {
	if Diff(nil, &Profile{}, true) != nil || Diff(&Profile{}, nil, true) != nil {
		t.Error("nil input should yield nil")
	}
}

func replaceOnce(t *testing.T, s, old, repl string) string {
	t.Helper()
	i := strings.Index(s, old)
	if i < 0 {
		t.Fatalf("fixture text %q not found", old)
	}
	return s[:i] + repl + s[i+len(old):]
}
