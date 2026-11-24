package dockerc

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxLabels is the most labels one created volume may carry.
	MaxLabels = 16
	// MaxLabelKeyLen and MaxLabelValueLen bound a label in bytes.
	MaxLabelKeyLen   = 63
	MaxLabelValueLen = 256
)

var labelKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// reservedLabelPrefixes belong to Docker and Compose tooling; a user label
// there could impersonate a managed object.
var reservedLabelPrefixes = []string{"com.docker.", "io.docker.", "org.dockerproject."}

// ValidLabelKey reports whether key is an allowed label key: 1 to 63 bytes of
// [a-zA-Z0-9_.-] starting with an alphanumeric, outside the prefixes Docker
// reserves for itself (compared without regard to case).
func ValidLabelKey(key string) bool {
	if len(key) > MaxLabelKeyLen || !labelKeyPattern.MatchString(key) {
		return false
	}
	lower := strings.ToLower(key)
	for _, p := range reservedLabelPrefixes {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	return true
}

// ValidLabelValue reports whether v is an allowed label value: valid UTF-8,
// at most 256 bytes, no control characters and no Unicode format characters (category Cf: zero
// width and bidirectional controls, which hide or reorder text). It may be empty.
func ValidLabelValue(v string) bool {
	if len(v) > MaxLabelValueLen || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// ValidLabels reports whether labels is acceptable for a new volume: at most
// MaxLabels entries, every key and value valid, and no two keys equal when
// compared without regard to case (Docker keeps them distinct, but two labels
// that differ only in case are almost always a mistake).
func ValidLabels(labels map[string]string) bool {
	if len(labels) > MaxLabels {
		return false
	}
	seen := make(map[string]bool, len(labels))
	for k, v := range labels {
		if !ValidLabelKey(k) || !ValidLabelValue(v) {
			return false
		}
		l := strings.ToLower(k)
		if seen[l] {
			return false
		}
		seen[l] = true
	}
	return true
}
