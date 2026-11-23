package sampler

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

const (
	defaultMaxContainers  = 64
	defaultContainerTO    = 4 * time.Second
	defaultDiskTO         = 10 * time.Second
	defaultDiskEvery      = 12 // ticks
	defaultDiskStaleAfter = 3 * time.Minute
	clientRetryAfter      = 30 * time.Second
)

// ClientCache keeps one dockerc.Client per profile. The provider resolves the
// socket by spawning colima, so it is called on a miss only. A failed lookup is
// remembered for 30 seconds so a broken profile does not spawn every tick.
type ClientCache struct {
	provider dockerc.Provider
	clock    Clock
	mu       sync.Mutex
	entries  map[string]*cacheEntry
}

type cacheEntry struct {
	mu      sync.Mutex
	client  dockerc.Client
	failErr error
	retryAt time.Time
}

// NewClientCache returns an empty cache. A nil clock means RealClock.
func NewClientCache(p dockerc.Provider, clock Clock) *ClientCache {
	if clock == nil {
		clock = RealClock{}
	}
	return &ClientCache{provider: p, clock: clock, entries: map[string]*cacheEntry{}}
}

func (c *ClientCache) entry(profile string) *cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[profile]
	if e == nil {
		e = &cacheEntry{}
		c.entries[profile] = e
	}
	return e
}

// Get returns the cached client or builds one.
func (c *ClientCache) Get(ctx context.Context, profile string) (dockerc.Client, error) {
	e := c.entry(profile)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client != nil {
		return e.client, nil
	}
	if e.failErr != nil && c.clock.Now().Before(e.retryAt) {
		return nil, e.failErr
	}
	cl, err := c.provider.For(ctx, profile)
	if err != nil {
		if ctx.Err() == nil {
			e.failErr, e.retryAt = err, c.clock.Now().Add(clientRetryAfter)
		}
		return nil, err
	}
	e.client, e.failErr = cl, nil
	return cl, nil
}

// Drop discards the client and any remembered failure of a profile.
func (c *ClientCache) Drop(profile string) {
	e := c.entry(profile)
	e.mu.Lock()
	e.client, e.failErr = nil, nil
	e.mu.Unlock()
}

// Forget drops the whole entry of a profile.
func (c *ClientCache) Forget(profile string) {
	c.mu.Lock()
	delete(c.entries, profile)
	c.mu.Unlock()
}

// dropOn discards the cached client when the error says the socket is bad.
func (c *ClientCache) dropOn(profile string, err error) {
	if errors.Is(err, dockerc.ErrDockerUnavailable) {
		c.Drop(profile)
	}
}

// ContainerSource reads running containers' CPU and memory and sums them into
// the VM figures.
type ContainerSource struct {
	cache   *ClientCache
	max     int
	timeout time.Duration
	mu      sync.Mutex
	prev    map[string]map[string]dockerc.Reading
}

// NewContainerSource returns the container source with a 4 s timeout and a
// cap of 64 recorded containers per profile.
func NewContainerSource(cache *ClientCache) *ContainerSource {
	return &ContainerSource{cache: cache, max: defaultMaxContainers, timeout: defaultContainerTO, prev: map[string]map[string]dockerc.Reading{}}
}

// Name implements Source.
func (*ContainerSource) Name() string { return "containers" }

// Timeout implements Source.
func (s *ContainerSource) Timeout() time.Duration { return s.timeout }

// Forget implements Source.
func (s *ContainerSource) Forget(profile string) {
	s.mu.Lock()
	delete(s.prev, profile)
	s.mu.Unlock()
	s.cache.Forget(profile)
}

func (s *ContainerSource) fail(profile string, err error) error {
	s.mu.Lock()
	delete(s.prev, profile)
	s.mu.Unlock()
	s.cache.dropOn(profile, err)
	return err
}

// Collect implements Source. The first call for a profile has no CPU baseline:
// the VM figure is 0 and no container is returned.
func (s *ContainerSource) Collect(ctx context.Context, t Target) (Partial, error) {
	c, err := s.cache.Get(ctx, t.Name)
	if err != nil {
		return Partial{}, err
	}
	cs, err := c.Containers(ctx)
	if err != nil {
		return Partial{}, s.fail(t.Name, err)
	}
	s.mu.Lock()
	prev := s.prev[t.Name]
	s.mu.Unlock()
	usage, err := dockerc.Snapshot(ctx, c, cs, prev)
	if err != nil {
		return Partial{}, s.fail(t.Name, err)
	}
	next := make(map[string]dockerc.Reading, len(cs))
	var (
		cpu float64
		mem int64
		obs []ContainerObs
	)
	for _, ct := range cs {
		if ct.State != "running" {
			continue
		}
		u, ok := usage[ct.ID]
		if !ok {
			continue // vanished between list and stats
		}
		next[ct.ID] = u.Reading
		m := clampInt64(u.MemoryBytes)
		cpu += u.CPUPercent
		mem += m
		if _, seen := prev[ct.ID]; seen {
			obs = append(obs, ContainerObs{ID: ct.ID, CPUPercent: u.CPUPercent, MemoryBytes: m})
		}
	}
	s.mu.Lock()
	s.prev[t.Name] = next
	s.mu.Unlock()
	p := Partial{VMCPU: ptr(cpu), VMMemory: ptr(mem), Containers: obs}
	if len(obs) > s.max {
		sort.Slice(obs, func(i, j int) bool {
			if obs[i].MemoryBytes != obs[j].MemoryBytes {
				return obs[i].MemoryBytes > obs[j].MemoryBytes
			}
			return obs[i].ID < obs[j].ID
		})
		p.Containers, p.Truncated = obs[:s.max], true
	}
	return p, nil
}

func clampInt64(v uint64) int64 {
	if v > 1<<63-1 {
		return 1<<63 - 1
	}
	return int64(v)
}

// DiskSource reads the Docker engine's disk usage on a slow cadence. The call
// is expensive, so it runs in a background goroutine, single flight per
// profile, and Collect returns the last good value.
type DiskSource struct {
	cache      *ClientCache
	clock      Clock
	every      int
	timeout    time.Duration
	staleAfter time.Duration

	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	state map[string]*diskState
}

type diskState struct {
	ticks    int
	inflight bool
	value    int64
	valueAt  time.Time
	have     bool
	lastErr  error
}

// NewDiskSource returns the disk source: refresh on every 12th Collect per
// profile, 10 s timeout, value stale after 3 minutes. Call Close to stop
// background reads. A nil clock means RealClock.
func NewDiskSource(cache *ClientCache, clock Clock) *DiskSource {
	if clock == nil {
		clock = RealClock{}
	}
	base, cancel := context.WithCancel(context.Background())
	return &DiskSource{
		cache: cache, clock: clock, every: defaultDiskEvery, timeout: defaultDiskTO, staleAfter: defaultDiskStaleAfter,
		base: base, cancel: cancel, state: map[string]*diskState{},
	}
}

// Name implements Source.
func (*DiskSource) Name() string { return "disk" }

// Timeout implements Source. Collect never blocks on the engine.
func (*DiskSource) Timeout() time.Duration { return time.Second }

// Forget implements Source. A read in flight finishes into a discarded state.
func (s *DiskSource) Forget(profile string) {
	s.mu.Lock()
	delete(s.state, profile)
	s.mu.Unlock()
}

// Close cancels background reads and waits for them.
func (s *DiskSource) Close() {
	s.cancel()
	s.wg.Wait()
}

// wait blocks until no background read is running (tests).
func (s *DiskSource) wait() { s.wg.Wait() }

// Collect implements Source.
func (s *DiskSource) Collect(ctx context.Context, t Target) (Partial, error) {
	s.mu.Lock()
	st := s.state[t.Name]
	if st == nil {
		st = &diskState{}
		s.state[t.Name] = st
	}
	due := st.ticks%s.every == 0
	st.ticks++
	if due && !st.inflight && s.base.Err() == nil {
		st.inflight = true
		s.wg.Add(1)
		go s.refresh(t.Name, st)
	}
	var p Partial
	if st.have && s.clock.Now().Sub(st.valueAt) <= s.staleAfter {
		p.VMDisk = ptr(st.value)
	}
	err := st.lastErr
	s.mu.Unlock()
	return p, err
}

func (s *DiskSource) refresh(profile string, st *diskState) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(s.base, s.timeout)
	defer cancel()
	var (
		du  dockerc.DiskUsage
		err error
	)
	c, err := s.cache.Get(ctx, profile)
	if err == nil {
		du, err = c.DiskUsage(ctx)
	}
	if err != nil {
		s.cache.dropOn(profile, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st.inflight = false
	if s.base.Err() != nil {
		return
	}
	if err != nil {
		st.lastErr = err
		return
	}
	st.lastErr = nil
	st.value = du.LayersBytes + du.ContainersBytes + du.VolumesBytes + du.BuildCacheBytes
	st.valueAt, st.have = s.clock.Now(), true
}
