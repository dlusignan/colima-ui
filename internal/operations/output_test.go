package operations

import (
	"strings"
	"testing"
)

func TestRingSeqCountsFromOneAndThroughEviction(t *testing.T) {
	r := newOutputRing(3, 1<<20)
	for i := 1; i <= 5; i++ {
		if got := r.add("x"); got != i {
			t.Fatalf("add #%d returned seq %d", i, got)
		}
	}
	got := r.after(0)
	if len(got) != 3 || got[0].Seq != r.dropped+1 || got[2].Seq != 5 {
		t.Fatalf("retained %+v dropped %d", got, r.dropped)
	}
	if r.dropped != 2 {
		t.Fatalf("dropped = %d, want 2", r.dropped)
	}
}

func TestRingByteBound(t *testing.T) {
	r := newOutputRing(100, 10)
	r.add("aaaa")
	r.add("bbbb")
	r.add("cccc") // 12 bytes: evicts the first
	got := r.after(0)
	if len(got) != 2 || got[0].Text != "bbbb" || r.bytes != 8 {
		t.Fatalf("retained %+v bytes %d", got, r.bytes)
	}
	r.add(strings.Repeat("z", 50)) // alone over the bound: everything goes
	if len(r.after(0)) != 0 || r.bytes != 0 {
		t.Fatalf("oversized line should not be retained: %+v", r.after(0))
	}
	if seq := r.add("ok"); seq != 5 {
		t.Fatalf("seq after eviction = %d, want 5", seq)
	}
}

func TestRingAfterCopies(t *testing.T) {
	r := newOutputRing(10, 100)
	r.add("a")
	r.add("b")
	r.add("c")
	got := r.after(1)
	if len(got) != 2 || got[0].Seq != 2 {
		t.Fatalf("after(1) = %+v", got)
	}
	got[0].Text = "mutated"
	if r.after(1)[0].Text != "b" {
		t.Fatal("after must return a copy")
	}
	if len(r.after(3)) != 0 {
		t.Fatal("after(last) must be empty")
	}
}
