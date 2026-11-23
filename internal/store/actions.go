package store

import (
	"context"
	"database/sql"
	"time"
)

// AppendAction records one action log row. A zero StartedAt means now. The log
// is capped per profile by Cleanup, not here.
func (s *Store) AppendAction(ctx context.Context, e ActionEntry) error {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return err
	}
	defer done()
	if e.Profile == "" || e.Action == "" || e.Outcome == "" {
		return ErrInvalidAction
	}
	started := e.StartedAt
	if started.IsZero() {
		started = s.clock()
	}
	err = s.writeTx(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx,
			"INSERT INTO action_log (profile, started_at, action, outcome, detail) VALUES (?, ?, ?, ?, ?)",
			e.Profile, started.Unix(), e.Action, e.Outcome, e.Detail)
		return err
	})
	return s.mapErr(err)
}

// RecentActions returns the newest rows of one profile first. limit is clamped
// to 1..MaxRecentActions. No rows gives an empty, non-nil slice.
func (s *Store) RecentActions(ctx context.Context, profile string, limit int) ([]ActionEntry, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	if profile == "" {
		return nil, ErrInvalidQuery
	}
	limit = max(1, min(limit, MaxRecentActions))
	out := []ActionEntry{}
	err = s.readTx(ctx, func(c *sql.Conn) error {
		rows, err := c.QueryContext(ctx,
			"SELECT profile, started_at, action, outcome, detail FROM action_log "+
				"WHERE profile = ? ORDER BY started_at DESC, id DESC LIMIT ?", profile, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				e      ActionEntry
				t      int64
				detail sql.NullString
			)
			if err := rows.Scan(&e.Profile, &t, &e.Action, &e.Outcome, &detail); err != nil {
				return err
			}
			e.StartedAt = time.Unix(t, 0).UTC()
			if detail.Valid {
				v := detail.String
				e.Detail = &v
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, s.mapErr(err)
	}
	return out, nil
}
