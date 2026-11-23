package httpapi

import (
	"bytes"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

func init() {
	// Do not depend on the machine's mime tables for fonts.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

const (
	indexName      = "index.html"
	cacheNoCache   = "no-cache"
	cacheImmutable = "public, max-age=31536000, immutable"
)

// newStaticHandler serves the built frontend from ui. It needs no token (the
// page must load before it can read the #token fragment) and is wrapped by
// the Host and Origin guard like every other route.
func newStaticHandler(ui fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			serveIndex(w, r, ui)
			return
		}
		if fs.ValidPath(name) && !hasDotSegment(name) && serveFile(w, r, ui, name) {
			return
		}
		// Missing. A path with an extension is an asset request: never HTML.
		if path.Ext(path.Base(name)) != "" ||
			name == "api" || strings.HasPrefix(name, "api/") ||
			!strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html") {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serveIndex(w, r, ui)
	})
}

func hasDotSegment(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func serveIndex(w http.ResponseWriter, r *http.Request, ui fs.FS) {
	if !serveFile(w, r, ui, indexName) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "UI not built: run `make build`.\n")
	}
}

// serveFile serves the regular file name and reports whether it existed.
func serveFile(w http.ResponseWriter, r *http.Request, ui fs.FS, name string) bool {
	f, err := ui.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	var rs io.ReadSeeker
	if s, ok := f.(io.ReadSeeker); ok {
		rs = s
	} else {
		b, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		rs = bytes.NewReader(b)
	}
	switch {
	case name == indexName:
		w.Header().Set("Cache-Control", cacheNoCache)
	case strings.HasPrefix(name, "assets/"):
		w.Header().Set("Cache-Control", cacheImmutable)
	}
	http.ServeContent(w, r, name, time.Time{}, rs)
	return true
}
