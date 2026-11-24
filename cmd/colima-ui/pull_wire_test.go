package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
)

// A pull goes through the real wiring: the route, the manager's task job, the
// hub observer (job.output and job.finished events), the action log row and the
// shutdown order. Docker is a fake; no socket and no network.
func TestAppImagePullEndToEnd(t *testing.T) {
	baseline := ownGoroutines()
	a, _ := testAppAt(t, &fakeRunner{list: listJSON})
	fake := &dockerctest.Fake{PullLines: []string{"Pulling from library/postgres", "Status: downloaded newer image"}}
	a.containers = &dockerctest.Provider{Client: fake}
	ts := serve(t, a)
	ch, _ := ts.openStream(t, "?profile=default")

	code, body := ts.do(t, "POST", "/api/profiles/default/images/pull", `{"reference":"postgres:18-alpine"}`)
	if code != 202 || !strings.Contains(body, `"action":"image-pull"`) || !strings.Contains(body, `"profile":"default"`) {
		t.Fatalf("pull: %d %s", code, body)
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
	if strings.Join(lines, "|") != "Pulling postgres:18-alpine|Pulling from library/postgres|Status: downloaded newer image" {
		t.Fatalf("lines = %q", lines)
	}
	eventually(t, "action row", func() bool { return len(ts.actions(t, "default")) == 1 })
	if rows := ts.actions(t, "default"); rows[0].Action != "docker.image.pull" || rows[0].Outcome != "success" {
		t.Fatalf("rows = %#v", rows)
	}
	// A stopped profile and an unknown one are refused before any pull.
	if code, _ := ts.do(t, "POST", "/api/profiles/dev/images/pull", `{"reference":"postgres:18"}`); code != 409 {
		t.Fatalf("stopped profile: %d", code)
	}
	if code, _ := ts.do(t, "POST", "/api/profiles/ghost/images/pull", `{"reference":"postgres:18"}`); code != 404 {
		t.Fatalf("unknown profile: %d", code)
	}
	if n := len(fake.CallsTo("PullImage")); n != 1 {
		t.Fatalf("%d pulls reached Docker", n)
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
	assertNoLeak(t, baseline)
}

// Shutdown cancels a running pull and waits for it before the history closes.
func TestAppShutdownCancelsARunningPull(t *testing.T) {
	baseline := ownGoroutines()
	a, dbPath := testAppAt(t, &fakeRunner{list: listJSON})
	started := make(chan struct{})
	fake := &dockerctest.Fake{PullFunc: func(ctx context.Context, emit func(string)) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	a.containers = &dockerctest.Provider{Client: fake}
	ts := serve(t, a)
	if code, body := ts.do(t, "POST", "/api/profiles/default/images/pull", `{"reference":"postgres:18"}`); code != 202 {
		t.Fatalf("pull: %d %s", code, body)
	}
	<-started
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
	rows := historyAt(t, dbPath, "default")
	if len(rows) != 1 || rows[0].Action != "docker.image.pull" || rows[0].Outcome != "cancelled" {
		t.Fatalf("rows = %+v", rows)
	}
}
