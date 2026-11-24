package dockerctest

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

func TestFakeRecordsCallsInOrder(t *testing.T) {
	f := &Fake{VolumeList: []dockerc.Volume{{Name: "v"}}}
	ctx := context.Background()
	if v, err := f.Volumes(ctx); err != nil || len(v) != 1 {
		t.Fatalf("Volumes = %v, %v", v, err)
	}
	f.Stats(ctx, "abc")
	f.Logs(ctx, "abc", 5)
	got := f.Calls()
	if len(got) != 3 || got[0].Method != "Volumes" || got[1].Method != "Stats" || got[2].Method != "Logs" {
		t.Fatalf("calls = %+v", got)
	}
	if a := got[2].Args; len(a) != 2 || a[0] != "abc" || a[1] != 5 {
		t.Fatalf("args = %v", a)
	}
	if n := len(f.CallsTo("Stats")); n != 1 {
		t.Fatalf("CallsTo(Stats) = %d", n)
	}
	// The returned slice is a copy.
	got[0].Method = "x"
	if f.Calls()[0].Method != "Volumes" {
		t.Fatal("Calls leaks internal state")
	}
	f.Reset()
	if len(f.Calls()) != 0 {
		t.Fatal("Reset kept calls")
	}
}

func TestFakeFailWith(t *testing.T) {
	f := &Fake{}
	boom := errors.New("boom")
	f.FailWith("Networks", boom)
	if _, err := f.Networks(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if len(f.CallsTo("Networks")) != 1 {
		t.Fatal("a failed call must still be recorded")
	}
	f.FailWith("Networks", nil)
	if _, err := f.Networks(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestFakeConcurrent(t *testing.T) {
	f := &Fake{}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.Images(context.Background())
			f.FailWith("Info", nil)
			_ = f.Calls()
		}()
	}
	wg.Wait()
	if n := len(f.CallsTo("Images")); n != 20 {
		t.Fatalf("calls = %d", n)
	}
}

func TestProvider(t *testing.T) {
	f := &Fake{}
	p := &Provider{Client: f}
	c, err := p.For(context.Background(), "synthetic-a")
	if err != nil || c != dockerc.Client(f) {
		t.Fatalf("For = %v, %v", c, err)
	}
	p.Err = dockerc.ErrVMNotRunning
	if _, err := p.For(context.Background(), "synthetic-b"); !errors.Is(err, dockerc.ErrVMNotRunning) {
		t.Fatalf("err = %v", err)
	}
	if got := p.Profiles(); len(got) != 2 || got[1] != "synthetic-b" {
		t.Fatalf("profiles = %v", got)
	}
}
