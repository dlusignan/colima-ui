package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ConfigFileName is the per-profile file inside a profile folder.
const ConfigFileName = "colima.yaml"

var profileNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateProfileName applies the allowlist. The regex already forbids a
// leading underscore (Colima's _templates, _lima, _store siblings), a leading
// dot, separators and NUL; ".." inside a name is rejected explicitly too.
func ValidateProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty", ErrInvalidProfileName)
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: traversal or separator", ErrInvalidProfileName)
	}
	if !profileNameRE.MatchString(name) {
		return fmt.Errorf("%w: must match %s", ErrInvalidProfileName, profileNameRE.String())
	}
	return nil
}

// Locate resolves the Colima base folder: $COLIMA_HOME if set, else
// <home>/.colima if it exists, else $XDG_CONFIG_HOME/colima (default
// <home>/.config/colima). getenv and home are injected so tests never touch the
// real folder. Returned paths are always absolute.
func Locate(getenv func(string) string, home string) (string, error) {
	if v := getenv("COLIMA_HOME"); v != "" {
		return requireAbs(v)
	}
	if home != "" {
		dot := filepath.Join(home, ".colima")
		if st, err := os.Stat(dot); err == nil && st.IsDir() {
			return requireAbs(dot)
		}
	}
	if v := getenv("XDG_CONFIG_HOME"); v != "" {
		return requireAbs(filepath.Join(v, "colima"))
	}
	if home == "" {
		return "", errors.New("cannot locate the Colima folder: no home directory")
	}
	return requireAbs(filepath.Join(home, ".config", "colima"))
}

func requireAbs(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", errors.New("the Colima folder setting must be an absolute path")
	}
	return filepath.Clean(p), nil
}

// Service reads profiles under one base folder.
type Service struct {
	base string
}

// New returns a Service rooted at base, which must be absolute.
func New(base string) (*Service, error) {
	b, err := requireAbs(base)
	if err != nil {
		return nil, err
	}
	return &Service{base: b}, nil
}

// ProfileDir returns <base>/<name> after validating the name and confirming
// that the joined path sits directly under the base folder.
func (s *Service) ProfileDir(name string) (string, error) {
	if err := ValidateProfileName(name); err != nil {
		return "", err
	}
	dir := filepath.Join(s.base, name)
	if filepath.Dir(dir) != s.base {
		return "", fmt.Errorf("%w: escapes base folder", ErrInvalidProfileName)
	}
	return dir, nil
}

// ConfigPath returns <base>/<name>/colima.yaml.
func (s *Service) ConfigPath(name string) (string, error) {
	dir, err := s.ProfileDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ConfigFileName), nil
}

// resolveProfileDir validates the name and also follows symlinks: a profile
// folder that is a symlink pointing outside the base folder is rejected. The
// colima.yaml file itself may be a symlink (dotfile managers do this); it is
// opened as a regular file and only parsed, never executed.
func (s *Service) resolveProfileDir(name string) (string, error) {
	dir, err := s.ProfileDir(name)
	if err != nil {
		return "", err
	}
	realBase, err := filepath.EvalSymlinks(s.base)
	if err != nil {
		return "", notFoundOrRead(err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", notFoundOrRead(err)
	}
	if filepath.Dir(realDir) != realBase {
		return "", fmt.Errorf("%w: escapes base folder", ErrInvalidProfileName)
	}
	return realDir, nil
}

func notFoundOrRead(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrProfileNotFound
	}
	return &ReadError{Err: stripPath(err)}
}

func stripPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}

// ListProfiles returns the sorted names of profile folders that hold a
// colima.yaml. Names starting with "_" (_templates, _lima, _store), names that
// fail validation, non-folders and escaping symlinks are skipped. A missing base
// folder yields an empty list.
func (s *Service) ListProfiles() ([]string, error) {
	entries, err := os.ReadDir(s.base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, &ReadError{Err: stripPath(err)}
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "_") || ValidateProfileName(name) != nil {
			continue
		}
		dir, err := s.resolveProfileDir(name)
		if err != nil {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, ConfigFileName)); err != nil || !st.Mode().IsRegular() {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
