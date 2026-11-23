package events

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// flushRecorder is a mutex-guarded ResponseWriter that implements
// http.Flusher and counts flushes.
type flushRecorder struct {
	mu      sync.Mutex
	hdr     http.Header
	code    int
	body    strings.Builder
	flushes int
}

func newFlushRecorder() *flushRecorder { return &flushRecorder{hdr: http.Header{}} }

func (r *flushRecorder) Header() http.Header { return r.hdr }
func (r *flushRecorder) WriteHeader(c int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		r.code = c
	}
}
func (r *flushRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(b)
}
func (r *flushRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushes++
}
func (r *flushRecorder) snapshot() (code int, body string, flushes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.code, r.body.String(), r.flushes
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestHandlerHeadersAndImmediateFlush(t *testing.T) {
	h := New(WithKeepalive(time.Hour))
	rec := newFlushRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { h.Handler().ServeHTTP(rec, req); close(done) }()

	waitFor(t, "initial flush", func() bool { _, _, f := rec.snapshot(); return f >= 1 })
	code, body, _ := rec.snapshot()
	if code != 200 || body != "" {
		t.Fatalf("code=%d body=%q before any event", code, body)
	}
	for k, v := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"X-Accel-Buffering": "no",
	} {
		rec.mu.Lock()
		got := rec.hdr.Get(k)
		rec.mu.Unlock()
		if got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	// Flush after every event.
	_, _, before := rec.snapshot()
	h.Publish(out("j", "p", 0))
	waitFor(t, "event flush", func() bool { _, b, f := rec.snapshot(); return f > before && strings.Contains(b, "event: job.output") })
	cancel()
	<-done
}

func TestHandlerKeepalive(t *testing.T) {
	h := New(WithKeepalive(10 * time.Millisecond))
	rec := newFlushRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	waitFor(t, "two keepalives", func() bool {
		_, b, _ := rec.snapshot()
		return strings.Count(b, ": keepalive\n\n") >= 2
	})
}

func TestHandlerFiltersAndIgnoresLastEventID(t *testing.T) {
	h := New(WithKeepalive(time.Hour))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/?profile=p1&job=j1", nil)
	req.Header.Set("Last-Event-ID", "99")
	resp, err := http.DefaultClient.Do(req) // resolves on the immediate flush
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	waitFor(t, "subscription", func() bool { return h.clientCount() == 1 })

	h.Publish(out("j2", "p1", 0))
	h.Publish(out("j1", "p2", 0))
	h.Publish(out("j1", "p1", 7))
	br := bufio.NewReader(resp.Body)
	var got []string
	for i := 0; i < 4; i++ {
		l, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, l)
	}
	// No replay and no Last-Event-ID effect: the first id is 3 because two
	// earlier events were filtered out for this client, and the counter is global.
	if got[0] != "id: 3\n" || got[1] != "event: job.output\n" || !strings.Contains(got[2], `"seq":7`) {
		t.Fatalf("unexpected stream: %q", got)
	}
}

func TestHandlerDisconnectReleasesSubscription(t *testing.T) {
	h := New(WithKeepalive(time.Hour))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "subscription", func() bool { return h.clientCount() == 1 })
	cancel()
	resp.Body.Close()
	waitFor(t, "release", func() bool { return h.clientCount() == 0 })
}

func TestHandlerSlowClientStreamEnds(t *testing.T) {
	h := New(WithBuffer(1), WithKeepalive(time.Hour))
	// A handler blocked in Write never drains its buffer: simulate with a
	// writer that blocks until released.
	gate := make(chan struct{})
	bw := &blockingWriter{flushRecorder: newFlushRecorder(), gate: gate}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.Handler().ServeHTTP(bw, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
		close(done)
	}()
	waitFor(t, "subscription", func() bool { return h.clientCount() == 1 })
	for i := 0; i < 5; i++ {
		h.Publish(out("j", "p", i))
	}
	waitFor(t, "drop", func() bool { return h.clientCount() == 0 })
	close(gate)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return after drop")
	}
}

type blockingWriter struct {
	*flushRecorder
	gate chan struct{}
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	<-b.gate
	return b.flushRecorder.Write(p)
}

func TestHandlerShutdownEndsStreamsAndRejectsNew(t *testing.T) {
	h := New(WithKeepalive(time.Hour))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	waitFor(t, "subscription", func() bool { return h.clientCount() == 1 })
	h.Shutdown()
	// The body hits EOF because the handler returned.
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, resp.Body); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read err: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream still open after Shutdown")
	}
	resp2, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status after Shutdown = %d", resp2.StatusCode)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	settle := func() int {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		return runtime.NumGoroutine()
	}
	baseline := settle()

	h := New(WithKeepalive(5 * time.Millisecond))
	srv := httptest.NewServer(h.Handler())
	tr := &http.Transport{}
	client := &http.Client{Transport: tr}
	var cancels []context.CancelFunc
	var bodies []io.Closer
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, resp.Body)
	}
	waitFor(t, "10 clients", func() bool { return h.clientCount() == 10 })
	for i := 0; i < 20; i++ {
		h.Publish(out("j", "p", i))
	}
	// Half leave by cancelling, the rest by Shutdown.
	for _, c := range cancels[:5] {
		c()
	}
	h.Shutdown()
	waitFor(t, "empty hub", func() bool { return h.clientCount() == 0 })
	for _, b := range bodies {
		b.Close()
	}
	srv.Close()
	tr.CloseIdleConnections()

	waitFor(t, "goroutines back to baseline", func() bool { return settle() <= baseline })
}
