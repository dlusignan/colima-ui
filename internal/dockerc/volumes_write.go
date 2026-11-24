package dockerc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// pruneTimeout bounds a volume prune, which walks every unused volume.
const pruneTimeout = 2 * time.Minute

// maxPrunedNames caps the removed names a prune answer carries.
const maxPrunedNames = 1000

// PruneIncludesNamedVolumes is true because the client pins API v1.41. Before
// v1.42 the engine prunes every unused volume, named ones included, and the
// "all" filter that selects that behaviour does not exist (it was added in
// v1.42 together with the anonymous-only default). The client sends no filters,
// so on v1.41 the answer is: all unused volumes are removed. A test pins this
// to apiVersion, so moving the pin forces a decision here.
const PruneIncludesNamedVolumes = true

// VolumeDetail is the inspect answer for one volume. It has no mountpoint, no
// driver options and no status map, so no host path can leak.
type VolumeDetail struct {
	Name           string
	Driver         string
	Created        time.Time         // UTC; zero when unparseable
	SizeBytes      *int64            // nil when the engine has no figure
	Labels         map[string]string // never nil
	ContainerNames []string          // containers, running or not, that mount it; never nil
}

// PruneResult is the outcome of a volume prune.
type PruneResult struct {
	Removed        []string // never nil, sorted, valid names only
	ReclaimedBytes int64
}

// InUseError wraps ErrConflict with the number of containers that mount the
// volume, so the API can say how many.
type InUseError struct{ Containers int }

func (e *InUseError) Error() string {
	return fmt.Sprintf("%v: %d containers mount it", ErrConflict, e.Containers)
}
func (e *InUseError) Unwrap() error { return ErrConflict }

// volumeUsers lists the names of containers (any state) that mount the volume.
// name must already be valid; the filter is built by json.Marshal, never by
// concatenation.
func (c *httpClient) volumeUsers(ctx context.Context, name string) ([]string, error) {
	f, err := json.Marshal(map[string][]string{"volume": {name}})
	if err != nil {
		return nil, ErrBadName
	}
	var raw []struct {
		Names []string `json:"Names"`
		ID    string   `json:"Id"`
	}
	q := url.Values{"all": {"1"}, "filters": {string(f)}}
	if err := c.get(ctx, c.listTimeout, "/containers/json", q, &raw); err != nil {
		return nil, remapNotFound(err)
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		n := ""
		if len(r.Names) > 0 {
			n = strings.TrimPrefix(r.Names[0], "/")
		}
		if n == "" {
			n = shortID(r.ID)
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// volumeInspect asks Docker for one volume. ErrObjectNotFound means absent.
func (c *httpClient) volumeInspect(ctx context.Context, name string) (VolumeDetail, error) {
	p, err := ObjectPath("/volumes", name)
	if err != nil {
		return VolumeDetail{}, err
	}
	var raw struct {
		Name      string            `json:"Name"`
		Driver    string            `json:"Driver"`
		CreatedAt string            `json:"CreatedAt"`
		Labels    map[string]string `json:"Labels"`
	}
	// get maps Docker's 404 to ErrContainerNotFound; here it means no volume.
	if err := c.get(ctx, c.listTimeout, p, nil, &raw); err != nil {
		if errors.Is(err, ErrContainerNotFound) {
			return VolumeDetail{}, ErrObjectNotFound
		}
		return VolumeDetail{}, err
	}
	d := VolumeDetail{Name: name, Driver: raw.Driver, Labels: map[string]string{}, ContainerNames: []string{}}
	if t, err := time.Parse(time.RFC3339, raw.CreatedAt); err == nil {
		d.Created = t.UTC()
	}
	for k, v := range raw.Labels {
		d.Labels[k] = v
	}
	return d, nil
}

// InspectVolume reads one volume, the containers that mount it and its size.
// The size comes from /system/df, the only call that has it, and is nil when
// that call fails or lacks the volume (best effort; the rest still answers).
func (c *httpClient) InspectVolume(ctx context.Context, name string) (VolumeDetail, error) {
	if !ValidObjectName(name) {
		return VolumeDetail{}, ErrBadName
	}
	d, err := c.volumeInspect(ctx, name)
	if err != nil {
		return VolumeDetail{}, err
	}
	users, err := c.volumeUsers(ctx, name)
	if err != nil {
		return VolumeDetail{}, err
	}
	d.ContainerNames = users
	if all, err := c.Volumes(ctx); err == nil {
		for _, v := range all {
			if v.Name == name {
				d.SizeBytes = v.SizeBytes
			}
		}
	} else if ctx.Err() != nil {
		return VolumeDetail{}, ctx.Err()
	}
	return d, nil
}

// CreateVolume creates a local volume with the labels. It refuses an existing
// name with ErrConflict: Docker itself answers 201 with the existing volume
// when the name and driver match, so the existence check is ours (inspect
// first, 404 means free). Two creates racing for one name can both pass the
// check; Docker then treats the second as a no-op on the same volume.
func (c *httpClient) CreateVolume(ctx context.Context, name string, labels map[string]string) (Volume, error) {
	if !ValidObjectName(name) || !ValidLabels(labels) {
		return Volume{}, ErrBadName
	}
	_, err := c.volumeInspect(ctx, name)
	switch {
	case err == nil:
		return Volume{}, ErrConflict
	case !errors.Is(err, ErrObjectNotFound):
		return Volume{}, err
	}
	body := map[string]any{"Name": name, "Driver": "local"}
	if len(labels) > 0 {
		body["Labels"] = labels
	}
	var raw struct {
		Driver    string `json:"Driver"`
		CreatedAt string `json:"CreatedAt"`
	}
	if err := c.post(ctx, writeTimeout, fixedTarget("/volumes/create"), nil, body, &raw, 201); err != nil {
		return Volume{}, err
	}
	v := Volume{Name: name, Driver: raw.Driver}
	if v.Driver == "" {
		v.Driver = "local"
	}
	if t, err := time.Parse(time.RFC3339, raw.CreatedAt); err == nil {
		v.Created = t.UTC()
	}
	return v, nil
}

// RemoveVolume removes one volume and never sends force. A volume mounted by
// any container, running or stopped, gives *InUseError without a DELETE; if a
// container appears after that check Docker's own 409 gives ErrConflict.
func (c *httpClient) RemoveVolume(ctx context.Context, name string) error {
	t, err := objectTarget("/volumes", name)
	if err != nil {
		return err
	}
	if _, err := c.volumeInspect(ctx, name); err != nil {
		return err
	}
	users, err := c.volumeUsers(ctx, name)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return &InUseError{Containers: len(users)}
	}
	return c.delete(ctx, writeTimeout, t, nil, nil, 204)
}

// PruneVolumes removes unused volumes. On API v1.41 that means every unused
// volume, named ones included (see PruneIncludesNamedVolumes). No filters are
// sent.
func (c *httpClient) PruneVolumes(ctx context.Context) (PruneResult, error) {
	var raw struct {
		Deleted []string `json:"VolumesDeleted"`
		Space   int64    `json:"SpaceReclaimed"`
	}
	if err := c.post(ctx, pruneTimeout, fixedTarget("/volumes/prune"), nil, nil, &raw, 200); err != nil {
		return PruneResult{}, err
	}
	out := PruneResult{Removed: []string{}, ReclaimedBytes: raw.Space}
	if out.ReclaimedBytes < 0 {
		out.ReclaimedBytes = 0
	}
	for _, n := range raw.Deleted {
		if ValidObjectName(n) && len(out.Removed) < maxPrunedNames {
			out.Removed = append(out.Removed, n)
		}
	}
	sort.Strings(out.Removed)
	return out, nil
}
