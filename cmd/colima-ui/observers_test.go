package main

import (
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/events"
	"github.com/dlusignan/colima-ui/internal/operations"
)

type capturePub struct {
	mu  sync.Mutex
	got []events.Event
}

func (c *capturePub) Publish(e events.Event) {
	c.mu.Lock()
	c.got = append(c.got, e)
	c.mu.Unlock()
}

func TestHubObserverOutputMapsFields(t *testing.T) {
	p := &capturePub{}
	o := newHubObserver(p)
	for i, l := range []string{"a", "b", "c"} {
		o.JobOutput(operations.JobLine{JobID: "j1", Profile: "dev", Seq: i + 1, Line: l})
	}
	if len(p.got) != 3 {
		t.Fatalf("got %d events", len(p.got))
	}
	for i, e := range p.got {
		out, ok := e.(events.JobOutput)
		if !ok || out.JobID != "j1" || out.Profile != "dev" || out.Seq != i+1 {
			t.Fatalf("event %d: %#v", i, e)
		}
	}
}

func TestHubObserverFinished(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	code := 3
	cases := []struct {
		name string
		s    operations.JobSummary
		want events.JobFinished
	}{
		{"failed with code", operations.JobSummary{ID: "j", Profile: "p", State: operations.StateFailed, FinishedAt: &at, ExitCode: &code},
			events.JobFinished{JobID: "j", Profile: "p", State: "failed", FinishedAt: at, ExitCode: &code}},
		{"cancelled without code", operations.JobSummary{ID: "j", Profile: "p", State: operations.StateCancelled, FinishedAt: &at},
			events.JobFinished{JobID: "j", Profile: "p", State: "cancelled", FinishedAt: at}},
		{"nil finish time falls back to now", operations.JobSummary{ID: "j", Profile: "p", State: operations.StateSucceeded},
			events.JobFinished{JobID: "j", Profile: "p", State: "succeeded", FinishedAt: at}},
	}
	for _, c := range cases {
		p := &capturePub{}
		o := newHubObserver(p)
		o.now = func() time.Time { return at }
		o.JobFinished(c.s)
		if len(p.got) != 1 {
			t.Fatalf("%s: %d events", c.name, len(p.got))
		}
		got := p.got[0].(events.JobFinished)
		if got.JobID != c.want.JobID || got.State != c.want.State || !got.FinishedAt.Equal(c.want.FinishedAt) ||
			(got.ExitCode == nil) != (c.want.ExitCode == nil) || (got.ExitCode != nil && *got.ExitCode != *c.want.ExitCode) {
			t.Fatalf("%s: %#v", c.name, got)
		}
	}
}

// TestHubObserverNeverBlocks wires the observer to a real hub with a client
// that never reads: the publisher must keep returning (the hub drops the client).
func TestHubObserverNeverBlocks(t *testing.T) {
	hub := events.New(events.WithBuffer(2))
	defer hub.Shutdown()
	sub, err := hub.Subscribe("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	o := newHubObserver(hub)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			o.JobOutput(operations.JobLine{JobID: "j", Profile: "p", Seq: i + 1, Line: "x"})
		}
		o.JobFinished(operations.JobSummary{ID: "j", Profile: "p", State: operations.StateSucceeded})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("observer blocked on a stalled client")
	}
	select {
	case <-sub.Done():
	case <-time.After(time.Second):
		t.Fatal("stalled client was not dropped")
	}
}

func TestRefreshObserverOnlyOnFinished(t *testing.T) {
	n := 0
	o := refreshObserver{refresh: func() { n++ }}
	o.JobOutput(operations.JobLine{})
	if n != 0 {
		t.Fatal("refresh on output")
	}
	o.JobFinished(operations.JobSummary{})
	if n != 1 {
		t.Fatalf("refresh calls %d", n)
	}
}
