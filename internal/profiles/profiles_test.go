package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/config"
)

type fakeColima struct {
	insts    []colima.Instance
	listErr  error
	ver      colima.VersionInfo
	verErr   error
	listHits atomic.Int32
	verHits  atomic.Int32
}

func (f *fakeColima) List(ctx context.Context) ([]colima.Instance, error) {
	f.listHits.Add(1)
	return f.insts, f.listErr
}

func (f *fakeColima) Version(ctx context.Context) (colima.VersionInfo, error) {
	f.verHits.Add(1)
	return f.ver, f.verErr
}

type fakeCfg struct {
	names    []string
	listErr  error
	files    map[string]string
	readErrs map[string]error
	hits     atomic.Int32
}

func (f *fakeCfg) ListProfiles() ([]string, error) { return f.names, f.listErr }

func (f *fakeCfg) Read(name string) (*config.Profile, error) {
	f.hits.Add(1)
	if err := f.readErrs[name]; err != nil {
		return nil, err
	}
	y, ok := f.files[name]
	if !ok {
		return nil, config.ErrProfileNotFound
	}
	return config.Parse([]byte(y))
}

func ver(n string, maj, min int, semver bool, pre string) colima.VersionInfo {
	return colima.VersionInfo{Number: n, Major: maj, Minor: min, Semver: semver, Prerelease: pre}
}

var v0103 = ver("v0.10.3+abc123", 0, 10, true, "")

func warnCodes(p Profile) []string {
	var c []string
	for _, w := range p.Warnings {
		c = append(c, w.Code)
	}
	return c
}

func hasWarn(p Profile, code string) bool {
	for _, c := range warnCodes(p) {
		if c == code {
			return true
		}
	}
	return false
}

func find(t *testing.T, ps []Profile, name string) Profile {
	t.Helper()
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("profile %q not in %v", name, ps)
	return Profile{}
}

const gib = int64(1) << 30

func TestSourceCombinations(t *testing.T) {
	c := &fakeColima{
		ver: v0103,
		insts: []colima.Instance{
			{Name: "both", Status: "Running", CPUs: 2, Memory: 2 * gib, Disk: 60 * gib, Runtime: "docker"},
			{Name: "cli", Status: "Stopped", CPUs: 4, Memory: 8 * gib, Disk: 100 * gib, Runtime: "containerd"},
			{Name: "odd", Status: "Broken"},
		},
	}
	cfg := &fakeCfg{
		names: []string{"both", "file"},
		files: map[string]string{
			"both": "cpu: 6\nmemory: 3.5\ndisk: 120\nruntime: incus\n",
			"file": "cpu: 1\nmemory: 0.5\ndisk: 10\nruntime: docker\n",
		},
	}
	ps, err := New(c, cfg, nil).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 4 {
		t.Fatalf("got %d profiles: %+v", len(ps), ps)
	}
	for i := 1; i < len(ps); i++ {
		if ps[i-1].Name > ps[i].Name {
			t.Errorf("not sorted: %v", ps)
		}
	}
	b := find(t, ps, "both")
	want := Profile{Name: "both", Status: "running", Runtime: "docker", CPUs: 6, MemoryBytes: 3*gib + gib/2, DiskBytes: 120 * gib, ColimaVersion: "0.10.3", Source: "both"}
	if b.Name != want.Name || b.Status != want.Status || b.Runtime != want.Runtime || b.CPUs != want.CPUs ||
		b.MemoryBytes != want.MemoryBytes || b.DiskBytes != want.DiskBytes || b.ColimaVersion != want.ColimaVersion ||
		b.Source != want.Source || b.ActiveJobID != nil || len(b.Warnings) != 0 {
		t.Errorf("both: got %+v want %+v", b, want)
	}
	cl := find(t, ps, "cli")
	if cl.Source != "cli_only" || cl.Status != "stopped" || cl.Runtime != "containerd" || cl.CPUs != 4 || cl.MemoryBytes != 8*gib || cl.DiskBytes != 100*gib {
		t.Errorf("cli_only: %+v", cl)
	}
	if find(t, ps, "odd").Status != "unknown" {
		t.Errorf("unknown status not mapped")
	}
	f := find(t, ps, "file")
	if f.Source != "file_only" || f.Status != "stopped" || f.Runtime != "docker" || f.CPUs != 1 || f.MemoryBytes != gib/2 || f.DiskBytes != 10*gib {
		t.Errorf("file_only: %+v", f)
	}
}

func TestStatusCaseInsensitive(t *testing.T) {
	c := &fakeColima{ver: v0103, insts: []colima.Instance{{Name: "a", Status: "running"}, {Name: "b", Status: "STOPPED"}}}
	ps, err := New(c, &fakeCfg{}, nil).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if find(t, ps, "a").Status != "running" || find(t, ps, "b").Status != "stopped" {
		t.Errorf("%+v", ps)
	}
}

func TestFileFirstAllocationFallbacks(t *testing.T) {
	c := &fakeColima{ver: v0103, insts: []colima.Instance{{Name: "p", Status: "Running", CPUs: 2, Memory: 4 * gib, Disk: 50 * gib, Runtime: "docker"}}}
	cfg := &fakeCfg{names: []string{"p"}, files: map[string]string{"p": "cpu: 8\n"}}
	p, err := New(c, cfg, nil).Get(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if p.CPUs != 8 || p.MemoryBytes != 4*gib || p.DiskBytes != 50*gib || p.Runtime != "docker" {
		t.Errorf("%+v", p)
	}
	// File only, empty file: zeros and empty runtime.
	cfg2 := &fakeCfg{names: []string{"q"}, files: map[string]string{"q": ""}}
	q, err := New(&fakeColima{ver: v0103}, cfg2, nil).Get(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if q.CPUs != 0 || q.MemoryBytes != 0 || q.DiskBytes != 0 || q.Runtime != "" || q.Source != "file_only" {
		t.Errorf("%+v", q)
	}
}

func TestVersionWarningTable(t *testing.T) {
	cases := []struct {
		name string
		v    colima.VersionInfo
		want bool
		ver  string
	}{
		{"0.10.0", ver("0.10.0", 0, 10, true, ""), false, "0.10.0"},
		{"0.10.9", ver("v0.10.9", 0, 10, true, ""), false, "0.10.9"},
		{"0.11.0", ver("0.11.0", 0, 11, true, ""), true, "0.11.0"},
		{"0.9.9", ver("0.9.9", 0, 9, true, ""), true, "0.9.9"},
		{"1.10.0", ver("1.10.0", 1, 10, true, ""), true, "1.10.0"},
		{"dev", ver("HEAD-abcdef0", 0, 0, false, ""), true, "HEAD-abcdef0"},
		{"prerelease", ver("0.10.1-rc.1", 0, 10, true, "rc.1"), false, "0.10.1-rc.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeColima{ver: tc.v, insts: []colima.Instance{{Name: "p", Status: "Running"}}}
			p, err := New(c, &fakeCfg{}, nil).Get(context.Background(), "p")
			if err != nil {
				t.Fatal(err)
			}
			if got := hasWarn(p, WarnColimaVersionUnsupported); got != tc.want {
				t.Errorf("warn=%v want %v", got, tc.want)
			}
			if p.ColimaVersion != tc.ver {
				t.Errorf("version %q want %q", p.ColimaVersion, tc.ver)
			}
		})
	}
}

func TestVersionFailureAlone(t *testing.T) {
	c := &fakeColima{verErr: errors.New("boom"), insts: []colima.Instance{{Name: "p", Status: "Running"}}}
	p, err := New(c, &fakeCfg{}, nil).Get(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if p.ColimaVersion != "" || len(p.Warnings) != 0 {
		t.Errorf("%+v", p)
	}
}

func TestAdapterFailureWithFileProfiles(t *testing.T) {
	c := &fakeColima{ver: v0103, listErr: &colima.BinaryNotFoundError{}}
	cfg := &fakeCfg{names: []string{"f"}, files: map[string]string{"f": "cpu: 2\n"}}
	s := New(c, cfg, nil)
	ps, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Source != "file_only" || ps[0].Status != "unknown" || !hasWarn(ps[0], WarnColimaUnavailable) {
		t.Errorf("%+v", ps)
	}
	p, err := s.Get(context.Background(), "f")
	if err != nil || p.Status != "unknown" || !hasWarn(p, WarnColimaUnavailable) {
		t.Errorf("get: %+v %v", p, err)
	}
}

func TestAdapterFailureWithoutFileProfiles(t *testing.T) {
	inner := &colima.ExitError{}
	c := &fakeColima{listErr: inner}
	s := New(c, &fakeCfg{}, nil)
	_, err := s.List(context.Background())
	var ce *ColimaError
	if !errors.As(err, &ce) {
		t.Fatalf("want ColimaError, got %v", err)
	}
	var ee *colima.ExitError
	if !errors.As(err, &ee) {
		t.Errorf("adapter error not wrapped")
	}
	_, err = s.Get(context.Background(), "nope")
	if !errors.As(err, &ce) || errors.Is(err, ErrNotFound) {
		t.Errorf("get: %v", err)
	}
}

func TestGetNotFoundWhenCLIAnswered(t *testing.T) {
	s := New(&fakeColima{ver: v0103}, &fakeCfg{}, nil)
	if _, err := s.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func TestConfigFolderUnreadable(t *testing.T) {
	c := &fakeColima{ver: v0103, insts: []colima.Instance{{Name: "p", Status: "Running", CPUs: 2}}}
	cfg := &fakeCfg{listErr: &config.ReadError{Err: errors.New("denied")}}
	ps, err := New(c, cfg, nil).List(context.Background())
	if err != nil || len(ps) != 1 || !hasWarn(ps[0], WarnConfigUnreadable) || ps[0].CPUs != 2 {
		t.Errorf("%+v %v", ps, err)
	}
	// Both sides failing yields the typed error.
	c2 := &fakeColima{listErr: errors.New("x")}
	if _, err := New(c2, cfg, nil).List(context.Background()); !errors.As(err, new(*ColimaError)) {
		t.Errorf("got %v", err)
	}
}

func TestInvalidYAMLStillListsWithCLINumbers(t *testing.T) {
	for _, bad := range []error{&config.ParseError{Message: "x"}, &config.TypeError{Problems: []string{"cpu"}}, &config.ReadError{Err: errors.New("x")}} {
		c := &fakeColima{ver: v0103, insts: []colima.Instance{{Name: "p", Status: "Running", CPUs: 3, Memory: gib, Disk: 5 * gib, Runtime: "docker"}}}
		cfg := &fakeCfg{names: []string{"p", "q"}, readErrs: map[string]error{"p": bad, "q": bad}}
		ps, err := New(c, cfg, nil).List(context.Background())
		if err != nil || len(ps) != 2 {
			t.Fatalf("%v %+v", err, ps)
		}
		p := find(t, ps, "p")
		if p.Source != "both" || p.CPUs != 3 || p.MemoryBytes != gib || p.DiskBytes != 5*gib || !hasWarn(p, WarnConfigInvalid) {
			t.Errorf("%+v", p)
		}
		q := find(t, ps, "q")
		if q.Source != "file_only" || !hasWarn(q, WarnConfigInvalid) || q.CPUs != 0 {
			t.Errorf("%+v", q)
		}
	}
}

func TestNameValidationDrops(t *testing.T) {
	c := &fakeColima{ver: v0103, insts: []colima.Instance{
		{Name: "ok", Status: "Running"},
		{Name: "a..b", Status: "Running"}, // passes colima, fails config
		{Name: "-flag", Status: "Running"},
		{Name: "../x", Status: "Running"},
	}}
	cfg := &fakeCfg{names: []string{"a..b", "ok"}, files: map[string]string{"ok": ""}}
	s := New(c, cfg, nil)
	ps, err := s.List(context.Background())
	if err != nil || len(ps) != 1 || ps[0].Name != "ok" {
		t.Errorf("%+v %v", ps, err)
	}
	for _, n := range []string{"a..b", "-flag", "../x", "", "a/b"} {
		if _, err := s.Get(context.Background(), n); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v", n, err)
		}
	}
}

func TestActiveJobID(t *testing.T) {
	c := &fakeColima{ver: v0103, insts: []colima.Instance{{Name: "a", Status: "Running"}, {Name: "b", Status: "Running"}}}
	busy := func(p string) (string, bool) {
		if p == "a" {
			return "job-1", true
		}
		return "ignored", false
	}
	ps, err := New(c, &fakeCfg{}, busy).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, b := find(t, ps, "a"), find(t, ps, "b")
	if a.ActiveJobID == nil || *a.ActiveJobID != "job-1" || b.ActiveJobID != nil {
		t.Errorf("%+v %+v", a, b)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	var ma, mb map[string]any
	_ = json.Unmarshal(ja, &ma)
	_ = json.Unmarshal(jb, &mb)
	if ma["activeJobId"] != "job-1" {
		t.Errorf("%s", ja)
	}
	if v, ok := mb["activeJobId"]; !ok || v != nil {
		t.Errorf("activeJobId must encode as null: %s", jb)
	}
	if _, ok := mb["warnings"]; ok {
		t.Errorf("empty warnings must be omitted: %s", jb)
	}
	for _, k := range []string{"name", "status", "runtime", "cpus", "memoryBytes", "diskBytes", "colimaVersion", "source"} {
		if _, ok := mb[k]; !ok {
			t.Errorf("missing json key %s", k)
		}
	}
}

func TestNoCache(t *testing.T) {
	c := &fakeColima{ver: v0103, insts: []colima.Instance{{Name: "p", Status: "Running"}}}
	cfg := &fakeCfg{names: []string{"p"}, files: map[string]string{"p": ""}}
	s := New(c, cfg, nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := s.List(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, "p"); err != nil {
			t.Fatal(err)
		}
	}
	if c.listHits.Load() != 4 || c.verHits.Load() != 4 || cfg.hits.Load() != 4 {
		t.Errorf("hits list=%d ver=%d read=%d", c.listHits.Load(), c.verHits.Load(), cfg.hits.Load())
	}
}

func TestContextPassthrough(t *testing.T) {
	for _, e := range []error{context.Canceled, context.DeadlineExceeded} {
		cfg := &fakeCfg{names: []string{"f"}, files: map[string]string{"f": ""}}
		s := New(&fakeColima{listErr: e}, cfg, nil)
		if _, err := s.List(context.Background()); err != e {
			t.Errorf("List: got %v want %v", err, e)
		}
		if _, err := s.Get(context.Background(), "f"); err != e {
			t.Errorf("Get: got %v want %v", err, e)
		}
		s = New(&fakeColima{verErr: e}, cfg, nil)
		if _, err := s.List(context.Background()); err != e {
			t.Errorf("version: got %v want %v", err, e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(&fakeColima{}, &fakeCfg{}, nil).List(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v", err)
	}
}

func TestNewColimaSourceSatisfiesInterface(t *testing.T) {
	var _ ColimaSource = NewColimaSource(nil)
	var _ ConfigSource = (*config.Service)(nil)
}
