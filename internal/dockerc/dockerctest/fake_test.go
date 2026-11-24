package dockerctest

import (
	"context"
	"errors"
	"strings"
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

func TestFakeReturnsZeroValuesOnInjectedError(t *testing.T) {
	f := &Fake{
		VolumeList: []dockerc.Volume{{Name: "v"}}, ImageList: []dockerc.Image{{ID: "i"}},
		EngineInfo: dockerc.Engine{Version: "x"}, Disk: dockerc.DiskUsage{LayersBytes: 1},
	}
	boom := errors.New("boom")
	ctx := context.Background()
	for _, m := range []string{"Volumes", "Images", "Networks", "Containers", "Info", "DiskUsage", "Logs"} {
		f.FailWith(m, boom)
	}
	if v, err := f.Volumes(ctx); err == nil || v != nil {
		t.Errorf("Volumes = %v, %v", v, err)
	}
	if v, err := f.Images(ctx); err == nil || v != nil {
		t.Errorf("Images = %v, %v", v, err)
	}
	if v, err := f.Info(ctx); err == nil || v != (dockerc.Engine{}) {
		t.Errorf("Info = %v, %v", v, err)
	}
	if v, err := f.DiskUsage(ctx); err == nil || v.LayersBytes != 0 {
		t.Errorf("DiskUsage = %v, %v", v, err)
	}
}

func TestFakeRecordObjectAndRefValidate(t *testing.T) {
	f := &Fake{}
	if err := f.RecordObject("VolumeRemove", "pgdata", true); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x", "a/b", "%2e%2e", "a\nb", strings.Repeat("a", 129), ""} {
		if err := f.RecordObject("VolumeRemove", bad); !errors.Is(err, dockerc.ErrBadName) {
			t.Errorf("RecordObject %q: %v", bad, err)
		}
	}
	if err := f.RecordRef("ImageRemove", "registry.example.com/team/app:v1"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x", "a//b", "A b", ""} {
		if err := f.RecordRef("ImageRemove", bad); !errors.Is(err, dockerc.ErrBadName) {
			t.Errorf("RecordRef %q: %v", bad, err)
		}
	}
	// A valid call still returns an injected failure, and every call is recorded.
	f.FailWith("VolumeRemove", dockerc.ErrConflict)
	if err := f.RecordObject("VolumeRemove", "pgdata"); !errors.Is(err, dockerc.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if got := f.CallsTo("VolumeRemove"); len(got) != 8 || got[0].Args[0] != "pgdata" || got[0].Args[1] != true {
		t.Fatalf("calls = %+v", got)
	}
}
