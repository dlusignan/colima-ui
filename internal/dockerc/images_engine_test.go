package dockerc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file tests the image methods against a stateful fake engine that
// follows Docker's rules for images: names before id prefixes (a 400 for an
// ambiguous prefix), a delete by id refused for an image with more than one
// reference, no force, a repository's digest references dropped when its last
// tag goes, the image deleted with its last reference, and a tag call that
// moves an existing tag.

// engImg is one image of a Docker-like fake engine.
type engImg struct {
	id      string
	tags    []string
	digests []string
}

type engDocker struct {
	mu        sync.Mutex
	imgs      []*engImg
	users     map[string]bool // image ids used by a container
	reqs      []string
	failOn    string // DELETE path to fail with 409
	slowCheck string // inspect of this reference is delayed (widens races)
}

func engNorm(r string) string {
	r = strings.TrimPrefix(r, "docker.io/")
	r = strings.TrimPrefix(r, "index.docker.io/")
	if strings.HasPrefix(r, "library/") && strings.Count(r, "/") == 1 {
		r = strings.TrimPrefix(r, "library/")
	}
	return r
}

func engRepoOf(r string) string {
	if i := strings.IndexByte(r, '@'); i >= 0 {
		return r[:i]
	}
	if i := strings.LastIndexByte(r, ':'); i > strings.LastIndexByte(r, '/') {
		return r[:i]
	}
	return r
}

// resolve mimics GetImage: 64 hex is an id, then names (normalised, plus
// :latest), then an id prefix of any length.
func (d *engDocker) resolve(ref string) (*engImg, int) {
	if len(ref) == 64 && isHexString(ref) {
		for _, im := range d.imgs {
			if im.id == ref {
				return im, 200
			}
		}
		return nil, 404
	}
	n := engNorm(ref)
	if !strings.ContainsRune(n, '@') && !refHasTag(n) {
		n += ":latest"
	}
	for _, im := range d.imgs {
		for _, e := range append(append([]string{}, im.tags...), im.digests...) {
			if e == n {
				return im, 200
			}
		}
	}
	if isHexString(ref) {
		var hit []*engImg
		for _, im := range d.imgs {
			if strings.HasPrefix(im.id, ref) {
				hit = append(hit, im)
			}
		}
		if len(hit) == 1 {
			return hit[0], 200
		}
		if len(hit) > 1 {
			return nil, 400
		}
	}
	return nil, 404
}

func engDrop(list []string, s string) []string {
	out := []string{}
	for _, e := range list {
		if e != s {
			out = append(out, e)
		}
	}
	return out
}

func (d *engDocker) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.slowCheck != "" && r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/images/"+d.slowCheck+"/json") {
			time.Sleep(60 * time.Millisecond)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		p := strings.TrimPrefix(r.URL.Path, "/v1.41")
		d.reqs = append(d.reqs, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		switch {
		case r.Method == "GET" && p == "/containers/json":
			var out []map[string]any
			for id := range d.users {
				out = append(out, map[string]any{"Id": "c0ffee" + id[:6], "Names": []string{"/u"}, "ImageID": "sha256:" + id})
			}
			if out == nil {
				out = []map[string]any{}
			}
			json.NewEncoder(w).Encode(out)
		case r.Method == "GET" && strings.HasSuffix(p, "/history"):
			w.Write([]byte(`[]`))
		case r.Method == "GET" && strings.HasPrefix(p, "/images/") && strings.HasSuffix(p, "/json"):
			ref := strings.TrimSuffix(strings.TrimPrefix(p, "/images/"), "/json")
			im, code := d.resolve(ref)
			if im == nil {
				w.WriteHeader(code)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:" + im.id, "RepoTags": im.tags, "RepoDigests": im.digests})
		case r.Method == "POST" && strings.HasSuffix(p, "/tag"):
			ref := strings.TrimSuffix(strings.TrimPrefix(p, "/images/"), "/tag")
			im, code := d.resolve(ref)
			if im == nil {
				w.WriteHeader(code)
				return
			}
			nt := engNorm(r.URL.Query().Get("repo") + ":" + r.URL.Query().Get("tag"))
			for _, o := range d.imgs {
				o.tags = engDrop(o.tags, nt)
			}
			im.tags = append(im.tags, nt)
			w.WriteHeader(201)
		case r.Method == "DELETE" && strings.HasPrefix(p, "/images/"):
			if r.URL.Query().Get("force") != "" {
				w.WriteHeader(500)
				return
			}
			ref := strings.TrimPrefix(p, "/images/")
			if d.failOn != "" && ref == d.failOn {
				w.WriteHeader(409)
				return
			}
			im, code := d.resolve(ref)
			if im == nil {
				w.WriteHeader(code)
				return
			}
			var recs []map[string]string
			if strings.HasPrefix(im.id, ref) {
				if len(im.tags)+len(im.digests) > 1 {
					w.WriteHeader(409)
					return
				}
				for _, e := range append(append([]string{}, im.tags...), im.digests...) {
					recs = append(recs, map[string]string{"Untagged": e})
				}
				im.tags, im.digests = nil, nil
			} else {
				n := engNorm(ref)
				if !strings.ContainsRune(n, '@') && !refHasTag(n) {
					n += ":latest"
				}
				im.tags = engDrop(im.tags, n)
				im.digests = engDrop(im.digests, n)
				recs = append(recs, map[string]string{"Untagged": n})
				if !strings.ContainsRune(n, '@') {
					// Docker: no tag left in this repository, so drop its digests.
					found := false
					for _, t := range im.tags {
						if engRepoOf(t) == engRepoOf(n) {
							found = true
						}
					}
					if !found {
						for _, dg := range im.digests {
							if engRepoOf(dg) == engRepoOf(n) {
								im.digests = engDrop(im.digests, dg)
								recs = append(recs, map[string]string{"Untagged": dg})
							}
						}
					}
				}
				if len(im.tags)+len(im.digests) > 0 {
					json.NewEncoder(w).Encode(recs)
					return
				}
			}
			if d.users[im.id] {
				w.WriteHeader(409)
				return
			}
			var keep []*engImg
			for _, o := range d.imgs {
				if o != im {
					keep = append(keep, o)
				}
			}
			d.imgs = keep
			recs = append(recs, map[string]string{"Deleted": "sha256:" + im.id})
			json.NewEncoder(w).Encode(recs)
		default:
			w.WriteHeader(500)
		}
	})
}

const (
	engA = "aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111"
	engB = "bbbbbbbbbbbb2222222222222222222222222222222222222222222222222222"
)

var engDigest = "postgres@sha256:" + strings.Repeat("a", 64)

func engClient(t *testing.T, d *engDocker) *httpClient {
	t.Helper()
	return newHTTPClient(startFakeDocker(t, d.handler()))
}

func (d *engDocker) deletes() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, r := range d.reqs {
		if strings.HasPrefix(r, "DELETE") {
			out = append(out, r)
		}
	}
	return out
}

// The common case: a pulled image has a tag and a digest. Removing the tag
// makes Docker drop the digest too, so a delete of the digest would be a 404.
func TestEngineRemoveByIDOfAPulledImage(t *testing.T) {
	d := &engDocker{imgs: []*engImg{{id: engA, tags: []string{"postgres:16"}, digests: []string{engDigest}}}}
	res, err := engClient(t, d).RemoveImage(context.Background(), engA[:12])
	if err != nil {
		t.Fatalf("err = %v (requests %v)", err, d.reqs)
	}
	if len(d.imgs) != 0 {
		t.Fatalf("image left: %+v", d.imgs[0])
	}
	if dels := d.deletes(); len(dels) != 1 || dels[0] != "DELETE /v1.41/images/postgres:16?noprune=1" {
		t.Fatalf("deletes = %v", dels)
	}
	if fmt.Sprint(res.Untagged) != "[postgres:16 "+engDigest+"]" || fmt.Sprint(res.Deleted) != "["+engA[:12]+"]" || !res.Complete || res.Count == nil || *res.Count != 3 {
		t.Fatalf("res = %+v", res)
	}
	for _, r := range d.reqs {
		if strings.Contains(r, "force") {
			t.Fatalf("force sent: %s", r)
		}
	}
}

func TestEngineRemoveByIDOfAnImageInTwoRepositories(t *testing.T) {
	d := &engDocker{imgs: []*engImg{{id: engA, tags: []string{"a:1", "b:1"},
		digests: []string{"a@sha256:" + strings.Repeat("a", 64), "b@sha256:" + strings.Repeat("a", 64)}}}}
	res, err := engClient(t, d).RemoveImage(context.Background(), engA)
	if err != nil || len(d.imgs) != 0 {
		t.Fatalf("err = %v, images left %d", err, len(d.imgs))
	}
	if len(res.Untagged) != 4 || len(res.Deleted) != 1 || !res.Complete {
		t.Fatalf("res = %+v", res)
	}
	if dels := d.deletes(); len(dels) != 2 {
		t.Fatalf("deletes = %v", dels)
	}
}

// Digest-only and dangling images are removed too.
func TestEngineRemoveByIDOfDigestOnlyAndDanglingImages(t *testing.T) {
	d := &engDocker{imgs: []*engImg{
		{id: engA, digests: []string{engDigest}},
		{id: engB},
	}}
	c := engClient(t, d)
	if _, err := c.RemoveImage(context.Background(), engA[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RemoveImage(context.Background(), engB[:12]); err != nil {
		t.Fatal(err)
	}
	if len(d.imgs) != 0 {
		t.Fatalf("images left: %d", len(d.imgs))
	}
	if dels := d.deletes(); len(dels) != 2 || dels[0] != "DELETE /v1.41/images/"+"postgres@sha256:"+strings.Repeat("a", 64)+"?noprune=1" || dels[1] != "DELETE /v1.41/images/"+engB+"?noprune=1" {
		t.Fatalf("deletes = %v", dels)
	}
}

func TestEngineRemoveByIDKeepsNotFoundOnTheFirstDelete(t *testing.T) {
	d := &engDocker{imgs: []*engImg{{id: engA, tags: []string{"a:1"}}}}
	c := engClient(t, d)
	if _, err := c.RemoveImage(context.Background(), "nope:1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("err = %v", err)
	}
	// An image that vanishes between the lookup and the first delete is a 404.
	if _, err := c.RemoveImage(context.Background(), "a:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RemoveImage(context.Background(), "a:1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("second removal = %v", err)
	}
}

// A later delete fails after an earlier one worked: the error carries what was
// already removed.
func TestEngineRemoveByIDPartialFailure(t *testing.T) {
	d := &engDocker{failOn: "b:1", imgs: []*engImg{{id: engA, tags: []string{"a:1", "b:1"},
		digests: []string{"a@sha256:" + strings.Repeat("a", 64)}}}}
	_, err := engClient(t, d).RemoveImage(context.Background(), engA[:12])
	var pr *PartialRemovalError
	if !errors.As(err, &pr) || !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if fmt.Sprint(pr.Removal.Untagged) != "[a:1 a@sha256:"+strings.Repeat("a", 64)+"]" || len(pr.Removal.Deleted) != 0 || !pr.Removal.Complete {
		t.Fatalf("removal = %+v", pr.Removal)
	}
	if len(d.imgs) != 1 || fmt.Sprint(d.imgs[0].tags) != "[b:1]" {
		t.Fatalf("state = %+v", d.imgs)
	}
	// A failure of the very first delete is not partial.
	d = &engDocker{failOn: "a:1", imgs: []*engImg{{id: engA, tags: []string{"a:1", "b:1"}}}}
	_, err = engClient(t, d).RemoveImage(context.Background(), engA[:12])
	if errors.As(err, &pr) || !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestEngineRemoveByTagUntagsOnlyThatTag(t *testing.T) {
	d := &engDocker{imgs: []*engImg{{id: engA, tags: []string{"a:1", "a:2"}, digests: []string{"a@sha256:" + strings.Repeat("a", 64)}}}}
	res, err := engClient(t, d).RemoveImage(context.Background(), "a:1")
	if err != nil || len(d.imgs) != 1 || fmt.Sprint(d.imgs[0].tags) != "[a:2]" || fmt.Sprint(res.Untagged) != "[a:1]" || len(res.Deleted) != 0 {
		t.Fatalf("%v %+v %+v", err, res, d.imgs)
	}
}

// A repository literally named like another image's id prefix: the id text
// is an id, and Docker's name-first answer is refused.
func TestEngineHexNamedRepositoryFailsClosed(t *testing.T) {
	d := &engDocker{imgs: []*engImg{
		{id: engA, tags: []string{"app:1"}},
		{id: engB, tags: []string{engA[:12] + ":latest"}},
	}}
	_, err := engClient(t, d).RemoveImage(context.Background(), engA[:12])
	if !errors.Is(err, ErrObjectNotFound) || len(d.imgs) != 2 || len(d.deletes()) != 0 {
		t.Fatalf("err = %v, deletes %v", err, d.deletes())
	}
}

func TestEngineTagDoesNotStealATag(t *testing.T) {
	d := &engDocker{imgs: []*engImg{{id: engA, tags: []string{"app:1"}}, {id: engB, tags: []string{"postgres:16"}}}}
	c := engClient(t, d)
	for _, tgt := range [][2]string{{"postgres", "16"}, {"docker.io/library/postgres", "16"}, {"index.docker.io/library/postgres", "16"}} {
		if err := c.TagImage(context.Background(), "app:1", tgt[0], tgt[1]); !errors.Is(err, ErrConflict) {
			t.Errorf("%v: %v", tgt, err)
		}
	}
	if fmt.Sprint(d.imgs[1].tags) != "[postgres:16]" {
		t.Fatalf("tag moved: %+v", d.imgs[1])
	}
	if err := c.TagImage(context.Background(), "app:1", "localhost:5000/app", "x"); err != nil {
		t.Fatal(err)
	}
}

// Two tag requests for one target through this backend cannot both pass the
// check: the second must see the first one's tag.
func TestEngineConcurrentTagsCannotStealFromEachOther(t *testing.T) {
	d := &engDocker{slowCheck: "shared:1", imgs: []*engImg{{id: engA, tags: []string{"a:1"}}, {id: engB, tags: []string{"b:1"}}}}
	sock := startFakeDocker(t, d.handler())
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, src := range []string{"a:1", "b:1"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each request builds its own client, as the provider does.
			errs[i] = newHTTPClient(sock).TagImage(context.Background(), src, "shared", "1")
		}()
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			ok++
		case errors.Is(e, ErrConflict):
			conflict++
		default:
			t.Fatalf("err = %v", e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("errs = %v", errs)
	}
	owners := 0
	for _, im := range d.imgs {
		for _, tg := range im.tags {
			if tg == "shared:1" {
				owners++
			}
		}
	}
	if owners != 1 {
		t.Fatalf("owners = %d", owners)
	}
}

func TestTagLockGivesUpWithTheContext(t *testing.T) {
	unlock, err := tagLocks.lock(context.Background(), "ctx-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := tagLocks.lock(ctx, "ctx-test"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	unlock()
	u2, err := tagLocks.lock(context.Background(), "ctx-test")
	if err != nil {
		t.Fatal(err)
	}
	u2()
}

func TestEngineLookupFollowsStrictRules(t *testing.T) {
	d := &engDocker{imgs: []*engImg{
		{id: engA, tags: []string{"app:1", "app:latest"}, digests: []string{engDigest}},
		{id: engB, tags: []string{"abc:latest"}},
	}}
	c := engClient(t, d)
	for _, ok := range []string{"app", "app:1", engA[:12], engA, engDigest} {
		if _, _, err := c.imageLookup(context.Background(), ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"aaaa", "docker.io/library/app:1", "nope"} {
		if _, _, err := c.imageLookup(context.Background(), bad); err == nil {
			t.Errorf("%s resolved", bad)
		}
	}
}
