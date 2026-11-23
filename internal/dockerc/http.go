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
	"time"
)

const (
	apiVersion       = "v1.41"
	listTimeout      = 5 * time.Second
	statsTimeout     = 5 * time.Second
	diskUsageTimeout = 15 * time.Second
	maxBodyBytes     = 8 << 20
)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type httpClient struct {
	hc               *http.Client
	maxBody          int64
	listTimeout      time.Duration
	statsTimeout     time.Duration
	diskUsageTimeout time.Duration
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
		hc:               &http.Client{Transport: tr},
		maxBody:          maxBodyBytes,
		listTimeout:      listTimeout,
		statsTimeout:     statsTimeout,
		diskUsageTimeout: diskUsageTimeout,
	}
}

// get performs one GET with a deadline and decodes a size-capped JSON body.
func (c *httpClient) get(ctx context.Context, timeout time.Duration, path string, query url.Values, dst any) error {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := "http://docker/" + apiVersion + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("%w: bad request", ErrDockerUnavailable)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return c.transportErr(ctx, callCtx)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return ErrContainerNotFound
	case resp.StatusCode != http.StatusOK:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%w: unexpected status %d", ErrDockerUnavailable, resp.StatusCode)
	}
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
	}
	if err := c.get(ctx, c.listTimeout, "/containers/json", url.Values{"all": {"1"}}, &raw); err != nil {
		if errors.Is(err, ErrContainerNotFound) {
			return nil, fmt.Errorf("%w: unexpected status 404", ErrDockerUnavailable)
		}
		return nil, err
	}
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		ct := Container{ID: shortID(r.ID), Image: r.Image, State: r.State}
		if len(r.Names) > 0 {
			ct.Name = trimSlash(r.Names[0])
		}
		out = append(out, ct)
	}
	return out, nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
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
