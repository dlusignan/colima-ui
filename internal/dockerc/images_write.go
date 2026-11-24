package dockerc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// maxExposedPorts and maxLayers bound the lists of an image inspect.
	maxExposedPorts = 100
	maxLayers       = 200
)

// PruneImagesIncludesTagged documents what PruneImages(all=true) does: the
// filter dangling=false removes every image no container uses, tagged ones
// included. The v1.41 API description says: "When set to false (or 0), all
// unused images are pruned."
const PruneImagesIncludesTagged = true

// ImageLayer is one layer of an image history: its time and size only. The
// command that built it (CreatedBy), its comment and its author are never
// decoded, because build commands can carry secrets from ARG and ENV lines.
type ImageLayer struct {
	Created   time.Time // UTC; zero when unknown
	SizeBytes int64
}

// ImageDetail is the inspect answer for one image. It has no environment, no
// labels, no command or entrypoint and no layer command text, by construction:
// those fields are not decoded.
type ImageDetail struct {
	ID             string   // 12-char short form
	Tags           []string // valid references only; never nil
	Digests        []string // valid references only; never nil
	SizeBytes      int64
	Created        time.Time // UTC; zero when unparseable
	Architecture   string    // plain token or ""
	OS             string    // plain token or ""
	ExposedPorts   []int     // numbers only, sorted, at most maxExposedPorts; never nil
	ContainerNames []string  // containers, any state, that use the image; never nil
	Layers         []ImageLayer
	LayerCount     int // all layers; Layers holds at most maxLayers
}

// ImageRemoval is what Docker reports for a remove or a prune. Docker has
// already acted when this is built, so an answer that cannot be read is a
// result with Complete false, not an error.
type ImageRemoval struct {
	Untagged  []string // valid references, sorted, at most maxPrunedNames; never nil
	Deleted   []string // 12-char image ids, sorted, at most maxPrunedNames; never nil
	Count     *int     // entries Docker listed (untagged plus deleted); nil when unknown
	Truncated bool     // the lists hold fewer entries than Count
	Complete  bool     // false when the answer could not be read
}

// ImagePruneResult is the outcome of an image prune.
type ImagePruneResult struct {
	ImageRemoval
	ReclaimedBytes *int64 // nil when unknown
}

type rawDeleteItem struct {
	Untagged string `json:"Untagged"`
	Deleted  string `json:"Deleted"`
}

// rawImage holds only the fields an image answer may use. Config.Env,
// Config.Labels, Config.Cmd, Config.Entrypoint and the rest of the config are
// not decoded.
type rawImage struct {
	ID           string   `json:"Id"`
	RepoTags     []string `json:"RepoTags"`
	RepoDigests  []string `json:"RepoDigests"`
	Size         int64    `json:"Size"`
	Created      string   `json:"Created"`
	Architecture string   `json:"Architecture"`
	OS           string   `json:"Os"`
	Config       struct {
		ExposedPorts map[string]json.RawMessage `json:"ExposedPorts"`
	} `json:"Config"`
}

func isHexString(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return s != ""
}

// refHasTag is true when ref has a tag (a colon after the last slash).
func refHasTag(ref string) bool {
	return strings.LastIndexByte(ref, ':') > strings.LastIndexByte(ref, '/')
}

// imageLookup asks Docker for one image and returns it only when ref really
// names it, strictly:
//
//  1. A 12 or 64 character lowercase hex ref is an image ID: the image's ID
//     must start with it. (A repository literally named like an ID cannot be
//     addressed; use one of its tags with a registry or path part.)
//  2. Any other ref must be a valid image reference, and must equal one of the
//     image's RepoTags or RepoDigests exactly, or equal one with ":latest"
//     added when ref has no tag and no digest. Docker also resolves ID prefixes
//     of any length and normalises registry names; neither is accepted, so the
//     image Docker found is always the one the text names.
//
// matched is the exact reference Docker reported for case 2, "" for an ID.
// The returned raw.ID is the bare 64 character ID.
func (c *httpClient) imageLookup(ctx context.Context, ref string) (raw rawImage, matched string, err error) {
	var p string
	if isHexID(ref) {
		p, err = ObjectPath("/images", ref)
	} else {
		p, err = RefPath("/images", ref)
	}
	if err != nil {
		return rawImage{}, "", err
	}
	// Docker answers 400 for an ambiguous ID prefix: no single image is meant.
	resp, callCtx, cancel, err := c.do(ctx, c.listTimeout, p+"/json", nil, map[int]error{400: ErrObjectNotFound})
	if err != nil {
		if errors.Is(err, ErrContainerNotFound) {
			return rawImage{}, "", ErrObjectNotFound
		}
		return rawImage{}, "", err
	}
	defer cancel()
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return rawImage{}, "", c.transportErr(ctx, callCtx)
	}
	if int64(len(body)) > c.maxBody || json.Unmarshal(body, &raw) != nil {
		return rawImage{}, "", fmt.Errorf("%w: malformed response", ErrDockerUnavailable)
	}
	raw.ID = strings.TrimPrefix(raw.ID, "sha256:")
	if len(raw.ID) != 64 || !isHexString(raw.ID) {
		return rawImage{}, "", fmt.Errorf("%w: malformed response", ErrDockerUnavailable)
	}
	if isHexID(ref) {
		if !strings.HasPrefix(raw.ID, ref) {
			return rawImage{}, "", ErrObjectNotFound
		}
		return raw, "", nil
	}
	cands := []string{ref}
	if !strings.ContainsRune(ref, '@') && !refHasTag(ref) && ValidImageRef(ref+":latest") {
		cands = append(cands, ref+":latest")
	}
	for _, cand := range cands {
		for _, list := range [][]string{raw.RepoTags, raw.RepoDigests} {
			for _, e := range list {
				if e == cand && ValidImageRef(e) {
					return raw, e, nil
				}
			}
		}
	}
	return rawImage{}, "", ErrObjectNotFound
}

// imageUsers lists the names of containers, in any state, whose image ID is id
// (the bare 64 character ID). Docker's own refusal is the second line of
// defence for a container created in between.
func (c *httpClient) imageUsers(ctx context.Context, id string) ([]string, error) {
	var list []struct {
		ID      string   `json:"Id"`
		Names   []string `json:"Names"`
		ImageID string   `json:"ImageID"`
	}
	if err := c.get(ctx, c.listTimeout, "/containers/json", url.Values{"all": {"1"}}, &list); err != nil {
		return nil, remapNotFound(err)
	}
	out := []string{}
	for _, e := range list {
		if strings.TrimPrefix(e.ImageID, "sha256:") != id {
			continue
		}
		n := ""
		if len(e.Names) > 0 {
			n = strings.TrimPrefix(e.Names[0], "/")
		}
		out = append(out, containerLabel(n, e.ID))
	}
	sort.Strings(out)
	return out, nil
}

func validRefs(in []string) []string {
	out := []string{}
	for _, r := range in {
		if ValidImageRef(r) {
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}

// InspectImage reads one image by ID or reference. Layers are time and size
// only. An unknown image gives ErrObjectNotFound.
func (c *httpClient) InspectImage(ctx context.Context, ref string) (ImageDetail, error) {
	if !ValidImageRef(ref) {
		return ImageDetail{}, ErrBadName
	}
	raw, _, err := c.imageLookup(ctx, ref)
	if err != nil {
		return ImageDetail{}, err
	}
	users, err := c.imageUsers(ctx, raw.ID)
	if err != nil {
		return ImageDetail{}, err
	}
	hp, err := objectTarget("/images", raw.ID, "/history")
	if err != nil {
		return ImageDetail{}, err
	}
	var hist []struct {
		Created int64 `json:"Created"`
		Size    int64 `json:"Size"`
	}
	if err := c.get(ctx, c.listTimeout, hp.path, nil, &hist); err != nil {
		return ImageDetail{}, remapNotFound(err)
	}
	d := ImageDetail{
		ID: shortID(raw.ID), Tags: []string{}, Digests: []string{}, SizeBytes: nonNeg(raw.Size),
		Architecture: plainToken(raw.Architecture), OS: plainToken(raw.OS),
		ExposedPorts: []int{}, ContainerNames: users, Layers: []ImageLayer{}, LayerCount: len(hist),
	}
	for _, t := range validRefs(raw.RepoTags) {
		d.Tags = append(d.Tags, t)
	}
	d.Digests = validRefs(raw.RepoDigests)
	if t, err := time.Parse(time.RFC3339Nano, raw.Created); err == nil {
		d.Created = t.UTC()
	}
	seen := map[int]bool{}
	for k := range raw.Config.ExposedPorts {
		num, _, _ := strings.Cut(k, "/")
		n, err := strconv.Atoi(num)
		if err != nil || n < 1 || n > 65535 || seen[n] || len(d.ExposedPorts) >= maxExposedPorts {
			continue
		}
		seen[n] = true
		d.ExposedPorts = append(d.ExposedPorts, n)
	}
	sort.Ints(d.ExposedPorts)
	for i, h := range hist {
		if i >= maxLayers {
			break
		}
		l := ImageLayer{SizeBytes: nonNeg(h.Size)}
		if h.Created > 0 {
			l.Created = time.Unix(h.Created, 0).UTC()
		}
		d.Layers = append(d.Layers, l)
	}
	return d, nil
}

func nonNeg(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

// plainToken returns s when it is a short plain token such as "amd64" or
// "linux", else "".
func plainToken(s string) string {
	if s == "" || len(s) > 32 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == '/') {
			return ""
		}
	}
	return s
}

// TagImage adds repository:tag to an image found by ID or reference. Every
// argument is validated here (ValidImageRef, ValidImageTarget). Docker's tag
// call silently moves an existing tag from the image that has it, so the
// client looks first: a repository:tag that exists on ANOTHER image gives
// ErrConflict and no request; one that already names this image succeeds
// without a request. Anything unclear about that lookup fails closed. The tag
// request goes to the image's full ID, not to the URL text.
func (c *httpClient) TagImage(ctx context.Context, ref, repository, tag string) error {
	if !ValidImageRef(ref) || !ValidImageTarget(repository, tag) {
		return ErrBadName
	}
	raw, _, err := c.imageLookup(ctx, ref)
	if err != nil {
		return err
	}
	tp, err := RefPath("/images", repository+":"+tag)
	if err != nil {
		return err
	}
	resp, callCtx, cancel, err := c.do(ctx, c.listTimeout, tp+"/json", nil, map[int]error{400: ErrInvalidRequest})
	switch {
	case err == nil:
		defer cancel()
		defer resp.Body.Close()
		var other struct {
			ID string `json:"Id"`
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
		if rerr != nil {
			return c.transportErr(ctx, callCtx)
		}
		if int64(len(body)) > c.maxBody || json.Unmarshal(body, &other) != nil {
			return fmt.Errorf("%w: malformed response", ErrDockerUnavailable)
		}
		if strings.TrimPrefix(other.ID, "sha256:") == raw.ID {
			return nil
		}
		return ErrConflict
	case errors.Is(err, ErrContainerNotFound):
		// free
	default:
		return err
	}
	t, err := objectTarget("/images", raw.ID, "/tag")
	if err != nil {
		return err
	}
	return c.post(ctx, writeTimeout, t, url.Values{"repo": {repository}, "tag": {tag}}, nil, nil, 201)
}

// removalFrom folds Docker's list of deleted items into an ImageRemoval part.
type removalAcc struct {
	untagged, deleted []string
	count             int
	dropped           bool
	unknown           bool
}

func (a *removalAcc) add(items []rawDeleteItem) {
	for _, it := range items {
		a.count++
		switch {
		case it.Untagged != "" && ValidImageRef(it.Untagged):
			a.untagged = append(a.untagged, it.Untagged)
		case it.Deleted != "":
			id := strings.TrimPrefix(it.Deleted, "sha256:")
			if len(id) >= 12 && len(id) <= 64 && isHexString(id) {
				a.deleted = append(a.deleted, id[:12])
				continue
			}
			a.dropped = true
		default:
			a.dropped = true
		}
	}
}

func (a *removalAcc) result() ImageRemoval {
	out := ImageRemoval{Untagged: []string{}, Deleted: []string{}, Complete: !a.unknown}
	if !a.unknown {
		n := a.count
		out.Count = &n
	}
	sort.Strings(a.untagged)
	sort.Strings(a.deleted)
	for _, u := range a.untagged {
		if len(out.Untagged) < maxPrunedNames {
			out.Untagged = append(out.Untagged, u)
		}
	}
	for _, d := range a.deleted {
		if len(out.Deleted) < maxPrunedNames {
			out.Deleted = append(out.Deleted, d)
		}
	}
	out.Truncated = out.Count != nil && len(out.Untagged)+len(out.Deleted) < *out.Count
	return out
}

// RemoveImage removes an image or one tag of it and never forces. noprune is
// sent (Docker keeps untagged parent images), so only what the caller named is
// touched; dangling parents can be removed by a prune.
//
//  1. ref is looked up strictly (imageLookup). An image used by any container,
//     running, stopped or created, gives *InUseError without a DELETE; if a
//     container appears later, Docker's own 409 gives ErrConflict.
//  2. A reference (a tag or digest) untags exactly the reference Docker
//     reported. Docker deletes the image when that was its last reference.
//  3. An ID removes the image and all its references: Docker refuses a
//     removal by ID when the image has more than one reference unless forced,
//     so each reference (tags, then digests) is removed on its own, never
//     forced; the last one deletes the image. A failure part-way leaves the
//     earlier references removed. An image with no reference is removed by
//     its full ID. An image with a reference this client cannot name safely
//     gives ErrConflict before anything is removed.
//
// A 200 means Docker acted; an unreadable answer then is a success with
// Complete false.
func (c *httpClient) RemoveImage(ctx context.Context, ref string) (ImageRemoval, error) {
	if !ValidImageRef(ref) {
		return ImageRemoval{}, ErrBadName
	}
	raw, matched, err := c.imageLookup(ctx, ref)
	if err != nil {
		return ImageRemoval{}, err
	}
	users, err := c.imageUsers(ctx, raw.ID)
	if err != nil {
		return ImageRemoval{}, err
	}
	if len(users) > 0 {
		return ImageRemoval{}, &InUseError{Containers: len(users)}
	}
	var targets []target
	if matched != "" {
		t, err := refTarget("/images", matched)
		if err != nil {
			return ImageRemoval{}, err
		}
		targets = append(targets, t)
	} else {
		var refs []string
		for _, e := range append(append([]string{}, raw.RepoTags...), raw.RepoDigests...) {
			if e == "<none>:<none>" || e == "<none>@<none>" {
				continue
			}
			if !ValidImageRef(e) {
				return ImageRemoval{}, ErrConflict
			}
			refs = append(refs, e)
		}
		if len(refs) == 0 {
			t, err := objectTarget("/images", raw.ID)
			if err != nil {
				return ImageRemoval{}, err
			}
			targets = append(targets, t)
		}
		for _, e := range refs {
			t, err := refTarget("/images", e)
			if err != nil {
				return ImageRemoval{}, err
			}
			targets = append(targets, t)
		}
	}
	acc := &removalAcc{}
	for _, t := range targets {
		var items []rawDeleteItem
		err := c.delete(ctx, writeTimeout, t, url.Values{"noprune": {"1"}}, &items, 200)
		switch {
		case errors.Is(err, errUnusableAnswer):
			acc.unknown = true
		case err != nil:
			return ImageRemoval{}, err
		default:
			acc.add(items)
		}
	}
	return acc.result(), nil
}

// PruneImages removes unused images. all false sends the filter dangling=true:
// only untagged images that no container uses. all true sends dangling=false:
// every image no container uses, tagged ones included (verified against the
// v1.41 API description, see PruneImagesIncludesTagged). The filter is always
// sent explicitly, built by json.Marshal; nothing else is. A 200 means the
// prune ran: an answer that is then unreadable is a success with Complete false.
func (c *httpClient) PruneImages(ctx context.Context, all bool) (ImagePruneResult, error) {
	dangling := "true"
	if all {
		dangling = "false"
	}
	f, err := json.Marshal(map[string][]string{"dangling": {dangling}})
	if err != nil {
		return ImagePruneResult{}, ErrBadName
	}
	var raw struct {
		Deleted []rawDeleteItem `json:"ImagesDeleted"`
		Space   json.RawMessage `json:"SpaceReclaimed"`
	}
	err = c.post(ctx, pruneTimeout, fixedTarget("/images/prune"), url.Values{"filters": {string(f)}}, nil, &raw, 200)
	if errors.Is(err, errUnusableAnswer) {
		return ImagePruneResult{ImageRemoval: (&removalAcc{unknown: true}).result()}, nil
	}
	if err != nil {
		return ImagePruneResult{}, err
	}
	acc := &removalAcc{}
	acc.add(raw.Deleted)
	out := ImagePruneResult{ImageRemoval: acc.result()}
	if n, perr := strconv.ParseInt(strings.TrimSpace(string(raw.Space)), 10, 64); perr == nil {
		n = nonNeg(n)
		out.ReclaimedBytes = &n
	} else {
		out.Complete = false
	}
	return out, nil
}
