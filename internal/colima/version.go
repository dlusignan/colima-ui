package colima

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// VersionInfo is the installed Colima version. Raw is the complete output of
// `colima version`. Number is the token after "colima version". The numeric
// fields are meaningful only when Semver is true, because a development build
// can print something that is not semantic versioning.
type VersionInfo struct {
	Raw        string
	Number     string
	Major      int
	Minor      int
	Patch      int
	Prerelease string
	Semver     bool
}

var (
	versionLineRe = regexp.MustCompile(`^colima version (\S+)`)
	semverRe      = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)
)

// Version runs `colima version` and parses only its first line.
func Version(ctx context.Context, r Runner) (VersionInfo, error) {
	res, err := run(ctx, r, []string{"version"})
	if err != nil {
		return VersionInfo{}, err
	}
	raw := string(res.Stdout)
	first, _, _ := strings.Cut(raw, "\n")
	m := versionLineRe.FindStringSubmatch(strings.TrimRight(first, "\r"))
	if m == nil {
		return VersionInfo{}, &ParseError{Op: "version", Raw: raw, Err: errors.New("first line is not 'colima version X'")}
	}
	v := VersionInfo{Raw: raw, Number: m[1]}
	if s := semverRe.FindStringSubmatch(m[1]); s != nil {
		v.Major, _ = strconv.Atoi(s[1])
		v.Minor, _ = strconv.Atoi(s[2])
		v.Patch, _ = strconv.Atoi(s[3])
		v.Prerelease = s[4]
		v.Semver = true
	}
	return v, nil
}
