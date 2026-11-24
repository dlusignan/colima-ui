package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Bounds for the creation mounts and environment. They mirror internal/colima
// (a test there pins the two together).
const (
	maxNewMounts  = 16
	maxMountPath  = 4096
	maxNewEnv     = 32
	maxEnvKeyLen  = 128
	maxEnvValueLn = 4096
)

// envKeyRE starts with a letter or underscore, so "KEY=value" never starts with
// a dash and the key holds no equals sign, comma, quote or whitespace.
var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NewMount is one host folder to share into a new VM.
type NewMount struct {
	Path     string
	Writable bool
}

// NewEnvVar is one environment variable for a new VM. The caller supplies the
// slice sorted by key, because an error on a bad key is named by its index.
type NewEnvVar struct {
	Key   string
	Value string
}

// mountPathProblem returns "" for an acceptable path, else a fixed message. The
// rules are the adapter's (internal/colima.mountPathProblem), spelled out:
// absolute and already clean, no ".." segment, no control character, valid
// UTF-8, no comma or double quote (Colima reads --mount as a CSV list, so a
// comma would add a mount), no colon (it starts the ":w" suffix) and no brace
// or dollar sign (template and environment expansion).
func mountPathProblem(p string) string {
	switch {
	case p == "":
		return "is required"
	case len(p) > maxMountPath:
		return fmt.Sprintf("must be at most %d bytes", maxMountPath)
	case !utf8.ValidString(p):
		return "must be valid UTF-8"
	case !path.IsAbs(p):
		return "must be an absolute path"
	case strings.ContainsAny(p, ",\":{}$"):
		return "must not contain a comma, double quote, colon, brace or dollar sign"
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f {
			return "must not contain a control character"
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "must not contain .. segments"
		}
	}
	if path.Clean(p) != p {
		return "must be a clean path: no repeated or trailing slash and no . segments"
	}
	return ""
}

// validateMountsEnv checks the syntax of the creation mounts and environment
// and returns field errors named with the creation body's keys:
// mounts, mounts[i].path, env, env[i] (a bad key, by index in key order) and
// env.KEY (a bad value; KEY has passed the key pattern, so it is safe to name).
// A count over the cap is the only error for its field, so no element is
// examined. It touches no file: see ResolveMounts for the host checks.
// Messages are fixed and never contain a path, key or value from the request.
func validateMountsEnv(mounts []NewMount, env []NewEnvVar) []FieldError {
	var errs []FieldError
	add := func(field, msg string) { errs = append(errs, FieldError{Field: field, Message: msg}) }

	if len(mounts) > maxNewMounts {
		add("mounts", fmt.Sprintf("must list at most %d mounts", maxNewMounts))
	} else {
		seen := make(map[string]bool, len(mounts))
		for i, m := range mounts {
			f := fmt.Sprintf("mounts[%d].path", i)
			if msg := mountPathProblem(m.Path); msg != "" {
				add(f, msg)
				continue
			}
			if m.Writable && m.Path == "/" {
				add(f, "the filesystem root cannot be mounted writable")
				continue
			}
			if seen[m.Path] {
				add(f, "repeats an earlier mount")
				continue
			}
			seen[m.Path] = true
		}
	}
	if len(env) > maxNewEnv {
		add("env", fmt.Sprintf("must list at most %d variables", maxNewEnv))
	} else {
		keys := make(map[string]bool, len(env))
		for i, e := range env {
			switch {
			case len(e.Key) > maxEnvKeyLen || !envKeyRE.MatchString(e.Key):
				add(fmt.Sprintf("env[%d]", i), fmt.Sprintf("name must be letters, digits and underscores, not starting with a digit, at most %d characters", maxEnvKeyLen))
			case keys[e.Key]:
				add(fmt.Sprintf("env[%d]", i), "repeats an earlier variable")
			case len(e.Value) > maxEnvValueLn:
				add("env."+e.Key, fmt.Sprintf("must be at most %d bytes", maxEnvValueLn))
			case strings.IndexByte(e.Value, 0) >= 0:
				add("env."+e.Key, "must not contain a NUL byte")
			case envValueUnsafe(e.Value):
				add("env."+e.Key, "must not end with a double quote, and must not combine = with a comma, double quote or line break")
			}
			keys[e.Key] = true
		}
	}
	return errs
}

// envValueUnsafe mirrors internal/colima.envValueUnsafe; see there for the
// pflag behaviour behind it.
func envValueUnsafe(v string) bool {
	if strings.HasSuffix(v, `"`) {
		return true
	}
	return strings.Contains(v, "=") && strings.ContainsAny(v, ",\"\r\n")
}

// HostFS is the part of the host filesystem the mount checks read. Tests pass
// a fake, so no test depends on the real home folder.
type HostFS interface {
	// Home returns the user's home folder.
	Home() (string, error)
	// EvalSymlinks resolves every symlink in p and returns a clean absolute path.
	EvalSymlinks(p string) (string, error)
	// IsDir reports whether p exists and is a directory (following symlinks).
	IsDir(p string) (bool, error)
	// SameFile reports whether a and b are the same file or folder by identity
	// (device and inode), which sees through APFS firmlinks, case and Unicode
	// normalisation differences and bind mounts that no string compare can.
	SameFile(a, b string) (bool, error)
}

// OSHostFS is HostFS over the real filesystem.
type OSHostFS struct{}

func (OSHostFS) Home() (string, error)                 { return os.UserHomeDir() }
func (OSHostFS) EvalSymlinks(p string) (string, error) { return filepath.EvalSymlinks(p) }
func (OSHostFS) IsDir(p string) (bool, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}

func (OSHostFS) SameFile(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(fa, fb), nil
}

// ResolveMounts checks the mounts that passed ValidateNew against the host and
// returns them with every path replaced by its symlink-resolved form, which is
// what must be handed to Colima.
//
// Symlinks are resolved on purpose, and the resolved path is both checked and
// mounted: a link that points at a broad folder cannot get past the "too
// broad" rule, two names for one folder count as a duplicate, and what was
// checked is what is mounted (a link swapped afterwards has no effect, though
// a swap of a real folder between check and start cannot be prevented here).
//
// Rules, per mount, on its resolved form:
//
//  1. It must exist and be a directory. A missing path, a file and a path that
//     cannot be read all get the same message.
//  2. A writable mount may not be "/" or the user's home folder or any folder
//     that contains the home folder (/Users on macOS), because each exposes
//     the whole home. A read-only mount of any of them is allowed, as the
//     issue decides.
//     This is decided by identity (HostFS.SameFile), not by comparing
//     strings, because strings miss aliases: APFS firmlinks
//     (/System/Volumes/Data/Users/me is /Users/me), case and Unicode
//     normalisation variants. The string comparison (case-insensitive, see
//     tooBroad) stays as a cheap first line.
//  3. Two mounts that are the same folder by identity are duplicates, so
//     case and normalisation variants of one folder are caught too.
//
// A failure to find the home folder rejects every writable mount (fail
// closed). Errors name mounts[i].path with fixed messages; neither the path
// nor the filesystem error text is kept. It does nothing for an entry whose
// path is syntactically bad, so run ValidateNew first.
func ResolveMounts(mounts []NewMount, host HostFS) ([]NewMount, []FieldError) {
	if len(mounts) == 0 {
		return nil, nil
	}
	var errs []FieldError
	add := func(i int, msg string) {
		errs = append(errs, FieldError{Field: fmt.Sprintf("mounts[%d].path", i), Message: msg})
	}
	var homes []string // home as reported and resolved
	if h, err := host.Home(); err == nil && h != "" && path.IsAbs(h) {
		homes = append(homes, path.Clean(h))
		if r, err := host.EvalSymlinks(h); err == nil && path.IsAbs(r) {
			homes = append(homes, path.Clean(r))
		}
	}
	out := make([]NewMount, len(mounts))
	var accepted []string // resolved paths of the mounts kept so far
	for i, m := range mounts {
		if mountPathProblem(m.Path) != "" {
			add(i, "must be an acceptable path")
			continue
		}
		res, err := host.EvalSymlinks(m.Path)
		if err != nil || !path.IsAbs(res) || mountPathProblem(res) != "" {
			// A resolved path the adapter would refuse (a comma in a link
			// target, say) is as unusable as a missing one.
			add(i, "must be an existing directory on this machine")
			continue
		}
		if ok, err := host.IsDir(res); err != nil || !ok {
			add(i, "must be an existing directory on this machine")
			continue
		}
		if m.Writable && (len(homes) == 0 || tooBroad(res, homes) || containsHome(host, res, homes)) {
			add(i, "is too broad to mount writable: the root, the home folder and the folders above it are allowed read-only only")
			continue
		}
		if sameAsAny(host, res, accepted) {
			add(i, "repeats an earlier mount")
			continue
		}
		accepted = append(accepted, res)
		out[i] = NewMount{Path: res, Writable: m.Writable}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// tooBroad reports whether p is "/" or equals or contains any of homes. The
// comparison ignores letter case: the default macOS filesystem is case
// insensitive and symlink resolution does not normalise case, so /users/me
// would otherwise slip past a check against /Users/me. On a case-sensitive
// filesystem this can refuse a lookalike folder, which is the safe side.
func tooBroad(p string, homes []string) bool {
	if p == "/" {
		return true
	}
	p = strings.ToLower(p)
	for _, h := range homes {
		h = strings.ToLower(h)
		if h == p || strings.HasPrefix(h, p+"/") {
			return true
		}
	}
	return false
}

// sameAsAny reports whether p is the same folder as any of paths, by string
// or by identity.
func sameAsAny(host HostFS, p string, paths []string) bool {
	for _, q := range paths {
		if q == p {
			return true
		}
		if ok, err := host.SameFile(p, q); err == nil && ok {
			return true
		}
	}
	return false
}

// containsHome reports whether the home folder is res or lies inside it, by
// identity. For every lexical ancestor A of home (including home itself and
// "/") it asks whether res plus the rest of home's path, below A, is home:
// for res = /System/Volumes/Data and home = /Users/me that is
// /System/Volumes/Data/Users/me, which the filesystem answers with home's own
// identity although no string says so. A lookup that fails counts as "no".
func containsHome(host HostFS, res string, homes []string) bool {
	for _, h := range homes {
		for a := h; ; a = path.Dir(a) {
			suffix := strings.TrimPrefix(h, a)
			if a == "/" {
				suffix = h
			}
			if ok, err := host.SameFile(path.Join(res, suffix), h); err == nil && ok {
				return true
			}
			if a == "/" {
				break
			}
		}
	}
	return false
}
