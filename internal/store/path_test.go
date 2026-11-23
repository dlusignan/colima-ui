package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	cfg := func() (string, error) { return "/home/user/Library/Application Support", nil }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	got, err := DefaultPath(env(nil), cfg)
	if err != nil || got != "/home/user/Library/Application Support/colima-ui/history.db" {
		t.Fatalf("default: %q %v", got, err)
	}
	got, err = DefaultPath(env(map[string]string{DataDirEnv: "/srv/data"}), cfg)
	if err != nil || got != "/srv/data/history.db" {
		t.Fatalf("override: %q %v", got, err)
	}
	if _, err := DefaultPath(env(map[string]string{DataDirEnv: "relative/dir"}), cfg); err == nil {
		t.Fatal("relative override accepted")
	}
	if _, err := DefaultPath(env(nil), func() (string, error) { return "", errors.New("boom /secret") }); err == nil {
		t.Fatal("expected error")
	} else if got := err.Error(); got == "" || filepath.IsAbs(got) {
		t.Fatalf("odd error %q", got)
	}
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestPermissionsFresh(t *testing.T) {
	path := dbPath(t)
	s := openAt(t, path, newClock(t0), nil)
	mustAppend(t, s, smp("p", "vm", t0, 1, 1)) // creates -wal and -shm
	if m := mode(t, filepath.Dir(path)); m != 0o700 {
		t.Errorf("folder mode = %o, want 700", m)
	}
	if m := mode(t, path); m != 0o600 {
		t.Errorf("file mode = %o, want 600", m)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if m := mode(t, path+suffix); m&0o077 != 0 {
			t.Errorf("%s mode = %o, group/other access", suffix, m)
		}
	}
}

func TestPermissionsTightenedOnExisting(t *testing.T) {
	path := dbPath(t)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	openAt(t, path, newClock(t0), nil)
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("folder mode = %o", m)
	}
	if m := mode(t, path); m != 0o600 {
		t.Errorf("file mode = %o", m)
	}
}

func TestPathWithSpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Application Support", "a?b#c%d", "history.db")
	s := openAt(t, path, newClock(t0), nil)
	mustAppend(t, s, smp("p", "vm", t0, 1, 1))
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestOpenErrorsCarryNoPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "plainfile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(t.Context(), Options{Path: filepath.Join(file, "sub", "history.db")})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); containsAny(got, dir) {
		t.Fatalf("error leaks path: %q", got)
	}
}

func containsAny(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
