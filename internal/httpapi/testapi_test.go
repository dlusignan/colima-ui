package httpapi

import (
	"encoding/json"
	"net/http"
)

// testAPI is a test-only API with one route, GET /api/ping, so the
// middleware tests have something to protect.
func testAPI() http.Handler {
	return NewAPI(func(rt *Router) {
		rt.Handle("GET /api/ping", func(w http.ResponseWriter, _ *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			return json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		})
	})
}
