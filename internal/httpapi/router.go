package httpapi

import (
	"fmt"
	"net/http"
	"strings"
)

// Router registers API routes. Every pattern must live under /api/ so no route
// group can sit outside the token guard. Registering the same pattern twice
// panics (ServeMux behaviour), so two groups claiming one route fail at
// startup.
type Router struct{ mux *http.ServeMux }

// Handle registers h for pattern, which is "/api/..." or "METHOD /api/...".
func (rt *Router) Handle(pattern string, h apiHandler) {
	p := pattern
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	if !strings.HasPrefix(p, "/api/") {
		panic(fmt.Sprintf("httpapi: route %q is not under /api/", pattern))
	}
	rt.mux.Handle(pattern, h)
}
