package dockerc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	netFullID  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	netShortID = "0123456789ab"
)

// netDocker is a fake engine for the network methods. It records every request
// as "METHOD path?query" and every body.
type netDocker struct {
	mu         sync.Mutex
	reqs       []string
	bodies     []string
	exists     bool   // GET /networks/{ref} answers 200
	inspect    string // override of the inspect answer
	list       string // /containers/json answer
	createCode int
	createBody string
	deleteCode int
	pruneBody  string
}

const netInspectDefault = `{"Id":"` + netFullID + `","Name":"synthetic-net","Driver":"bridge","Scope":"local","Internal":false,
"Options":{"com.docker.network.bridge.name":"/home/user/br"},"Labels":{"secret":"synthetic-secret"},
"IPAM":{"Driver":"default","Config":[{"Subnet":"172.28.0.0/16","Gateway":"172.28.0.1"},{"Subnet":"junk","Gateway":"x"},{"Subnet":"fd00:1::/64","Gateway":"fd00:1::1"}]},
"Containers":{"c1c1c1c1c1c1c1c1":{"Name":"synthetic-web","EndpointID":"e","MacAddress":"02:42:ac:1c:00:02","IPv4Address":"172.28.0.2/16","IPv6Address":"fd00:1::2/64"}}}`

func (d *netDocker) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.reqs = append(d.reqs, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		d.bodies = append(d.bodies, string(b))
		d.mu.Unlock()
		p := r.URL.EscapedPath()
		switch {
		case r.Method == "GET" && strings.HasPrefix(p, "/v1.41/networks/"):
			if !d.exists {
				w.WriteHeader(404)
				return
			}
			if d.inspect != "" {
				io.WriteString(w, d.inspect)
				return
			}
			io.WriteString(w, netInspectDefault)
		case r.Method == "GET" && p == "/v1.41/containers/json":
			if d.list == "" {
				io.WriteString(w, `[]`)
				return
			}
			io.WriteString(w, d.list)
		case r.Method == "POST" && p == "/v1.41/networks/create":
			code := d.createCode
			if code == 0 {
				code = 201
			}
			w.WriteHeader(code)
			if d.createBody != "" {
				io.WriteString(w, d.createBody)
				return
			}
			io.WriteString(w, `{"Id":"`+netFullID+`","Warning":"/home/user/secret"}`)
		case r.Method == "DELETE":
			code := d.deleteCode
			if code == 0 {
				code = 204
			}
			w.WriteHeader(code)
		case r.Method == "POST" && p == "/v1.41/networks/prune":
			io.WriteString(w, d.pruneBody)
		default:
			w.WriteHeader(500)
		}
	})
}

func (d *netDocker) requests() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.reqs...)
}

func (d *netDocker) deletes() []string {
	var out []string
	for _, r := range d.requests() {
		if strings.HasPrefix(r, "DELETE") {
			out = append(out, r)
		}
	}
	return out
}

func newNetClient(t *testing.T, d *netDocker) *httpClient {
	t.Helper()
	return newHTTPClient(startFakeDocker(t, d.handler()))
}

func TestNormalizeSubnetTable(t *testing.T) {
	ok := map[string]string{
		"172.28.0.0/16":    "172.28.0.0/16",
		"10.0.0.0/8":       "10.0.0.0/8",
		"192.168.50.0/28":  "192.168.50.0/28",
		"fd00:1::/64":      "fd00:1::/64",
		"FD00:1::/64":      "fd00:1::/64",
		"2001:db8:1::/48":  "2001:db8:1::/48",
		"fd00:1::/120":     "fd00:1::/120",
		"172.28.0.5/16":    "172.28.0.0/16",  // host bits ignored: masked
		"192.168.1.200/24": "192.168.1.0/24", // host bits ignored: masked
		"fd00:1::dead/64":  "fd00:1::/64",    // host bits ignored: masked
		"172.028.0.0/16":   "",               // leading zeros are not accepted by netip
		"172.28.0.0/016":   "",
	}
	for in, want := range ok {
		got, valid := NormalizeSubnet(in)
		if (want != "") != valid || got != want {
			t.Errorf("NormalizeSubnet(%q) = %q, %v; want %q", in, got, valid, want)
		}
	}
	bad := []string{
		"", " ", "garbage", "172.28.0.0", "172.28.0.0/", "/16", "172.28.0.0/33", "172.28.0.0/-1", "172.28.0.0/16/16",
		"172.28.0.0/16 ", " 172.28.0.0/16", "172.28.0.0/16\n", "172.28.0.0 /16", "172.28.0.0/1 6",
		"0.0.0.0/0", "::/0", "0.0.0.0/8", "0.0.0.0/16", "::/64", "::/128",
		"127.0.0.0/8", "127.0.0.0/16", "127.0.0.1/24", "::1/128", "::1/64",
		"169.254.0.0/16", "169.254.1.0/24", "fe80::/64", "fe80::/10", "fe80::1/64",
		"224.0.0.0/8", "224.0.0.0/4", "239.1.0.0/16", "ff00::/8", "ff02::/64",
		"240.0.0.0/8", "250.1.0.0/16", "::ffff:10.0.0.0/104", "::ffff:0:0/96",
		"172.28.0.0/7", "172.28.0.0/29", "172.28.0.0/32", "10.0.0.0/30",
		"fd00::/47", "fd00:1::/121", "fd00:1::/128",
		"fe80::1%eth0/64", "172.28.0.0%x/16", "0::/8", "0::0/16",
		"172.28.0.0/16\x00", "172.28.0.0/16\u200b", "172.28.0.0/1\u0666", "\uff11\uff17\uff12.28.0.0/16",
		strings.Repeat("1", 70) + "/16", "172.28.0.0/16;rm", "172.28.0.0/16,10.0.0.0/8",
		"0x7f.0.0.0/8", "0177.0.0.0/8",
	}
	for _, in := range bad {
		if got, valid := NormalizeSubnet(in); valid {
			t.Errorf("NormalizeSubnet(%q) accepted as %q", in, got)
		}
	}
}

func TestCreateNetworkSendsBridgeOnlyExactBody(t *testing.T) {
	for _, tc := range []struct {
		name     string
		internal bool
		subnet   string
		want     string
	}{
		{"plain", false, "", `{"CheckDuplicate":true,"Driver":"bridge","Internal":false,"Name":"synthetic-net"}`},
		{"internal", true, "", `{"CheckDuplicate":true,"Driver":"bridge","Internal":true,"Name":"synthetic-net"}`},
		{"v4", false, "172.28.0.0/16", `{"CheckDuplicate":true,"Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.28.0.0/16"}]},"Internal":false,"Name":"synthetic-net"}`},
		{"v4 host bits", false, "172.28.0.9/16", `{"CheckDuplicate":true,"Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.28.0.0/16"}]},"Internal":false,"Name":"synthetic-net"}`},
		{"v6", true, "fd00:1::/64", `{"CheckDuplicate":true,"Driver":"bridge","EnableIPv6":true,"IPAM":{"Config":[{"Subnet":"fd00:1::/64"}]},"Internal":true,"Name":"synthetic-net"}`},
	} {
		d := &netDocker{}
		n, err := newNetClient(t, d).CreateNetwork(context.Background(), "synthetic-net", tc.internal, tc.subnet)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if n.ID != netShortID || n.Name != "synthetic-net" || n.Driver != "bridge" || n.Scope != "local" || n.Internal != tc.internal {
			t.Fatalf("%s: network = %+v", tc.name, n)
		}
		d.mu.Lock()
		got := d.bodies[len(d.bodies)-1]
		d.mu.Unlock()
		if got != tc.want {
			t.Fatalf("%s: body = %s", tc.name, got)
		}
		// Whatever the body carries, no driver other than bridge and none of
		// the options the brief excludes.
		var m map[string]any
		json.Unmarshal([]byte(got), &m)
		for _, k := range []string{"Options", "Attachable", "Ingress", "Labels", "ConfigFrom", "ConfigOnly", "Scope"} {
			if _, has := m[k]; has {
				t.Fatalf("%s: body has %s", tc.name, k)
			}
		}
	}
}

func TestCreateNetworkRefusesExistingNameWithoutCreate(t *testing.T) {
	d := &netDocker{exists: true}
	_, err := newNetClient(t, d).CreateNetwork(context.Background(), "synthetic-net", false, "")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	for _, r := range d.requests() {
		if strings.HasPrefix(r, "POST") {
			t.Fatal("create sent for an existing name")
		}
	}
}

// If the existence check cannot answer, nothing is created.
func TestCreateNetworkStopsWhenTheExistenceCheckFails(t *testing.T) {
	var posts atomic.Int32
	var mode atomic.Int32 // 0 = 500, 1 = hang, 2 = garbage 200, 3 = 400
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			w.WriteHeader(201)
			return
		}
		switch mode.Load() {
		case 1:
			select {
			case <-block:
			case <-r.Context().Done():
			}
		case 2:
			io.WriteString(w, `not json`)
		case 3:
			w.WriteHeader(400)
		default:
			w.WriteHeader(500)
		}
	}))
	c := newHTTPClient(sock)
	for _, m := range []int32{0, 2, 3} {
		mode.Store(m)
		if _, err := c.CreateNetwork(context.Background(), "n1", false, ""); err == nil || errors.Is(err, ErrConflict) {
			t.Fatalf("mode %d: err = %v", m, err)
		}
	}
	mode.Store(1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.CreateNetwork(ctx, "n1", false, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hang: err = %v", err)
	}
	if posts.Load() != 0 {
		t.Fatalf("%d create requests were sent", posts.Load())
	}
}

// An existing network with a different name but an ID that starts with the
// requested text is not "the same name", but it is not free either: Docker's
// lookup is by name then ID prefix, so the result is a conflict only when the
// engine answers 200; a wrong-name 200 answer is handled as not found by the
// lookup, and the create then goes ahead with CheckDuplicate.
func TestCreateNetworkWrongNameAnswerIsNotAConflict(t *testing.T) {
	d := &netDocker{exists: true, inspect: strings.Replace(netInspectDefault, "synthetic-net", "other-net", 1)}
	if _, err := newNetClient(t, d).CreateNetwork(context.Background(), "synthetic-net", false, ""); err != nil {
		t.Fatal(err)
	}
}

func TestCreateNetworkDockerErrorsAreTyped(t *testing.T) {
	secret := "/home/user/secret"
	for _, tc := range []struct {
		code int
		want error
	}{{403, ErrConflict}, {409, ErrConflict}, {500, ErrDaemon}, {400, ErrInvalidRequest}} {
		d := &netDocker{createCode: tc.code}
		_, err := newNetClient(t, d).CreateNetwork(context.Background(), "n1", false, "")
		if !errors.Is(err, tc.want) || strings.Contains(err.Error(), secret) {
			t.Errorf("%d: err = %v", tc.code, err)
		}
	}
}

// A 201 whose answer cannot be read still created the network: success, with
// no ID rather than a failure that invites a retry.
func TestCreateNetworkUnreadableAnswerAfter201IsSuccess(t *testing.T) {
	for _, body := range []string{`not json`, `{"Id":`, `{"Id":"../x"}`, `{"Id":12}`, `{}`} {
		d := &netDocker{createBody: body}
		n, err := newNetClient(t, d).CreateNetwork(context.Background(), "n1", false, "")
		if err != nil || n.ID != "" || n.Name != "n1" {
			t.Errorf("%q: %v %+v", body, err, n)
		}
	}
}

func TestInspectNetworkIsBoundedAndCarriesNoFreeText(t *testing.T) {
	d := &netDocker{exists: true, list: `[{"Id":"c1c1c1c1c1c1c1c1","Names":["/synthetic-web"]},{"Id":"d2d2d2d2d2d2d2d2","Names":["/synthetic-stopped"]},{"Id":"e3e3e3e3e3e3e3e3","Names":["/bad name\n"]}]`}
	got, err := newNetClient(t, d).InspectNetwork(context.Background(), "synthetic-net")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != netShortID || got.Name != "synthetic-net" || got.Driver != "bridge" || got.Scope != "local" || got.Internal {
		t.Fatalf("detail = %+v", got)
	}
	if len(got.Subnets) != 2 || got.Subnets[0] != (NetworkSubnet{"172.28.0.0/16", "172.28.0.1"}) || got.Subnets[1] != (NetworkSubnet{"fd00:1::/64", "fd00:1::1"}) {
		t.Fatalf("subnets = %+v", got.Subnets)
	}
	// Running container with addresses, stopped one without, bad name as short id.
	want := []NetworkContainer{
		{"e3e3e3e3e3e3", "", ""},
		{"synthetic-stopped", "", ""},
		{"synthetic-web", "172.28.0.2", "fd00:1::2"},
	}
	if fmt.Sprint(got.Containers) != fmt.Sprint(want) {
		t.Fatalf("containers = %+v", got.Containers)
	}
	flat := fmt.Sprintf("%+v", got)
	for _, leak := range []string{"/home/user", "synthetic-secret", "com.docker", "02:42", "junk"} {
		if strings.Contains(flat, leak) {
			t.Fatalf("detail leaks %q: %s", leak, flat)
		}
	}
	// The container filter is JSON built from the full ID, stopped included.
	var seen bool
	for _, r := range d.requests() {
		if strings.Contains(r, "/containers/json") {
			v, _ := url.ParseQuery(r[strings.Index(r, "?")+1:])
			if v.Get("all") != "1" || v.Get("filters") != `{"network":["`+netFullID+`"]}` {
				t.Fatalf("filter query = %q", r)
			}
			seen = true
		}
	}
	if !seen {
		t.Fatal("containers were not listed")
	}
}

func TestInspectNetworkNoneAttachedGivesEmptyLists(t *testing.T) {
	d := &netDocker{exists: true, inspect: `{"Id":"` + netFullID + `","Name":"n","Driver":"bridge","Scope":"local","IPAM":null,"Containers":null}`}
	got, err := newNetClient(t, d).InspectNetwork(context.Background(), "n")
	if err != nil || got.Subnets == nil || got.Containers == nil || len(got.Subnets) != 0 || len(got.Containers) != 0 {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestInspectAndRemoveByShortIDAndFullID(t *testing.T) {
	for _, ref := range []string{netShortID, netFullID} {
		d := &netDocker{exists: true}
		c := newNetClient(t, d)
		if _, err := c.InspectNetwork(context.Background(), ref); err != nil {
			t.Fatalf("inspect %s: %v", ref, err)
		}
		// no containers attached in this fake: use a client where the list is empty
		d2 := &netDocker{exists: true, inspect: `{"Id":"` + netFullID + `","Name":"synthetic-net","Driver":"bridge","Scope":"local","Containers":{}}`}
		if err := newNetClient(t, d2).RemoveNetwork(context.Background(), ref); err != nil {
			t.Fatalf("remove %s: %v", ref, err)
		}
		// The DELETE goes to the full ID Docker gave, never to the typed text.
		if got := d2.deletes(); len(got) != 1 || got[0] != "DELETE /v1.41/networks/"+netFullID+"?" {
			t.Fatalf("deletes = %v", got)
		}
	}
}

// A short text that is not a name and not a 12 or 64 character hex ID does not
// reach a network through Docker's ID-prefix lookup.
func TestNetworkLookupRefusesPrefixMatchesOfOtherLengths(t *testing.T) {
	for _, ref := range []string{"0123", "01234567", "0123456789a", netFullID[:20], netFullID[:63], strings.ToUpper(netShortID)} {
		d := &netDocker{exists: true, inspect: `{"Id":"` + netFullID + `","Name":"synthetic-net","Driver":"bridge","Scope":"local"}`}
		c := newNetClient(t, d)
		if _, err := c.InspectNetwork(context.Background(), ref); !errors.Is(err, ErrObjectNotFound) {
			t.Errorf("inspect %q: %v", ref, err)
		}
		if err := c.RemoveNetwork(context.Background(), ref); !errors.Is(err, ErrObjectNotFound) {
			t.Errorf("remove %q: %v", ref, err)
		}
		if len(d.deletes()) != 0 {
			t.Errorf("%q: DELETE sent", ref)
		}
	}
}

func TestRemoveNetworkUnknownSendsNoDelete(t *testing.T) {
	d := &netDocker{}
	if err := newNetClient(t, d).RemoveNetwork(context.Background(), "gone"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("err = %v", err)
	}
	if len(d.deletes()) != 0 {
		t.Fatal("DELETE sent")
	}
}

// The predefined networks are refused by name in any letter case and by their
// ID (reached through the ID form), with no DELETE and no container listing.
func TestRemoveNetworkRefusesPredefinedWithoutDelete(t *testing.T) {
	for _, tc := range []struct{ ref, name string }{
		{"bridge", "bridge"}, {"host", "host"}, {"none", "none"},
		{netShortID, "bridge"}, {netFullID, "host"}, {netShortID, "none"},
		{"Bridge", "Bridge"}, {"HOST", "HOST"}, {"None", "None"},
	} {
		d := &netDocker{exists: true, inspect: `{"Id":"` + netFullID + `","Name":"` + tc.name + `","Driver":"bridge","Scope":"local"}`}
		err := newNetClient(t, d).RemoveNetwork(context.Background(), tc.ref)
		var in *InUseError
		if !errors.Is(err, ErrConflict) || errors.As(err, &in) {
			t.Errorf("%+v: err = %v", tc, err)
		}
		if len(d.deletes()) != 0 {
			t.Errorf("%+v: DELETE sent", tc)
		}
	}
	// The ingress network too.
	d := &netDocker{exists: true, inspect: `{"Id":"` + netFullID + `","Name":"ingress","Driver":"overlay","Scope":"swarm","Ingress":true}`}
	if err := newNetClient(t, d).RemoveNetwork(context.Background(), "ingress"); !errors.Is(err, ErrConflict) || len(d.deletes()) != 0 {
		t.Fatalf("ingress: %v %v", err, d.deletes())
	}
}

func TestRemoveNetworkInUseSendsNoDeleteAndCountsBothSources(t *testing.T) {
	// One running container (inspect) and one stopped (list only), the running
	// one in both: two containers.
	d := &netDocker{exists: true, list: `[{"Id":"c1c1c1c1c1c1c1c1","Names":["/synthetic-web"]},{"Id":"d2d2d2d2d2d2d2d2","Names":["/synthetic-stopped"]}]`}
	err := newNetClient(t, d).RemoveNetwork(context.Background(), "synthetic-net")
	var in *InUseError
	if !errors.Is(err, ErrConflict) || !errors.As(err, &in) || in.Containers != 2 {
		t.Fatalf("err = %v", err)
	}
	if len(d.deletes()) != 0 {
		t.Fatalf("DELETE sent: %v", d.deletes())
	}
	// Only a stopped container: still refused.
	d = &netDocker{exists: true, inspect: `{"Id":"` + netFullID + `","Name":"n","Driver":"bridge","Scope":"local"}`, list: `[{"Id":"d2d2d2d2d2d2d2d2","Names":["/s"]}]`}
	err = newNetClient(t, d).RemoveNetwork(context.Background(), "n")
	if !errors.As(err, &in) || in.Containers != 1 || len(d.deletes()) != 0 {
		t.Fatalf("stopped only: %v", err)
	}
}

func TestRemoveNetworkNeverForcesAndMapsDockerRefusals(t *testing.T) {
	empty := `{"Id":"` + netFullID + `","Name":"n","Driver":"bridge","Scope":"local"}`
	d := &netDocker{exists: true, inspect: empty}
	if err := newNetClient(t, d).RemoveNetwork(context.Background(), "n"); err != nil {
		t.Fatal(err)
	}
	for _, r := range d.requests() {
		if strings.Contains(r, "force") {
			t.Fatalf("force sent: %q", r)
		}
	}
	for code, want := range map[int]error{403: ErrConflict, 409: ErrConflict, 404: ErrObjectNotFound, 500: ErrDaemon} {
		d := &netDocker{exists: true, inspect: empty, deleteCode: code}
		if err := newNetClient(t, d).RemoveNetwork(context.Background(), "n"); !errors.Is(err, want) {
			t.Errorf("%d: err = %v", code, err)
		}
	}
}

func pruneNets(t *testing.T, body string) (NetworkPruneResult, []string, error) {
	t.Helper()
	d := &netDocker{pruneBody: body}
	res, err := newNetClient(t, d).PruneNetworks(context.Background())
	return res, d.requests(), err
}

func TestPruneNetworksSendsNoFiltersAndCleansNames(t *testing.T) {
	res, reqs, err := pruneNets(t, `{"NetworksDeleted":["b","a","../evil","`+strings.Repeat("z", 129)+`"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0] != "POST /v1.41/networks/prune?" {
		t.Fatalf("requests = %v", reqs)
	}
	if strings.Join(res.Removed, ",") != "a,b" || *res.RemovedCount != 4 || !res.Truncated || !res.Complete {
		t.Fatalf("result = %+v", res)
	}
	res, _, _ = pruneNets(t, `{"NetworksDeleted":null}`)
	if res.Removed == nil || len(res.Removed) != 0 || *res.RemovedCount != 0 || res.Truncated || !res.Complete {
		t.Fatalf("result = %+v", res)
	}
}

func TestPruneNetworksCapsTheListAndSaysSo(t *testing.T) {
	var names []string
	for i := 0; i < maxPrunedNames+5; i++ {
		names = append(names, fmt.Sprintf("n%05d", i))
	}
	j, _ := json.Marshal(map[string]any{"NetworksDeleted": names})
	res, _, err := pruneNets(t, string(j))
	if err != nil || len(res.Removed) != maxPrunedNames || *res.RemovedCount != maxPrunedNames+5 || !res.Truncated {
		t.Fatalf("err = %v, %d names, count %v, truncated %v", err, len(res.Removed), res.RemovedCount, res.Truncated)
	}
}

// After a 200 the networks are gone: an unreadable answer is success with an
// unknown result, never an error.
func TestPruneNetworksUnreadableAnswerAfter200IsSuccess(t *testing.T) {
	for name, body := range map[string]string{
		"not json": `not json`, "truncated": `{"NetworksDeleted":["a"`, "wrong type": `{"NetworksDeleted":[1,2]}`,
		"empty": ``, "not array": `{"NetworksDeleted":"a"}`,
	} {
		res, _, err := pruneNets(t, body)
		if err != nil {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		if res.Complete || res.RemovedCount != nil || len(res.Removed) != 0 || res.Removed == nil {
			t.Errorf("%s: result = %+v", name, res)
		}
	}
	d := &netDocker{pruneBody: `{"NetworksDeleted":["` + strings.Repeat("a", 200) + `"]}`}
	c := newNetClient(t, d)
	c.maxBody = 50
	res, err := c.PruneNetworks(context.Background())
	if err != nil || res.Complete || res.RemovedCount != nil {
		t.Fatalf("oversize: %v %+v", err, res)
	}
}

func TestPruneNetworksErrorStatusesStayErrors(t *testing.T) {
	for _, code := range []int{500, 404, 409} {
		sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		if _, err := newHTTPClient(sock).PruneNetworks(context.Background()); err == nil {
			t.Errorf("status %d gave no error", code)
		}
	}
}

// Every network write validates its own arguments: hostile input never
// reaches the socket.
func TestNetworkWritesRefuseHostileInputOnTheRealClient(t *testing.T) {
	d := &netDocker{exists: true}
	c := newNetClient(t, d)
	ctx := context.Background()
	hostile := append(hostileNames(), strings.Repeat("a", 129), "a\u200bb", "a\u202eb", "\ufeffa", "a b", "a;b", "a$(x)", "a`x`", "a\tb", "a%00b", "a%0Ab")
	for _, h := range hostile {
		if err := c.RemoveNetwork(ctx, h); !errors.Is(err, ErrBadName) {
			t.Errorf("RemoveNetwork(%q) = %v", h, err)
		}
		if _, err := c.InspectNetwork(ctx, h); !errors.Is(err, ErrBadName) {
			t.Errorf("InspectNetwork(%q) = %v", h, err)
		}
		if _, err := c.CreateNetwork(ctx, h, false, ""); !errors.Is(err, ErrBadName) {
			t.Errorf("CreateNetwork(%q) = %v", h, err)
		}
	}
	for _, s := range []string{
		" ", "garbage", "0.0.0.0/0", "127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/4", "::/0", "ff00::/8", "fe80::/10",
		"172.28.0.0", "172.28.0.0/16 ", "172.28.0.0/16\n", "172.28.0.0/16\u200b", "172.28.0.0/7", "172.28.0.0/29",
		"fe80::1%eth0/64", "::ffff:10.0.0.0/104", "172.28.0.0/16\x00", "?x=1", "../x/16",
	} {
		if _, err := c.CreateNetwork(ctx, "ok", false, s); !errors.Is(err, ErrBadName) {
			t.Errorf("CreateNetwork subnet %q = %v", s, err)
		}
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("requests reached the socket: %v", got)
	}
}
