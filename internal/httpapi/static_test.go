package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func uiFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte("<html>app</html>")},
		"favicon.svg":           {Data: []byte("<svg/>")},
		"theme-init.js":         {Data: []byte("document.x=1")},
		"assets/app-abc123.js":  {Data: []byte("console.log(1)")},
		"assets/app-abc123.css": {Data: []byte("a{}")},
		"assets/font-abc.woff2": {Data: []byte("wOF2....")},
		".gitkeep":              {Data: nil},
	}
}

func staticDo(t *testing.T, ui fstest.MapFS, method, target, host string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var hit bool
	var h http.Handler
	if ui == nil {
		h = NewHandler(testPort, testToken, stub(&hit), nil)
	} else {
		h = NewHandler(testPort, testToken, stub(&hit), ui)
	}
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const htmlAccept = "text/html,application/xhtml+xml"

func TestStaticRoot(t *testing.T) {
	rec := staticDo(t, uiFS(), "GET", "/", goodHost, nil)
	if rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("cache-control %q", cc)
	}
}

func TestStaticNeedsNoToken(t *testing.T) {
	for _, p := range []string{"/", "/favicon.svg", "/assets/app-abc123.js", "/profiles/default"} {
		rec := staticDo(t, uiFS(), "GET", p, goodHost, map[string]string{"Accept": htmlAccept})
		if rec.Code != 200 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
}

func TestStaticDeepLinkFallback(t *testing.T) {
	rec := staticDo(t, uiFS(), "GET", "/profiles/default", goodHost, map[string]string{"Accept": htmlAccept})
	if rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("fallback index must be no-cache")
	}
}

func TestStaticFallbackNeedsHTMLAccept(t *testing.T) {
	for _, accept := range []string{"", "application/json", "*/*"} {
		hdr := map[string]string{}
		if accept != "" {
			hdr["Accept"] = accept
		}
		rec := staticDo(t, uiFS(), "GET", "/profiles/default", goodHost, hdr)
		if rec.Code != 404 {
			t.Fatalf("accept %q: %d", accept, rec.Code)
		}
	}
}

func TestStaticMissingWithExtensionIs404(t *testing.T) {
	for _, p := range []string{"/assets/missing.js", "/nope.css", "/x/y.png", "/assets/app-abc123.js.map"} {
		rec := staticDo(t, uiFS(), "GET", p, goodHost, map[string]string{"Accept": htmlAccept})
		if rec.Code != 404 || strings.Contains(rec.Body.String(), "<html>") {
			t.Fatalf("%s: %d %q", p, rec.Code, rec.Body.String())
		}
	}
}

func TestStaticAssetTypesAndCache(t *testing.T) {
	cases := []struct{ path, ct, cc string }{
		{"/assets/app-abc123.js", "text/javascript", "public, max-age=31536000, immutable"},
		{"/assets/app-abc123.css", "text/css", "public, max-age=31536000, immutable"},
		{"/assets/font-abc.woff2", "font/woff2", "public, max-age=31536000, immutable"},
		{"/theme-init.js", "text/javascript", ""},
		{"/favicon.svg", "image/svg+xml", ""},
	}
	for _, c := range cases {
		rec := staticDo(t, uiFS(), "GET", c.path, goodHost, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", c.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, c.ct) {
			t.Fatalf("%s: content type %q", c.path, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != c.cc {
			t.Fatalf("%s: cache-control %q, want %q", c.path, cc, c.cc)
		}
	}
}

func TestStaticHead(t *testing.T) {
	rec := staticDo(t, uiFS(), "HEAD", "/", goodHost, nil)
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("got %d with %d body bytes", rec.Code, rec.Body.Len())
	}
	if rec := staticDo(t, uiFS(), "HEAD", "/healthz", goodHost, nil); rec.Code != 200 {
		t.Fatalf("HEAD /healthz: %d", rec.Code)
	}
}

func TestStaticMethodNotAllowed(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		for _, p := range []string{"/", "/healthz", "/profiles/default", "/assets/app-abc123.js"} {
			rec := staticDo(t, uiFS(), m, p, goodHost, nil)
			if rec.Code != 405 {
				t.Fatalf("%s %s: %d", m, p, rec.Code)
			}
			if p == "/" || p == "/healthz" {
				if a := rec.Header().Get("Allow"); !strings.Contains(a, "GET") {
					t.Fatalf("%s %s: Allow %q", m, p, a)
				}
			}
		}
	}
}

func TestStaticDoesNotShadowAPIOrHealthz(t *testing.T) {
	rec := staticDo(t, uiFS(), "GET", "/healthz", goodHost, map[string]string{"Accept": htmlAccept})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status"`) {
		t.Fatalf("healthz: %d %q", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/api", "/api/", "/api/x", "/api/ping", "/api/x.js"} {
		rec := staticDo(t, uiFS(), "GET", p, goodHost, map[string]string{"Accept": htmlAccept})
		if rec.Code != 401 {
			t.Fatalf("%s without token: %d, want 401", p, rec.Code)
		}
	}
	rec = staticDo(t, uiFS(), "GET", "/api/anything", goodHost,
		map[string]string{"Authorization": "Bearer " + testToken, "Accept": htmlAccept})
	if strings.Contains(rec.Body.String(), "<html>") {
		t.Fatal("api route returned index.html")
	}
}

func TestStaticHostAndOriginGuard(t *testing.T) {
	for _, p := range []string{"/", "/assets/app-abc123.js", "/profiles/default"} {
		rec := staticDo(t, uiFS(), "GET", p, "evil.example:8787", map[string]string{"Accept": htmlAccept})
		if rec.Code != 403 {
			t.Fatalf("%s foreign host: %d", p, rec.Code)
		}
		rec = staticDo(t, uiFS(), "GET", p, goodHost, map[string]string{"Origin": "http://evil.example"})
		if rec.Code != 403 {
			t.Fatalf("%s foreign origin: %d", p, rec.Code)
		}
	}
}

func TestStaticTraversalAndDotfiles(t *testing.T) {
	for _, p := range []string{"/.gitkeep", "/../go.mod", "/assets/../../x", "/%2e%2e/etc/passwd", "/assets//..%2f..%2fx"} {
		rec := staticDo(t, uiFS(), "GET", p, goodHost, nil)
		if rec.Code == 200 {
			t.Fatalf("%s: served 200 %q", p, rec.Body.String())
		}
	}
	// Directory paths do not list.
	rec := staticDo(t, uiFS(), "GET", "/assets", goodHost, nil)
	if rec.Code != 404 {
		t.Fatalf("/assets without html accept: %d", rec.Code)
	}
}

func TestStaticUnbuiltUI(t *testing.T) {
	ui := fstest.MapFS{".gitkeep": {}}
	for _, p := range []string{"/", "/profiles/default"} {
		rec := staticDo(t, ui, "GET", p, goodHost, map[string]string{"Accept": htmlAccept})
		if rec.Code != 503 || !strings.Contains(rec.Body.String(), "make build") {
			t.Fatalf("%s: %d %q", p, rec.Code, rec.Body.String())
		}
	}
	if rec := staticDo(t, ui, "GET", "/missing.js", goodHost, nil); rec.Code != 404 {
		t.Fatalf("missing js: %d", rec.Code)
	}
}

func TestNilUIHasNoStaticRoutes(t *testing.T) {
	rec := staticDo(t, nil, "GET", "/", goodHost, nil)
	if rec.Code != 404 {
		t.Fatalf("/ with nil ui: %d", rec.Code)
	}
	if rec := staticDo(t, nil, "POST", "/healthz", goodHost, nil); rec.Code != 405 {
		t.Fatalf("POST /healthz: %d", rec.Code)
	}
}
