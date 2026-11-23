package httpapi

import (
	"net/http"
	"strconv"
	"strings"
)

// hostOriginGuard is the outer layer. It wraps every route, including
// /healthz, and rejects any request whose Host or Origin is not the server's
// own loopback identity (DNS rebinding and cross-site defense).
//
// Host is compared by strict equality against an exact allowlist built from
// the real listener port: no prefix, suffix, trailing-dot or IPv6 forms.
//
// A missing Origin is accepted on purpose: browsers omit it on same-origin
// GETs and non-browser clients never send it. That is only safe because the
// bearer token is required on every /api route. Weakening the token rule
// also reopens this gap.
func hostOriginGuard(port int, next http.Handler) http.Handler {
	p := strconv.Itoa(port)
	hosts := map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true}
	origins := map[string]bool{"http://127.0.0.1:" + p: true, "http://localhost:" + p: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[strings.ToLower(r.Host)] {
			writeError(w, http.StatusForbidden, "forbidden_host")
			return
		}
		if vals := r.Header["Origin"]; len(vals) > 0 {
			if len(vals) != 1 || !origins[vals[0]] {
				writeError(w, http.StatusForbidden, "forbidden_origin")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
