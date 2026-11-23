package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type wireError struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// decodeErr decodes rec as the contract error shape and fails if the body has
// any other shape (flat string, missing details, extra keys).
func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) wireError {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || len(raw) != 1 {
		t.Fatalf("body %q is not a single-key object: %v", rec.Body.String(), err)
	}
	var w wireError
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		t.Fatalf("body %q is not the contract shape: %v", rec.Body.String(), err)
	}
	if w.Error.Code == "" || w.Error.Message == "" || w.Error.Details == nil {
		t.Fatalf("body %q misses code, message or details", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
	return w
}

func TestErrorShapeOnEveryLayer(t *testing.T) {
	ui := fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}
	cases := []struct {
		name   string
		method string
		target string
		host   string
		hdr    http.Header
		status int
		code   string
	}{
		{"host guard", "GET", "/api/ping", "evil.example:8787", bearer("Bearer " + testToken), 403, "forbidden_host"},
		{"origin guard", "GET", "/api/ping", goodHost, http.Header{"Origin": {"http://evil.example"}, "Authorization": {"Bearer " + testToken}}, 403, "forbidden_origin"},
		{"token check", "GET", "/api/ping", goodHost, nil, 401, "unauthorized"},
		{"static 405", "POST", "/", goodHost, nil, 405, "method_not_allowed"},
		{"static 404", "GET", "/missing.js", goodHost, nil, 404, "not_found"},
		{"router 404", "GET", "/api/nope", goodHost, bearer("Bearer " + testToken), 404, "not_found"},
		{"router 405", "POST", "/api/ping", goodHost, bearer("Bearer " + testToken), 405, "method_not_allowed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := NewHandler(testPort, testToken, testAPI(), ui)
			req := httptest.NewRequest(c.method, c.target, nil)
			req.Host = c.host
			for k, v := range c.hdr {
				req.Header[k] = v
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.status {
				t.Fatalf("status %d, want %d (%s)", rec.Code, c.status, rec.Body.String())
			}
			if got := decodeErr(t, rec).Error.Code; got != c.code {
				t.Fatalf("code %q, want %q", got, c.code)
			}
		})
	}
}

func TestRouterFallbacksKeepAllow(t *testing.T) {
	api := NewAPI(func(rt *Router) {
		rt.Handle("GET /api/a", func(http.ResponseWriter, *http.Request) error { return nil })
		rt.Handle("POST /api/a", func(http.ResponseWriter, *http.Request) error { return nil })
	})
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/a", nil))
	if rec.Code != 405 {
		t.Fatalf("status %d", rec.Code)
	}
	allow := rec.Header().Get("Allow")
	if !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Fatalf("Allow %q", allow)
	}
	if strings.Contains(rec.Body.String(), "Method Not Allowed") {
		t.Fatalf("plain-text body leaked: %q", rec.Body.String())
	}
	decodeErr(t, rec)

	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/zzz", nil))
	if rec.Code != 404 || rec.Header().Get("Allow") != "" {
		t.Fatalf("status %d allow %q", rec.Code, rec.Header().Get("Allow"))
	}
	decodeErr(t, rec)
}

func TestRouterPanics(t *testing.T) {
	ok := func(http.ResponseWriter, *http.Request) error { return nil }
	for _, p := range []string{"/healthz", "GET /healthz", "/", "GET /", "/apix", "/api", "GET /other/api/x", "example.com/api/x", "/API/x"} {
		t.Run("outside "+p, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("Handle(%q) did not panic", p)
				}
			}()
			NewAPI(func(rt *Router) { rt.Handle(p, ok) })
		})
	}
	t.Run("duplicate", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("duplicate route did not panic")
			}
		}()
		NewAPI(
			func(rt *Router) { rt.Handle("GET /api/x", ok) },
			func(rt *Router) { rt.Handle("GET /api/x", ok) },
		)
	})
	t.Run("inside is fine", func(t *testing.T) {
		NewAPI(func(rt *Router) { rt.Handle("GET /api/x/{id}", ok); rt.Handle("/api/y/", ok) })
	})
}

func TestAPIHandlerErrors(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)

	api := NewAPI(func(rt *Router) {
		rt.Handle("GET /api/typed", func(http.ResponseWriter, *http.Request) error {
			return &APIError{Status: 409, Code: "profile_busy", Message: "busy", Details: map[string]any{"jobId": "j_1"}}
		})
		rt.Handle("GET /api/wrapped", func(http.ResponseWriter, *http.Request) error {
			return errors.Join(errors.New("x"), &APIError{Status: 400, Code: "bad_request", Message: "m"})
		})
		rt.Handle("GET /api/plain", func(http.ResponseWriter, *http.Request) error {
			return errors.New("open /home/user/secret: permission denied")
		})
		rt.Handle("GET /api/ok", func(w http.ResponseWriter, _ *http.Request) error {
			writeJSON(w, 200, map[string]int{"a": 1})
			return nil
		})
	})
	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		return rec
	}
	rec := get("/api/typed")
	if w := decodeErr(t, rec); rec.Code != 409 || w.Error.Details["jobId"] != "j_1" {
		t.Fatalf("typed: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/api/wrapped"); rec.Code != 400 {
		t.Fatalf("wrapped: %d", rec.Code)
	}
	rec = get("/api/plain")
	if w := decodeErr(t, rec); rec.Code != 500 || w.Error.Code != "internal_error" {
		t.Fatalf("plain: %d %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "/home") {
		t.Fatalf("error text leaked: %q", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "permission denied") {
		t.Fatalf("real error not logged: %q", logs.String())
	}
	rec = get("/api/ok")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("ok: %d %v", rec.Code, rec.Header())
	}
}

func TestNilDetailsEncodeAsObject(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAPIError(rec, &APIError{Status: 400, Code: "bad_request", Message: "m"})
	if !strings.Contains(rec.Body.String(), `"details":{}`) {
		t.Fatalf("body %q", rec.Body.String())
	}
}

type body struct {
	A int    `json:"a"`
	B string `json:"b"`
}

func decodeCase(t *testing.T, raw string, limit ...int64) error {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	var b body
	return decodeBody(rec, req, &b, limit...)
}

func TestDecodeBody(t *testing.T) {
	if err := decodeCase(t, `{"a":1,"b":"x"}`); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := decodeCase(t, " {\"a\":1}\n"); err != nil {
		t.Fatalf("whitespace: %v", err)
	}
	bad := map[string]string{
		"unknown field": `{"a":1,"zzz":2}`,
		"trailing":      `{"a":1} {"a":2}`,
		"trailing junk": `{"a":1}x`,
		"wrong type":    `{"a":"s"}`,
		"malformed":     `{"a":`,
		"empty":         ``,
		"array":         `[1]`,
	}
	for name, raw := range bad {
		err := decodeCase(t, raw)
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != 400 || ae.Code != "bad_request" {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	big := `{"b":"` + strings.Repeat("a", 2000) + `"}`
	if err := decodeCase(t, big, 100); err == nil {
		t.Fatal("body over the override limit accepted")
	}
	if err := decodeCase(t, big, 4096); err != nil {
		t.Fatalf("body under the override limit rejected: %v", err)
	}
	huge := `{"b":"` + strings.Repeat("a", int(defaultBodyLimit)+10) + `"}`
	if err := decodeCase(t, huge); err == nil {
		t.Fatal("body over the default limit accepted")
	}
}

func TestEmptyBody(t *testing.T) {
	good := []string{"", "{}", " {} ", "\n", "{ }"}
	for _, raw := range good[:4] {
		rec := httptest.NewRecorder()
		if err := emptyBody(rec, httptest.NewRequest("POST", "/", strings.NewReader(raw))); err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"a":1}`, `[]`, `null`, `x`, `{ }`, strings.Repeat(" ", 2000) + "{}", strings.Repeat("a", 5000)} {
		rec := httptest.NewRecorder()
		err := emptyBody(rec, httptest.NewRequest("POST", "/", strings.NewReader(raw)))
		var ae *APIError
		if !errors.As(err, &ae) || ae.Code != "bad_request" {
			t.Fatalf("%.20q: got %v", raw, err)
		}
	}
	if err := emptyBody(httptest.NewRecorder(), httptest.NewRequest("POST", "/", io.NopCloser(strings.NewReader("")))); err != nil {
		t.Fatal(err)
	}
}

func TestAPIHandlerRecoversPanic(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	api := NewAPI(func(rt *Router) {
		rt.Handle("GET /api/early", func(http.ResponseWriter, *http.Request) error { panic("secret /home/user") })
		rt.Handle("GET /api/late", func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(200)
			panic("late")
		})
	})
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/early", nil))
	if w := decodeErr(t, rec); rec.Code != 500 || w.Error.Code != "internal_error" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") || !strings.Contains(logs.String(), "secret") {
		t.Fatalf("leak or missing log: %q / %q", rec.Body.String(), logs.String())
	}
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/late", nil))
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("late panic rewrote response: %d %q", rec.Code, rec.Body.String())
	}
}
