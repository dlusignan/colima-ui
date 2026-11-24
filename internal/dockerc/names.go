package dockerc

import (
	"net/url"
	"regexp"
	"strings"
)

const (
	// MaxNameLen is the longest Docker object name (volume or network) the
	// API accepts.
	MaxNameLen = 128
	// MaxRefLen is the longest image reference the API accepts. It is
	// Docker's own limit for a repository name plus room for a tag or digest.
	MaxRefLen = 255
)

// ValidObjectName reports whether name is a Docker object name:
// [a-zA-Z0-9][a-zA-Z0-9_.-]* up to 128 characters. That excludes the empty
// string, slashes, dots at the start (so "." and ".."), percent signs, spaces
// and every control character, so a valid name is always one safe URL path
// segment. Use it for path parameters as well as for body values.
func ValidObjectName(name string) bool {
	return len(name) <= MaxNameLen && idPattern.MatchString(name)
}

var (
	refComponent = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
	refDomain    = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)*(?::[0-9]{1,5})?$`)
	refTag       = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)
	refDigest    = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*(?:[-_+.][a-zA-Z][a-zA-Z0-9]*)*:[0-9a-fA-F]{32,}$`)
)

// ValidImageRef reports whether ref is a well-formed image reference in the
// grammar Docker uses: [domain[:port]/]path[/path]*[:tag][@digest], at most
// 255 bytes. Path components are lowercase, separated by one slash, and use
// single separators ("." "_" "__" or dashes) between alphanumerics, so ".."
// and empty components never match. A reference is not a name: it may contain
// "/", ":" and "@", and RefPath escapes it for a URL. It does not check that
// the registry or repository exists.
func ValidImageRef(ref string) bool {
	if ref == "" || len(ref) > MaxRefLen {
		return false
	}
	rest := ref
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		if !refDigest.MatchString(rest[i+1:]) {
			return false
		}
		rest = rest[:i]
	}
	// A tag is a colon after the last slash; a colon before it is a port.
	if i := strings.LastIndexByte(rest, ':'); i > strings.LastIndexByte(rest, '/') {
		if !refTag.MatchString(rest[i+1:]) {
			return false
		}
		rest = rest[:i]
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 1 {
		d := parts[0]
		if strings.ContainsAny(d, ".:") || d == "localhost" || d != strings.ToLower(d) {
			if !refDomain.MatchString(d) {
				return false
			}
			parts = parts[1:]
		}
	}
	for _, c := range parts {
		if !refComponent.MatchString(c) {
			return false
		}
	}
	return true
}

// ObjectPath returns root + "/" + the escaped name, for example
// ObjectPath("/volumes", "pgdata"). It fails with ErrBadName unless name passes
// ValidObjectName. Write methods build every URL path with it.
func ObjectPath(root, name string) (string, error) {
	if !ValidObjectName(name) {
		return "", ErrBadName
	}
	return root + "/" + url.PathEscape(name), nil
}

// RefPath is ObjectPath for an image reference: each slash-separated part is
// escaped on its own, so the slashes of the reference stay path separators
// (Docker expects them raw) and nothing else can. It fails with ErrBadName
// unless ref passes ValidImageRef.
func RefPath(root, ref string) (string, error) {
	if !ValidImageRef(ref) {
		return "", ErrBadName
	}
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return root + "/" + strings.Join(parts, "/"), nil
}
