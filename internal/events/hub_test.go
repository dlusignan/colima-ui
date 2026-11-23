package events

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func recv(t *testing.T, s *Subscription) string {
	t.Helper()
	select {
	case f := <-s.C:
		return string(f)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a frame")
		return ""
	}
}

func idOf(t *testing.T, frame string) int {
	t.Helper()
	var id int
	if _, err := fmt.Sscanf(frame, "id: %d\n", &id); err != nil {
		t.Fatalf("bad frame %q: %v", frame, err)
	}
	return id
}

func out(job, profile string, seq int) JobOutput {
	return JobOutput{JobID: job, Profile: profile, Seq: seq, Line: "l"}
}

func TestOrderAndIDs(t *testing.T) {
	h := New()
	s, _ := h.Subscribe("", "")
	defer s.Close()
	for i := 0; i < 50; i++ {
		h.Publish(out("j1", "p", i))
	}
	for i := 0; i < 50; i++ {
		f := recv(t, s)
		if got := idOf(t, f); got != i+1 {
			t.Fatalf("frame %d has id %d", i, got)
		}
		if !strings.Contains(f, fmt.Sprintf(`"seq":%d,`, i)) {
			t.Fatalf("frame %d out of order: %s", i, f)
		}
	}
}

func TestTotalOrderConcurrentPublishers(t *testing.T) {
	const pubs, per = 8, 100
	h := New(WithBuffer(pubs * per))
	a, _ := h.Subscribe("", "")
	b, _ := h.Subscribe("", "")
	var wg sync.WaitGroup
	for p := 0; p < pubs; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				h.Publish(out(fmt.Sprintf("j%d", p), "p", i))
			}
		}()
	}
	wg.Wait()
	lastSeq := map[string]int{}
	for i := 1; i <= pubs*per; i++ {
		fa, fb := recv(t, a), recv(t, b)
		if fa != fb {
			t.Fatalf("clients disagree at %d", i)
		}
		if idOf(t, fa) != i {
			t.Fatalf("id gap at %d: %s", i, fa)
		}
		var ev JobOutput
		data := fa[strings.Index(fa, "data: ")+6:]
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &ev); err != nil {
			t.Fatal(err)
		}
		if prev, ok := lastSeq[ev.JobID]; ok && ev.Seq != prev+1 {
			t.Fatalf("job %s seq %d after %d", ev.JobID, ev.Seq, prev)
		}
		lastSeq[ev.JobID] = ev.Seq
	}
}

func TestSlowClientDropped(t *testing.T) {
	h := New(WithBuffer(1))
	slow, _ := h.Subscribe("", "")
	fast, _ := h.Subscribe("", "")
	h.Publish(out("j", "p", 0)) // fills both buffers
	recv(t, fast)
	h.Publish(out("j", "p", 1)) // slow is full: dropped
	select {
	case <-slow.Done():
	default:
		t.Fatal("slow client not dropped")
	}
	if got := recv(t, fast); idOf(t, got) != 2 {
		t.Fatalf("fast client got %s", got)
	}
	h.Publish(out("j", "p", 2))
	if got := recv(t, fast); idOf(t, got) != 3 {
		t.Fatalf("fast client got %s", got)
	}
	if n := h.clientCount(); n != 1 {
		t.Fatalf("clientCount = %d, want 1", n)
	}
}

func TestPublishNeverBlocksWithStalledClient(t *testing.T) {
	h := New(WithBuffer(2))
	_, _ = h.Subscribe("", "") // never read
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			h.Publish(out("j", "p", i))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked")
	}
}

func TestFilters(t *testing.T) {
	h := New()
	all, _ := h.Subscribe("", "")
	prof, _ := h.Subscribe("p1", "")
	job, _ := h.Subscribe("", "j1")
	both, _ := h.Subscribe("p1", "j1")

	h.Publish(out("j1", "p1", 0))
	h.Publish(out("j2", "p1", 0))
	h.Publish(out("j1", "p2", 0))
	h.Publish(JobFinished{JobID: "j2", Profile: "p2", State: "failed"})
	h.Publish(Metric{Profile: "p1", Scope: "vm"})
	h.Publish(Metric{Profile: "p2", Scope: "vm"})

	// ids of events each client must see.
	want := map[*Subscription][]int{
		all:  {1, 2, 3, 4, 5, 6},
		prof: {1, 2, 5},
		job:  {1, 3, 5, 6}, // metrics pass the job filter
		both: {1, 5},
	}
	for s, ids := range want {
		for _, id := range ids {
			if got := idOf(t, recv(t, s)); got != id {
				t.Fatalf("got id %d, want %d", got, id)
			}
		}
		select {
		case f := <-s.C:
			t.Fatalf("unexpected extra frame %q", f)
		default:
		}
	}
}

func TestPayloadsMatchContract(t *testing.T) {
	zero, big := 0, int64(21474836480)
	cases := []struct {
		name string
		ev   Event
		want string
	}{
		{"output", JobOutput{JobID: "j_8f3a2c", Profile: "default", Seq: 3, Line: "\x1b[33mwaiting for the VM to boot\x1b[0m"},
			"id: 1\nevent: job.output\ndata: {\"jobId\":\"j_8f3a2c\",\"profile\":\"default\",\"seq\":3,\"line\":\"\\u001b[33mwaiting for the VM to boot\\u001b[0m\"}\n\n"},
		{"finished", JobFinished{JobID: "j_8f3a2c", Profile: "default", State: "succeeded",
			FinishedAt: time.Date(2026, 11, 23, 1, 10, 42, 999, time.UTC), ExitCode: &zero},
			"id: 1\nevent: job.finished\ndata: {\"jobId\":\"j_8f3a2c\",\"profile\":\"default\",\"state\":\"succeeded\",\"finishedAt\":\"2026-11-23T01:10:42Z\",\"exitCode\":0}\n\n"},
		{"finished null", JobFinished{JobID: "j", Profile: "p", State: "failed",
			FinishedAt: time.Date(2026, 11, 23, 3, 10, 42, 0, time.FixedZone("x", 2*3600))},
			"id: 1\nevent: job.finished\ndata: {\"jobId\":\"j\",\"profile\":\"p\",\"state\":\"failed\",\"finishedAt\":\"2026-11-23T01:10:42Z\",\"exitCode\":null}\n\n"},
		{"metric", Metric{Profile: "default", Scope: "vm", TakenAt: time.Date(2026, 11, 23, 1, 10, 30, 0, time.UTC),
			CPUPercent: 14.1, MemoryBytes: 2181038080, DiskBytes: &big},
			"id: 1\nevent: metric\ndata: {\"profile\":\"default\",\"scope\":\"vm\",\"takenAt\":\"2026-11-23T01:10:30Z\",\"cpuPercent\":14.1,\"memoryBytes\":2181038080,\"diskBytes\":21474836480}\n\n"},
		{"metric null", Metric{Profile: "default", Scope: "abc123", TakenAt: time.Date(2026, 11, 23, 1, 10, 30, 0, time.UTC),
			CPUPercent: 1, MemoryBytes: 5},
			"id: 1\nevent: metric\ndata: {\"profile\":\"default\",\"scope\":\"abc123\",\"takenAt\":\"2026-11-23T01:10:30Z\",\"cpuPercent\":1,\"memoryBytes\":5,\"diskBytes\":null}\n\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := New()
			s, _ := h.Subscribe("", "")
			h.Publish(c.ev)
			if got := recv(t, s); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestShutdown(t *testing.T) {
	h := New()
	s, _ := h.Subscribe("", "")
	h.Shutdown()
	h.Shutdown() // idempotent
	select {
	case <-s.Done():
	default:
		t.Fatal("subscription not ended")
	}
	if _, err := h.Subscribe("", ""); err != ErrClosed {
		t.Fatalf("Subscribe after Shutdown err = %v", err)
	}
	h.Publish(out("j", "p", 0)) // must not panic
	s.Close()                   // idempotent after drop
}

func TestCloseIdempotentAndUnregisters(t *testing.T) {
	h := New()
	s, _ := h.Subscribe("", "")
	s.Close()
	s.Close()
	if n := h.clientCount(); n != 0 {
		t.Fatalf("clientCount = %d", n)
	}
}
