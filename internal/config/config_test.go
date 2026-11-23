package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readFixture(t *testing.T, name string) *Profile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return p
}

func TestParseFull(t *testing.T) {
	p := readFixture(t, "full.yaml")
	if *p.CPU != 4 || *p.Memory != 8 || *p.Disk != 100 || *p.RootDisk != 20 {
		t.Fatalf("resources: %+v", p)
	}
	if p.Arch != "aarch64" || p.Runtime != "docker" || p.VMType != "vz" || p.MountType != "virtiofs" {
		t.Fatalf("enums: %+v", p)
	}
	if len(p.Mounts) != 2 || p.Mounts[0].MountPoint != "/work" || !p.Mounts[0].Writable {
		t.Fatalf("mounts: %+v", p.Mounts)
	}
	if p.Env["PORT"] != "8080" || p.Env["FOO"] != "bar" {
		t.Fatalf("env: %+v", p.Env)
	}
	if p.Kubernetes.Enabled == nil || !*p.Kubernetes.Enabled || p.Kubernetes.Version != "v1.31.2+k3s1" {
		t.Fatalf("k8s: %+v", p.Kubernetes)
	}
	if p.Network.Mode != "shared" {
		t.Fatalf("network: %+v", p.Network)
	}
	if errs := Validate(p, Limits{MaxCPU: 16, MaxMemoryGiB: 64, MaxDiskGiB: 1000}, nil); errs != nil {
		t.Fatalf("full fixture invalid: %v", errs)
	}
}

func TestParseMinimalAndEmpty(t *testing.T) {
	p := readFixture(t, "minimal.yaml")
	if *p.CPU != 2 || p.Disk != nil || p.Runtime != "" {
		t.Fatalf("%+v", p)
	}
	e := readFixture(t, "empty.yaml")
	if e.CPU != nil || e.Node() == nil || e.Node().Kind != yaml.MappingNode {
		t.Fatalf("%+v", e)
	}
}

func TestUnknownKeysAndCommentsPreserved(t *testing.T) {
	p := readFixture(t, "unknown_keys.yaml")
	if *p.Memory != 4.5 {
		t.Fatalf("memory %v", *p.Memory)
	}
	out, err := yaml.Marshal(p.Node())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"futureKey: keep-me", "nested:", "- two", "# top comment stays", "# inline comment stays"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("lost %q in:\n%s", want, out)
		}
	}
}

func TestParseErrorsTyped(t *testing.T) {
	cases := map[string]struct {
		data string
		as   any
	}{
		"malformed":   {"cpu: 2\nmemory: [unclosed\n", new(*ParseError)},
		"not mapping": {"- a\n- b\n", new(*ParseError)},
		"wrong type":  {"cpu: \"four\"\n", new(*TypeError)},
		"float cpu":   {"cpu: 2.5\n", new(*TypeError)},
		"map cpu":     {"cpu: {a: 1}\n", new(*TypeError)},
		"dup key":     {"cpu: 1\ncpu: 2\n", nil},
		"tabs":        {"\tcpu: 1\n", new(*ParseError)},
		"binary":      {"\x00\x01\x02", nil},
		"alias root":  {"*a\n", nil},
	}
	for name, c := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic %v", name, r)
				}
			}()
			_, err := Parse([]byte(c.data))
			if err == nil {
				t.Errorf("%s: no error", name)
				return
			}
			switch target := c.as.(type) {
			case **ParseError:
				if !errors.As(err, target) {
					t.Errorf("%s: want ParseError, got %T %v", name, err, err)
				}
			case **TypeError:
				if !errors.As(err, target) {
					t.Errorf("%s: want TypeError, got %T %v", name, err, err)
				}
			}
		}()
	}
	if _, err := Parse(make([]byte, maxFileSize+1)); err == nil {
		t.Error("oversize accepted")
	}
}

func TestNullValuesDecode(t *testing.T) {
	if _, err := Parse([]byte("cpu: null\nmounts: null\nenv: null\nnetwork:\n  dns: null\n")); err != nil {
		t.Fatal(err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("cpu: 2\n"))
	f.Add([]byte("a: &x [*x]\n"))
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = Parse(b) })
}
