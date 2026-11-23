package httpapi

import "net/http"

// WithEvents registers the event stream route:
//
//	GET /api/events
//
// h is the stream handler (events.Hub.Handler). It is a plain http.Handler
// that streams, so it is adapted to the router's handler type; the adapter
// keeps http.Flusher through trackWriter and sets no write timeout. The route
// sits under /api/, so it passes the same token, Host and Origin guard as every
// other route.
func WithEvents(h http.Handler) Option {
	return func(rt *Router) {
		rt.Handle("GET /api/events", func(w http.ResponseWriter, r *http.Request) error {
			// "GET /api/events" also matches HEAD, which would hold a hub
			// subscription open with no one reading it.
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				return errMethodNotAllowed()
			}
			h.ServeHTTP(w, r)
			return nil
		})
	}
}
