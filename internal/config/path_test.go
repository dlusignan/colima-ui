package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProfileName(t *testing.T) {
	bad := map[string]string{
		"empty":            "",
		"dotdot":           "..",
		"dotdot inside":    "a..b",
		"parent traversal": "../etc",
		"slash":            "a/b",
		"backslash":        `a\b`,
		"absolute":         "/etc/passwd",
		"leading under":    "_templates",
		"leading dot":      ".hidden",
		"leading dash":     "-x",
		"space":            "a b",
		"nul":              "a\x00b",
		"newline":          "a\nb",
		"too long":         strings.Repeat("a", 65),
		"unicode":          "pröfile",
	}
	for label, name := range bad {
		if err := ValidateProfileName(name); !errors.Is(err, ErrInvalidProfileName) {
			t.Errorf("%s: %q accepted, err=%v", label, name, err)
		}
	}
	for _, name := range []string{"default", "work", "a", "My-Prof_1.2", strings.Repeat("a", 64)} {
		if err := ValidateProfileName(name); err != nil {
			t.Errorf("%q rejected: %v", name, err)
		}
	}
}

func TestProfileDirAndConfigPath(t *testing.T) {
	base := t.TempDir()
	s, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.ProfileDir("default")
	if err != nil || dir != filepath.Join(base, "default") {
		t.Fatalf("dir=%q err=%v", dir, err)
	}
	cfg, err := s.ConfigPath("work")
	if err != nil || cfg != filepath.Join(base, "work", "colima.yaml") {
		t.Fatalf("cfg=%q err=%v", cfg, err)
	}
	if _, err := s.ConfigPath("../x"); !errors.Is(err, ErrInvalidProfileName) {
		t.Fatalf("err=%v", err)
	}
	if _, err := New("relative/dir"); err == nil {
		t.Fatal("relative base accepted")
	}
}

func TestLocate(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	// COLIMA_HOME wins.
	got, err := Locate(env(map[string]string{"COLIMA_HOME": "/srv/c"}), home)
	if err != nil || got != "/srv/c" {
		t.Fatalf("got %q %v", got, err)
	}
	// XDG used when ~/.colima is absent.
	got, _ = Locate(env(map[string]string{"XDG_CONFIG_HOME": xdg}), home)
	if got != filepath.Join(xdg, "colima") {
		t.Fatalf("got %q", got)
	}
	// ~/.colima beats XDG when present.
	if err := os.Mkdir(filepath.Join(home, ".colima"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, _ = Locate(env(map[string]string{"XDG_CONFIG_HOME": xdg}), home)
	if got != filepath.Join(home, ".colima") {
		t.Fatalf("got %q", got)
	}
	// Default XDG fallback.
	home2 := t.TempDir()
	got, _ = Locate(env(nil), home2)
	if got != filepath.Join(home2, ".config", "colima") {
		t.Fatalf("got %q", got)
	}
	if _, err := Locate(env(map[string]string{"COLIMA_HOME": "rel"}), home); err == nil {
		t.Fatal("relative COLIMA_HOME accepted")
	}
	if _, err := Locate(env(nil), ""); err == nil {
		t.Fatal("no home accepted")
	}
}

func copyFixture(t *testing.T, fixture, base, profile string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "colima.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListProfiles(t *testing.T) {
	base := t.TempDir()
	copyFixture(t, "minimal.yaml", base, "default")
	copyFixture(t, "minimal.yaml", base, "work")
	copyFixture(t, "minimal.yaml", base, "_templates")
	for _, d := range []string{"_lima", "_store", "empty"} {
		if err := os.Mkdir(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "ssh_config"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := New(base)
	got, err := s.ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "default,work" {
		t.Fatalf("got %v", got)
	}
	missing, _ := New(filepath.Join(base, "nope"))
	if got, err := missing.ListProfiles(); err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestSymlinkedProfileEscapingBaseRejected(t *testing.T) {
	outside := t.TempDir()
	copyFixture(t, "minimal.yaml", outside, "evil")
	base := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "evil"), filepath.Join(base, "evil")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	s, _ := New(base)
	if _, err := s.Read("evil"); !errors.Is(err, ErrInvalidProfileName) {
		t.Fatalf("err=%v", err)
	}
	if got, _ := s.ListProfiles(); len(got) != 0 {
		t.Fatalf("listed %v", got)
	}
}

// The exclusive hold is keyed by the request name, so a second spelling of the
// same folder must not resolve.
func TestAlternateSpellingsOfAProfileDoNotResolve(t *testing.T) {
	base := t.TempDir()
	copyFixture(t, "minimal.yaml", base, "real")
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "alias")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	g := &fakeGate{busy: map[string]bool{}}
	s, _ := New(base, WithGate(g))
	if _, err := s.Read("real"); err != nil {
		t.Fatalf("exact name: %v", err)
	}
	if _, err := s.Read("alias"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("symlinked sibling read: %v", err)
	}
	if _, err := s.Apply("alias", Changes{CPU: ip(3)}); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("symlinked sibling apply: %v", err)
	}
	// Case variant: on a case-insensitive filesystem the stat succeeds, so the
	// exact-spelling check is what must refuse it. On a case-sensitive one it
	// is simply absent. Either way: not found, and the file is untouched.
	before, _ := os.ReadFile(filepath.Join(base, "real", ConfigFileName))
	for _, n := range []string{"REAL", "Real"} {
		if _, err := s.Read(n); !errors.Is(err, ErrProfileNotFound) {
			t.Fatalf("%s read: %v", n, err)
		}
		if _, err := s.Apply(n, Changes{CPU: ip(3)}); !errors.Is(err, ErrProfileNotFound) {
			t.Fatalf("%s apply: %v", n, err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(base, "real", ConfigFileName))
	if string(before) != string(after) {
		t.Fatal("file changed through an alternate spelling")
	}
	if got, _ := s.ListProfiles(); strings.Join(got, ",") != "real" {
		t.Fatalf("listed %v", got)
	}
}

func TestReadErrors(t *testing.T) {
	base := t.TempDir()
	s, _ := New(base)
	_, err := s.Read("nope")
	if !errors.Is(err, ErrProfileNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err=%v", err)
	}
	// Folder exists without colima.yaml.
	_ = os.Mkdir(filepath.Join(base, "bare"), 0o755)
	if _, err := s.Read("bare"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("err=%v", err)
	}
	// colima.yaml is a directory: not a regular file, typed ReadError, no path.
	_ = os.MkdirAll(filepath.Join(base, "dirprof", "colima.yaml"), 0o755)
	_, err = s.Read("dirprof")
	var re *ReadError
	if !errors.As(err, &re) {
		t.Fatalf("err=%v", err)
	}
	assertNoPath(t, err, base)

	copyFixture(t, "malformed.yaml", base, "bad")
	_, err = s.Read("bad")
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Line == 0 {
		t.Fatalf("err=%v", err)
	}
	assertNoPath(t, err, base)

	copyFixture(t, "wrong_types.yaml", base, "types")
	_, err = s.Read("types")
	var te *TypeError
	if !errors.As(err, &te) || len(te.Problems) < 2 {
		t.Fatalf("err=%v", err)
	}
	assertNoPath(t, err, base)

	for _, name := range []string{"..", "a/b", ""} {
		if _, err := s.Read(name); !errors.Is(err, ErrInvalidProfileName) {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func assertNoPath(t *testing.T, err error, base string) {
	t.Helper()
	if strings.Contains(err.Error(), base) || strings.Contains(err.Error(), "colima.yaml") {
		t.Errorf("error leaks path: %v", err)
	}
}
