package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeGate runs fn like the Operation Manager would, or refuses.
type fakeGate struct {
	mu      sync.Mutex
	busy    map[string]bool
	inside  bool
	calls   []string
	gateErr error
}

func (g *fakeGate) TryExclusive(profile string, fn func() error) (bool, error) {
	g.mu.Lock()
	g.calls = append(g.calls, profile)
	if g.gateErr != nil {
		g.mu.Unlock()
		return false, g.gateErr
	}
	if g.busy[profile] {
		g.mu.Unlock()
		return false, nil
	}
	g.mu.Unlock()
	g.inside = true
	defer func() { g.inside = false }()
	return true, fn()
}

func ip(n int) *int         { return &n }
func fp(f float64) *float64 { return &f }
func running(b bool) Option { return WithVMRunning(func(string) (bool, error) { return b, nil }) }
func applyLimits() Option   { return WithLimits(Limits{MaxCPU: 16, MaxMemoryGiB: 64, MaxDiskGiB: 1000}) }

type env struct {
	base string
	dir  string
	file string
	gate *fakeGate
	svc  *Service
}

func newEnv(t *testing.T, content string, opts ...Option) *env {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "default")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, ConfigFileName)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &fakeGate{busy: map[string]bool{}}
	all := append([]Option{WithGate(g), applyLimits(), running(true)}, opts...)
	svc, err := New(base, all...)
	if err != nil {
		t.Fatal(err)
	}
	return &env{base: base, dir: dir, file: file, gate: g, svc: svc}
}

func (e *env) content(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.file)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (e *env) strays(t *testing.T) []string {
	t.Helper()
	ents, _ := os.ReadDir(e.dir)
	var out []string
	for _, en := range ents {
		if en.Name() != ConfigFileName {
			out = append(out, en.Name())
		}
	}
	return out
}

const basic = "cpu: 2\nmemory: 4\ndisk: 60\nruntime: docker\n"

func TestApplyBasicAndRestartList(t *testing.T) {
	e := newEnv(t, basic)
	got, err := e.svc.Apply("default", Changes{CPU: ip(4), Memory: fp(8), Disk: ip(100)})
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{"cpu", ClassRestart}, {"memory", ClassRestart}, {"disk", ClassRestart}}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("changes = %+v", got)
	}
	if c := e.content(t); c != "cpu: 4\nmemory: 8\ndisk: 100\nruntime: docker\n" {
		t.Fatalf("content = %q", c)
	}
	if len(e.gate.calls) != 1 {
		t.Fatalf("calls = %v", e.gate.calls)
	}
}

func TestApplyFractionalMemoryAndAppendMissingKey(t *testing.T) {
	e := newEnv(t, "cpu: 2\n")
	if _, err := e.svc.Apply("default", Changes{Memory: fp(2.5), Disk: ip(50)}); err != nil {
		t.Fatal(err)
	}
	if c := e.content(t); c != "cpu: 2\nmemory: 2.5\ndisk: 50\n" {
		t.Fatalf("content = %q", c)
	}
	p, err := e.svc.Read("default")
	if err != nil || *p.Memory != 2.5 || *p.Disk != 50 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestApplyRestartListPerFieldAndVMState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ch      Changes
		running bool
		want    []string
	}{
		{"cpu running", Changes{CPU: ip(3)}, true, []string{"cpu"}},
		{"memory running", Changes{Memory: fp(5)}, true, []string{"memory"}},
		{"disk running", Changes{Disk: ip(70)}, true, []string{"disk"}},
		{"cpu stopped", Changes{CPU: ip(3)}, false, nil},
		{"memory stopped", Changes{Memory: fp(5)}, false, nil},
		{"disk stopped", Changes{Disk: ip(70)}, false, nil},
		{"no-op", Changes{CPU: ip(2)}, true, nil},
		{"empty", Changes{}, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, basic, running(tc.running))
			got, err := e.svc.Apply("default", tc.ch)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v want %v", got, tc.want)
			}
			for i, w := range tc.want {
				if got[i].Field != w {
					t.Fatalf("got %+v want %v", got, tc.want)
				}
			}
		})
	}
}

func TestApplyVMRunningErrorTreatedAsRunning(t *testing.T) {
	e := newEnv(t, basic, WithVMRunning(func(string) (bool, error) { return false, errors.New("boom") }))
	got, err := e.svc.Apply("default", Changes{CPU: ip(3)})
	if err != nil || len(got) != 1 || got[0].Field != "cpu" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestApplyVMRunningCalledOutsideGate(t *testing.T) {
	var e *env
	e = newEnv(t, basic, WithVMRunning(func(string) (bool, error) {
		if e.gate.inside {
			t.Error("vmRunning called inside the gate")
		}
		return true, nil
	}))
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
}

func TestApplyNoOpLeavesFileUntouched(t *testing.T) {
	e := newEnv(t, "# keep\ncpu:    2   # x\n")
	before := e.content(t)
	got, err := e.svc.Apply("default", Changes{CPU: ip(2)})
	if err != nil || len(got) != 0 || e.content(t) != before {
		t.Fatalf("%+v %v %q", got, err, e.content(t))
	}
}

func TestApplyValidationFailureWritesNothing(t *testing.T) {
	e := newEnv(t, "# c\ncpu:   2   # x\n\nmemory: 4\ndisk: 60\n")
	before := e.content(t)
	_, err := e.svc.Apply("default", Changes{CPU: ip(99), Memory: fp(0.5), Disk: ip(10)})
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Fields) != 3 {
		t.Fatalf("err = %v", err)
	}
	if e.content(t) != before {
		t.Fatalf("file changed: %q", e.content(t))
	}
	if s := e.strays(t); len(s) != 0 {
		t.Fatalf("strays: %v", s)
	}
}

func TestApplyValidatesOnlyChangedFields(t *testing.T) {
	// mountType is hand-edited to something invalid; a cpu edit still works.
	e := newEnv(t, "cpu: 2\nmountType: bogus\n")
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.content(t), "mountType: bogus") || !strings.Contains(e.content(t), "cpu: 3") {
		t.Fatal(e.content(t))
	}
}

func TestApplyBusyRefusedAndOtherProfileSucceeds(t *testing.T) {
	e := newEnv(t, basic)
	other := filepath.Join(e.base, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, ConfigFileName), []byte(basic), 0o644); err != nil {
		t.Fatal(err)
	}
	e.gate.busy["default"] = true
	before := e.content(t)
	_, err := e.svc.Apply("default", Changes{CPU: ip(3)})
	var be *BusyError
	if !errors.As(err, &be) || !errors.Is(err, ErrProfileBusy) {
		t.Fatalf("err = %v", err)
	}
	if e.content(t) != before {
		t.Fatal("busy apply wrote")
	}
	if _, err := e.svc.Apply("other", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(other, ConfigFileName)); !strings.Contains(string(b), "cpu: 3") {
		t.Fatal("other profile not written")
	}
}

func TestApplyWorkRunsInsideGate(t *testing.T) {
	e := newEnv(t, basic)
	e.svc.beforeRename = func() {
		if !e.gate.inside {
			t.Error("write happened outside the gate hold")
		}
	}
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
	if e.gate.inside {
		t.Fatal("gate still held after Apply")
	}
}

func TestApplyNilGateFailsClosed(t *testing.T) {
	e := newEnv(t, basic)
	svc, _ := New(e.base, applyLimits())
	before := e.content(t)
	if _, err := svc.Apply("default", Changes{CPU: ip(3)}); !errors.Is(err, ErrNoGate) {
		t.Fatalf("err = %v", err)
	}
	if e.content(t) != before {
		t.Fatal("wrote without a gate")
	}
}

func TestApplyGateErrorPropagates(t *testing.T) {
	e := newEnv(t, basic)
	e.gate.gateErr = errors.New("gate down")
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err == nil || errors.Is(err, ErrProfileBusy) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyBadNamesRejectedBeforeGate(t *testing.T) {
	e := newEnv(t, basic)
	for _, n := range []string{"..", "../x", "a/b", "", "_templates", ".hidden", "a\x00b", `a\b`} {
		_, err := e.svc.Apply(n, Changes{CPU: ip(3)})
		if !errors.Is(err, ErrInvalidProfileName) {
			t.Errorf("%q: err = %v", n, err)
		}
	}
	if len(e.gate.calls) != 0 {
		t.Fatalf("gate called for bad names: %v", e.gate.calls)
	}
}

func TestApplyMissingProfile(t *testing.T) {
	e := newEnv(t, basic)
	if _, err := e.svc.Apply("nope", Changes{CPU: ip(3)}); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyNoAbsolutePathsInErrors(t *testing.T) {
	e := newEnv(t, basic)
	check := func(err error) {
		t.Helper()
		if err == nil {
			t.Fatal("want error")
		}
		for _, p := range []string{e.base, os.TempDir()} {
			if strings.Contains(err.Error(), p) {
				t.Fatalf("path leaked: %v", err)
			}
		}
	}
	e.svc.renameFn = func(a, b string) error { return &os.LinkError{Op: "rename", Old: a, New: b, Err: os.ErrPermission} }
	_, err := e.svc.Apply("default", Changes{CPU: ip(3)})
	check(err)
	e.svc.renameFn = nil
	e.svc.beforeRename = func() { os.WriteFile(e.file, []byte("cpu: 1\n"), 0o644) }
	_, err = e.svc.Apply("default", Changes{CPU: ip(3)})
	check(err)
	// Unwritable folder: temp creation fails with a path error.
	e2 := newEnv(t, basic)
	if err := os.Chmod(e2.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(e2.dir, 0o755)
	if os.Geteuid() != 0 {
		_, err = e2.svc.Apply("default", Changes{CPU: ip(3)})
		if err == nil || strings.Contains(err.Error(), e2.base) {
			t.Fatalf("err = %v", err)
		}
		var we *WriteError
		if !errors.As(err, &we) {
			t.Fatalf("want WriteError, got %T", err)
		}
	}
}

func TestApplyAtomicRenameFailureLeavesOriginalAndNoStray(t *testing.T) {
	e := newEnv(t, basic)
	before := e.content(t)
	e.svc.renameFn = func(string, string) error { return errors.New("disk on fire") }
	_, err := e.svc.Apply("default", Changes{CPU: ip(3)})
	var we *WriteError
	if !errors.As(err, &we) {
		t.Fatalf("err = %v", err)
	}
	if e.content(t) != before {
		t.Fatal("original modified")
	}
	if s := e.strays(t); len(s) != 0 {
		t.Fatalf("stray files: %v", s)
	}
}

func TestApplyTempFileIsInTargetFolderBeforeRename(t *testing.T) {
	e := newEnv(t, basic)
	e.svc.beforeRename = func() {
		if s := e.strays(t); len(s) != 1 || !strings.HasPrefix(s[0], ".colima.yaml.") {
			t.Errorf("temp not in folder: %v", s)
		}
		if e.content(t) != basic {
			t.Error("target modified before rename")
		}
	}
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
	if s := e.strays(t); len(s) != 0 {
		t.Fatalf("stray: %v", s)
	}
}

func TestApplyPreservesMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o640, 0o644, 0o664} {
		e := newEnv(t, basic)
		if err := os.Chmod(e.file, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(e.file)
		if st.Mode().Perm() != mode {
			t.Errorf("mode = %v want %v", st.Mode().Perm(), mode)
		}
	}
}

func TestApplyConcurrentExternalEditConflicts(t *testing.T) {
	e := newEnv(t, basic)
	external := "cpu: 8\nmemory: 4\ndisk: 60\n"
	e.svc.beforeRename = func() {
		if err := os.WriteFile(e.file, []byte(external), 0o644); err != nil {
			t.Error(err)
		}
	}
	_, err := e.svc.Apply("default", Changes{CPU: ip(3)})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if e.content(t) != external {
		t.Fatalf("external edit lost: %q", e.content(t))
	}
	if s := e.strays(t); len(s) != 0 {
		t.Fatalf("stray: %v", s)
	}
}

func TestApplySymlinkWrittenThrough(t *testing.T) {
	e := newEnv(t, basic)
	store := t.TempDir()
	real := filepath.Join(store, "dotfile.yaml")
	if err := os.WriteFile(real, []byte(basic), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(e.file)
	if err := os.Symlink(real, e.file); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Apply("default", Changes{CPU: ip(5)}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(e.file)
	if err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v %v", st, err)
	}
	if dest, _ := os.Readlink(e.file); dest != real {
		t.Fatalf("link retargeted to %q", dest)
	}
	b, _ := os.ReadFile(real)
	if string(b) != "cpu: 5\nmemory: 4\ndisk: 60\nruntime: docker\n" {
		t.Fatalf("target = %q", b)
	}
	if rs, _ := os.Stat(real); rs.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", rs.Mode().Perm())
	}
	if ents, _ := os.ReadDir(store); len(ents) != 1 {
		t.Fatalf("stray in target folder: %v", ents)
	}
}

func TestApplyDanglingSymlinkRefused(t *testing.T) {
	e := newEnv(t, basic)
	os.Remove(e.file)
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone.yaml"), e.file); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Apply("default", Changes{CPU: ip(3)})
	if !errors.Is(err, ErrUnwritableTarget) {
		t.Fatalf("err = %v", err)
	}
	if s := e.strays(t); len(s) != 0 {
		t.Fatalf("stray: %v", s)
	}
}

func TestApplyNonRegularTargetRefused(t *testing.T) {
	e := newEnv(t, basic)
	os.Remove(e.file)
	// Symlink to a folder.
	if err := os.Symlink(t.TempDir(), e.file); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); !errors.Is(err, ErrUnwritableTarget) {
		t.Fatalf("symlink to dir: %v", err)
	}
	os.Remove(e.file)
	// colima.yaml itself a directory.
	if err := os.Mkdir(e.file, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); !errors.Is(err, ErrUnwritableTarget) {
		t.Fatalf("dir: %v", err)
	}
}

func TestApplyEscapingProfileFolderRejected(t *testing.T) {
	e := newEnv(t, basic)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, ConfigFileName), []byte(basic), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.base, "evil")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Apply("evil", Changes{CPU: ip(3)}); !errors.Is(err, ErrInvalidProfileName) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, ConfigFileName)); string(b) != basic {
		t.Fatal("wrote outside the base folder")
	}
}

func TestApplyMultiDocumentRefused(t *testing.T) {
	for _, in := range []string{"cpu: 2\n---\ncpu: 3\n", "cpu: 2\n---\n", "cpu: 2\n...\n---\nx: 1\n"} {
		e := newEnv(t, in)
		_, err := e.svc.Apply("default", Changes{CPU: ip(4)})
		if !errors.Is(err, ErrMultiDocument) {
			t.Errorf("%q: err = %v", in, err)
		}
		if e.content(t) != in {
			t.Errorf("%q: file changed", in)
		}
	}
}

func TestApplyOversizedFileRefused(t *testing.T) {
	e := newEnv(t, "cpu: 2\n#"+strings.Repeat("x", maxFileSize))
	_, err := e.svc.Apply("default", Changes{CPU: ip(3)})
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyCommentOnlyFile(t *testing.T) {
	e := newEnv(t, "# my colima file\n# second line\n")
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
	c := e.content(t)
	if !strings.Contains(c, "# my colima file") || !strings.Contains(c, "# second line") || !strings.Contains(c, "cpu: 3") {
		t.Fatalf("content = %q", c)
	}
	if strings.Index(c, "# second line") > strings.Index(c, "cpu: 3") {
		t.Fatalf("comments moved below: %q", c)
	}
	if _, err := e.svc.Read("default"); err != nil {
		t.Fatal(err)
	}
}

func TestApplyEmptyAndFlowEmptyFiles(t *testing.T) {
	for _, in := range []string{"", "\n", "{}\n"} {
		e := newEnv(t, in)
		if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if c := e.content(t); c != "cpu: 3\n" {
			t.Fatalf("%q -> %q", in, c)
		}
	}
}

const rich = `# head comment

# second block
cpu: 2   # inline

# about memory
memory: 4
base: &base
    image: "colima"
    tag: 'single'
    list:
        - a
        - b
other: *base
script: |
    echo hi
    echo there
mounts:
  - location: /home/user/work
    writable: true
future_key: [1, 2, 3]
disk: 60
# foot comment
`

func TestApplyRoundTripKeepsCommentsKeysAndUnknown(t *testing.T) {
	e := newEnv(t, rich)
	if _, err := e.svc.Apply("default", Changes{CPU: ip(6), Memory: fp(9.5), Disk: ip(80)}); err != nil {
		t.Fatal(err)
	}
	got := e.content(t)
	want := `# head comment

# second block
cpu: 6 # inline
# about memory
memory: 9.5
base: &base
  image: "colima"
  tag: 'single'
  list:
    - a
    - b
other: *base
script: |
  echo hi
  echo there
mounts:
  - location: /home/user/work
    writable: true
future_key: [1, 2, 3]
disk: 80
# foot comment
`
	// Compare modulo the exact placement yaml.v3 picks for head comments, but
	// require every preserved item and the exact documented losses below.
	for _, must := range []string{
		"# head comment", "# second block", "cpu: 6 # inline", "# about memory", "memory: 9.5",
		"base: &base", `image: "colima"`, `tag: 'single'`, "other: *base", "script: |",
		"  echo hi\n  echo there", "future_key: [1, 2, 3]", "disk: 80", "# foot comment",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("lost %q in:\n%s", must, got)
		}
	}
	// Key order unchanged.
	order := []string{"cpu:", "memory:", "base:", "other:", "script:", "mounts:", "future_key:", "disk:"}
	last := -1
	for _, k := range order {
		i := strings.Index(got, "\n"+k)
		if i == -1 && strings.HasPrefix(got, k) {
			i = 0
		}
		if i <= last {
			t.Errorf("key order broken at %s", k)
		}
		last = i
	}
	if got != want {
		t.Errorf("exact output differs.\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestApplyDocumentedFormattingLosses pins the losses listed in the package
// doc and nothing beyond them.
func TestApplyDocumentedFormattingLosses(t *testing.T) {
	in := "cpu:   2   # aligned\n\n\n# about memory\nmemory: 4\nlist:\n    - a\n"
	e := newEnv(t, in)
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
	got := e.content(t)
	// 1. alignment spaces before the inline comment are gone
	if !strings.Contains(got, "cpu: 3 # aligned\n") {
		t.Errorf("alignment: %q", got)
	}
	// 2. blank lines between sections are gone
	if strings.Contains(got, "\n\n") {
		t.Errorf("blank lines kept: %q", got)
	}
	// 3. indentation normalised to two spaces (sequence under a key)
	if !strings.Contains(got, "list:\n  - a\n") {
		t.Errorf("indent: %q", got)
	}
	// Nothing else lost.
	if !strings.Contains(got, "# about memory\nmemory: 4\n") {
		t.Errorf("comment/key lost: %q", got)
	}
	// A comment before an explicit --- marker is kept (it moves to the key).
	e2 := newEnv(t, "# before marker\n---\ncpu: 2\n")
	if _, err := e2.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
	if c := e2.content(t); c != "# before marker\ncpu: 3\n" {
		t.Errorf("content = %q", c)
	}
}

func TestApplyLeadingCommentWithBlankLineSurvives(t *testing.T) {
	e := newEnv(t, "# lead\n\ncpu: 2\n")
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
	if c := e.content(t); !strings.HasPrefix(c, "# lead\n") || !strings.Contains(c, "cpu: 3") {
		t.Fatalf("content = %q", c)
	}
}

func TestApplyAliasValueReplacedWithPlainScalar(t *testing.T) {
	e := newEnv(t, "defaults: &d 2\ncpu: *d # shared\n")
	if _, err := e.svc.Apply("default", Changes{CPU: ip(4)}); err != nil {
		t.Fatal(err)
	}
	c := e.content(t)
	if !strings.Contains(c, "defaults: &d 2") || !strings.Contains(c, "cpu: 4 # shared") {
		t.Fatalf("content = %q", c)
	}
}

func TestApplyQuotedAndNullValues(t *testing.T) {
	e := newEnv(t, "cpu:\nmemory: 4\n")
	if _, err := e.svc.Apply("default", Changes{CPU: ip(4)}); err != nil {
		t.Fatal(err)
	}
	if c := e.content(t); c != "cpu: 4\nmemory: 4\n" {
		t.Fatalf("content = %q", c)
	}
}

func TestApplyUnchangedBytesOutsideEditedKeys(t *testing.T) {
	// A profile with no formatting quirks round-trips byte-identically around
	// the edited value.
	in := "cpu: 2\nenv:\n  A: b\nmounts:\n  - location: /home/user\n    writable: true\n"
	e := newEnv(t, in)
	if _, err := e.svc.Apply("default", Changes{CPU: ip(3)}); err != nil {
		t.Fatal(err)
	}
	if c := e.content(t); c != strings.Replace(in, "cpu: 2", "cpu: 3", 1) {
		t.Fatalf("content = %q", c)
	}
}

func TestApplyMalformedFileWritesNothing(t *testing.T) {
	for _, in := range []string{"cpu: [", "- a\n- b\n", "cpu: four\n", "\tcpu: 2\n"} {
		e := newEnv(t, in)
		_, err := e.svc.Apply("default", Changes{CPU: ip(3)})
		if err == nil {
			t.Errorf("%q: want error", in)
		}
		if e.content(t) != in {
			t.Errorf("%q: changed", in)
		}
	}
}

func FuzzApply(f *testing.F) {
	for _, s := range []string{
		"", "cpu: 2\n", "# c\n", "---\n", "{}", "a: &x 1\nb: *x\n", "cpu: !!str x\n",
		"cpu: 2\n---\ncpu: 3\n", "memory: .nan\n", "? [a]\n: b\n", "cpu: 2\r\nmemory: 4\r\n",
		"<<: {cpu: 1}\n", "cpu: 0x10\n", "\xef\xbb\xbfcpu: 2\n", "cpu: 1\ncpu: 2\n", "x: " + strings.Repeat("[", 50),
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		e := newEnv(t, string(data))
		_, err := e.svc.Apply("default", Changes{CPU: ip(3), Memory: fp(5), Disk: ip(70)})
		after, _ := os.ReadFile(e.file)
		if err != nil {
			if !bytes.Equal(after, data) {
				t.Fatalf("error %v but file changed", err)
			}
			return
		}
		p, rerr := e.svc.Read("default")
		if rerr != nil {
			t.Fatalf("wrote an unreadable file: %v", rerr)
		}
		if p.CPU == nil || *p.CPU != 3 || *p.Memory != 5 || *p.Disk != 70 {
			t.Fatalf("values not written: %+v", p)
		}
		if s := e.strays(t); len(s) != 0 {
			t.Fatalf("stray: %v", s)
		}
	})
}
