package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Retention constants. These are settled decisions (C4 section 9), not
// configuration.
const (
	// RawRetention is how long raw samples are kept before being folded into
	// 1 minute averages.
	RawRetention = 24 * time.Hour
	// MinuteRetention is how long 1 minute averages are kept.
	MinuteRetention = 30 * 24 * time.Hour
	// SampleInterval is the sampler cadence the ring is sized for.
	SampleInterval = 5 * time.Second

	// DefaultRingSlots is 24 hours at one sample per 5 seconds.
	DefaultRingSlots = int(RawRetention / SampleInterval)
	// DefaultMaxScopes caps the number of in-memory rings.
	DefaultMaxScopes = 256
	// DefaultScopeIdle is how long a scope may go without a sample before its
	// ring is dropped.
	DefaultScopeIdle = 10 * time.Minute
	// DefaultReaderConns is the size of the reader pool.
	DefaultReaderConns = 4
	// DefaultActionLogCap is the number of action log rows kept per profile.
	DefaultActionLogCap = 1000

	// MaxPointsLimit is the largest number of points QueryMetrics returns
	// (docs/api.md section 3.9).
	MaxPointsLimit = 2000

	// MaxRecentActions is the largest limit RecentActions honours.
	MaxRecentActions = 100
)

// Errors returned by the store.
var (
	// ErrClosed is returned by every call made after Close.
	ErrClosed = errors.New("store: closed")
	// ErrCorrupt means the database file is not a usable SQLite database.
	ErrCorrupt = errors.New("store: database file is corrupt or not a database")
	// ErrInvalidSample means a sample lacks a profile or scope.
	ErrInvalidSample = errors.New("store: invalid sample")
	// ErrInvalidQuery means a query lacks a profile, scope or start, or its
	// start is after its end.
	ErrInvalidQuery = errors.New("store: invalid query")
	// ErrInvalidAction means an action entry lacks required fields.
	ErrInvalidAction = errors.New("store: invalid action entry")
)

// ErrSchemaTooNew is returned by Open when the database was written by a newer
// version of the program. There are no down migrations: delete the history
// file (it holds history only) or run the newer program.
type ErrSchemaTooNew struct {
	Have int // schema version found in the file
	Want int // newest version this binary knows
}

func (e ErrSchemaTooNew) Error() string {
	return fmt.Sprintf("store: database schema version %d is newer than supported version %d", e.Have, e.Want)
}

// Sample is one measurement. A nil pointer means NULL (unknown), for example
// container disk usage.
type Sample struct {
	Profile     string
	Scope       string // "vm" or a container id
	TakenAt     time.Time
	CPUPercent  *float64
	MemoryBytes *int64
	DiskBytes   *int64
}

// Point is one returned data point. After downsampling it is the average of the
// samples in its time bucket and TakenAt is the bucket start.
type Point struct {
	TakenAt     time.Time
	CPUPercent  *float64
	MemoryBytes *int64
	DiskBytes   *int64
}

// MetricQuery selects points for one (profile, scope) in [From, To]. A zero To
// means now. MaxPoints is clamped to 1..MaxPointsLimit, and zero means
// MaxPointsLimit.
type MetricQuery struct {
	Profile   string
	Scope     string
	From, To  time.Time
	MaxPoints int
}

// ActionEntry is one action log row.
type ActionEntry struct {
	Profile   string
	StartedAt time.Time
	Action    string
	Outcome   string
	Detail    *string
}

// SampleSink receives sampler output (issue #22).
type SampleSink interface {
	AppendSamples(ctx context.Context, samples []Sample) error
}

// MetricReader answers history queries (issue #23).
type MetricReader interface {
	QueryMetrics(ctx context.Context, q MetricQuery) ([]Point, error)
}

// ActionLog records and lists actions (issues #21 and #39).
type ActionLog interface {
	AppendAction(ctx context.Context, e ActionEntry) error
	RecentActions(ctx context.Context, profile string, limit int) ([]ActionEntry, error)
}

var (
	_ SampleSink   = (*Store)(nil)
	_ MetricReader = (*Store)(nil)
	_ ActionLog    = (*Store)(nil)
)
