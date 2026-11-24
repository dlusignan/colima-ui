package dockerc

import (
	"errors"
	"strings"
	"testing"
)

func TestValidObjectName(t *testing.T) {
	good := []string{"a", "A", "0", "pgdata", "my_vol-1.x", strings.Repeat("a", 128)}
	bad := []string{
		"", "../x", "..", ".", ".hidden", "-x", "_x", "a/b", "/a", "a/", `a\b`, "%2e%2e", "a%2fb", "a%00",
		"a\nb", "a\r", "a\x00b", "a b", " a", "a ", "a:b", "a@b", "a?b", "a#b", "é", "a‮b",
		strings.Repeat("a", 129), "a\x7f",
	}
	for _, s := range good {
		if !ValidObjectName(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range bad {
		if ValidObjectName(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestValidImageRef(t *testing.T) {
	dig := "sha256:" + strings.Repeat("a", 64)
	good := []string{
		"nginx", "nginx:1.27", "library/nginx", "library/nginx:latest", "synthetic/web:1",
		"registry.example.com/team/app:v1", "registry.example.com:5000/team/app", "localhost/app",
		"localhost:5000/app:tag", "nginx@" + dig, "nginx:1.27@" + dig, "a.b/c__d/e-f.g", "Example.COM/app",
		"app:_tag", strings.Repeat("a", 255),
	}
	bad := []string{
		"", " ", "..", "../x", "a/../b", "a//b", "/a", "a/", ":tag", "nginx:", "nginx::x", "nginx:a b",
		"Nginx", "library/Nginx", "nginx\n", "nginx:1\n", "nginx:tag/extra", "a/./b", "a/b/", "nginx@",
		"nginx@sha256:short", "nginx@" + dig + "x!", "ngin%78", "a?b", "a#b", "a\\b", "-a", "a-", "a--",
		"a..b", "a._b", "http://a/b", "a/b:c:d", "user@host/app", "nginx:" + strings.Repeat("a", 129),
		strings.Repeat("a", 256), "host:99999999/app", "ho st/app", "-host.com/app", "a\x00b",
	}
	for _, s := range good {
		if !ValidImageRef(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range bad {
		if ValidImageRef(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestObjectPathAndRefPath(t *testing.T) {
	p, err := ObjectPath("/networks", "net-1")
	if err != nil || p != "/networks/net-1" {
		t.Fatalf("%q %v", p, err)
	}
	p, err = RefPath("/images", "registry.example.com:5000/team/app:v1")
	if err != nil || p != "/images/registry.example.com:5000/team/app:v1" {
		t.Fatalf("%q %v", p, err)
	}
	for _, s := range []string{"../x", "a/b", "%2e%2e", "a\nb", strings.Repeat("a", 129), ""} {
		if _, err := ObjectPath("/volumes", s); !errors.Is(err, ErrBadName) {
			t.Errorf("ObjectPath %q: %v", s, err)
		}
	}
	for _, s := range []string{"../x", "a//b", "a/../b", "%2e%2e", "a\nb", ""} {
		if _, err := RefPath("/images", s); !errors.Is(err, ErrBadName) {
			t.Errorf("RefPath %q: %v", s, err)
		}
	}
	// Every path the builders return passes the write helper's own check.
	for _, ref := range []string{"nginx", "a/b:c", "h.io:1/a@sha256:" + strings.Repeat("0", 32)} {
		p, err := RefPath("/images", ref)
		if err != nil || !safeURLPath(p) {
			t.Errorf("RefPath %q = %q, %v", ref, p, err)
		}
	}
}
