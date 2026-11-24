package colima

import (
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Mount is one --mount entry: a host folder shared into the VM.
type Mount struct {
	Path     string
	Writable bool // path:w when true
}

// EnvVar is one --env KEY=value entry.
type EnvVar struct {
	Key   string
	Value string
}

// Bounds for mounts and environment variables. They mirror internal/config (a
// test pins the two together).
const (
	maxMounts     = 16
	maxMountPath  = 4096
	maxEnv        = 32
	maxEnvKeyLen  = 128
	maxEnvValueLn = 4096
)

// envKeyRE starts with a letter or underscore, so "KEY=value" never starts with
// a dash and the key can hold no equals sign, comma, quote or whitespace.
var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// mountPathProblem reports why p cannot be a mount path, or "" when it can.
// Rules, each for a stated reason:
//
//   - absolute, already clean (no "..", ".", "//" or trailing slash): the path
//     goes to Colima unchanged, so what was checked is what is mounted;
//   - no control character (NUL, newline and so on) and valid UTF-8;
//   - no comma or double quote: Colima reads --mount as a comma separated,
//     CSV-quoted list (probed with `colima start --mount ... --help`), so a
//     comma would add a second mount and a quote is a parse error;
//   - no brace or dollar sign: they start template and environment expansion
//     in the tools Colima hands the value to;
//   - no colon: a colon starts the ":w" suffix, so it would be ambiguous;
//   - at most 4096 bytes.
func mountPathProblem(p string) string {
	switch {
	case p == "":
		return "empty"
	case len(p) > maxMountPath:
		return "too long"
	case !utf8.ValidString(p):
		return "not UTF-8"
	case !path.IsAbs(p):
		return "not absolute"
	case strings.ContainsAny(p, ",\":{}$"):
		return "contains a comma, quote, colon, brace or dollar sign"
	case path.Clean(p) != p:
		return "not clean"
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f {
			return "control character"
		}
	}
	return ""
}

// envValueUnsafe reports a value that Colima's --env parser (pflag
// stringToString, probed with `colima start --env ... --help` on 0.10.3)
// would not read back as written. With exactly one "=" in KEY=value (the key
// has none, so none in the value) pflag only trims double quotes from both
// ends of the whole argument, so a value ending in a quote loses it. With
// two or more it CSV-parses the argument, so a comma, a double quote or a line
// break in the value can split off or drop further variables.
func envValueUnsafe(v string) bool {
	if strings.HasSuffix(v, `"`) {
		return true
	}
	return strings.Contains(v, "=") && strings.ContainsAny(v, ",\"\r\n")
}

func (o StartOptions) validateMountsEnv() error {
	if len(o.Mounts) > maxMounts {
		return bad("mount", "too many mounts")
	}
	seen := make(map[string]bool, len(o.Mounts))
	for _, m := range o.Mounts {
		if mountPathProblem(m.Path) != "" {
			return bad("mount", "not an acceptable path")
		}
		if m.Writable && m.Path == "/" {
			return bad("mount", "the filesystem root cannot be writable")
		}
		if seen[m.Path] {
			return bad("mount", "duplicate")
		}
		seen[m.Path] = true
	}
	if len(o.Env) > maxEnv {
		return bad("env", "too many variables")
	}
	keys := make(map[string]bool, len(o.Env))
	for _, e := range o.Env {
		if len(e.Key) > maxEnvKeyLen || !envKeyRE.MatchString(e.Key) {
			return bad("env", "not a variable name")
		}
		if len(e.Value) > maxEnvValueLn || strings.IndexByte(e.Value, 0) >= 0 || envValueUnsafe(e.Value) {
			return bad("env", "not an acceptable value")
		}
		if keys[e.Key] {
			return bad("env", "duplicate")
		}
		keys[e.Key] = true
	}
	return nil
}

// mountEnvArgs renders --mount and --env: one flag and one argv element per
// entry, never a joined string. The caller has validated the options.
func (o StartOptions) mountEnvArgs() []string {
	var a []string
	for _, m := range o.Mounts {
		v := m.Path
		if m.Writable {
			v += ":w"
		}
		a = append(a, "--mount", v)
	}
	for _, e := range o.Env {
		a = append(a, "--env", e.Key+"="+e.Value)
	}
	return a
}
