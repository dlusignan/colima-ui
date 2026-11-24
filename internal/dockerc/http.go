package dockerc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	apiVersion       = "v1.41"
	listTimeout      = 5 * time.Second
	statsTimeout     = 5 * time.Second
	diskUsageTimeout = 15 * time.Second
	logsTimeout      = 15 * time.Second
	maxBodyBytes     = 8 << 20
)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type httpClient struct {
	hc               *http.Client
	maxBody          int64
	listTimeout      time.Duration
	statsTimeout     time.Duration
	diskUsageTimeout time.Duration
	logsTimeout      time.Duration
	// pullTimeout bounds a whole pull; pullIdle ends one that goes quiet.
	pullTimeout time.Duration
	pullIdle    time.Duration
	// lockKey identifies the engine (its socket) for tagLocks.
	lockKey string
}

var _ Client = (*httpClient)(nil)

// newHTTPClient builds a Client that talks only to the given Unix socket.
// Proxies are never used.
func newHTTPClient(socket string) *httpClient {
	var d net.Dialer
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:    2,
		IdleConnTimeout: 30 * time.Second,
	}
	return &httpClient{
		// A redirect is never followed: Docker does not send one, and a
		// write must not be replayed against a URL nobody validated.
		hc: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		maxBody:          maxBodyBytes,
		listTimeout:      listTimeout,
		statsTimeout:     statsTimeout,
		diskUsageTimeout: diskUsageTimeout,
		logsTimeout:      logsTimeout,
		pullTimeout:      pullTimeout,
		pullIdle:         pullIdleTimeout,
		lockKey:          socket,
	}
}

// do performs one GET with a deadline and maps the status. A status in
// special maps to its error; otherwise 404 and 409 give ErrContainerNotFound
// and any other non-200 gives ErrDockerUnavailable. On success the caller owns
// the body and must call cancel after reading it. callCtx is the context the
// body read is bound to, for transportErr.
func (c *httpClient) do(ctx context.Context, timeout time.Duration, path string, query url.Values, special map[int]error) (resp *http.Response, callCtx context.Context, cancel context.CancelFunc, err error) {
	callCtx, cancel = context.WithTimeout(ctx, timeout)
	u := "http://docker/" + apiVersion + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, u, nil)
	if err != nil {
		cancel()
		return nil, nil, nil, fmt.Errorf("%w: bad request", ErrDockerUnavailable)
	}
	resp, err = c.hc.Do(req)
	if err != nil {
		terr := c.transportErr(ctx, callCtx)
		cancel()
		return nil, nil, nil, terr
	}
	if resp.StatusCode == http.StatusOK {
		return resp, callCtx, cancel, nil
	}
	defer cancel()
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if e, ok := special[resp.StatusCode]; ok {
		return nil, nil, nil, e
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict {
		return nil, nil, nil, ErrContainerNotFound
	}
	return nil, nil, nil, fmt.Errorf("%w: unexpected status %d", ErrDockerUnavailable, resp.StatusCode)
}

// get performs one GET with a deadline and decodes a size-capped JSON body.
func (c *httpClient) get(ctx context.Context, timeout time.Duration, path string, query url.Values, dst any) error {
	resp, callCtx, cancel, err := c.do(ctx, timeout, path, query, nil)
	if err != nil {
		return err
	}
	defer cancel()
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return c.transportErr(ctx, callCtx)
	}
	if int64(len(body)) > c.maxBody {
		return fmt.Errorf("%w: response too large", ErrDockerUnavailable)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%w: malformed response", ErrDockerUnavailable)
	}
	return nil
}

// Logs reads the last tail lines of a container's stdout and stderr. It makes
// two read-only calls: an inspect, for Config.Tty and a clean not-found, then
// the logs call with tail+1 so that older lines can be detected.
func (c *httpClient) Logs(ctx context.Context, id string, tail int) (LogResult, error) {
	if !ValidLogID(id) {
		return LogResult{}, ErrContainerNotFound
	}
	if tail < 1 {
		tail = 1
	}
	if tail > MaxLogTail {
		tail = MaxLogTail
	}
	var info struct {
		Config struct {
			Tty bool `json:"Tty"`
		} `json:"Config"`
	}
	if err := c.get(ctx, c.listTimeout, "/containers/"+id+"/json", nil, &info); err != nil {
		return LogResult{}, logsDeadline(err)
	}
	q := url.Values{
		"stdout":     {"1"},
		"stderr":     {"1"},
		"timestamps": {"1"},
		"tail":       {strconv.Itoa(tail + 1)},
	}
	special := map[int]error{http.StatusNotImplemented: ErrLogsUnavailable}
	resp, callCtx, cancel, err := c.do(ctx, c.logsTimeout, "/containers/"+id+"/logs", q, special)
	if err != nil {
		return LogResult{}, logsDeadline(err)
	}
	defer cancel()
	defer resp.Body.Close()
	res, err := decodeLogs(resp.Body, info.Config.Tty, tail)
	if err != nil {
		if errors.Is(err, ErrDockerUnavailable) {
			return LogResult{}, err
		}
		return LogResult{}, logsDeadline(c.transportErr(ctx, callCtx))
	}
	return res, nil
}

// logsDeadline turns an expired read deadline into ErrDockerUnavailable, so the
// route answers 502 as documented. A cancelled caller keeps its own error.
func logsDeadline(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: log read timed out", ErrDockerUnavailable)
	}
	return err
}

// transportErr maps a failed round trip without leaking the socket path.
func (c *httpClient) transportErr(parent, call context.Context) error {
	if err := parent.Err(); err != nil {
		return fmt.Errorf("dockerc: %w", err)
	}
	if errors.Is(call.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("dockerc: request timed out: %w", context.DeadlineExceeded)
	}
	return fmt.Errorf("%w: cannot reach docker socket", ErrDockerUnavailable)
}

func (c *httpClient) Containers(ctx context.Context) ([]Container, error) {
	var raw []struct {
		ID    string   `json:"Id"`
		Names []string `json:"Names"`
		Image string   `json:"Image"`
		State string   `json:"State"`

		Status  string `json:"Status"`
		Created int64  `json:"Created"`
		Ports   []struct {
			IP          string `json:"IP"`
			PrivatePort uint16 `json:"PrivatePort"`
			PublicPort  uint16 `json:"PublicPort"`
			Type        string `json:"Type"`
		} `json:"Ports"`
	}
	if err := c.get(ctx, c.listTimeout, "/containers/json", url.Values{"all": {"1"}}, &raw); err != nil {
		if errors.Is(err, ErrContainerNotFound) {
			return nil, fmt.Errorf("%w: unexpected status 404", ErrDockerUnavailable)
		}
		return nil, err
	}
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		ct := Container{
			ID: shortID(r.ID), Image: r.Image, State: r.State,
			Status: r.Status, Created: time.Unix(r.Created, 0).UTC(),
		}
		if ValidContainerID(r.ID) {
			ct.FullID = r.ID
		}
		for _, p := range r.Ports {
			ct.Ports = append(ct.Ports, Port{
				ContainerPort: p.PrivatePort, Protocol: p.Type,
				HostIP: p.IP, HostPort: p.PublicPort,
			})
		}
		if len(r.Names) > 0 {
			ct.Name = trimSlash(r.Names[0])
		}
		out = append(out, ct)
	}
	return out, nil
}

// countContents is CountContents for the real client: three list calls, each
// decoded only as far as its length. No name or id is kept.
func (c *httpClient) countContents(ctx context.Context) (Contents, error) {
	var containers []json.RawMessage
	if err := c.get(ctx, c.listTimeout, "/containers/json", url.Values{"all": {"1"}}, &containers); err != nil {
		return Contents{}, remapNotFound(err)
	}
	var images []json.RawMessage
	if err := c.get(ctx, c.listTimeout, "/images/json", nil, &images); err != nil {
		return Contents{}, remapNotFound(err)
	}
	var vols struct {
		Volumes []json.RawMessage `json:"Volumes"`
	}
	if err := c.get(ctx, c.listTimeout, "/volumes", nil, &vols); err != nil {
		return Contents{}, remapNotFound(err)
	}
	return Contents{Containers: len(containers), Images: len(images), Volumes: len(vols.Volumes)}, nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// imageShortID drops the "sha256:" prefix, then takes 12 characters.
func imageShortID(id string) string {
	return shortID(strings.TrimPrefix(id, "sha256:"))
}

func trimSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}

func (c *httpClient) Stats(ctx context.Context, id string) (Reading, error) {
	if !idPattern.MatchString(id) {
		return Reading{}, fmt.Errorf("dockerc: invalid container id")
	}
	var raw struct {
		CPUStats struct {
			CPUUsage struct {
				Total  uint64   `json:"total_usage"`
				PerCPU []uint64 `json:"percpu_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
			Online uint32 `json:"online_cpus"`
		} `json:"cpu_stats"`
		MemoryStats struct {
			Usage uint64            `json:"usage"`
			Limit uint64            `json:"limit"`
			Stats map[string]uint64 `json:"stats"`
		} `json:"memory_stats"`
	}
	q := url.Values{"stream": {"false"}, "one-shot": {"true"}}
	if err := c.get(ctx, c.statsTimeout, "/containers/"+url.PathEscape(id)+"/stats", q, &raw); err != nil {
		return Reading{}, err
	}
	online := raw.CPUStats.Online
	if online == 0 {
		online = uint32(len(raw.CPUStats.CPUUsage.PerCPU))
	}
	if online == 0 {
		online = 1
	}
	return Reading{
		At:            time.Now(),
		CPUTotalNanos: raw.CPUStats.CPUUsage.Total,
		SystemNanos:   raw.CPUStats.System,
		OnlineCPUs:    online,
		MemoryBytes:   memoryBytes(raw.MemoryStats.Usage, raw.MemoryStats.Stats),
		MemoryLimit:   raw.MemoryStats.Limit,
	}, nil
}

func (c *httpClient) DiskUsage(ctx context.Context) (DiskUsage, error) {
	var raw struct {
		LayersSize int64 `json:"LayersSize"`
		Images     []struct {
			Size int64 `json:"Size"`
		} `json:"Images"`
		Containers []struct {
			SizeRw int64 `json:"SizeRw"`
		} `json:"Containers"`
		Volumes []struct {
			UsageData *struct {
				Size int64 `json:"Size"`
			} `json:"UsageData"`
		} `json:"Volumes"`
		BuildCache []struct {
			Size int64 `json:"Size"`
		} `json:"BuildCache"`
	}
	if err := c.get(ctx, c.diskUsageTimeout, "/system/df", nil, &raw); err != nil {
		if errors.Is(err, ErrContainerNotFound) {
			return DiskUsage{}, fmt.Errorf("%w: unexpected status 404", ErrDockerUnavailable)
		}
		return DiskUsage{}, err
	}
	pos := func(n int64) int64 { // Docker reports -1 for "unknown"
		if n < 0 {
			return 0
		}
		return n
	}
	du := DiskUsage{LayersBytes: pos(raw.LayersSize)}
	for _, i := range raw.Images {
		du.ImagesBytes += pos(i.Size)
	}
	for _, ct := range raw.Containers {
		du.ContainersBytes += pos(ct.SizeRw)
	}
	for _, v := range raw.Volumes {
		if v.UsageData != nil {
			du.VolumesBytes += pos(v.UsageData.Size)
		}
	}
	for _, b := range raw.BuildCache {
		du.BuildCacheBytes += pos(b.Size)
	}
	return du, nil
}

// Info reads the engine facts. It decodes only the fields of Engine from
// /info, so the host name and the data root never leave this function. The
// engine's API version comes from a second, sequential call to /version,
// because /info has no such field and the client pins an older API.
func (c *httpClient) Info(ctx context.Context) (Engine, error) {
	var raw struct {
		ServerVersion     string `json:"ServerVersion"`
		OSType            string `json:"OSType"`
		Architecture      string `json:"Architecture"`
		NCPU              int    `json:"NCPU"`
		MemTotal          int64  `json:"MemTotal"`
		Containers        int    `json:"Containers"`
		ContainersRunning int    `json:"ContainersRunning"`
		ContainersPaused  int    `json:"ContainersPaused"`
		ContainersStopped int    `json:"ContainersStopped"`
		Images            int    `json:"Images"`
	}
	if err := c.get(ctx, c.listTimeout, "/info", nil, &raw); err != nil {
		return Engine{}, remapNotFound(err)
	}
	var ver struct {
		APIVersion string `json:"ApiVersion"`
	}
	if err := c.get(ctx, c.listTimeout, "/version", nil, &ver); err != nil {
		return Engine{}, remapNotFound(err)
	}
	return Engine{
		Version:     raw.ServerVersion,
		APIVersion:  ver.APIVersion,
		OS:          raw.OSType,
		Arch:        raw.Architecture,
		CPUs:        raw.NCPU,
		MemoryBytes: raw.MemTotal,
		Containers: ContainerCounts{
			Total: raw.Containers, Running: raw.ContainersRunning,
			Paused: raw.ContainersPaused, Stopped: raw.ContainersStopped,
		},
		Images: raw.Images,
	}, nil
}

// Images lists images and counts the containers that use each one. It makes
// two calls: /images/json and /containers/json?all=1. The Containers field of
// /images/json is -1 on API v1.41, so the count is taken from each
// container's ImageID instead. The two calls are not atomic, so the count is
// a snapshot.
func (c *httpClient) Images(ctx context.Context) ([]Image, error) {
	var rawImages []struct {
		ID       string   `json:"Id"`
		RepoTags []string `json:"RepoTags"`
		Size     int64    `json:"Size"`
		Created  int64    `json:"Created"`
	}
	if err := c.get(ctx, c.listTimeout, "/images/json", nil, &rawImages); err != nil {
		return nil, remapNotFound(err)
	}
	var rawContainers []struct {
		ImageID string `json:"ImageID"`
	}
	if err := c.get(ctx, c.listTimeout, "/containers/json", url.Values{"all": {"1"}}, &rawContainers); err != nil {
		return nil, remapNotFound(err)
	}
	uses := make(map[string]int, len(rawContainers))
	for _, ct := range rawContainers {
		uses[ct.ImageID]++
	}
	out := make([]Image, 0, len(rawImages))
	for _, r := range rawImages {
		img := Image{
			ID: imageShortID(r.ID), Tags: []string{}, SizeBytes: r.Size,
			Created: time.Unix(r.Created, 0).UTC(), Containers: uses[r.ID],
		}
		for _, tag := range r.RepoTags {
			if tag != "" && tag != "<none>:<none>" {
				img.Tags = append(img.Tags, tag)
			}
		}
		out = append(out, img)
	}
	return out, nil
}

// Volumes lists volumes from /system/df. Mountpoint is deliberately not
// decoded. SizeBytes and Containers are nil when UsageData is absent or the
// value is negative (Docker's -1 for unknown); zero stays zero.
func (c *httpClient) Volumes(ctx context.Context) ([]Volume, error) {
	var raw struct {
		Volumes []struct {
			Name      string `json:"Name"`
			Driver    string `json:"Driver"`
			CreatedAt string `json:"CreatedAt"`
			UsageData *struct {
				Size     int64 `json:"Size"`
				RefCount int64 `json:"RefCount"`
			} `json:"UsageData"`
		} `json:"Volumes"`
	}
	if err := c.get(ctx, c.diskUsageTimeout, "/system/df", nil, &raw); err != nil {
		return nil, remapNotFound(err)
	}
	out := make([]Volume, 0, len(raw.Volumes))
	for _, r := range raw.Volumes {
		v := Volume{Name: r.Name, Driver: r.Driver}
		if t, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil {
			v.Created = t.UTC()
		}
		if u := r.UsageData; u != nil {
			if u.Size >= 0 {
				size := u.Size
				v.SizeBytes = &size
			}
			if u.RefCount >= 0 {
				n := int(u.RefCount)
				v.Containers = &n
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// Networks lists networks and counts the containers attached to each. It
// makes two calls: /networks and /containers/json?all=1, because the network
// list has no Containers map and inspecting each network would be N+1.
// Stopped containers count. The two calls are not atomic.
func (c *httpClient) Networks(ctx context.Context) ([]Network, error) {
	var rawNets []struct {
		ID       string `json:"Id"`
		Name     string `json:"Name"`
		Driver   string `json:"Driver"`
		Scope    string `json:"Scope"`
		Internal bool   `json:"Internal"`
	}
	if err := c.get(ctx, c.listTimeout, "/networks", nil, &rawNets); err != nil {
		return nil, remapNotFound(err)
	}
	var rawContainers []struct {
		NetworkSettings *struct {
			Networks map[string]struct {
				NetworkID string `json:"NetworkID"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := c.get(ctx, c.listTimeout, "/containers/json", url.Values{"all": {"1"}}, &rawContainers); err != nil {
		return nil, remapNotFound(err)
	}
	uses := make(map[string]int)
	for _, ct := range rawContainers {
		if ct.NetworkSettings == nil {
			continue
		}
		seen := make(map[string]bool, len(ct.NetworkSettings.Networks))
		for _, n := range ct.NetworkSettings.Networks {
			if n.NetworkID != "" && !seen[n.NetworkID] {
				seen[n.NetworkID] = true
				uses[n.NetworkID]++
			}
		}
	}
	out := make([]Network, 0, len(rawNets))
	for _, r := range rawNets {
		out = append(out, Network{
			ID: shortID(r.ID), Name: r.Name, Driver: r.Driver, Scope: r.Scope,
			Internal: r.Internal, Containers: uses[r.ID],
		})
	}
	return out, nil
}

// remapNotFound turns the 404 and 409 that get maps to ErrContainerNotFound
// into ErrDockerUnavailable, because list calls have no container to miss.
func remapNotFound(err error) error {
	if errors.Is(err, ErrContainerNotFound) {
		return fmt.Errorf("%w: unexpected status 404", ErrDockerUnavailable)
	}
	return err
}
