package main

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/config"
	"github.com/dlusignan/colima-ui/internal/httpapi"
	"github.com/dlusignan/colima-ui/internal/operations"
)

type fakeMgr struct {
	busyID string
	reqs   []operations.Request
	err    error
}

func (m *fakeMgr) Busy(string) (string, bool) { return m.busyID, m.busyID != "" }
func (m *fakeMgr) Submit(r operations.Request) (operations.JobSummary, error) {
	m.reqs = append(m.reqs, r)
	return operations.JobSummary{ID: "7"}, m.err
}

var testLimits = config.Limits{MaxCPU: 8, MaxMemoryGiB: 32, MaxDiskGiB: 500}

func newCreator(m *fakeMgr, exists bool, existsErr error) (profileCreator, *int) {
	calls := new(int)
	return profileCreator{mgr: m, limits: testLimits, exists: func(context.Context, string) (bool, error) {
		*calls++
		return exists, existsErr
	}}, calls
}

func ip(n int) *int { return &n }

func TestCreatorSubmitsTypedOptions(t *testing.T) {
	m := &fakeMgr{}
	c, _ := newCreator(m, false, nil)
	mem := 4.5
	id, err := c.Create(t.Context(), httpapi.NewProfile{
		Name: "dev", CPUs: ip(4), MemoryGiB: &mem, DiskGiB: ip(100), RootDiskGiB: ip(20),
		Runtime: "containerd", VMType: "vz", Arch: "aarch64", MountType: "virtiofs",
		Kubernetes: true, KubernetesVersion: "v1.31.2+k3s1",
	})
	if err != nil || id != "7" {
		t.Fatalf("%q %v", id, err)
	}
	r := m.reqs[0]
	if r.Action != operations.ActionCreate || r.Profile != "dev" {
		t.Fatalf("req %+v", r)
	}
	o := r.Options
	if *o.CPUs != 4 || *o.MemoryGiB != 4.5 || *o.DiskGiB != 100 || *o.RootDiskGiB != 20 || o.Runtime != "containerd" ||
		o.VMType != "vz" || o.Arch != "aarch64" || o.MountType != "virtiofs" || !o.Kubernetes || o.KubernetesVersion != "v1.31.2+k3s1" {
		t.Fatalf("options %+v", o)
	}
	// The options pass the adapter's own backstop with the mapped limits.
	if err := o.Validate(colimaLimits(testLimits)); err != nil {
		t.Fatalf("backstop: %v", err)
	}
}

func TestCreatorNameOnly(t *testing.T) {
	m := &fakeMgr{}
	c, _ := newCreator(m, false, nil)
	if _, err := c.Create(t.Context(), httpapi.NewProfile{Name: "default"}); err != nil {
		t.Fatal(err) // "default" is allowed
	}
	if o := m.reqs[0].Options; !reflect.DeepEqual(o, colima.StartOptions{}) {
		t.Fatalf("options %+v", o)
	}
}

func TestCreatorValidationReportsEveryFieldAndLaunchesNothing(t *testing.T) {
	m := &fakeMgr{}
	c, existsCalls := newCreator(m, false, nil)
	mem := 1e9
	_, err := c.Create(t.Context(), httpapi.NewProfile{
		Name: "../x", CPUs: ip(99), MemoryGiB: &mem, DiskGiB: ip(0), RootDiskGiB: ip(-5),
		Runtime: "podman", VMType: "xen", Arch: "arm", MountType: "nfs", KubernetesVersion: "--edit",
	})
	var verr *httpapi.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v", err)
	}
	for _, k := range []string{"name", "cpus", "memoryGiB", "diskGiB", "rootDiskGiB", "runtime", "vmType", "arch", "mountType", "kubernetesVersion"} {
		if verr.Fields[k] == "" {
			t.Errorf("missing field %q in %v", k, verr.Fields)
		}
	}
	if len(verr.Fields) != 10 {
		t.Errorf("fields %v", verr.Fields)
	}
	if len(m.reqs) != 0 || *existsCalls != 0 {
		t.Fatalf("launched %d, existence checked %d times", len(m.reqs), *existsCalls)
	}
	for k, msg := range verr.Fields {
		if strings.Contains(msg, "--edit") || strings.Contains(msg, "../x") {
			t.Errorf("%s echoes request text: %q", k, msg)
		}
	}
}

func TestCreatorRejectsHostileNames(t *testing.T) {
	bad := []string{"", ".", "..", "a..b", "a/b", `a\b`, "../x", "-x", "--edit", "--help", "_templates", ".hidden", "a b", "a\x00b", "a\nb",
		"a;b", "$(id)", "`id`", "a&b", "a|b", "ünï", strings.Repeat("a", 65), "-p", "a=b", "a\tb"}
	for _, name := range bad {
		m := &fakeMgr{}
		c, _ := newCreator(m, false, nil)
		_, err := c.Create(t.Context(), httpapi.NewProfile{Name: name})
		var verr *httpapi.ValidationError
		if !errors.As(err, &verr) || verr.Fields["name"] == "" {
			t.Errorf("%q: %v", name, err)
		}
		if len(m.reqs) != 0 {
			t.Errorf("%q launched", name)
		}
		if name != "" && verr != nil && verr.Fields["name"] != nameRule {
			t.Errorf("%q: message %q", name, verr.Fields["name"])
		}
	}
}

func TestCreatorEdgeValues(t *testing.T) {
	ok := []httpapi.NewProfile{
		{Name: "a"}, {Name: "A1_b.c-d"}, {Name: strings.Repeat("a", 64)},
		{Name: "dev", CPUs: ip(1)}, {Name: "dev", CPUs: ip(8)}, {Name: "dev", DiskGiB: ip(500)},
		{Name: "dev", KubernetesVersion: "v1.31.2+k3s1"}, {Name: "dev", KubernetesVersion: "1.31"},
	}
	for _, p := range ok {
		if _, err := newCreate().Create(t.Context(), p); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
	}
	bad := []httpapi.NewProfile{
		{Name: "dev", CPUs: ip(0)}, {Name: "dev", CPUs: ip(9)}, {Name: "dev", CPUs: ip(-1)},
		{Name: "dev", CPUs: ip(int(^uint(0) >> 1))}, {Name: "dev", DiskGiB: ip(501)},
		{Name: "dev", KubernetesVersion: "v1.31.2; rm -rf /"}, {Name: "dev", KubernetesVersion: "-v1"},
		{Name: "dev", KubernetesVersion: strings.Repeat("1", 65)}, {Name: "dev", Runtime: "Docker"},
		{Name: "dev", Runtime: "docker "}, {Name: "dev", Arch: "AARCH64"},
	}
	for _, p := range bad {
		if _, err := newCreate().Create(t.Context(), p); err == nil {
			t.Errorf("%+v: want error", p)
		}
	}
	f := 0.5
	nan := math.NaN()
	for _, v := range []float64{f, nan, 33} {
		if _, err := newCreate().Create(t.Context(), httpapi.NewProfile{Name: "dev", MemoryGiB: &v}); err == nil {
			t.Errorf("memory %v: want error", v)
		}
	}
}

func newCreate() profileCreator {
	c, _ := newCreator(&fakeMgr{}, false, nil)
	return c
}

func TestCreatorOrderBusyThenExistsThenSubmit(t *testing.T) {
	m := &fakeMgr{busyID: "3.4"}
	c, existsCalls := newCreator(m, true, nil)
	_, err := c.Create(t.Context(), httpapi.NewProfile{Name: "dev"})
	var busy *operations.BusyError
	if !errors.As(err, &busy) || busy.JobID != "3.4" || *existsCalls != 0 {
		t.Fatalf("err %v exists calls %d", err, *existsCalls)
	}
	m = &fakeMgr{}
	c, _ = newCreator(m, true, nil)
	if _, err := c.Create(t.Context(), httpapi.NewProfile{Name: "dev"}); !errors.Is(err, httpapi.ErrProfileExists) || len(m.reqs) != 0 {
		t.Fatalf("exists: %v", err)
	}
	boom := errors.New("list failed")
	m = &fakeMgr{}
	c, _ = newCreator(m, false, boom)
	if _, err := c.Create(t.Context(), httpapi.NewProfile{Name: "dev"}); !errors.Is(err, boom) || len(m.reqs) != 0 {
		t.Fatalf("exists failure: %v", err)
	}
	m = &fakeMgr{err: operations.ErrClosed}
	c, _ = newCreator(m, false, nil)
	if _, err := c.Create(t.Context(), httpapi.NewProfile{Name: "dev"}); !errors.Is(err, operations.ErrClosed) {
		t.Fatalf("submit error: %v", err)
	}
}

func TestProfileGateExists(t *testing.T) {
	g := profileGate{runner: &fakeRunner{list: listJSON}, folders: fakeFolders{names: []string{"onlyfolder"}}}
	for name, want := range map[string]bool{
		"default": true, "Default": true, "dev": true, "Dev": true, "DEV": true, "odd": true,
		"onlyfolder": true, "OnlyFolder": true, "ghost": false, "dev2": false,
	} {
		got, err := g.ProfileExists(t.Context(), name)
		if err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", name, got, err, want)
		}
	}
	// default is not special: a first-run machine has none.
	empty := profileGate{runner: &fakeRunner{}, folders: fakeFolders{}}
	if got, err := empty.ProfileExists(t.Context(), "default"); err != nil || got {
		t.Fatalf("default on empty machine: %v %v", got, err)
	}
	if got, err := (profileGate{runner: &fakeRunner{}}).ProfileExists(t.Context(), "dev"); err != nil || got {
		t.Fatalf("nil folders: %v %v", got, err)
	}
}

func TestProfileGateExistsValidatesFirstAndSurfacesFailures(t *testing.T) {
	g := profileGate{runner: &fakeRunner{listErr: errors.New("must not run")}}
	for _, name := range []string{"", "../x", "a..b", "-x", "_t", strings.Repeat("a", 65)} {
		if _, err := g.ProfileExists(t.Context(), name); !errors.Is(err, httpapi.ErrInvalidProfileName) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := g.ProfileExists(t.Context(), "dev"); err == nil || errors.Is(err, httpapi.ErrInvalidProfileName) {
		t.Fatalf("list failure: %v", err)
	}
	g = profileGate{runner: &fakeRunner{}, folders: fakeFolders{err: errors.New("boom")}}
	if _, err := g.ProfileExists(t.Context(), "dev"); err == nil {
		t.Fatal("folder failure must surface")
	}
}

type fakeFolders struct {
	names []string
	err   error
}

func (f fakeFolders) ProfileFolderExists(name string) (bool, error) {
	for _, n := range f.names {
		if strings.EqualFold(n, name) {
			return true, f.err
		}
	}
	return false, f.err
}
