package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
)

// seqProvider hands out one client per For call: the route's read is the
// first, the launcher's in-slot read the second.
type seqProvider struct {
	mu      sync.Mutex
	clients []dockerc.Client
	errs    []error
	calls   int
}

func (p *seqProvider) For(context.Context, string) (dockerc.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.calls
	p.calls++
	return p.clients[min(n, len(p.clients)-1)], p.errs[min(n, len(p.errs)-1)]
}

func (p *seqProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func nonEmptyFake() *dockerctest.Fake {
	return &dockerctest.Fake{ContainerList: []dockerc.Container{{ID: "aaaaaaaaaaaa", Name: "synthetic-web", State: "exited"}}}
}

func rowDetail(t *testing.T, ts *testServer) string {
	t.Helper()
	eventually(t, "action row", func() bool { return len(ts.actions(t, "dev")) == 1 })
	row := ts.actions(t, "dev")[0]
	if row.Action != "delete" || row.Outcome != "failed" || row.Detail == nil {
		t.Fatalf("row %#v", row)
	}
	if strings.Contains(*row.Detail, "colima exited") {
		t.Fatalf("row claims colima ran: %q", *row.Detail)
	}
	return *row.Detail
}

// The route sees an empty engine, a container appears before the slot's own
// count: no delete vector may reach colima and the row says why.
func TestDeleteRefusesWhenDockerFillsBetweenRouteAndSlot(t *testing.T) {
	r := &fakeRunner{list: devRunningJSON, script: []string{"x"}}
	a := testApp(t, r)
	prov := &seqProvider{clients: []dockerc.Client{&dockerctest.Fake{}, nonEmptyFake()}, errs: []error{nil}}
	a.containers = prov
	ts := serve(t, a)
	if code, body := ts.do(t, "POST", "/api/profiles/dev/delete", `{"confirm":"dev","deleteData":true}`); code != 202 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if got := rowDetail(t, ts); got != "not run: docker not empty; with runtime data" {
		t.Fatalf("detail %q", got)
	}
	if n := r.streamCount(); n != 0 || prov.count() != 2 {
		t.Fatalf("colima streams %d, docker reads %d", n, prov.count())
	}
}

// The slot's count fails closed when Docker cannot be read at that moment.
func TestDeleteRefusesWhenDockerUnreadableInTheSlot(t *testing.T) {
	r := &fakeRunner{list: devRunningJSON, script: []string{"x"}}
	a := testApp(t, r)
	prov := &seqProvider{clients: []dockerc.Client{&dockerctest.Fake{}, &dockerctest.Fake{}},
		errs: []error{nil, dockerc.ErrDockerUnavailable}}
	a.containers = prov
	ts := serve(t, a)
	if code, body := ts.do(t, "POST", "/api/profiles/dev/delete", `{"confirm":"dev"}`); code != 202 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if got := rowDetail(t, ts); got != "not run: could not check docker contents" {
		t.Fatalf("detail %q", got)
	}
	if n := r.streamCount(); n != 0 {
		t.Fatalf("colima streams %d", n)
	}
}

// The status the slot reads can differ from the route's: broken at the route,
// running with containers in the slot.
func TestDeleteRechecksStatusInsideTheSlotInProduction(t *testing.T) {
	r := &fakeRunner{lists: []string{devBrokenJSON, devRunningJSON}, script: []string{"x"}}
	a := testApp(t, r)
	// Broken at the route, whose best-effort read cannot reach Docker; running
	// with a container in the slot.
	a.containers = &seqProvider{clients: []dockerc.Client{nonEmptyFake()}, errs: []error{dockerc.ErrVMNotRunning, nil}}
	ts := serve(t, a)
	if code, body := ts.do(t, "POST", "/api/profiles/dev/delete", `{"confirm":"dev"}`); code != 202 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if got := rowDetail(t, ts); got != "not run: docker not empty" {
		t.Fatalf("detail %q", got)
	}
	if n := r.streamCount(); n != 0 {
		t.Fatalf("colima streams %d", n)
	}
}

func TestDeleteRunsForEmptyRunningDockerAndBrokenProfiles(t *testing.T) {
	for name, list := range map[string]string{"running": devRunningJSON, "broken": devBrokenJSON} {
		r := &fakeRunner{list: list, script: []string{"done"}}
		a := testApp(t, r)
		a.containers = &seqProvider{clients: []dockerc.Client{&dockerctest.Fake{}}, errs: []error{nil}}
		sub, err := a.hub.Subscribe("", "")
		if err != nil {
			t.Fatal(err)
		}
		if rec := postJSON(t, a.handler(), "/api/profiles/dev/delete", `{"confirm":"dev"}`); rec.Code != 202 {
			t.Fatalf("%s: delete %d %s", name, rec.Code, rec.Body.String())
		}
		waitFinished(t, sub)
		sub.Close()
		if got := r.streams; len(got) != 1 || strings.Join(got[0], " ") != "delete -p dev --force" {
			t.Fatalf("%s: streams %v", name, got)
		}
	}
}

func TestDeleteOfAStoppedProfileIsRefusedAsCannotVerify(t *testing.T) {
	r := &fakeRunner{list: devStoppedJSON, script: []string{"x"}}
	a := testApp(t, r)
	ts := serve(t, a)
	code, body := ts.do(t, "POST", "/api/profiles/dev/delete", `{"confirm":"dev"}`)
	if code != 409 || !strings.Contains(body, "cannot_verify") || r.streamCount() != 0 {
		t.Fatalf("%d %s", code, body)
	}
}

func TestContentsRouteIsWired(t *testing.T) {
	r := &fakeRunner{list: devRunningJSON}
	a := testApp(t, r)
	a.containers = &seqProvider{clients: []dockerc.Client{nonEmptyFake()}, errs: []error{nil}}
	ts := serve(t, a)
	code, body := ts.do(t, "GET", "/api/profiles/dev/contents", "")
	if code != 200 || body != `{"applicable":true,"containers":1,"images":0,"volumes":0}`+"\n" {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := ts.do(t, "GET", "/api/profiles/..%2Fx/contents", ""); code != 404 {
		t.Fatalf("hostile: %d", code)
	}
	// A read: no job and no row.
	if rows := ts.actions(t, "dev"); len(rows) != 0 || r.streamCount() != 0 {
		t.Fatalf("rows %v streams %d", rows, r.streamCount())
	}
}

// dockerSocket serves the given number of containers and no image or volume.
func dockerSocket(t *testing.T, containers int) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.41/containers/json":
			w.Write([]byte(`[` + strings.TrimSuffix(strings.Repeat(`{"Id":"aaaaaaaaaaaa"},`, containers), ",") + `]`))
		case "/v1.41/images/json":
			w.Write([]byte(`[]`))
		case "/v1.41/volumes":
			w.Write([]byte(`{"Volumes":null}`))
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return sock
}

// The fake colima answers `status` with the same line as `list`, so the line
// carries the runtime and the Docker socket for the production resolver.
func runtimeList(runtime, sock string) string {
	return `{"name":"dev","status":"Running","runtime":"` + runtime + `","docker_socket":"unix://` + sock + `"}` + "\n"
}

func TestProductionResolverCountsDockerPlusK3s(t *testing.T) {
	r := &fakeRunner{list: runtimeList("docker+k3s", dockerSocket(t, 2)), script: []string{"x"}}
	a := testApp(t, r)
	ts := serve(t, a)
	code, body := ts.do(t, "GET", "/api/profiles/dev/contents", "")
	if code != 200 || body != `{"applicable":true,"containers":2,"images":0,"volumes":0}`+"\n" {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := ts.do(t, "POST", "/api/profiles/dev/delete", `{"confirm":"dev"}`); code != 409 || !strings.Contains(body, "docker_not_empty") {
		t.Fatalf("delete: %d %s", code, body)
	}
	if r.streamCount() != 0 {
		t.Fatal("colima ran")
	}
}

func TestProductionResolverEmptyDockerPlusK3sIsDeletable(t *testing.T) {
	r := &fakeRunner{list: runtimeList("docker+k3s", dockerSocket(t, 0)), script: []string{"done"}}
	a := testApp(t, r)
	sub, err := a.hub.Subscribe("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if rec := postJSON(t, a.handler(), "/api/profiles/dev/delete", `{"confirm":"dev"}`); rec.Code != 202 {
		t.Fatalf("delete %d %s", rec.Code, rec.Body.String())
	}
	waitFinished(t, sub)
	if len(r.streams) != 1 {
		t.Fatalf("streams %v", r.streams)
	}
}

func TestProductionResolverUnknownRuntimeFailsClosed(t *testing.T) {
	for _, rt := range []string{"", "podman", "docker-ce"} {
		r := &fakeRunner{list: runtimeList(rt, dockerSocket(t, 0)), script: []string{"x"}}
		a := testApp(t, r)
		ts := serve(t, a)
		if code, body := ts.do(t, "GET", "/api/profiles/dev/contents", ""); code != 502 {
			t.Fatalf("%q contents: %d %s", rt, code, body)
		}
		if code, body := ts.do(t, "POST", "/api/profiles/dev/delete", `{"confirm":"dev"}`); code != 502 {
			t.Fatalf("%q delete: %d %s", rt, code, body)
		}
		if r.streamCount() != 0 {
			t.Fatalf("%q: colima ran", rt)
		}
	}
}

// The route's read is fine; in the slot the runtime is unknown: refused, and
// the row says it could not check.
func TestDeleteRefusesInTheSlotOnAnUnknownRuntime(t *testing.T) {
	sock := dockerSocket(t, 0)
	r := &fakeRunner{list: runtimeList("podman", sock), script: []string{"x"}}
	a := testApp(t, r)
	good := &dockerctest.Fake{}
	strict := dockerc.NewProvider(dockerc.NewColimaResolver(r))
	var mu sync.Mutex
	calls := 0
	a.containers = providerFunc(func(ctx context.Context, p string) (dockerc.Client, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			return good, nil
		}
		return strict.For(ctx, p)
	})
	ts := serve(t, a)
	if code, body := ts.do(t, "POST", "/api/profiles/dev/delete", `{"confirm":"dev"}`); code != 202 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if got := rowDetail(t, ts); got != "not run: could not check docker contents" {
		t.Fatalf("detail %q", got)
	}
	if r.streamCount() != 0 {
		t.Fatal("colima ran")
	}
}

type providerFunc func(ctx context.Context, profile string) (dockerc.Client, error)

func (f providerFunc) For(ctx context.Context, profile string) (dockerc.Client, error) {
	return f(ctx, profile)
}
