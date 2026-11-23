package httpapi

import (
	"bytes"
	"encoding/base64"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testPort  = 8787
	testToken = "s3cr3t-token-value"
	goodHost  = "127.0.0.1:8787"
)

// stub records whether the protected handler ran.
func stub(hit *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hit = true
		w.WriteHeader(http.StatusOK)
	})
}

func do(t *testing.T, method, target, host string, hdr http.Header) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	var hit bool
	h := NewHandler(testPort, testToken, stub(&hit))
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, hit
}

func bearer(v string) http.Header { return http.Header{"Authorization": {v}} }

func TestTokenRules(t *testing.T) {
	long := strings.Repeat("a", 100000)
	cases := []struct {
		name string
		hdr  http.Header
		want int
	}{
		{"valid", bearer("Bearer " + testToken), 200},
		{"valid lowercase scheme", bearer("bearer " + testToken), 200},
		{"valid uppercase scheme", bearer("BEARER " + testToken), 200},
		{"no token", nil, 401},
		{"wrong token", bearer("Bearer nope"), 401},
		{"token prefix", bearer("Bearer " + testToken[:5]), 401},
		{"token plus suffix", bearer("Bearer " + testToken + "x"), 401},
		{"much longer token", bearer("Bearer " + long), 401},
		{"empty bearer value", bearer("Bearer "), 401},
		{"bearer no space", bearer("Bearer"), 401},
		{"empty header", bearer(""), 401},
		{"wrong scheme basic", bearer("Basic " + testToken), 401},
		{"no scheme", bearer(testToken), 401},
		{"double space", bearer("Bearer  " + testToken), 401},
		{"two auth headers, good first", http.Header{"Authorization": {"Bearer " + testToken, "Bearer x"}}, 401},
		{"two auth headers, good second", http.Header{"Authorization": {"Bearer x", "Bearer " + testToken}}, 401},
		{"two identical good headers", http.Header{"Authorization": {"Bearer " + testToken, "Bearer " + testToken}}, 401},
	}
	for _, p := range []string{"/api", "/api/", "/api/ping", "/api/x", "/api/x/y"} {
		for _, c := range cases {
			t.Run(p+" "+c.name, func(t *testing.T) {
				rec, hit := do(t, "GET", p, goodHost, c.hdr)
				if rec.Code != c.want {
					t.Fatalf("status = %d, want %d", rec.Code, c.want)
				}
				if hit != (c.want == 200) {
					t.Fatalf("protected handler hit = %v", hit)
				}
				if c.want == 401 && rec.Header().Get("WWW-Authenticate") != "Bearer" {
					t.Fatalf("missing WWW-Authenticate")
				}
				if strings.Contains(rec.Body.String(), testToken) || strings.Contains(rec.Header().Get("WWW-Authenticate"), testToken) {
					t.Fatal("response leaks token")
				}
			})
		}
	}
}

func TestTokenInQueryRejected(t *testing.T) {
	for _, q := range []string{"token=" + testToken, "access_token=" + testToken, "Token=" + testToken} {
		rec, hit := do(t, "GET", "/api/ping?"+q, goodHost, nil)
		if rec.Code != 401 || hit {
			t.Fatalf("%s: status %d hit %v", q, rec.Code, hit)
		}
	}
}

func TestTokenInOtherChannelsRejected(t *testing.T) {
	for _, h := range []http.Header{
		{"X-Token": {testToken}},
		{"Cookie": {"token=" + testToken}},
		{"Authorization": {"Token " + testToken}},
	} {
		if rec, hit := do(t, "GET", "/api/ping", goodHost, h); rec.Code != 401 || hit {
			t.Fatalf("%v: status %d", h, rec.Code)
		}
	}
}

func TestAllMethodsNeedToken(t *testing.T) {
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"} {
		if rec, hit := do(t, m, "/api/ping", goodHost, nil); rec.Code != 401 || hit {
			t.Fatalf("%s: status %d hit %v", m, rec.Code, hit)
		}
	}
}

func TestHealthzNeedsHostNotToken(t *testing.T) {
	rec, _ := do(t, "GET", "/healthz", goodHost, nil)
	if rec.Code != 200 {
		t.Fatalf("no token: %d", rec.Code)
	}
	rec, _ = do(t, "GET", "/healthz", "evil.example:8787", nil)
	if rec.Code != 403 {
		t.Fatalf("bad host: %d", rec.Code)
	}
	rec, _ = do(t, "GET", "/healthz", goodHost, http.Header{"Origin": {"http://evil.example"}})
	if rec.Code != 403 {
		t.Fatalf("bad origin: %d", rec.Code)
	}
}

func TestOtherRoutesNotOpen(t *testing.T) {
	for _, p := range []string{"/", "/healthz/", "/apix", "/API/ping", "/other"} {
		rec, hit := do(t, "GET", p, goodHost, nil)
		if rec.Code == 200 || hit {
			t.Fatalf("%s: status %d hit %v", p, rec.Code, hit)
		}
	}
}

func TestTokenCheckedAfterHostOrigin(t *testing.T) {
	// A valid token does not rescue a bad Host or Origin.
	rec, hit := do(t, "GET", "/api/ping", "evil.example:8787", bearer("Bearer "+testToken))
	if rec.Code != 403 || hit {
		t.Fatalf("bad host valid token: %d", rec.Code)
	}
	h := bearer("Bearer " + testToken)
	h["Origin"] = []string{"http://evil.example"}
	rec, hit = do(t, "GET", "/api/ping", goodHost, h)
	if rec.Code != 403 || hit {
		t.Fatalf("bad origin valid token: %d", rec.Code)
	}
	// Bad Host with no token is 403, not 401.
	rec, _ = do(t, "GET", "/api/ping", "evil.example:8787", nil)
	if rec.Code != 403 {
		t.Fatalf("bad host no token: %d", rec.Code)
	}
}

func TestNewToken(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if a == b {
		t.Fatal("tokens repeat")
	}
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 16 {
		t.Fatalf("token has %d bytes, need >= 16", len(raw))
	}
	if len(raw) != 32 {
		t.Fatalf("token has %d bytes, want 32", len(raw))
	}
}

func TestTokenNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	oldW, oldF := log.Writer(), log.Flags()
	oldSlog := slog.Default()
	log.SetOutput(&buf) // also redirects slog's default handler
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer func() {
		log.SetOutput(oldW)
		log.SetFlags(oldF)
		slog.SetDefault(oldSlog)
	}()

	cases := []struct {
		host string
		hdr  http.Header
	}{
		{goodHost, bearer("Bearer " + testToken)},
		{goodHost, bearer("Bearer wrong")},
		{goodHost, nil},
		{"evil.example:8787", bearer("Bearer " + testToken)},
		{goodHost, http.Header{"Origin": {"http://evil.example"}, "Authorization": {"Bearer " + testToken}}},
	}
	for _, c := range cases {
		_, _ = do(t, "GET", "/api/ping?token="+testToken, c.host, c.hdr)
	}
	// Also over a real listener.
	s := NewServer(testToken, NewPlaceholderAPI())
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/api/ping", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("real listener status %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	_ = s.Shutdown(t.Context())
	<-done

	if strings.Contains(buf.String(), testToken) {
		t.Fatalf("token found in log output: %q", buf.String())
	}
}

func TestPlaceholderAPIOverRealListener(t *testing.T) {
	s := NewServer(testToken, NewPlaceholderAPI())
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	defer func() { _ = s.Shutdown(t.Context()); <-done }()
	base := "http://" + ln.Addr().String()

	resp, err := http.Get(base + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", base+"/api/ping", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("with token: %d", resp.StatusCode)
	}
	// localhost resolves to the same listener and is an allowed Host.
	_, port, _ := strings.Cut(ln.Addr().String(), ":")
	req, _ = http.NewRequest("GET", base+"/api/ping", nil)
	req.Host = "localhost:" + port
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("localhost host: %d", resp.StatusCode)
	}
}
