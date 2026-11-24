package dockerc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const (
	imgFull  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	imgShort = "0123456789ab"
	imgOther = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

// imgDocker is a fake engine for the image methods. It records every request
// as "METHOD escapedpath?query" and every body.
type imgDocker struct {
	mu      sync.Mutex
	reqs    []string
	inspect map[string]string // path under /v1.41/images/ (without /json) to answer; absent is 404
	status  map[string]int    // path to forced status
	history string
	list    string // /containers/json
	delCode int
	delBody map[string]string // path to DELETE answer
	prune   string
	tagCode int
}

var imgInspectSecrets = `{"Id":"sha256:` + imgFull + `","RepoTags":["app:v1","app:v2","bad TAG:x"],"RepoDigests":["app@sha256:` + "aa" + strings.Repeat("bb", 31) + `"],
"Size":1234,"Created":"2026-05-02T09:41:07.123456789Z","Architecture":"amd64","Os":"linux",
"Author":"SECRET-AUTHOR","Comment":"SECRET-COMMENT","Container":"SECRET-CONTAINER",
"Config":{"Env":["API_KEY=SECRET-ENV"],"Labels":{"k":"SECRET-LABEL"},"Cmd":["SECRET-CMD"],"Entrypoint":["SECRET-ENTRY"],
"User":"SECRET-USER","WorkingDir":"/home/user/SECRET-DIR","Volumes":{"/SECRET-VOL":{}},
"ExposedPorts":{"80/tcp":{},"80/udp":{},"443/tcp":{},"0/tcp":{},"70000/tcp":{},"x/tcp":{}}},
"ContainerConfig":{"Env":["SECRET-CC"]},"GraphDriver":{"Data":{"UpperDir":"/home/user/SECRET-UPPER"}},"RootFS":{"Layers":["sha256:SECRET-LAYER"]}}`

var imgHistorySecrets = `[{"Id":"sha256:` + imgFull + `","Created":1777700000,"CreatedBy":"ARG TOKEN=SECRET-CREATEDBY","Size":10,"Comment":"SECRET-HC","Tags":["SECRET-HT"]},
{"Id":"<missing>","Created":0,"CreatedBy":"ENV PASSWORD=SECRET-CREATEDBY2","Size":-5,"Comment":""}]`

func (d *imgDocker) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		p := r.URL.EscapedPath()
		d.mu.Lock()
		d.reqs = append(d.reqs, r.Method+" "+p+"?"+r.URL.RawQuery)
		d.mu.Unlock()
		const pre = "/v1.41/images/"
		switch {
		case r.Method == "GET" && p == "/v1.41/containers/json":
			if d.list == "" {
				io.WriteString(w, `[]`)
				return
			}
			io.WriteString(w, d.list)
		case r.Method == "GET" && strings.HasPrefix(p, pre) && strings.HasSuffix(p, "/history"):
			if d.history == "" {
				io.WriteString(w, `[]`)
				return
			}
			io.WriteString(w, d.history)
		case r.Method == "GET" && strings.HasPrefix(p, pre) && strings.HasSuffix(p, "/json"):
			k := strings.TrimSuffix(strings.TrimPrefix(p, pre), "/json")
			if c := d.status[k]; c != 0 {
				w.WriteHeader(c)
				return
			}
			b, ok := d.inspect[k]
			if !ok {
				w.WriteHeader(404)
				return
			}
			io.WriteString(w, b)
		case r.Method == "POST" && strings.HasSuffix(p, "/tag"):
			c := d.tagCode
			if c == 0 {
				c = 201
			}
			w.WriteHeader(c)
		case r.Method == "DELETE":
			c := d.delCode
			if c == 0 {
				c = 200
			}
			w.WriteHeader(c)
			if b, ok := d.delBody[strings.TrimPrefix(p, pre)]; ok {
				io.WriteString(w, b)
				return
			}
			io.WriteString(w, `[]`)
		case r.Method == "POST" && p == "/v1.41/images/prune":
			io.WriteString(w, d.prune)
		default:
			w.WriteHeader(500)
		}
	})
}

func (d *imgDocker) requests() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.reqs...)
}

func (d *imgDocker) only(method string) []string {
	var out []string
	for _, r := range d.requests() {
		if strings.HasPrefix(r, method+" ") {
			out = append(out, r)
		}
	}
	return out
}

func containsReq(reqs []string, want string) bool {
	for _, r := range reqs {
		if r == want {
			return true
		}
	}
	return false
}

func newImgClient(t *testing.T, d *imgDocker) *httpClient {
	t.Helper()
	return newHTTPClient(startFakeDocker(t, d.handler()))
}

func inspectAll(ids ...string) map[string]string {
	m := map[string]string{}
	for _, i := range ids {
		m[i] = imgInspectSecrets
	}
	return m
}

func TestInspectImageCarriesNoSecretsAndIsBounded(t *testing.T) {
	d := &imgDocker{inspect: inspectAll("app:v1"), history: imgHistorySecrets,
		list: `[{"Id":"c1c1c1c1c1c1c1c1","Names":["/synthetic-web"],"ImageID":"sha256:` + imgFull + `"},{"Id":"c2c2","Names":["/other"],"ImageID":"sha256:` + imgOther + `"},{"Id":"c3c3c3c3c3c3c3c3","Names":["/bad name"],"ImageID":"sha256:` + imgFull + `"}]`}
	c := newImgClient(t, d)
	got, err := c.InspectImage(context.Background(), "app:v1")
	if err != nil {
		t.Fatal(err)
	}
	dump := fmt.Sprintf("%+v", got)
	for _, secret := range []string{"SECRET", "/home/user", "API_KEY", "PASSWORD", "TOKEN"} {
		if strings.Contains(dump, secret) {
			t.Fatalf("result carries %q: %s", secret, dump)
		}
	}
	if got.ID != imgShort || len(got.Tags) != 2 || got.Tags[0] != "app:v1" || len(got.Digests) != 1 || got.SizeBytes != 1234 ||
		got.Architecture != "amd64" || got.OS != "linux" || got.Created.IsZero() {
		t.Fatalf("got = %+v", got)
	}
	if fmt.Sprint(got.ExposedPorts) != "[80 443]" {
		t.Fatalf("ports = %v", got.ExposedPorts)
	}
	if len(got.ContainerNames) != 2 || got.ContainerNames[0] != "c3c3c3c3c3c3" || got.ContainerNames[1] != "synthetic-web" {
		t.Fatalf("containers = %v", got.ContainerNames)
	}
	if got.LayerCount != 2 || len(got.Layers) != 2 || got.Layers[0].SizeBytes != 10 || got.Layers[1].SizeBytes != 0 || !got.Layers[1].Created.IsZero() {
		t.Fatalf("layers = %+v", got.Layers)
	}
	for _, r := range d.requests() {
		if strings.HasPrefix(r, "GET") {
			continue
		}
		t.Fatalf("inspect sent a write: %s", r)
	}
	// Containers of every state are listed (all=1), or stopped ones would be missed.
	if !containsReq(d.requests(), "GET /v1.41/containers/json?all=1") {
		t.Fatalf("requests = %v", d.requests())
	}
	// Architecture and OS go through plainToken.
	d.inspect["app:v1"] = strings.Replace(strings.Replace(imgInspectSecrets, `"amd64"`, `"amd64\n<b>"`, 1), `"linux"`, `"`+strings.Repeat("l", 40)+`"`, 1)
	got, err = c.InspectImage(context.Background(), "app:v1")
	if err != nil || got.Architecture != "" || got.OS != "" {
		t.Fatalf("%v arch=%q os=%q", err, got.Architecture, got.OS)
	}
}

func TestInspectImageLayersAreCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 500; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"Created":1,"Size":1,"CreatedBy":"x"}`)
	}
	b.WriteString("]")
	d := &imgDocker{inspect: inspectAll("app:v1"), history: b.String()}
	got, err := newImgClient(t, d).InspectImage(context.Background(), "app:v1")
	if err != nil || len(got.Layers) != maxLayers || got.LayerCount != 500 {
		t.Fatalf("%v %d %d", err, len(got.Layers), got.LayerCount)
	}
}

func TestImageLookupIsStrict(t *testing.T) {
	ctx := context.Background()
	d := &imgDocker{inspect: map[string]string{
		imgShort:       imgInspectSecrets,
		imgFull:        imgInspectSecrets,
		"app":          imgInspectSecrets, // matched through app:latest only if listed
		"app:v1":       imgInspectSecrets,
		"abcd":         imgInspectSecrets, // Docker resolved an ID prefix of another length
		"other:1":      imgInspectSecrets, // Docker normalised a different name
		"aaaaaaaaaaaa": imgInspectSecrets, // 12 hex that is not this image's ID
	}}
	c := newImgClient(t, d)
	for _, ok := range []string{imgShort, imgFull, "app:v1"} {
		if _, err := c.InspectImage(ctx, ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"abcd", "other:1", "aaaaaaaaaaaa", "app"} {
		if _, err := c.InspectImage(ctx, bad); !errors.Is(err, ErrObjectNotFound) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// "app" without a tag matches app:latest when that is listed.
	d.inspect["app"] = strings.Replace(imgInspectSecrets, `"app:v1"`, `"app:latest"`, 1)
	if _, err := c.InspectImage(ctx, "app"); err != nil {
		t.Errorf("app:latest: %v", err)
	}
	// A digest reference matches RepoDigests.
	dig := "app@sha256:" + "aa" + strings.Repeat("bb", 31)
	d.inspect[dig] = imgInspectSecrets
	if _, err := c.InspectImage(ctx, dig); err != nil {
		t.Errorf("digest: %v", err)
	}
	// Unknown is not found; 400 (ambiguous) is not found; 5xx is unavailable.
	if _, err := c.InspectImage(ctx, "nope:1"); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("unknown: %v", err)
	}
	d.status = map[string]int{"cafecafecafe": 400, "boom:1": 500}
	if _, err := c.InspectImage(ctx, "cafecafecafe"); !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("400: %v", err)
	}
	if _, err := c.InspectImage(ctx, "boom:1"); !errors.Is(err, ErrDockerUnavailable) {
		t.Errorf("500: %v", err)
	}
	// An answer with a bad ID is unusable.
	d.inspect["bad:1"] = `{"Id":"sha256:zz","RepoTags":["bad:1"]}`
	if _, err := c.InspectImage(ctx, "bad:1"); !errors.Is(err, ErrDockerUnavailable) {
		t.Errorf("bad id: %v", err)
	}
}

func TestRemoveImageByTagUntagsExactReferenceNeverForces(t *testing.T) {
	d := &imgDocker{inspect: inspectAll("app:v1"), delBody: map[string]string{
		"app:v1": `[{"Untagged":"app:v1"},{"Deleted":"sha256:` + imgFull + `"},{"Untagged":"bad TAG"},{"Deleted":"zz"}]`,
	}}
	c := newImgClient(t, d)
	res, err := c.RemoveImage(context.Background(), "app:v1")
	if err != nil {
		t.Fatal(err)
	}
	dels := d.only("DELETE")
	if len(dels) != 1 || dels[0] != "DELETE /v1.41/images/app:v1?noprune=1" {
		t.Fatalf("deletes = %v", dels)
	}
	for _, r := range d.requests() {
		if strings.Contains(r, "force") {
			t.Fatalf("force sent: %s", r)
		}
	}
	if fmt.Sprint(res.Untagged) != "[app:v1]" || fmt.Sprint(res.Deleted) != "["+imgShort+"]" || res.Count == nil || *res.Count != 4 ||
		!res.Truncated || !res.Complete {
		t.Fatalf("res = %+v", res)
	}
}

func TestRemoveImageByTagSendsTheReportedReferenceNotTheURLText(t *testing.T) {
	// "app" resolves to the reported app:latest; the DELETE goes to that.
	d := &imgDocker{inspect: map[string]string{"app": strings.Replace(imgInspectSecrets, `"app:v1"`, `"app:latest"`, 1)}}
	if _, err := newImgClient(t, d).RemoveImage(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	if dels := d.only("DELETE"); len(dels) != 1 || dels[0] != "DELETE /v1.41/images/app:latest?noprune=1" {
		t.Fatalf("deletes = %v", dels)
	}
}

func TestRemoveImageInUseSendsNoDelete(t *testing.T) {
	for name, list := range map[string]string{
		"running or stopped": `[{"Id":"c1","Names":["/a"],"ImageID":"sha256:` + imgFull + `","State":"exited"}]`,
		"created":            `[{"Id":"c1","Names":["/a"],"ImageID":"sha256:` + imgFull + `","State":"created"},{"Id":"c2","Names":["/b"],"ImageID":"sha256:` + imgFull + `"}]`,
	} {
		d := &imgDocker{inspect: inspectAll("app:v1", imgShort), list: list}
		c := newImgClient(t, d)
		for _, ref := range []string{"app:v1", imgShort} {
			_, err := c.RemoveImage(context.Background(), ref)
			var iu *InUseError
			if !errors.As(err, &iu) || !errors.Is(err, ErrConflict) || iu.Containers < 1 {
				t.Fatalf("%s %s: %v", name, ref, err)
			}
		}
		if dels := d.only("DELETE"); len(dels) != 0 {
			t.Fatalf("%s: deletes = %v", name, dels)
		}
		// Every state is asked for: the container list must carry all=1.
		if !containsReq(d.requests(), "GET /v1.41/containers/json?all=1") {
			t.Fatalf("%s: requests = %v", name, d.requests())
		}
	}
	// A container of another image does not block.
	d := &imgDocker{inspect: inspectAll("app:v1"), list: `[{"Id":"c1","Names":["/a"],"ImageID":"sha256:` + imgOther + `"}]`}
	if _, err := newImgClient(t, d).RemoveImage(context.Background(), "app:v1"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveImageByIDRemovesEveryReferenceOneByOne(t *testing.T) {
	dg := "app@sha256:" + "aa" + strings.Repeat("bb", 31)
	d := &imgDocker{inspect: map[string]string{
		imgShort: strings.Replace(imgInspectSecrets, `,"bad TAG:x"`, ``, 1),
		// After the tags the image is read again by its full ID; here only a digest is left.
		imgFull: `{"Id":"sha256:` + imgFull + `","RepoTags":[],"RepoDigests":["` + dg + `"]}`,
	}, delBody: map[string]string{
		"app:v1": `[{"Untagged":"app:v1"}]`, "app:v2": `[{"Untagged":"app:v2"}]`,
		"app@sha256:" + "aa" + strings.Repeat("bb", 31): `[{"Untagged":"app@sha256:x"},{"Deleted":"sha256:` + imgFull + `"}]`,
	}}
	res, err := newImgClient(t, d).RemoveImage(context.Background(), imgShort)
	if err != nil {
		t.Fatal(err)
	}
	dels := d.only("DELETE")
	want := []string{
		"DELETE /v1.41/images/app:v1?noprune=1", "DELETE /v1.41/images/app:v2?noprune=1",
		"DELETE /v1.41/images/" + dg + "?noprune=1",
	}
	if fmt.Sprint(dels) != fmt.Sprint(want) {
		t.Fatalf("deletes = %v", dels)
	}
	if res.Count == nil || *res.Count != 4 || len(res.Deleted) != 1 || !res.Complete {
		t.Fatalf("res = %+v", res)
	}
}

func TestRemoveImageByIDWithoutReferencesUsesTheFullID(t *testing.T) {
	d := &imgDocker{inspect: map[string]string{imgShort: `{"Id":"sha256:` + imgFull + `","RepoTags":null,"RepoDigests":[]}`}}
	if _, err := newImgClient(t, d).RemoveImage(context.Background(), imgShort); err != nil {
		t.Fatal(err)
	}
	if dels := d.only("DELETE"); len(dels) != 1 || dels[0] != "DELETE /v1.41/images/"+imgFull+"?noprune=1" {
		t.Fatalf("deletes = %v", dels)
	}
}

func TestRemoveImageByIDRefusesAnUnnameableReferenceBeforeDeleting(t *testing.T) {
	d := &imgDocker{inspect: map[string]string{imgShort: imgInspectSecrets}} // has "bad TAG:x"
	if _, err := newImgClient(t, d).RemoveImage(context.Background(), imgShort); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if dels := d.only("DELETE"); len(dels) != 0 {
		t.Fatalf("deletes = %v", dels)
	}
}

func TestRemoveImageDockerAnswers(t *testing.T) {
	for code, want := range map[int]error{404: ErrObjectNotFound, 409: ErrConflict, 403: ErrConflict, 500: ErrDaemon} {
		d := &imgDocker{inspect: inspectAll("app:v1"), delCode: code}
		_, err := newImgClient(t, d).RemoveImage(context.Background(), "app:v1")
		if !errors.Is(err, want) {
			t.Errorf("%d: %v", code, err)
		}
	}
	// Unknown image: no delete.
	d := &imgDocker{}
	if _, err := newImgClient(t, d).RemoveImage(context.Background(), "nope:1"); !errors.Is(err, ErrObjectNotFound) || len(d.only("DELETE")) != 0 {
		t.Fatalf("err = %v", err)
	}
	// Unreadable answer after a 200 is a success with unknown result.
	d = &imgDocker{inspect: inspectAll("app:v1"), delBody: map[string]string{"app:v1": `<html>`}}
	res, err := newImgClient(t, d).RemoveImage(context.Background(), "app:v1")
	if err != nil || res.Complete || res.Count != nil || res.Untagged == nil || res.Deleted == nil {
		t.Fatalf("%v %+v", err, res)
	}
	d = &imgDocker{inspect: inspectAll("app:v1"), delBody: map[string]string{"app:v1": `{"not":"an array"}`}}
	if res, err := newImgClient(t, d).RemoveImage(context.Background(), "app:v1"); err != nil || res.Complete {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestRemoveImageCapsListsAndSaysSo(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 1500; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"Untagged":"r%d:v"}`, i)
	}
	b.WriteString("]")
	d := &imgDocker{inspect: inspectAll("app:v1"), delBody: map[string]string{"app:v1": b.String()}}
	res, err := newImgClient(t, d).RemoveImage(context.Background(), "app:v1")
	if err != nil || len(res.Untagged) != maxPrunedNames || res.Count == nil || *res.Count != 1500 || !res.Truncated {
		t.Fatalf("%v %d %+v", err, len(res.Untagged), res.Count)
	}
}

func TestPruneImagesSendsTheRightFilter(t *testing.T) {
	for all, want := range map[bool]string{false: `{"dangling":["true"]}`, true: `{"dangling":["false"]}`} {
		d := &imgDocker{prune: `{"ImagesDeleted":[{"Untagged":"app@sha256:` + imgFull + `"},{"Deleted":"sha256:` + imgFull + `"},{"Deleted":"bad"},{"Untagged":"bad TAG"}],"SpaceReclaimed":4096}`}
		res, err := newImgClient(t, d).PruneImages(context.Background(), all)
		if err != nil {
			t.Fatal(err)
		}
		reqs := d.only("POST")
		if len(reqs) != 1 {
			t.Fatalf("reqs = %v", reqs)
		}
		q := reqs[0][strings.Index(reqs[0], "?")+1:]
		v, _ := url.ParseQuery(q)
		if !strings.HasPrefix(reqs[0], "POST /v1.41/images/prune?") || len(v) != 1 || v.Get("filters") != want {
			t.Fatalf("all=%v req = %s", all, reqs[0])
		}
		if res.ReclaimedBytes == nil || *res.ReclaimedBytes != 4096 || res.Count == nil || *res.Count != 4 || !res.Truncated || !res.Complete ||
			fmt.Sprint(res.Deleted) != "["+imgShort+"]" || len(res.Untagged) != 1 {
			t.Fatalf("res = %+v", res)
		}
	}
}

func TestPruneImagesUnreadableAnswersAreSuccessWithUnknowns(t *testing.T) {
	for name, body := range map[string]string{"html": `<html>`, "not object": `[]`, "empty": ``} {
		d := &imgDocker{prune: body}
		res, err := newImgClient(t, d).PruneImages(context.Background(), true)
		if err != nil || res.Complete || res.Count != nil || res.ReclaimedBytes != nil || res.Untagged == nil || res.Deleted == nil {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
	}
	// A space figure that does not fit is unknown, the list still counts.
	d := &imgDocker{prune: `{"ImagesDeleted":[{"Deleted":"sha256:` + imgFull + `"}],"SpaceReclaimed":99999999999999999999}`}
	res, err := newImgClient(t, d).PruneImages(context.Background(), false)
	if err != nil || res.Complete || res.ReclaimedBytes != nil || res.Count == nil || *res.Count != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	// Null list and negative space.
	d = &imgDocker{prune: `{"ImagesDeleted":null,"SpaceReclaimed":-5}`}
	res, err = newImgClient(t, d).PruneImages(context.Background(), false)
	if err != nil || !res.Complete || *res.Count != 0 || *res.ReclaimedBytes != 0 || res.Untagged == nil {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestPruneImagesErrorStatusesStayErrors(t *testing.T) {
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	if _, err := newHTTPClient(sock).PruneImages(context.Background(), true); !errors.Is(err, ErrDaemon) {
		t.Fatalf("err = %v", err)
	}
}

func TestTagImageSendsToTheFullIDWithEscapedQuery(t *testing.T) {
	d := &imgDocker{inspect: inspectAll("app:v1")}
	if err := newImgClient(t, d).TagImage(context.Background(), "app:v1", "registry.example.com:5000/team/app", "v2"); err != nil {
		t.Fatal(err)
	}
	posts := d.only("POST")
	if len(posts) != 1 || posts[0] != "POST /v1.41/images/"+imgFull+"/tag?repo=registry.example.com%3A5000%2Fteam%2Fapp&tag=v2" {
		t.Fatalf("posts = %v", posts)
	}
	// The existence check was a GET of the target.
	if r := d.requests(); !strings.Contains(strings.Join(r, "\n"), "GET /v1.41/images/registry.example.com:5000/team/app:v2/json") {
		t.Fatalf("reqs = %v", r)
	}
}

func TestTagImageRefusesToMoveATagFromAnotherImage(t *testing.T) {
	d := &imgDocker{inspect: map[string]string{
		"app:v1": imgInspectSecrets,
		"app:v9": `{"Id":"sha256:` + imgOther + `","RepoTags":["app:v9"]}`,
		"app:v2": imgInspectSecrets, // same image already has it
	}}
	c := newImgClient(t, d)
	if err := c.TagImage(context.Background(), "app:v1", "app", "v9"); !errors.Is(err, ErrConflict) {
		t.Fatalf("other: %v", err)
	}
	if err := c.TagImage(context.Background(), "app:v1", "app", "v2"); err != nil {
		t.Fatalf("same: %v", err)
	}
	if posts := d.only("POST"); len(posts) != 0 {
		t.Fatalf("posts = %v", posts)
	}
}

func TestTagImageFailsClosedAndMapsAnswers(t *testing.T) {
	ctx := context.Background()
	// The existence check cannot be answered: no POST.
	d := &imgDocker{inspect: inspectAll("app:v1"), status: map[string]int{"app:v2": 500}}
	if err := newImgClient(t, d).TagImage(ctx, "app:v1", "app", "v2"); !errors.Is(err, ErrDockerUnavailable) || len(d.only("POST")) != 0 {
		t.Fatalf("500: %v", err)
	}
	d = &imgDocker{inspect: inspectAll("app:v1"), status: map[string]int{"app:v2": 400}}
	if err := newImgClient(t, d).TagImage(ctx, "app:v1", "app", "v2"); !errors.Is(err, ErrInvalidRequest) || len(d.only("POST")) != 0 {
		t.Fatalf("400: %v", err)
	}
	d = &imgDocker{inspect: inspectAll("app:v1"), status: map[string]int{"app:v2": 409}}
	if err := newImgClient(t, d).TagImage(ctx, "app:v1", "app", "v2"); !errors.Is(err, ErrConflict) || len(d.only("POST")) != 0 {
		t.Fatalf("409: %v", err)
	}
	d = &imgDocker{inspect: map[string]string{"app:v1": imgInspectSecrets, "app:v2": `junk`}}
	if err := newImgClient(t, d).TagImage(ctx, "app:v1", "app", "v2"); !errors.Is(err, ErrDockerUnavailable) || len(d.only("POST")) != 0 {
		t.Fatalf("junk: %v", err)
	}
	// Unknown source: nothing sent but the lookup.
	d = &imgDocker{}
	if err := newImgClient(t, d).TagImage(ctx, "nope:1", "app", "v2"); !errors.Is(err, ErrObjectNotFound) || len(d.only("POST")) != 0 {
		t.Fatalf("unknown: %v", err)
	}
	for code, want := range map[int]error{404: ErrObjectNotFound, 409: ErrConflict, 400: ErrInvalidRequest, 500: ErrDaemon} {
		d = &imgDocker{inspect: inspectAll("app:v1"), tagCode: code}
		if err := newImgClient(t, d).TagImage(ctx, "app:v1", "app", "v2"); !errors.Is(err, want) {
			t.Errorf("%d: %v", code, err)
		}
	}
}

// Every image write validates its own arguments: hostile input never reaches
// the socket.
func TestImageWritesRefuseHostileInputOnTheRealClient(t *testing.T) {
	d := &imgDocker{inspect: inspectAll("app:v1", imgShort)}
	c := newImgClient(t, d)
	ctx := context.Background()
	hostile := append(hostileNames(), strings.Repeat("a", 300), "a​b", "a‮b", "\ufeffa", "a b", "a;b", "a$(x)", "a`x`", "a\tb",
		"a%00b", "a%0Ab", "A", "a//b", "/a", "a/", "a/../b", "a?force=1", "a#x", "a\\b", "a:b:c", "a@b", "a@sha256:zz", "a:", ":a", "a:-x")
	for _, h := range hostile {
		if ValidImageRef(h) {
			continue // a valid reference is looked up; the grammar tests cover it
		}
		if _, err := c.RemoveImage(ctx, h); !errors.Is(err, ErrBadName) {
			t.Errorf("RemoveImage(%q) = %v", h, err)
		}
		if _, err := c.InspectImage(ctx, h); !errors.Is(err, ErrBadName) {
			t.Errorf("InspectImage(%q) = %v", h, err)
		}
		if err := c.TagImage(ctx, h, "app", "v2"); !errors.Is(err, ErrBadName) {
			t.Errorf("TagImage ref %q = %v", h, err)
		}
	}
	badRepos := []string{"", "App", "a:b", "a@sha256:" + strings.Repeat("a", 64), "a&force=1", "a b", "a​b", "../x", "a//b", "host:5000",
		strings.Repeat("a", 64), "a?x", "a#x", "a\nb", "a%2Fb", strings.Repeat("a", 256), "-a", "a/"}
	for _, r := range badRepos {
		if err := c.TagImage(ctx, "app:v1", r, "v2"); !errors.Is(err, ErrBadName) {
			t.Errorf("TagImage repo %q = %v", r, err)
		}
	}
	badTags := []string{"", "-x", ".x", "a/b", "a:b", "a b", "a​b", "a‮b", "a\nb", "a&force=1", "a%2F", strings.Repeat("a", 129), "a@b", "a?b", "a#b"}
	for _, tg := range badTags {
		if err := c.TagImage(ctx, "app:v1", "app", tg); !errors.Is(err, ErrBadName) {
			t.Errorf("TagImage tag %q = %v", tg, err)
		}
	}
	if err := c.TagImage(ctx, "app:v1", strings.Repeat("a", 200)+"/"+strings.Repeat("b", 40), strings.Repeat("t", 128)); !errors.Is(err, ErrBadName) {
		t.Errorf("total length: %v", err)
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("requests reached the socket: %v", got)
	}
}

func TestImageNamesAndTagGrammar(t *testing.T) {
	for _, r := range []string{"app", "team/app", "localhost:5000/app", "ghcr.io/owner/app", "Registry.Example.com:5000/a/b", "a.b", "a_b", "a__b", "a-b"} {
		if !ValidImageRepository(r) {
			t.Errorf("repo %q refused", r)
		}
	}
	if ValidImageRepository("sha256") || !ValidImageRepository("sha256x") {
		t.Error("sha256 repository rule")
	}
	for _, tg := range []string{"v1", "latest", "_x", "1.2.3-rc.1", strings.Repeat("a", 128)} {
		if !ValidImageTag(tg) {
			t.Errorf("tag %q refused", tg)
		}
	}
}

// The dangling filter semantics were verified for API v1.41 only. If the pin
// moves, re-check PruneImages and docs/api.md 3.29.
func TestImagePruneBehaviourIsPinnedToV141(t *testing.T) {
	if apiVersion != "v1.41" || !PruneImagesIncludesTagged {
		t.Fatalf("apiVersion = %s: revisit PruneImages and docs/api.md 3.29", apiVersion)
	}
}
