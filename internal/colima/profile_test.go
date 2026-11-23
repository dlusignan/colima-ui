package colima

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateProfile(t *testing.T) {
	valid := []string{"default", "a", "A1", "synthetic-a", "dev_1.2", "0abc", strings.Repeat("a", 64)}
	for _, n := range valid {
		if err := ValidateProfile(n); err != nil {
			t.Errorf("ValidateProfile(%q) = %v, want nil", n, err)
		}
	}
	invalid := []string{
		"", "-x", "--help", "_a", ".a", "a b", "a/b", "a;b", "a\nb", "a$(x)", "a`x`",
		"../x", "é", "a\x00b", strings.Repeat("a", 65),
	}
	for _, n := range invalid {
		err := ValidateProfile(n)
		var perr *InvalidProfileError
		if !errors.As(err, &perr) {
			t.Errorf("ValidateProfile(%q) = %v, want *InvalidProfileError", n, err)
		}
	}
}

func TestInvalidProfileErrorDoesNotEchoControlBytes(t *testing.T) {
	err := ValidateProfile("a\nb")
	if strings.ContainsAny(err.Error(), "\n\x00") {
		t.Errorf("error text contains raw control bytes: %q", err.Error())
	}
}

func TestLimaInstanceName(t *testing.T) {
	if got := LimaInstanceName("default"); got != "colima" {
		t.Errorf("default -> %q, want colima", got)
	}
	if got := LimaInstanceName("synthetic-a"); got != "colima-synthetic-a" {
		t.Errorf("got %q", got)
	}
}
