package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A cache prune goes through the real wiring: the route, the manager's task
// job running the colima streamer with the fixed vector, the hub observer
// events, the global action log row and the shutdown order. The runner is a
// fake: the real colima binary is never run.
func TestAppCachePruneEndToEnd(t *testing.T) {
	baseline := ownGoroutines()
	r := &fakeRunner{list: listJSON, script: []string{"removing cached image"}, exit: 0}
	a, _ := testAppAt(t, r)
	ts := serve(t, a)
	ch, _ := ts.openStream(t, "")

	if code, _ := ts.do(t, "POST", "/api/colima/cache/prune", `{"confirm":"nope"}`); code != 422 {
		t.Fatalf("wrong confirm: %d", code)
	}
	if r.streamCount() != 0 {
		t.Fatal("colima was reached without a confirmation")
	}
	code, body := ts.do(t, "POST", "/api/colima/cache/prune", `{"confirm":"delete-cached-assets","includeLima":true}`)
	if code != 202 || !strings.Contains(body, `"action":"cache-prune"`) {
		t.Fatalf("prune: %d %s", code, body)
	}
	var lines []string
	for {
		e := nextJobEvent(t, ch)
		if e.name == "job.output" {
			lines = append(lines, e.data["line"].(string))
			continue
		}
		if e.name != "job.finished" || e.data["state"] != "succeeded" {
			t.Fatalf("event: %#v", e)
		}
		break
	}
	if strings.Join(lines, "|") != "removing cached image" {
		t.Fatalf("lines = %q", lines)
	}
	r.mu.Lock()
	argv := strings.Join(r.streams[0], " ")
	r.mu.Unlock()
	if argv != "prune --force --all" {
		t.Fatalf("argv = %q", argv)
	}
	eventually(t, "global action row", func() bool {
		rows, _ := a.store.RecentActions(t.Context(), "_global", 10)
		return len(rows) == 1 && rows[0].Action == "cache-prune" && rows[0].Outcome == "success" &&
			rows[0].Detail != nil && *rows[0].Detail == "colima prune --force --all"
	})
	a.closeStreams()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := ts.srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoLeak(t, baseline)
}

// Shutdown cancels a running prune and waits for the child before the history
// closes, and the row says it was cancelled.
func TestAppShutdownCancelsARunningPrune(t *testing.T) {
	baseline := ownGoroutines()
	r := &fakeRunner{list: listJSON}
	a, _ := testAppAt(t, r)
	ts := serve(t, a)
	if code, body := ts.do(t, "POST", "/api/colima/cache/prune", `{"confirm":"delete-cached-assets"}`); code != 202 {
		t.Fatalf("prune: %d %s", code, body)
	}
	eventually(t, "child started", func() bool { return r.streamCount() == 1 })
	if code, _ := ts.do(t, "POST", "/api/colima/cache/prune", `{"confirm":"delete-cached-assets"}`); code != 409 {
		t.Fatalf("second prune: %d", code)
	}
	a.closeStreams()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := ts.srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	p := r.procs[0]
	r.mu.Unlock()
	select {
	case <-p.cancelled:
	default:
		t.Fatal("child was not cancelled")
	}
	assertNoLeak(t, baseline)
}
