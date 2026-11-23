package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Batch sizes. Variables only so tests can shrink them.
var (
	foldSlice   = time.Hour   // raw time folded per transaction
	deleteChunk = int64(5000) // 1 minute rows deleted per transaction
)

// Cleanup applies retention. It is synchronous and uses the injected clock.
//
//  1. Raw samples older than now-24h (rounded down to a whole minute) are folded
//     into 1 minute averages and deleted, one hour of raw time per transaction.
//     Slices and the cutoff are minute aligned, so a rerun finds nothing to do,
//     and a late raw sample for a folded minute is merged into its bucket.
//  2. 1 minute rows older than 30 days are deleted in chunks.
//  3. The action log keeps the newest rows per profile.
//  4. Free pages are returned (incremental vacuum) and the WAL is checkpointed.
//
// Between slices it pauses so AppendSamples can use the single writer. Cancelling
// ctx stops it between transactions and leaves consistent data.
func (s *Store) Cleanup(ctx context.Context) error {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.mapErr(s.cleanup(ctx))
}

func (s *Store) cleanup(ctx context.Context) error {
	nowU := s.clock().Unix()
	s.ringMu.Lock()
	s.sweepIdleLocked(s.clock())
	s.ringMu.Unlock()

	if err := s.foldRaw(ctx, floor60(nowU-int64(RawRetention/time.Second))); err != nil {
		return err
	}
	if err := s.expireMinutes(ctx, nowU); err != nil {
		return err
	}
	if err := s.trimActions(ctx); err != nil {
		return err
	}
	s.sqlCalls.Add(1)
	if _, err := s.wdb.ExecContext(ctx, "PRAGMA incremental_vacuum(1000)"); err != nil {
		return err
	}
	if _, err := s.wdb.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
		return err
	}
	return nil
}

func (s *Store) pause(ctx context.Context) error {
	if s.opts.SlicePause < 0 {
		return ctx.Err()
	}
	t := time.NewTimer(s.opts.SlicePause)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// oldestRaw returns the oldest raw sample time in [from, cutoff), if any.
func (s *Store) oldestRaw(ctx context.Context, from, cutoff int64) (int64, bool, error) {
	s.sqlCalls.Add(1)
	var t sql.NullInt64
	err := s.rdb.QueryRowContext(ctx,
		"SELECT MIN(taken_at) FROM metric_sample WHERE taken_at >= ? AND taken_at < ?", from, cutoff).Scan(&t)
	if err != nil {
		return 0, false, err
	}
	return t.Int64, t.Valid, nil
}

// foldSQL folds raw rows in [?1, ?2) into 1 minute buckets. A bucket that
// already exists (a late raw sample for a folded minute) is merged: each field
// becomes the weighted average over both parts, using the per-field counts, and
// the counts add up. Averages of integer fields are rounded half away from zero.
const foldSQL = `
INSERT INTO metric_sample_1m
    (profile, scope, bucket_start, cpu_percent, memory_bytes, disk_bytes, samples, cpu_samples, mem_samples, disk_samples)
SELECT profile, scope, taken_at / 60 * 60,
       AVG(cpu_percent),
       CAST(ROUND(AVG(memory_bytes)) AS INTEGER),
       CAST(ROUND(AVG(disk_bytes)) AS INTEGER),
       COUNT(*), COUNT(cpu_percent), COUNT(memory_bytes), COUNT(disk_bytes)
FROM metric_sample WHERE taken_at >= ?1 AND taken_at < ?2
GROUP BY profile, scope, taken_at / 60
ON CONFLICT DO UPDATE SET
    cpu_percent = CASE WHEN cpu_samples + excluded.cpu_samples = 0 THEN NULL ELSE
        (COALESCE(cpu_percent, 0) * cpu_samples + COALESCE(excluded.cpu_percent, 0) * excluded.cpu_samples)
        / (cpu_samples + excluded.cpu_samples) END,
    memory_bytes = CASE WHEN mem_samples + excluded.mem_samples = 0 THEN NULL ELSE
        CAST(ROUND((COALESCE(memory_bytes, 0) * 1.0 * mem_samples + COALESCE(excluded.memory_bytes, 0) * 1.0 * excluded.mem_samples)
        / (mem_samples + excluded.mem_samples)) AS INTEGER) END,
    disk_bytes = CASE WHEN disk_samples + excluded.disk_samples = 0 THEN NULL ELSE
        CAST(ROUND((COALESCE(disk_bytes, 0) * 1.0 * disk_samples + COALESCE(excluded.disk_bytes, 0) * 1.0 * excluded.disk_samples)
        / (disk_samples + excluded.disk_samples)) AS INTEGER) END,
    samples = samples + excluded.samples,
    cpu_samples = cpu_samples + excluded.cpu_samples,
    mem_samples = mem_samples + excluded.mem_samples,
    disk_samples = disk_samples + excluded.disk_samples`

// onFoldSlice, when set by a test, observes each fold slice [start, end).
var onFoldSlice func(start, end int64)

func (s *Store) foldRaw(ctx context.Context, cutoff int64) error {
	t, ok, err := s.oldestRaw(ctx, -1<<62, cutoff)
	if err != nil || !ok {
		return err
	}
	start := floor60(t)
	for start < cutoff {
		end := min(start+int64(foldSlice/time.Second), cutoff)
		if onFoldSlice != nil {
			onFoldSlice(start, end)
		}
		var n int64
		err := s.writeTx(ctx, func(c *sql.Conn) error {
			if _, err := c.ExecContext(ctx, foldSQL, start, end); err != nil {
				return err
			}
			res, err := c.ExecContext(ctx, "DELETE FROM metric_sample WHERE taken_at >= ? AND taken_at < ?", start, end)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		start = end
		if start >= cutoff {
			break
		}
		if n == 0 { // empty slice: jump to the next raw row, if any
			t, ok, err := s.oldestRaw(ctx, start, cutoff)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			start = floor60(t)
		}
		if err := s.pause(ctx); err != nil {
			return err
		}
	}
	return nil
}

// expireMinutes deletes 1 minute rows older than 30 days, unless the clock
// looks wrong: if now is more than MinuteRetention past the newest stored row,
// a forward clock jump would wipe every row, so the step is skipped until newer
// samples arrive (samples stamped with a sane clock make the newest row recent).
func (s *Store) expireMinutes(ctx context.Context, nowU int64) error {
	before := nowU - int64(MinuteRetention/time.Second)
	s.sqlCalls.Add(1)
	var one int
	err := s.rdb.QueryRowContext(ctx, "SELECT 1 FROM metric_sample_1m WHERE bucket_start < ? LIMIT 1", before).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // nothing to expire
	}
	if err != nil {
		return err
	}
	var newest sql.NullInt64
	if err := s.rdb.QueryRowContext(ctx,
		"SELECT MAX(COALESCE((SELECT MAX(bucket_start) FROM metric_sample_1m), 0), COALESCE((SELECT MAX(taken_at) FROM metric_sample), 0))").
		Scan(&newest); err != nil {
		return err
	}
	if newest.Valid && newest.Int64 < before {
		return nil
	}
	return s.deleteOldMinutes(ctx, before)
}

func (s *Store) deleteOldMinutes(ctx context.Context, before int64) error {
	for {
		var n int64
		err := s.writeTx(ctx, func(c *sql.Conn) error {
			res, err := c.ExecContext(ctx,
				"DELETE FROM metric_sample_1m WHERE (profile, scope, bucket_start) IN "+
					"(SELECT profile, scope, bucket_start FROM metric_sample_1m WHERE bucket_start < ? LIMIT ?)",
				before, deleteChunk)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		if n < deleteChunk {
			return nil
		}
		if err := s.pause(ctx); err != nil {
			return err
		}
	}
}

func (s *Store) trimActions(ctx context.Context) error {
	s.sqlCalls.Add(1)
	rows, err := s.rdb.QueryContext(ctx,
		"SELECT profile FROM action_log GROUP BY profile HAVING COUNT(*) > ?", s.opts.ActionLogCap)
	if err != nil {
		return err
	}
	var profiles []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		profiles = append(profiles, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, p := range profiles {
		err := s.writeTx(ctx, func(c *sql.Conn) error {
			_, err := c.ExecContext(ctx,
				"DELETE FROM action_log WHERE profile = ? AND id IN "+
					"(SELECT id FROM action_log WHERE profile = ? ORDER BY started_at DESC, id DESC LIMIT -1 OFFSET ?)",
				p, p, s.opts.ActionLogCap)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Start launches the background cleanup loop: a first run after
// CleanupInitialDelay, then every CleanupInterval. It stops when ctx is
// cancelled or the store is closed (Close waits for it). A second call is a
// no-op.
func (s *Store) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.closing.Load() {
		return ErrClosed
	}
	if s.started {
		return nil
	}
	s.started = true
	s.loopWG.Add(1)
	go s.loop(ctx)
	return nil
}

func (s *Store) loop(ctx context.Context) {
	defer s.loopWG.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.baseCtx, cancel)
	defer stop()

	delay := s.opts.CleanupInitialDelay
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		err := s.Cleanup(ctx)
		if err != nil && ctx.Err() == nil && !errors.Is(err, ErrClosed) && s.opts.OnCleanupError != nil {
			s.opts.OnCleanupError(err)
		}
		timer.Reset(s.opts.CleanupInterval)
	}
}
