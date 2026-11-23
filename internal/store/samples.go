package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

func floor60(x int64) int64 { return x - ((x%60)+60)%60 }

func validateSample(s Sample) error {
	if s.Profile == "" || s.Scope == "" || s.TakenAt.IsZero() {
		return ErrInvalidSample
	}
	return nil
}

// AppendSamples stores a batch (typically one sampler tick) in a single
// transaction, then pushes it into the in-memory rings. SQLite is written
// first, so a failed write never leaves the ring ahead of the disk.
func (s *Store) AppendSamples(ctx context.Context, samples []Sample) error {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return err
	}
	defer done()
	if len(samples) == 0 {
		return nil
	}
	limit := s.clock().Add(MaxFutureSkew)
	for _, sm := range samples {
		if err := validateSample(sm); err != nil {
			return err
		}
		if sm.TakenAt.After(limit) {
			return fmt.Errorf("%w: taken_at is in the future", ErrInvalidSample)
		}
	}

	s.appendMu.Lock()
	defer s.appendMu.Unlock()

	err = s.writeTx(ctx, func(c *sql.Conn) error {
		stmt, err := c.PrepareContext(ctx,
			"INSERT INTO metric_sample (profile, taken_at, scope, cpu_percent, memory_bytes, disk_bytes) VALUES (?, ?, ?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, sm := range samples {
			if _, err := stmt.ExecContext(ctx, sm.Profile, sm.TakenAt.Unix(), sm.Scope,
				sm.CPUPercent, sm.MemoryBytes, sm.DiskBytes); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return s.mapErr(err)
	}

	now := s.clock()
	var missing []scopeKey
	seen := make(map[scopeKey]bool)
	s.ringMu.Lock()
	s.sweepIdleLocked(now)
	for _, sm := range samples {
		k := scopeKey{sm.Profile, sm.Scope}
		if r := s.rings[k]; r != nil {
			r.push(entryOf(sm))
			r.lastSeen = now
		} else if !seen[k] {
			seen[k] = true
			missing = append(missing, k)
		}
	}
	room := s.opts.MaxScopes - len(s.rings)
	s.ringMu.Unlock()

	// A scope without a ring gets one built from the disk (which already holds
	// the batch), so the ring is complete for the whole window. Over the scope
	// cap the scope simply stays disk-backed.
	for _, k := range missing {
		if room <= 0 {
			break
		}
		r, err := s.loadScope(ctx, k, now)
		if err != nil {
			continue // ring stays absent; queries fall back to SQLite
		}
		s.ringMu.Lock()
		if s.rings[k] == nil && len(s.rings) < s.opts.MaxScopes {
			r.lastSeen = now
			s.rings[k] = r
			room--
		}
		s.ringMu.Unlock()
	}
	return nil
}

// sweepIdleLocked drops rings with no sample for ScopeIdle. ringMu must be
// held for writing.
func (s *Store) sweepIdleLocked(now time.Time) {
	for k, r := range s.rings {
		if now.Sub(r.lastSeen) > s.opts.ScopeIdle {
			delete(s.rings, k)
		}
	}
}

// loadScope builds a ring from the last RawRetention of one scope's raw rows.
func (s *Store) loadScope(ctx context.Context, k scopeKey, now time.Time) (*ring, error) {
	r := newRing(s.opts.RingSlots)
	since := now.Add(-RawRetention).Unix()
	err := s.readTx(ctx, func(c *sql.Conn) error {
		rows, err := c.QueryContext(ctx,
			"SELECT taken_at, cpu_percent, memory_bytes, disk_bytes FROM metric_sample "+
				"WHERE profile = ? AND scope = ? AND taken_at >= ? ORDER BY taken_at, id", k.profile, k.scope, since)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				return err
			}
			r.push(e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanEntry(r scanner) (entry, error) {
	var (
		e    entry
		cpu  sql.NullFloat64
		mem  sql.NullInt64
		disk sql.NullInt64
	)
	if err := r.Scan(&e.t, &cpu, &mem, &disk); err != nil {
		return entry{}, err
	}
	if cpu.Valid {
		e.cpu, e.has = cpu.Float64, e.has|hasCPU
	}
	if mem.Valid {
		e.mem, e.has = mem.Int64, e.has|hasMem
	}
	if disk.Valid {
		e.disk, e.has = disk.Int64, e.has|hasDisk
	}
	return e, nil
}

// rehydrate fills the rings at Open from the last 24 hours of raw samples in
// one ordered scan. Scopes already idle, and scopes beyond the cap, are skipped.
func (s *Store) rehydrate(ctx context.Context) error {
	now := s.clock()
	since := now.Add(-RawRetention).Unix()
	rows, err := s.rdb.QueryContext(ctx,
		"SELECT profile, scope, taken_at, cpu_percent, memory_bytes, disk_bytes FROM metric_sample "+
			"WHERE taken_at >= ? ORDER BY profile, scope, taken_at, id", since)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cur     scopeKey
		r       *ring
		lastT   int64
		have    bool
		skipCur bool
	)
	finish := func() {
		if have && !skipCur && r != nil {
			seen := time.Unix(lastT, 0)
			if now.Sub(seen) <= s.opts.ScopeIdle {
				r.lastSeen = seen
				s.rings[cur] = r
			}
		}
	}
	for rows.Next() {
		var (
			k    scopeKey
			t    int64
			cpu  sql.NullFloat64
			mem  sql.NullInt64
			disk sql.NullInt64
		)
		if err := rows.Scan(&k.profile, &k.scope, &t, &cpu, &mem, &disk); err != nil {
			return err
		}
		if !have || k != cur {
			finish()
			cur, have, lastT = k, true, 0
			skipCur = len(s.rings) >= s.opts.MaxScopes
			r = nil
			if !skipCur {
				r = newRing(s.opts.RingSlots)
			}
		}
		if skipCur {
			continue
		}
		e := entry{t: t}
		if cpu.Valid {
			e.cpu, e.has = cpu.Float64, e.has|hasCPU
		}
		if mem.Valid {
			e.mem, e.has = mem.Int64, e.has|hasMem
		}
		if disk.Valid {
			e.disk, e.has = disk.Int64, e.has|hasDisk
		}
		r.push(e)
		lastT = t
	}
	if err := rows.Err(); err != nil {
		return err
	}
	finish()
	return nil
}

// QueryMetrics returns the points of one (profile, scope) in [From, To], at
// most MaxPoints of them, in time order. A range starting within the last 24
// hours is answered from the ring without touching the disk; older ranges read
// the 1 minute table and raw rows in one snapshot. No data gives an empty,
// non-nil slice.
func (s *Store) QueryMetrics(ctx context.Context, q MetricQuery) ([]Point, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	if q.Profile == "" || q.Scope == "" || q.From.IsZero() {
		return nil, ErrInvalidQuery
	}
	now := s.clock()
	to := q.To
	if to.IsZero() {
		to = now
	}
	if q.From.After(to) {
		return nil, ErrInvalidQuery
	}
	maxPts := q.MaxPoints
	if maxPts < 1 {
		maxPts = MaxPointsLimit
	}
	if maxPts > MaxPointsLimit {
		maxPts = MaxPointsLimit
	}
	from, toU := q.From.Unix(), to.Unix()
	key := scopeKey{q.Profile, q.Scope}

	if !q.From.Before(now.Add(-RawRetention)) {
		s.ringMu.RLock()
		r := s.rings[key]
		var pts []wpoint
		ok := r != nil && from >= r.minComplete
		if ok {
			pts = r.collect(from, toU)
		}
		s.ringMu.RUnlock()
		if ok {
			return downsample(pts, from, toU, maxPts), nil
		}
	}

	pts, err := s.queryDisk(ctx, key, from, toU)
	if err != nil {
		return nil, s.mapErr(err)
	}
	return downsample(pts, from, toU, maxPts), nil
}

// queryDisk merges 1 minute rows and raw rows from one read snapshot, so a
// concurrent Cleanup can neither drop nor double count a folded range.
func (s *Store) queryDisk(ctx context.Context, k scopeKey, from, to int64) ([]wpoint, error) {
	var pts []wpoint
	err := s.readTx(ctx, func(c *sql.Conn) error {
		rows, err := c.QueryContext(ctx,
			"SELECT bucket_start, cpu_percent, memory_bytes, disk_bytes, cpu_samples, mem_samples, disk_samples FROM metric_sample_1m "+
				"WHERE profile = ? AND scope = ? AND bucket_start >= ? AND bucket_start <= ? ORDER BY bucket_start",
			k.profile, k.scope, floor60(from+59), to) // buckets starting before From would yield an earlier TakenAt
		if err != nil {
			return err
		}
		for rows.Next() {
			var (
				p    wpoint
				cpu  sql.NullFloat64
				mem  sql.NullInt64
				disk sql.NullInt64
			)
			if err := rows.Scan(&p.t, &cpu, &mem, &disk, &p.cw, &p.mw, &p.dw); err != nil {
				rows.Close()
				return err
			}
			if cpu.Valid {
				v := cpu.Float64
				p.cpu = &v
			}
			if mem.Valid {
				v := mem.Int64
				p.mem = &v
			}
			if disk.Valid {
				v := disk.Int64
				p.disk = &v
			}
			pts = append(pts, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		rows, err = c.QueryContext(ctx,
			"SELECT taken_at, cpu_percent, memory_bytes, disk_bytes FROM metric_sample "+
				"WHERE profile = ? AND scope = ? AND taken_at >= ? AND taken_at <= ? ORDER BY taken_at, id",
			k.profile, k.scope, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				return err
			}
			pts = append(pts, e.wpoint())
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].t < pts[j].t })
	return pts, nil
}
