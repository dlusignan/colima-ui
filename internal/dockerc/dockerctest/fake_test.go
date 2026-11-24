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

func TestFakeVolumeWritesValidateLikeTheRealClient(t *testing.T) {
	f := &Fake{CreatedVolume: dockerc.Volume{Name: "ok"}, VolumeDetail: dockerc.VolumeDetail{Name: "ok"}}
	ctx := context.Background()
	if _, err := f.CreateVolume(ctx, "../x", nil); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("bad name: %v", err)
	}
	if v, err := f.CreateVolume(ctx, "ok", map[string]string{"com.docker.x": "v"}); !errors.Is(err, dockerc.ErrBadName) || v.Name != "" {
		t.Fatalf("bad label: %v %+v", err, v)
	}
	if v, err := f.CreateVolume(ctx, "ok", map[string]string{"k": "v"}); err != nil || v.Name != "ok" {
		t.Fatalf("ok: %v %+v", err, v)
	}
	if _, err := f.InspectVolume(ctx, "a/b"); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("inspect: %v", err)
	}
	if err := f.RemoveVolume(ctx, ""); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("remove: %v", err)
	}
	if got := len(f.CallsTo("CreateVolume")); got != 3 {
		t.Fatalf("calls = %d (rejected calls are still recorded)", got)
	}
	f.FailWith("PruneVolumes", dockerc.ErrConflict)
	if r, err := f.PruneVolumes(ctx); !errors.Is(err, dockerc.ErrConflict) || r.Removed != nil {
		t.Fatalf("prune: %v %+v", err, r)
	}
}

func TestFakeNetworkWritesValidateLikeTheRealClient(t *testing.T) {
	f := &Fake{CreatedNetwork: dockerc.Network{Name: "ok"}, NetworkDetail: dockerc.NetworkDetail{Name: "ok"}}
	ctx := context.Background()
	if _, err := f.CreateNetwork(ctx, "../x", false, ""); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("bad name: %v", err)
	}
	if n, err := f.CreateNetwork(ctx, "ok", false, "0.0.0.0/0"); !errors.Is(err, dockerc.ErrBadName) || n.Name != "" {
		t.Fatalf("bad subnet: %v %+v", err, n)
	}
	if n, err := f.CreateNetwork(ctx, "ok", true, "172.28.0.0/16"); err != nil || n.Name != "ok" {
		t.Fatalf("ok: %v %+v", err, n)
	}
	if _, err := f.InspectNetwork(ctx, "a/b"); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("inspect: %v", err)
	}
	if err := f.RemoveNetwork(ctx, ""); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("remove: %v", err)
	}
	if got := len(f.CallsTo("CreateNetwork")); got != 3 {
		t.Fatalf("calls = %d (rejected calls are still recorded)", got)
	}
	f.FailWith("PruneNetworks", dockerc.ErrConflict)
	if r, err := f.PruneNetworks(ctx); !errors.Is(err, dockerc.ErrConflict) || r.Removed != nil {
		t.Fatalf("prune: %v %+v", err, r)
	}
}

func TestFakeImageWritesValidateLikeTheRealClient(t *testing.T) {
	f := &Fake{ImageDetail: dockerc.ImageDetail{ID: "ok"}}
	ctx := context.Background()
	if err := f.TagImage(ctx, "../x", "app", "v1"); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("bad ref: %v", err)
	}
	if err := f.TagImage(ctx, "app", "app:v1", "v1"); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("bad repository: %v", err)
	}
	if err := f.TagImage(ctx, "app", "app", "-x"); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("bad tag: %v", err)
	}
	if err := f.TagImage(ctx, "app:v1", "team/app", "v2"); err != nil {
		t.Fatalf("ok: %v", err)
	}
	if _, err := f.InspectImage(ctx, "a b"); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("inspect: %v", err)
	}
	if _, err := f.RemoveImage(ctx, ""); !errors.Is(err, dockerc.ErrBadName) {
		t.Fatalf("remove: %v", err)
	}
	if d, err := f.InspectImage(ctx, "app"); err != nil || d.ID != "ok" {
		t.Fatalf("inspect ok: %v %+v", err, d)
	}
	f.FailWith("PruneImages", dockerc.ErrConflict)
	if r, err := f.PruneImages(ctx, true); !errors.Is(err, dockerc.ErrConflict) || r.Untagged != nil {
		t.Fatalf("prune: %v %+v", err, r)
	}
	if c := f.CallsTo("PruneImages"); len(c) != 1 || c[0].Args[0] != true {
		t.Fatalf("calls = %+v", c)
	}
}

func TestFakeContainerActionsValidateLikeTheRealClient(t *testing.T) {
	f := &Fake{}
	ctx := context.Background()
	id := strings.Repeat("ab", 32)
	calls := map[string]func(string) (bool, error){
		"StartContainer":   func(s string) (bool, error) { return f.StartContainer(ctx, s) },
		"StopContainer":    func(s string) (bool, error) { return f.StopContainer(ctx, s) },
		"RestartContainer": func(s string) (bool, error) { return f.RestartContainer(ctx, s) },
	}
	for name, run := range calls {
		if changed, err := run(id); err != nil || !changed {
			t.Errorf("%s: changed=%v err=%v", name, changed, err)
		}
		for _, bad := range []string{"", "web", "0123456789ab", id[:63], strings.ToUpper(id), id[:63] + "/", "../x"} {
			if changed, err := run(bad); !errors.Is(err, dockerc.ErrBadName) || changed {
				t.Errorf("%s %q: changed=%v err=%v", name, bad, changed, err)
			}
		}
		if n := len(f.CallsTo(name)); n != 8 {
			t.Errorf("%s recorded %d calls", name, n)
		}
	}
	f.ContainerUnchanged = true
	if c, _ := f.StartContainer(ctx, id); c {
		t.Error("start should report unchanged")
	}
	if c, _ := f.StopContainer(ctx, id); c {
		t.Error("stop should report unchanged")
	}
	if c, _ := f.RestartContainer(ctx, id); !c {
		t.Error("restart always changes")
	}
	f.FailWith("StopContainer", dockerc.ErrConflict)
	if c, err := f.StopContainer(ctx, id); !errors.Is(err, dockerc.ErrConflict) || c {
		t.Errorf("injected failure: %v %v", c, err)
	}
}
