package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
)

type netEnv struct {
	gate     *fakeGate
	busy     *fakeBusy
	fake     *dockerctest.Fake
	provider *dockerctest.Provider
	audit    *recordingAudit
	h        http.Handler
}

func newNetEnv() *netEnv {
	e := &netEnv{gate: &fakeGate{status: "Running"}, busy: &fakeBusy{}, fake: &dockerctest.Fake{}, audit: &recordingAudit{}}
	e.provider = &dockerctest.Provider{Client: e.fake}
	e.h = NewHandler(testPort, testToken, NewAPI(WithNetworks(e.gate, e.provider, e.busy, e.audit)), nil)
	return e
}

func (e *netEnv) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, e.h, method, target, body)
}

func (e *netEnv) untouched() bool {
	return len(e.fake.Calls()) == 0 && len(e.provider.Profiles()) == 0 && len(e.audit.Rows()) == 0
}

const netBase = "/api/profiles/synthetic-a/networks"

func TestNetworkCreateSuccess(t *testing.T) {
	e := newNetEnv()
	e.fake.CreatedNetwork = dockerc.Network{ID: "0123456789ab", Name: "backend", Driver: "bridge", Scope: "local", Internal: true}
	rec := e.do(t, "POST", netBase, `{"name":"backend","internal":true,"subnet":"172.28.0.9/16"}`)
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if m["id"] != "0123456789ab" || m["name"] != "backend" || m["driver"] != "bridge" || m["scope"] != "local" ||
		m["internal"] != true || m["containers"] != float64(0) || len(m) != 6 {
		t.Fatalf("body = %v", m)
	}
	c := e.fake.CallsTo("CreateNetwork")
	// Host bits are masked before dockerc sees the subnet.
	if len(c) != 1 || fmt.Sprint(c[0].Args) != "[backend true 172.28.0.0/16]" {
		t.Fatalf("calls = %+v", c)
	}
	rows := e.audit.Rows()
	if len(rows) != 1 || rows[0].Action != "docker.network.create" || rows[0].Outcome != "success" || *rows[0].Detail != "backend" ||
		strings.Contains(fmt.Sprintf("%+v", rows[0]), "172.28") {
		t.Fatalf("rows = %+v", rows)
	}
	for _, b := range []string{`{"name":"a"}`, `{"name":"a","internal":null,"subnet":null}`, `{"name":"a","internal":false}`} {
		e.fake.Reset()
		if rec := e.do(t, "POST", netBase, b); rec.Code != 201 {
			t.Fatalf("%s: %d", b, rec.Code)
		}
		if c := e.fake.CallsTo("CreateNetwork"); len(c) != 1 || c[0].Args[1] != false || c[0].Args[2] != "" {
			t.Fatalf("%s: calls = %+v", b, c)
		}
	}
}

func TestNetworkCreateRefusals(t *testing.T) {
	tests := []struct {
		name, body string
		status     int
		code, key  string
	}{
		{"no body", ``, 400, "bad_request", ""},
		{"unknown key driver", `{"name":"a","driver":"overlay"}`, 400, "bad_request", ""},
		{"unknown key options", `{"name":"a","options":{"x":"y"}}`, 400, "bad_request", ""},
		{"unknown attachable", `{"name":"a","attachable":true}`, 400, "bad_request", ""},
		{"unknown ipv6", `{"name":"a","enableIPv6":true}`, 400, "bad_request", ""},
		{"unknown gateway", `{"name":"a","gateway":"172.28.0.1"}`, 400, "bad_request", ""},
		{"wrong case", `{"Name":"a"}`, 400, "bad_request", ""},
		{"duplicate key", `{"name":"a","name":"b"}`, 400, "bad_request", ""},
		{"internal string", `{"name":"a","internal":"yes"}`, 400, "bad_request", ""},
		{"internal number", `{"name":"a","internal":1}`, 400, "bad_request", ""},
		{"subnet number", `{"name":"a","subnet":1}`, 400, "bad_request", ""},
		{"subnet array", `{"name":"a","subnet":["172.28.0.0/16"]}`, 400, "bad_request", ""},
		{"trailing data", `{"name":"a"} {}`, 400, "bad_request", ""},
		{"oversize", `{"name":"a","subnet":"` + strings.Repeat("1", 5000) + `"}`, 400, "bad_request", ""},
		{"bad name", `{"name":"../a"}`, 422, "invalid_request", "name"},
		{"missing name", `{}`, 422, "invalid_request", "name"},
		{"zero width name", `{"name":"a\u200bb"}`, 422, "invalid_request", "name"},
		{"bidi name", `{"name":"a\u202eb"}`, 422, "invalid_request", "name"},
		{"long name", `{"name":"` + strings.Repeat("a", 129) + `"}`, 422, "invalid_request", "name"},
		{"reserved bridge", `{"name":"bridge"}`, 422, "invalid_request", "name"},
		{"reserved Host", `{"name":"Host"}`, 422, "invalid_request", "name"},
		{"reserved NONE", `{"name":"NONE"}`, 422, "invalid_request", "name"},
		{"reserved ingress", `{"name":"ingress"}`, 422, "invalid_request", "name"},
		{"colima network", `{"name":"a","subnet":"192.168.5.0/24"}`, 422, "invalid_request", "subnet"},
		{"garbage subnet", `{"name":"a","subnet":"garbage"}`, 422, "invalid_request", "subnet"},
		{"empty subnet", `{"name":"a","subnet":""}`, 422, "invalid_request", "subnet"},
		{"bare address", `{"name":"a","subnet":"172.28.0.1"}`, 422, "invalid_request", "subnet"},
		{"loopback", `{"name":"a","subnet":"127.0.0.0/8"}`, 422, "invalid_request", "subnet"},
		{"link local", `{"name":"a","subnet":"169.254.0.0/16"}`, 422, "invalid_request", "subnet"},
		{"multicast", `{"name":"a","subnet":"224.0.0.0/4"}`, 422, "invalid_request", "subnet"},
		{"v6 multicast", `{"name":"a","subnet":"ff00::/8"}`, 422, "invalid_request", "subnet"},
		{"slash zero", `{"name":"a","subnet":"0.0.0.0/0"}`, 422, "invalid_request", "subnet"},
		{"v6 slash zero", `{"name":"a","subnet":"::/0"}`, 422, "invalid_request", "subnet"},
		{"too small", `{"name":"a","subnet":"172.28.0.0/30"}`, 422, "invalid_request", "subnet"},
		{"too large", `{"name":"a","subnet":"172.0.0.0/7"}`, 422, "invalid_request", "subnet"},
		{"zero width subnet", `{"name":"a","subnet":"172.28.0.0/16\u200b"}`, 422, "invalid_request", "subnet"},
		{"padded subnet", `{"name":"a","subnet":" 172.28.0.0/16"}`, 422, "invalid_request", "subnet"},
		{"two subnets", `{"name":"a","subnet":"172.28.0.0/16,10.1.0.0/16"}`, 422, "invalid_request", "subnet"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newNetEnv()
			rec := e.do(t, "POST", netBase, tc.body)
			wantErr(t, rec, tc.status, tc.code)
			if tc.key != "" {
				d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
				f, _ := d["fields"].(map[string]any)
				if _, ok := f[tc.key]; !ok || len(f) != 1 {
					t.Fatalf("details = %v", d)
				}
			}
			for _, leak := range []string{"garbage", "127.0.0", "169.254", "../a", "172.28.0.1"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Fatalf("answer echoes %q: %s", leak, rec.Body.String())
				}
			}
			if !e.untouched() {
				t.Fatal("Docker, provider or audit reached")
			}
		})
	}
}

func TestNetworkInspect(t *testing.T) {
	e := newNetEnv()
	e.fake.NetworkDetail = dockerc.NetworkDetail{
		ID: "0123456789ab", Name: "backend", Driver: "bridge", Scope: "local", Internal: true,
		Subnets:    []dockerc.NetworkSubnet{{Subnet: "172.28.0.0/16", Gateway: "172.28.0.1"}},
		Containers: []dockerc.NetworkContainer{{Name: "web", IPv4Address: "172.28.0.2", IPv6Address: ""}},
	}
	rec := e.do(t, "GET", netBase+"/backend", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	subs := m["subnets"].([]any)
	cts := m["containers"].([]any)
	if m["id"] != "0123456789ab" || m["name"] != "backend" || m["driver"] != "bridge" || m["scope"] != "local" || m["internal"] != true ||
		len(subs) != 1 || subs[0].(map[string]any)["gateway"] != "172.28.0.1" || subs[0].(map[string]any)["subnet"] != "172.28.0.0/16" ||
		len(cts) != 1 || len(cts[0].(map[string]any)) != 3 || cts[0].(map[string]any)["ipv4Address"] != "172.28.0.2" || len(m) != 7 {
		t.Fatalf("body = %v", m)
	}
	// Nil slices become [].
	e.fake.NetworkDetail = dockerc.NetworkDetail{Name: "x"}
	rec = e.do(t, "GET", netBase+"/short", "")
	if !strings.Contains(rec.Body.String(), `"subnets":[]`) || !strings.Contains(rec.Body.String(), `"containers":[]`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if rows := e.audit.Rows(); len(rows) != 0 {
		t.Fatalf("inspect is a read and leaves no row: %+v", rows)
	}
	// By short ID and full ID.
	for _, id := range []string{"0123456789ab", strings.Repeat("ab", 32)} {
		if rec := e.do(t, "GET", netBase+"/"+id, ""); rec.Code != 200 {
			t.Fatalf("id %s: %d", id, rec.Code)
		}
	}
	// Works while a job runs; failures leave no row; a body is refused.
	*e.busy = fakeBusy{id: "j_1"}
	if rec := e.do(t, "GET", netBase+"/backend", ""); rec.Code != 200 {
		t.Fatalf("busy profile: %d", rec.Code)
	}
	e.fake.FailWith("InspectNetwork", dockerc.ErrObjectNotFound)
	rec = e.do(t, "GET", netBase+"/backend", "")
	wantErr(t, rec, 404, "not_found")
	if d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any); d["name"] != "backend" {
		t.Fatalf("details = %v", d)
	}
	if rows := e.audit.Rows(); len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
	wantErr(t, e.do(t, "GET", netBase+"/backend", `{"a":1}`), 400, "bad_request")
}

func TestNetworkInspectStillNeedsARunningKnownProfile(t *testing.T) {
	e := newNetEnv()
	e.gate.status = "Stopped"
	wantErr(t, e.do(t, "GET", netBase+"/a", ""), 409, "invalid_state")
	e = newNetEnv()
	e.gate.err = ErrProfileNotFound
	wantErr(t, e.do(t, "GET", netBase+"/a", ""), 404, "profile_not_found")
	if len(e.fake.Calls()) != 0 {
		t.Fatal("docker reached")
	}
}

func TestNetworkRemove(t *testing.T) {
	e := newNetEnv()
	rec := e.do(t, "DELETE", netBase+"/backend", "")
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	c := e.fake.CallsTo("RemoveNetwork")
	if len(c) != 1 || len(c[0].Args) != 1 || c[0].Args[0] != "backend" {
		t.Fatalf("calls = %+v", c)
	}
	rows := e.audit.Rows()
	if len(rows) != 1 || rows[0].Action != "docker.network.remove" || rows[0].Outcome != "success" || *rows[0].Detail != "backend" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestNetworkRemoveErrors(t *testing.T) {
	secret := "/home/user/.colima/synthetic"
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"in use", &dockerc.InUseError{Containers: 3}, 409, "conflict"},
		{"wrapped in use", fmt.Errorf("x %s: %w", secret, &dockerc.InUseError{Containers: 3}), 409, "conflict"},
		{"predefined", dockerc.ErrConflict, 409, "conflict"},
		{"unknown", dockerc.ErrObjectNotFound, 404, "not_found"},
		{"docker down", dockerc.ErrDockerUnavailable, 502, "docker_unavailable"},
		{"daemon", fmt.Errorf("%w %s", dockerc.ErrDaemon, secret), 502, "docker_unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newNetEnv()
			e.fake.FailWith("RemoveNetwork", tc.err)
			rec := e.do(t, "DELETE", netBase+"/backend", "")
			wantErr(t, rec, tc.status, tc.code)
			d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
			if tc.code == "conflict" || tc.code == "not_found" {
				if d["name"] != "backend" {
					t.Fatalf("details = %v", d)
				}
			}
			if strings.Contains(tc.name, "in use") {
				if d["containers"] != float64(3) {
					t.Fatalf("details = %v", d)
				}
			} else if _, has := d["containers"]; has {
				t.Fatalf("details = %v", d)
			}
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatal("daemon text leaked")
			}
			if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "failed" {
				t.Fatalf("rows = %+v", rows)
			}
		})
	}
}

const netPruneOK = `{"confirm":"delete-unused-networks"}`

func TestNetworkPrune(t *testing.T) {
	e := newNetEnv()
	n := 2
	e.fake.NetworkPrune = dockerc.NetworkPruneResult{Removed: []string{"a", "b"}, RemovedCount: &n, Complete: true}
	rec := e.do(t, "POST", netBase+"/prune", netPruneOK)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if len(m["removed"].([]any)) != 2 || m["removedCount"] != float64(2) || m["truncated"] != false || m["resultComplete"] != true || len(m) != 4 {
		t.Fatalf("body = %v", m)
	}
	e.fake.NetworkPrune = dockerc.NetworkPruneResult{Complete: true, RemovedCount: new(int)}
	rec = e.do(t, "POST", netBase+"/prune", " "+netPruneOK+"\n")
	if !strings.Contains(rec.Body.String(), `"removed":[]`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	rows := e.audit.Rows()
	if len(rows) != 2 || rows[0].Action != "docker.network.prune" || rows[0].Detail != nil || rows[0].Outcome != "success" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestNetworkPruneUnknownResultIsSuccess(t *testing.T) {
	e := newNetEnv()
	e.fake.NetworkPrune = dockerc.NetworkPruneResult{Removed: []string{}}
	rec := e.do(t, "POST", netBase+"/prune", netPruneOK)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if m["removedCount"] != nil || m["resultComplete"] != false || len(m["removed"].([]any)) != 0 {
		t.Fatalf("body = %v", m)
	}
	if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "success" {
		t.Fatalf("rows = %+v", rows)
	}
	n := 1500
	e.fake.NetworkPrune = dockerc.NetworkPruneResult{Removed: []string{"a"}, RemovedCount: &n, Truncated: true}
	m = decodeMap(t, e.do(t, "POST", netBase+"/prune", netPruneOK))
	if m["truncated"] != true || m["removedCount"] != float64(1500) {
		t.Fatalf("body = %v", m)
	}
}

func TestNetworkPruneNeedsTheConfirmation(t *testing.T) {
	for name, body := range map[string]string{
		"empty": ``, "spaces": "  \n", "empty object": `{}`, "null confirm": `{"confirm":null}`,
		"empty string": `{"confirm":""}`, "wrong string": `{"confirm":"yes"}`, "wrong case": `{"confirm":"DELETE-UNUSED-NETWORKS"}`,
		"trailing space":  `{"confirm":"delete-unused-networks "}`,
		"the volume text": `{"confirm":"delete-unused-volumes"}`,
	} {
		e := newNetEnv()
		rec := e.do(t, "POST", netBase+"/prune", body)
		wantErr(t, rec, 422, "invalid_request")
		d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
		if f, _ := d["fields"].(map[string]any); len(f) != 1 || f["confirm"] == nil {
			t.Fatalf("%s: details = %v", name, d)
		}
		if !e.untouched() {
			t.Fatalf("%s: Docker or the log reached", name)
		}
	}
	for name, body := range map[string]string{
		"extra key":  `{"confirm":"delete-unused-networks","filters":{"until":["1h"]}}`,
		"only extra": `{"filters":{}}`, "wrong case key": `{"Confirm":"delete-unused-networks"}`,
		"duplicate": `{"confirm":"x","confirm":"delete-unused-networks"}`, "not an object": `"delete-unused-networks"`,
		"array": `[]`, "null": `null`, "garbage": `x`, "wrong type": `{"confirm":1}`,
		"trailing data": netPruneOK + ` {}`, "oversize": `{"confirm":"` + strings.Repeat("a", 2000) + `"}`,
	} {
		e := newNetEnv()
		wantErr(t, e.do(t, "POST", netBase+"/prune", body), 400, "bad_request")
		if !e.untouched() {
			t.Fatalf("%s: Docker or the log reached", name)
		}
	}
	// GET never prunes: the path is a network name for GET.
	e := newNetEnv()
	rec := e.do(t, "GET", netBase+"/prune", "")
	if rec.Code != 200 || len(e.fake.CallsTo("PruneNetworks")) != 0 || len(e.fake.CallsTo("InspectNetwork")) != 1 {
		t.Fatalf("GET prune: %d, calls %+v", rec.Code, e.fake.Calls())
	}
}

// A create whose existence check or Docker call fails is a failed row and a
// 502, never a success.
func TestNetworkCreateDockerFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"docker 500": {fmt.Errorf("%w: unexpected status 500", dockerc.ErrDockerUnavailable), 502, "docker_unavailable"},
		"duplicate":  {dockerc.ErrConflict, 409, "conflict"},
		"rejected":   {dockerc.ErrInvalidRequest, 422, "invalid_request"},
		"daemon":     {fmt.Errorf("%w: status 500 /home/user/secret", dockerc.ErrDaemon), 502, "docker_unavailable"},
	} {
		e := newNetEnv()
		e.fake.FailWith("CreateNetwork", tc.err)
		rec := e.do(t, "POST", netBase, `{"name":"a"}`)
		wantErr(t, rec, tc.status, tc.code)
		if strings.Contains(rec.Body.String(), "/home/user") {
			t.Fatalf("%s: Docker text leaked", name)
		}
		if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "failed" {
			t.Fatalf("%s: rows = %+v", name, rows)
		}
	}
	e := newNetEnv()
	e.fake.FailWith("CreateNetwork", dockerc.ErrConflict)
	if d := decodeMap(t, e.do(t, "POST", netBase, `{"name":"a"}`))["error"].(map[string]any)["details"].(map[string]any); d["name"] != "a" {
		t.Fatalf("details = %v", d)
	}
}

func TestNetworkRoutesSharedRules(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{"POST", netBase, `{"name":"a"}`},
		{"DELETE", netBase + "/a", ""},
		{"POST", netBase + "/prune", netPruneOK},
	}
	setups := []struct {
		name   string
		setup  func(*netEnv)
		status int
		code   string
	}{
		{"unknown profile", func(e *netEnv) { e.gate.err = ErrProfileNotFound }, 404, "profile_not_found"},
		{"stopped", func(e *netEnv) { e.gate.status = "Stopped" }, 409, "invalid_state"},
		{"busy", func(e *netEnv) { *e.busy = fakeBusy{id: "j_1"} }, 409, "profile_busy"},
	}
	for _, r := range routes {
		for _, s := range setups {
			t.Run(r.method+" "+r.path+" "+s.name, func(t *testing.T) {
				e := newNetEnv()
				s.setup(e)
				wantErr(t, e.do(t, r.method, r.path, r.body), s.status, s.code)
				if !e.untouched() {
					t.Fatal("Docker, provider or audit reached")
				}
			})
		}
	}
	e := newNetEnv()
	e.provider.Err = dockerc.ErrDockerUnavailable
	wantErr(t, e.do(t, "DELETE", netBase+"/a", ""), 502, "docker_unavailable")
	e = newNetEnv()
	e.provider.Err = dockerc.ErrRuntimeNotDocker
	wantErr(t, e.do(t, "POST", netBase+"/prune", netPruneOK), 409, "invalid_state")
}

func TestNetworkPathParameterHostileRefs(t *testing.T) {
	for _, p := range []string{"%2e%2e", "a%2Fb", "a%0Ab", "a%00b", strings.Repeat("a", 129), "a%20b", "%2e", "a%5Cb", "a%3Fb", "a%23b", "a%E2%80%8Bb", "a%E2%80%AEb"} {
		for _, method := range []string{"GET", "DELETE"} {
			e := newNetEnv()
			rec := e.do(t, method, netBase+"/"+p, "")
			if rec.Code != 422 && rec.Code != 404 && rec.Code != 301 && rec.Code != 307 {
				t.Errorf("%s %s: %d", method, p, rec.Code)
			}
			if !e.untouched() {
				t.Errorf("%s %s reached Docker or the log", method, p)
			}
		}
	}
	// The prune path is not a network named prune for POST; DELETE is.
	e := newNetEnv()
	if rec := e.do(t, "DELETE", netBase+"/prune", ""); rec.Code != 204 {
		t.Fatalf("delete network named prune: %d", rec.Code)
	}
	if c := e.fake.CallsTo("RemoveNetwork"); len(c) != 1 || c[0].Args[0] != "prune" {
		t.Fatalf("calls = %+v", c)
	}
	wantErr(t, e.do(t, "PUT", netBase+"/a", ""), 405, "method_not_allowed")
	wantErr(t, e.do(t, "POST", netBase+"/a", `{}`), 405, "method_not_allowed")
	wantErr(t, e.do(t, "DELETE", netBase, ""), 405, "method_not_allowed")
}

func TestNetworkRoutesNeedToken(t *testing.T) {
	e := newNetEnv()
	for _, r := range [][2]string{{"POST", netBase}, {"GET", netBase + "/a"}, {"DELETE", netBase + "/a"}, {"POST", netBase + "/prune"}} {
		req := httptest.NewRequest(r[0], r[1], strings.NewReader(`{"name":"a"}`))
		req.Host = goodHost
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Errorf("%v: %d", r, rec.Code)
		}
	}
	if len(e.fake.Calls()) != 0 {
		t.Fatal("docker reached without a token")
	}
}
