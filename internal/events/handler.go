package events

import (
	"errors"
	"net/http"
	"time"
)

var keepaliveFrame = []byte(": keepalive\n\n")

// Handler returns the handler for one SSE stream. Mount it behind the Host,
// Origin and token guards, for example
// mux.Handle("GET /api/events", hub.Handler()).
//
// The query parameters profile and job filter the stream; an empty value
// means no filter and both combine with AND. A metric event is not job
// scoped, so it passes a job filter.
//
// Last-Event-ID is ignored: the contract promises no replay of missed events.
// Middleware in front of this handler must not wrap the ResponseWriter
// without an Unwrap method, or flushing breaks.
func (h *Hub) Handler() http.Handler {
	return http.HandlerFunc(h.serve)
}

func (h *Hub) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sub, err := h.Subscribe(q.Get("profile"), q.Get("job"))
	if err != nil {
		http.Error(w, "event stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer sub.Close()

	rc := http.NewResponseController(w)
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if rc.Flush() != nil {
		return
	}

	ticker := time.NewTicker(h.cfg.keepalive)
	defer ticker.Stop()

	write := func(b []byte) bool {
		// Bound a client whose TCP window is full; ErrNotSupported just
		// means the writer has no deadlines (tests).
		if err := rc.SetWriteDeadline(time.Now().Add(h.cfg.writeDeadline)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false
		}
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	for {
		select {
		case frame := <-sub.C:
			if !write(frame) {
				return
			}
		case <-ticker.C:
			if !write(keepaliveFrame) {
				return
			}
		case <-r.Context().Done():
			return
		case <-sub.Done():
			return
		}
	}
}
