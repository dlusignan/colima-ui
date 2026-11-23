package operations

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// drain collects a subscription's live lines until its channel closes.
func drain(t *testing.T, s *Subscription) []Line {
	t.Helper()
	var out []Line
	timeout := time.After(5 * time.Second)
	for {
		select {
		case l, ok := <-s.Lines():
			if !ok {
				return out
			}
			out = append(out, l)
		case <-timeout:
			t.Fatal("timed out draining subscription")
		}
	}
}

func TestSubscribeCatchUpWithoutGapsOrDuplicates(t *testing.T) {
	const total = 2000
	const after = 10
	m, l, _ := newTestManager(t, func(o *Options) { o.SubscriberBuffer = total + 1 })
	s := mustSubmit(t, m, "default")
	p := l.last()

	go func() {
		for i := 1; i <= total; i++ {
			p.emit(fmt.Sprintf("line %d", i))
		}
		p.exitWith(0)
	}()
	// Subscribe while the producer is still appending.
	eventually(t, "some output", func() bool { g, _ := m.Get(s.ID); return len(g.Output) >= 50 })
	sub, err := m.Subscribe(s.ID, after)
	if err != nil {
		t.Fatal(err)
	}
	got := append(append([]Line(nil), sub.Backlog...), drain(t, sub)...)
	if len(got) != total-after {
		t.Fatalf("got %d lines, want %d", len(got), total-after)
	}
	for i, ln := range got {
		want := after + 1 + i
		if ln.Seq != want || ln.Text != fmt.Sprintf("line %d", want) {
			t.Fatalf("line %d = %+v, want seq %d", i, ln, want)
		}
	}
	if sub.Err() != nil {
		t.Fatalf("Err = %v", sub.Err())
	}
}

func TestSubscribeUnknownJob(t *testing.T) {
	m, _, _ := newTestManager(t, nil)
	if _, err := m.Subscribe("j_x", 0); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestSlowSubscriberDroppedOthersKeepReceiving(t *testing.T) {
	m, l, _ := newTestManager(t, func(o *Options) { o.SubscriberBuffer = 2 })
	s := mustSubmit(t, m, "default")
	p := l.last()
	slow, _ := m.Subscribe(s.ID, 0) // never reads
	fast, _ := m.Subscribe(s.ID, 0)

	const n = 10
	got := make(chan Line)
	collected := make(chan []Line, 1)
	go func() {
		var all []Line
		for ln := range fast.Lines() {
			all = append(all, ln)
			got <- ln
		}
		collected <- all
	}()
	for i := 1; i <= n; i++ {
		p.emit(fmt.Sprintf("l%d", i))
		select { // pace the producer so the fast reader never falls behind
		case <-got:
		case <-time.After(5 * time.Second):
			t.Fatal("fast subscriber stalled")
		}
	}
	p.exitWith(0)
	all := <-collected
	if len(all) != n {
		t.Fatalf("fast got %d lines, want %d", len(all), n)
	}
	for i, ln := range all {
		if ln.Seq != i+1 {
			t.Fatalf("fast line %d seq %d", i, ln.Seq)
		}
	}
	if fast.Err() != nil {
		t.Fatalf("fast Err = %v", fast.Err())
	}
	// The slow one got its buffer's worth, then was closed with the error.
	slowGot := drain(t, slow)
	if len(slowGot) != 2 || slowGot[0].Seq != 1 || slowGot[1].Seq != 2 {
		t.Fatalf("slow got %+v", slowGot)
	}
	if !errors.Is(slow.Err(), ErrSlowSubscriber) {
		t.Fatalf("slow Err = %v", slow.Err())
	}
	if g, _ := m.Get(s.ID); len(g.Output) != n {
		t.Fatalf("job lost output: %d", len(g.Output))
	}
}

func TestSubscribersReleasedWhenJobEnds(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	a, _ := m.Subscribe(s.ID, 0)
	b, _ := m.Subscribe(s.ID, 0)
	l.last().emit("x")
	l.last().exitWith(0)
	for _, sub := range []*Subscription{a, b} {
		got := drain(t, sub)
		if len(got) != 1 || got[0].Text != "x" {
			t.Fatalf("got %+v", got)
		}
		if sub.Err() != nil {
			t.Fatalf("Err = %v", sub.Err())
		}
	}
	waitState(t, m, s.ID, StateSucceeded)
	j := jobOf(t, m, s.ID)
	j.mu.Lock()
	left := len(j.subs)
	j.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d subscribers still registered", left)
	}
}

func TestSubscribeToFinishedJobReturnsBacklogAndClosedChannel(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	l.last().emit("a")
	l.last().emit("b")
	l.last().exitWith(0)
	waitState(t, m, s.ID, StateSucceeded)
	sub, err := m.Subscribe(s.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Backlog) != 1 || sub.Backlog[0].Seq != 2 {
		t.Fatalf("backlog = %+v", sub.Backlog)
	}
	if got := drain(t, sub); len(got) != 0 {
		t.Fatalf("live lines on finished job: %+v", got)
	}
	sub.Close() // idempotent on a closed subscription
}

func TestSubscriptionCloseIsIdempotentAndReleases(t *testing.T) {
	m, l, _ := newTestManager(t, nil)
	s := mustSubmit(t, m, "default")
	sub, _ := m.Subscribe(s.ID, 0)
	sub.Close()
	sub.Close()
	if _, ok := <-sub.Lines(); ok {
		t.Fatal("channel should be closed")
	}
	l.last().emit("after close") // must not panic on the closed channel
	l.last().exitWith(0)
	waitState(t, m, s.ID, StateSucceeded)
}

func jobOf(t *testing.T, m *Manager, id string) *job {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		t.Fatalf("job %s not found", id)
	}
	return j
}
