package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushRecorder) Flush() { f.flushed = true }

func TestWithEventsServesStreamHandler(t *testing.T) {
	var sawFlusher bool
	h := NewAPI(WithEvents(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		sawFlusher = ok
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if ok {
			fl.Flush()
		}
	})))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/events", nil))
	if rec.Code != 200 || !sawFlusher || !rec.flushed {
		t.Fatalf("code %d flusher %v flushed %v", rec.Code, sawFlusher, rec.flushed)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type %q", got)
	}
}

func TestWithEventsOnlyGET(t *testing.T) {
	h := NewAPI(WithEvents(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run")
	})))
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/api/events", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s: %d", m, rec.Code)
		}
	}
}

func TestWithEventsDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering the events route twice must panic")
		}
	}()
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	NewAPI(WithEvents(h), WithEvents(h))
}

func TestWithEventsRejectsHEAD(t *testing.T) {
	h := NewAPI(WithEvents(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run for HEAD")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("HEAD", "/api/events", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET" {
		t.Fatalf("HEAD: %d allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}
