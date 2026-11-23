package operations


// DefaultSubscriberBuffer is the per-subscriber live channel capacity.
const DefaultSubscriberBuffer = 256

// Subscription delivers a job's output. Backlog holds the retained lines newer
// than the requested afterSeq at subscribe time; Lines delivers every later
// line in order, with no gap or duplicate. Lines is closed when the job ends,
// when Close is called, or when the subscriber is dropped for being slow (then
// Err is ErrSlowSubscriber).
type Subscription struct {
	Backlog []Line

	ch     chan Line
	j      *job
	closed bool  // guarded by j.mu
	err    error // guarded by j.mu
}

// Lines is the live channel.
func (s *Subscription) Lines() <-chan Line { return s.ch }

// Err reports why Lines closed: nil for job end or Close, ErrSlowSubscriber
// when dropped. Read it after Lines is closed.
func (s *Subscription) Err() error {
	s.j.mu.Lock()
	defer s.j.mu.Unlock()
	return s.err
}

// Close releases the subscription early. It is idempotent.
func (s *Subscription) Close() {
	s.j.mu.Lock()
	defer s.j.mu.Unlock()
	s.dropLocked(nil)
}

// dropLocked closes the channel once. The job mutex must be held.
func (s *Subscription) dropLocked(err error) {
	if s.closed {
		return
	}
	s.closed = true
	s.err = err
	delete(s.j.subs, s)
	close(s.ch)
}

// Subscribe returns the retained lines with Seq > afterSeq plus a live
// channel. Both are taken under the job lock, so no line is missed or seen
// twice. Subscribing to a finished job returns its backlog and a closed
// channel.
func (m *Manager) Subscribe(jobID string, afterSeq int) (*Subscription, error) {
	m.mu.Lock()
	j, ok := m.jobs[jobID]
	m.mu.Unlock()
	if !ok {
		return nil, ErrJobNotFound
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	s := &Subscription{
		Backlog: j.ring.after(afterSeq),
		ch:      make(chan Line, m.opts.SubscriberBuffer),
		j:       j,
	}
	if j.state != StateRunning {
		s.closed = true
		close(s.ch)
		return s, nil
	}
	if j.subs == nil {
		j.subs = make(map[*Subscription]struct{})
	}
	j.subs[s] = struct{}{}
	return s, nil
}

