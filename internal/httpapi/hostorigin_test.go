package httpapi

import (
	"net/http"
	"testing"
)

func TestHostRules(t *testing.T) {
	cases := []struct {
		name string
		host string
		want int
	}{
		{"127.0.0.1 with port", "127.0.0.1:8787", 200},
		{"localhost with port", "localhost:8787", 200},
		{"localhost uppercase", "LOCALHOST:8787", 200},
		{"empty host", "", 403},
		{"dns rebinding host", "evil.example:8787", 403},
		{"dns rebinding no port", "evil.example", 403},
		{"localhost trailing dot", "localhost.:8787", 403},
		{"127.0.0.1 trailing dot", "127.0.0.1.:8787", 403},
		{"missing port 127", "127.0.0.1", 403},
		{"missing port localhost", "localhost", 403},
		{"empty port", "localhost:", 403},
		{"wrong port", "127.0.0.1:8788", 403},
		{"wrong port localhost", "localhost:80", 403},
		{"port with leading zero", "localhost:08787", 403},
		{"other loopback 127.0.0.2", "127.0.0.2:8787", 403},
		{"ipv6 loopback", "[::1]:8787", 403},
		{"ipv6 loopback no port", "[::1]", 403},
		{"ipv4-mapped ipv6", "[::ffff:127.0.0.1]:8787", 403},
		{"zero address", "0.0.0.0:8787", 403},
		{"decimal ip", "2130706433:8787", 403},
		{"hex ip", "0x7f.0.0.1:8787", 403},
		{"prefix attack", "localhost.evil.example:8787", 403},
		{"suffix attack", "evil.example.localhost:8787", 403},
		{"suffix attack 127", "evil127.0.0.1:8787", 403},
		{"userinfo", "localhost:8787@evil.example", 403},
		{"userinfo 2", "evil.example@localhost:8787", 403},
		{"embedded port", "evil.example:8787:8787", 403},
		{"leading space", " localhost:8787", 403},
		{"trailing space", "localhost:8787 ", 403},
		{"trailing slash", "localhost:8787/", 403},
		{"comma list", "localhost:8787,evil.example", 403},
		{"nul byte", "localhost\x00.evil.example:8787", 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, p := range []string{"/healthz", "/api/ping"} {
				h := bearer("Bearer " + testToken)
				rec, _ := do(t, "GET", p, c.host, h)
				if rec.Code != c.want {
					t.Fatalf("%s host %q: status %d, want %d", p, c.host, rec.Code, c.want)
				}
			}
		})
	}
}

func TestHostHeaderIgnoredForAbsoluteURL(t *testing.T) {
	// r.Host is what the check uses; X-Forwarded-Host and friends are not
	// consulted.
	h := http.Header{"X-Forwarded-Host": {"localhost:8787"}}
	if rec, _ := do(t, "GET", "/healthz", "evil.example:8787", h); rec.Code != 403 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestOriginRules(t *testing.T) {
	cases := []struct {
		name    string
		origins []string // nil = header absent
		want    int
	}{
		{"absent", nil, 200},
		{"own 127", []string{"http://127.0.0.1:8787"}, 200},
		{"own localhost", []string{"http://localhost:8787"}, 200},
		{"foreign", []string{"http://evil.example"}, 403},
		{"foreign with port", []string{"http://evil.example:8787"}, 403},
		{"null", []string{"null"}, 403},
		{"empty value", []string{""}, 403},
		{"https scheme", []string{"https://localhost:8787"}, 403},
		{"wrong port", []string{"http://localhost:8788"}, 403},
		{"no port", []string{"http://localhost"}, 403},
		{"trailing dot", []string{"http://localhost.:8787"}, 403},
		{"trailing slash", []string{"http://localhost:8787/"}, 403},
		{"ipv6", []string{"http://[::1]:8787"}, 403},
		{"other loopback", []string{"http://127.0.0.2:8787"}, 403},
		{"prefix attack", []string{"http://localhost:8787.evil.example"}, 403},
		{"userinfo", []string{"http://localhost:8787@evil.example"}, 403},
		{"file", []string{"file://"}, 403},
		{"repeated same valid", []string{"http://localhost:8787", "http://localhost:8787"}, 403},
		{"repeated valid then foreign", []string{"http://localhost:8787", "http://evil.example"}, 403},
		{"repeated foreign then valid", []string{"http://evil.example", "http://localhost:8787"}, 403},
		{"comma separated", []string{"http://localhost:8787, http://evil.example"}, 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, p := range []string{"/healthz", "/api/ping"} {
				h := bearer("Bearer " + testToken)
				if c.origins != nil {
					h["Origin"] = c.origins
				}
				rec, _ := do(t, "GET", p, goodHost, h)
				if rec.Code != c.want {
					t.Fatalf("%s origin %q: status %d, want %d", p, c.origins, rec.Code, c.want)
				}
			}
		})
	}
}

func TestOriginAbsentWithoutTokenIs401(t *testing.T) {
	rec, hit := do(t, "GET", "/api/ping", goodHost, nil)
	if rec.Code != 401 || hit {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestRejectionBodyFixed(t *testing.T) {
	rec, _ := do(t, "GET", "/api/ping", "evil.example:8787", http.Header{"Authorization": {"Bearer " + testToken}})
	want := `{"error":{"code":"forbidden_host","message":"Host not allowed.","details":{}}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q", got)
	}
}
