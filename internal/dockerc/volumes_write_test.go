package dockerc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// volDocker is a fake engine for the volume write methods. It records every
// request as "METHOD path?query" (path as sent, escaped).
type volDocker struct {
	mu         sync.Mutex
	reqs       []string
	bodies     []string
	exists     bool   // GET /volumes/{name} answers 200
	users      string // /containers/json answer
	createCode int
	deleteCode int
	pruneBody  string
}

func (d *volDocker) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.reqs = append(d.reqs, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		d.bodies = append(d.bodies, string(b))
		d.mu.Unlock()
		p := r.URL.EscapedPath()
		switch {
		case r.Method == "GET" && strings.HasPrefix(p, "/v1.41/volumes/"):
			if !d.exists {
				w.WriteHeader(404)
				return
			}
			io.WriteString(w, `{"Name":"x","Driver":"local","Mountpoint":"/home/user/secret","CreatedAt":"2026-04-11T18:02:55Z","Labels":{"k":"v"},"Options":{"device":"/home/user/x"}}`)
		case r.Method == "GET" && p == "/v1.41/containers/json":
			if d.users == "" {
				io.WriteString(w, `[]`)
				return
			}
			io.WriteString(w, d.users)
		case r.Method == "GET" && p == "/v1.41/system/df":
			io.WriteString(w, `{"Volumes":[{"Name":"x","UsageData":{"Size":42,"RefCount":1}}]}`)
		case r.Method == "POST" && p == "/v1.41/volumes/create":
			code := d.createCode
			if code == 0 {
				code = 201
			}
			w.WriteHeader(code)
			io.WriteString(w, `{"Name":"x","Driver":"local","Mountpoint":"/home/user/secret","CreatedAt":"2026-04-11T18:02:55Z"}`)
		case r.Method == "DELETE":
			code := d.deleteCode
			if code == 0 {
				code = 204
			}
			w.WriteHeader(code)
		case r.Method == "POST" && p == "/v1.41/volumes/prune":
			io.WriteString(w, d.pruneBody)
		default:
			w.WriteHeader(500)
		}
	})
}

func (d *volDocker) requests() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.reqs...)
}

func newVolClient(t *testing.T, d *volDocker) *httpClient {
	t.Helper()
	return newHTTPClient(startFakeDocker(t, d.handler()))
}

func TestRemoveVolumeNeverForces(t *testing.T) {
	d := &volDocker{exists: true}
	c := newVolClient(t, d)
	if err := c.RemoveVolume(context.Background(), "synthetic-vol"); err != nil {
		t.Fatal(err)
	}
	got := d.requests()
	last := got[len(got)-1]
	if last != "DELETE /v1.41/volumes/synthetic-vol?" {
		t.Fatalf("delete request = %q (all: %v)", last, got)
	}
	for _, r := range got {
		if strings.Contains(r, "force") {
			t.Fatalf("force sent: %q", r)
		}
	}
}

func TestRemoveVolumeInUseSendsNoDelete(t *testing.T) {
	d := &volDocker{exists: true, users: `[{"Id":"aaaaaaaaaaaaaaaa","Names":["/synthetic-web"]},{"Id":"bbbb","Names":[]}]`}
	c := newVolClient(t, d)
	err := c.RemoveVolume(context.Background(), "synthetic-vol")
	var in *InUseError
	if !errors.Is(err, ErrConflict) || !errors.As(err, &in) || in.Containers != 2 {
		t.Fatalf("err = %v", err)
	}
	for _, r := range d.requests() {
		if strings.HasPrefix(r, "DELETE") {
			t.Fatalf("DELETE sent for an in-use volume: %v", d.requests())
		}
	}
	// The container filter is JSON built from the validated name, stopped included.
	var seenFilter bool
	for _, r := range d.requests() {
		if strings.Contains(r, "/containers/json") {
			q := r[strings.Index(r, "?")+1:]
			v, _ := url.ParseQuery(q)
			if v.Get("all") != "1" || v.Get("filters") != `{"volume":["synthetic-vol"]}` {
				t.Fatalf("filter query = %q", q)
			}
			seenFilter = true
		}
	}
	if !seenFilter {
		t.Fatal("containers were not checked")
	}
}

func TestRemoveVolumeDockerRefusalAfterCheck(t *testing.T) {
	// A container that appears between check and delete: Docker's own 409.
	d := &volDocker{exists: true, deleteCode: 409}
	err := newVolClient(t, d).RemoveVolume(context.Background(), "v1")
	var in *InUseError
	if !errors.Is(err, ErrConflict) || errors.As(err, &in) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveUnknownVolume(t *testing.T) {
	d := &volDocker{}
	err := newVolClient(t, d).RemoveVolume(context.Background(), "v1")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("err = %v", err)
	}
	for _, r := range d.requests() {
		if strings.HasPrefix(r, "DELETE") {
			t.Fatal("DELETE sent for unknown volume")
		}
	}
}

func TestCreateVolumeRefusesExistingName(t *testing.T) {
	d := &volDocker{exists: true}
	_, err := newVolClient(t, d).CreateVolume(context.Background(), "v1", nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	for _, r := range d.requests() {
		if strings.HasPrefix(r, "POST") {
			t.Fatal("create sent for an existing name")
		}
	}
}

func TestCreateVolumeSendsLocalDriverAndLabelsOnly(t *testing.T) {
	d := &volDocker{}
	v, err := newVolClient(t, d).CreateVolume(context.Background(), "synthetic-vol", map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "synthetic-vol" || v.Driver != "local" || v.Created.IsZero() {
		t.Fatalf("volume = %+v", v)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if got := d.bodies[len(d.bodies)-1]; got != `{"Driver":"local","Labels":{"k":"v"},"Name":"synthetic-vol"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestInspectVolumeHasNoPathAndGetsUsersAndSize(t *testing.T) {
	d := &volDocker{exists: true, users: `[{"Id":"aaaaaaaaaaaaaaaa","Names":["/synthetic-web"]}]`}
	got, err := newVolClient(t, d).InspectVolume(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "x" || got.Driver != "local" || got.Labels["k"] != "v" ||
		len(got.ContainerNames) != 1 || got.ContainerNames[0] != "synthetic-web" ||
		got.SizeBytes == nil || *got.SizeBytes != 42 {
		t.Fatalf("detail = %+v", got)
	}
	for _, f := range []string{"/home/user"} {
		if strings.Contains(strings.Join(append(got.ContainerNames, got.Name, got.Driver), " "), f) {
			t.Fatal("path leaked")
		}
	}
}

func TestPruneVolumesSendsNoFiltersAndCleansNames(t *testing.T) {
	d := &volDocker{pruneBody: `{"VolumesDeleted":["b","a","../evil","` + strings.Repeat("z", 129) + `"],"SpaceReclaimed":-5}`}
	res, err := newVolClient(t, d).PruneVolumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := d.requests(); len(got) != 1 || got[0] != "POST /v1.41/volumes/prune?" {
		t.Fatalf("requests = %v", got)
	}
	if strings.Join(res.Removed, ",") != "a,b" || res.ReclaimedBytes != 0 {
		t.Fatalf("result = %+v", res)
	}
	d = &volDocker{pruneBody: `{"VolumesDeleted":null,"SpaceReclaimed":7}`}
	res, _ = newVolClient(t, d).PruneVolumes(context.Background())
	if res.Removed == nil || len(res.Removed) != 0 || res.ReclaimedBytes != 7 {
		t.Fatalf("result = %+v", res)
	}
}

// TestPruneBehaviourIsPinnedToV141 fails when the API pin moves, so whoever
// moves it must re-decide whether prune still removes named volumes (from
// v1.42 it removes anonymous ones only unless the "all" filter is sent).
func TestPruneBehaviourIsPinnedToV141(t *testing.T) {
	if apiVersion != "v1.41" || !PruneIncludesNamedVolumes {
		t.Fatalf("apiVersion = %s: revisit PruneIncludesNamedVolumes and docs/api.md", apiVersion)
	}
}

func hostileNames() []string {
	return []string{
		"", "..", ".", "../x", "a/b", "a%2Fb", "%2e%2e", "a\nb", "a\x00b", "x?force=1", "x#f",
		" x", "x ", "/x", "x/", ".hidden", "-x", "a\\b", strings.Repeat("a", 129), "a‮b", "né",
	}
}

func TestVolumeWritesRefuseHostileInputOnTheRealClient(t *testing.T) {
	d := &volDocker{exists: true}
	c := newVolClient(t, d)
	ctx := context.Background()
	for _, h := range hostileNames() {
		if err := c.RemoveVolume(ctx, h); !errors.Is(err, ErrBadName) {
			t.Errorf("RemoveVolume(%q) = %v", h, err)
		}
		if _, err := c.InspectVolume(ctx, h); !errors.Is(err, ErrBadName) {
			t.Errorf("InspectVolume(%q) = %v", h, err)
		}
		if _, err := c.CreateVolume(ctx, h, nil); !errors.Is(err, ErrBadName) {
			t.Errorf("CreateVolume(%q) = %v", h, err)
		}
	}
	bad := []map[string]string{
		{"": "v"}, {"-k": "v"}, {"k k": "v"}, {"k\n": "v"}, {"k/x": "v"}, {"k=": "v"},
		{strings.Repeat("k", 64): "v"}, {"com.docker.compose.project": "v"}, {"COM.DOCKER.x": "v"},
		{"io.docker.x": "v"}, {"org.dockerproject.x": "v"},
		{"k": "a\nb"}, {"k": "a\x00b"}, {"k": "a\x7fb"}, {"k": "\xff\xfe"}, {"k": strings.Repeat("v", 257)},
		{"k": "a\u0085b"}, {"k": "a b"}, {"Env": "1", "env": "2"},
	}
	many := map[string]string{}
	for i := 0; i < 17; i++ {
		many["k"+strings.Repeat("a", i)] = "v"
	}
	bad = append(bad, many)
	for _, l := range bad {
		if _, err := c.CreateVolume(ctx, "ok", l); !errors.Is(err, ErrBadName) {
			t.Errorf("CreateVolume labels %q = %v", l, err)
		}
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("requests reached the socket: %v", got)
	}
}

func TestValidLabelsAccepts(t *testing.T) {
	ok := map[string]string{"app": "web", "com.example.team": "", "a_b-c.d": strings.Repeat("v", 256), "unicode": "café"}
	if !ValidLabels(ok) || !ValidLabels(nil) {
		t.Fatal("valid labels rejected")
	}
	sixteen := map[string]string{}
	for i := 0; i < 16; i++ {
		sixteen["k"+strings.Repeat("a", i)] = "v"
	}
	if !ValidLabels(sixteen) {
		t.Fatal("16 labels rejected")
	}
}

func TestVolumeWriteDockerErrorsAreTyped(t *testing.T) {
	secret := "/home/user/secret"
	for _, tc := range []struct {
		code int
		want error
	}{{403, ErrConflict}, {500, ErrDaemon}, {400, ErrInvalidRequest}} {
		d := &volDocker{createCode: tc.code}
		_, err := newVolClient(t, d).CreateVolume(context.Background(), "v1", nil)
		if !errors.Is(err, tc.want) || strings.Contains(err.Error(), secret) {
			t.Errorf("%d: err = %v", tc.code, err)
		}
	}
}
