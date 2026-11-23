package colima

import "regexp"

// DefaultProfile is the name of Colima's default profile.
const DefaultProfile = "default"

// MaxProfileLength caps profile names. Colima derives a unix socket path from
// the name and macOS limits those paths to about 104 bytes.
const MaxProfileLength = 64

// The first character must be alphanumeric, so a valid name can never look
// like a command-line flag.
var profilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ValidateProfile checks name against the allowed pattern and length. It must
// be called before a name is used as an argument.
func ValidateProfile(name string) error {
	switch {
	case name == "":
		return &InvalidProfileError{Name: name, Reason: "empty"}
	case len(name) > MaxProfileLength:
		return &InvalidProfileError{Name: name[:MaxProfileLength] + "...", Reason: "too long"}
	case !profilePattern.MatchString(name):
		return &InvalidProfileError{Name: name, Reason: "must match ^[A-Za-z0-9][A-Za-z0-9_.-]*$"}
	}
	return nil
}

// LimaInstanceName maps a Colima profile to its Lima instance name. The
// default profile maps to "colima"; others map to "colima-<profile>".
func LimaInstanceName(profile string) string {
	if profile == DefaultProfile {
		return "colima"
	}
	return "colima-" + profile
}
