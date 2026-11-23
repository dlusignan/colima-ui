package httpapi

import (
	"net/http"
)

// Option registers one route group on the Router. Each group lives in its own
// file with its own WithX constructor, so there is no central route list.
type Option func(*Router)

// NewAPI builds the /api handler from the given route groups. Unmatched
// requests get JSON 404 and 405 bodies in the contract shape; a 405 keeps the
// Allow header. Matched routes are served directly, unwrapped, so streaming
// handlers keep http.Flusher.
func NewAPI(opts ...Option) http.Handler {
	rt := &Router{mux: http.NewServeMux()}
	for _, o := range opts {
		o(rt)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := rt.mux.Handler(r)
		if pattern != "" {
			h.ServeHTTP(w, r)
			return
		}
		// No route: let the mux decide between 404 and 405, but replace its
		// plain-text body with the contract's JSON.
		fw := &fallbackWriter{header: http.Header{}}
		h.ServeHTTP(fw, r)
		if allow := fw.header.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		if fw.status == http.StatusMethodNotAllowed {
			writeAPIError(w, errMethodNotAllowed())
			return
		}
		writeAPIError(w, errNotFound())
	})
}

// fallbackWriter captures the status and headers of the mux's own error
// responses and drops the body.
type fallbackWriter struct {
	header http.Header
	status int
}

func (f *fallbackWriter) Header() http.Header { return f.header }

func (f *fallbackWriter) WriteHeader(code int) {
	if f.status == 0 {
		f.status = code
	}
}

func (f *fallbackWriter) Write(b []byte) (int, error) { return len(b), nil }
