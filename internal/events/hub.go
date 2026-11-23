package events

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"
)

// Defaults for the hub options.
const (
	DefaultBuffer        = 256 // same as operations.DefaultSubscriberBuffer
	DefaultKeepalive     = 15 * time.Second
	DefaultWriteDeadline = 10 * time.Second
)

// ErrClosed is returned by Subscribe after Shutdown.
var ErrClosed = errors.New("events: hub is shut down")

type config struct {
	buffer        int
	keepalive     time.Duration
	writeDeadline time.Duration
}

// Option configures a Hub.
type Option func(*config)

// WithBuffer sets the per-client buffer in events. A client whose buffer is
// full when an event arrives is dropped. Values below 1 are ignored.
func WithBuffer(n int) Option {
	return func(c *config) {
		if n >= 1 {
			c.buffer = n
		}
	}
}

// WithKeepalive sets the idle keepalive interval. Values at or below zero are
// ignored.
func WithKeepalive(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.keepalive = d
		}
	}
}

// WithWriteDeadline sets the per-write deadline of a stream. Values at or
// below zero are ignored.
func WithWriteDeadline(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.writeDeadline = d
		}
	}
}

// Hub fans events out to SSE clients. Publish never blocks, so it is safe to
// call from the Operation Manager's job pump (operations.Observer).
//
// Delivery guarantees: every event gets a global id (1, 2, 3, ...) and every
// client sees the events matching its filters in id order. A client that
// cannot keep up (its buffer is full) is dropped and its stream ends; there
// is no replay for missed events.
type Hub struct {
	cfg config

	mu      sync.Mutex
	nextID  uint64
	clients map[*Subscription]struct{}
	closed  bool
}

// New returns a running Hub.
func New(opts ...Option) *Hub {
	cfg := config{
		buffer:        DefaultBuffer,
		keepalive:     DefaultKeepalive,
		writeDeadline: DefaultWriteDeadline,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Hub{cfg: cfg, clients: make(map[*Subscription]struct{})}
}

// Subscription is one registered client. Frames arrive on C, already encoded
// as SSE frames. C is never closed; watch Done instead.
type Subscription struct {
	// C carries complete SSE frames in id order.
	C <-chan []byte

	hub     *Hub
	ch      chan []byte
	profile string
	job     string

	done     chan struct{}
	doneOnce sync.Once
}

// Done is closed when the subscription ends: Close, a drop for being too slow,
// or hub shutdown.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Close unregisters the subscription. It is idempotent.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	s.hub.dropLocked(s)
	s.hub.mu.Unlock()
}

func (s *Subscription) matches(profile, job string) bool {
	if s.profile != "" && s.profile != profile {
		return false
	}
	// Events with no job (metrics) pass a job filter.
	if s.job != "" && job != "" && s.job != job {
		return false
	}
	return true
}

// dropLocked removes s and ends its stream. h.mu must be held.
func (h *Hub) dropLocked(s *Subscription) {
	delete(h.clients, s)
	s.doneOnce.Do(func() { close(s.done) })
}

// Subscribe registers a client. An empty profile or job means no filter.
func (h *Hub) Subscribe(profile, job string) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	ch := make(chan []byte, h.cfg.buffer)
	s := &Subscription{C: ch, hub: h, ch: ch, profile: profile, job: job, done: make(chan struct{})}
	h.clients[s] = struct{}{}
	return s, nil
}

// Publish delivers e to every matching client without blocking. A client with
// a full buffer is dropped. After Shutdown it does nothing.
func (h *Hub) Publish(e Event) {
	if e == nil {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return // defensive: the event structs cannot fail to marshal
	}
	profile, job := e.scope()
	name := e.eventName()

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.nextID++
	frame := make([]byte, 0, len(data)+len(name)+32)
	frame = append(frame, "id: "...)
	frame = strconv.AppendUint(frame, h.nextID, 10)
	frame = append(frame, "\nevent: "...)
	frame = append(frame, name...)
	frame = append(frame, "\ndata: "...)
	frame = append(frame, data...)
	frame = append(frame, "\n\n"...)
	for s := range h.clients {
		if !s.matches(profile, job) {
			continue
		}
		select {
		case s.ch <- frame:
		default:
			h.dropLocked(s)
		}
	}
}

// Shutdown ends every stream and rejects new subscriptions. It is idempotent.
// Call it before http.Server.Shutdown: Shutdown waits for active requests and
// does not cancel their contexts, so a live stream would otherwise hold it
// until its timeout.
func (h *Hub) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for s := range h.clients {
		h.dropLocked(s)
	}
}

func (h *Hub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}
