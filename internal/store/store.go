package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
)

// MemoryPath opens a private in-memory database (one connection).
const MemoryPath = ":memory:"

// Options configures Open. Only Path is required.
type Options struct {
	// Path is the database file (see DefaultPath), or MemoryPath. Its parent
	// folder is owned by the store: it is created 0700 and tightened to 0700
	// (even if it already exists), and the file and its -wal and -shm files are
	// created or tightened to 0600 before SQLite uses them. Never point it at a
	// shared folder such as the home folder or /tmp.
	Path string
	// Clock returns the current time. Default time.Now.
	Clock func() time.Time

	// RingSlots is the samples kept per scope (default DefaultRingSlots).
	RingSlots int
	// MaxScopes caps the number of rings (default DefaultMaxScopes).
	MaxScopes int
	// ScopeIdle drops a scope's ring after this long without a sample
	// (default DefaultScopeIdle).
	ScopeIdle time.Duration
	// ReaderConns is the reader pool size (default DefaultReaderConns).
	ReaderConns int
	// ActionLogCap is the action log rows kept per profile (default
	// DefaultActionLogCap).
	ActionLogCap int

	// CleanupInterval is the period of the loop started by Start (default
	// 1 hour). CleanupInitialDelay is the wait before its first run (default
	// 1 minute; negative means run at once).
	CleanupInterval     time.Duration
	CleanupInitialDelay time.Duration
	// SlicePause is the wait between cleanup slices (default 20ms; negative
	// means none).
	SlicePause time.Duration
	// OnCleanupError receives errors from the background cleanup loop.
	OnCleanupError func(error)
}

// Store is the History Store. All methods are safe for concurrent use.
type Store struct {
	opts  Options
	clock func() time.Time

	wdb, rdb *sql.DB // the same pool for an in-memory database

	// sqlCalls counts every use of a SQLite pool after Open. Tests use it to
	// prove that in-window queries never reach the disk.
	sqlCalls atomic.Int64

	baseCtx context.Context // cancelled first thing in Close
	cancel  context.CancelFunc
	closing atomic.Bool

	life   sync.RWMutex // held shared by operations, exclusively by Close
	closed bool

	closeMu sync.Mutex // serialises Close calls
	startMu sync.Mutex // guards started and the loop WaitGroup
	started bool
	loopWG  sync.WaitGroup

	appendMu sync.Mutex // serialises AppendSamples (write + ring update)

	ringMu sync.RWMutex
	rings  map[scopeKey]*ring
}

// Open opens (creating and migrating if needed) the history database and
// rehydrates the in-memory rings from the last 24 hours of raw samples.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if opts.Path == "" {
		return nil, errors.New("store: Path is required")
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.RingSlots <= 0 {
		opts.RingSlots = DefaultRingSlots
	}
	if opts.MaxScopes <= 0 {
		opts.MaxScopes = DefaultMaxScopes
	}
	if opts.ScopeIdle <= 0 {
		opts.ScopeIdle = DefaultScopeIdle
	}
	if opts.ReaderConns <= 0 {
		opts.ReaderConns = DefaultReaderConns
	}
	if opts.ActionLogCap <= 0 {
		opts.ActionLogCap = DefaultActionLogCap
	}
	if opts.CleanupInterval <= 0 {
		opts.CleanupInterval = time.Hour
	}
	if opts.CleanupInitialDelay == 0 {
		opts.CleanupInitialDelay = time.Minute
	}
	if opts.SlicePause == 0 {
		opts.SlicePause = 20 * time.Millisecond
	}

	ms, err := loadMigrations(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}

	s := &Store{opts: opts, clock: opts.Clock, rings: make(map[scopeKey]*ring)}
	s.baseCtx, s.cancel = context.WithCancel(context.Background())

	if err := s.openPools(); err != nil {
		s.cancel()
		return nil, err
	}
	fail := func(err error) (*Store, error) {
		s.cancel()
		_ = s.wdb.Close()
		if s.rdb != s.wdb {
			_ = s.rdb.Close()
		}
		return nil, classify(err)
	}
	if err := migrate(ctx, s.wdb, ms); err != nil {
		return fail(err)
	}
	if err := s.rehydrate(ctx); err != nil {
		return fail(err)
	}
	return s, nil
}

// classify maps driver errors that mean "not a usable database" to ErrCorrupt.
func classify(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case 26, 11: // SQLITE_NOTADB, SQLITE_CORRUPT
			return fmt.Errorf("%w", ErrCorrupt)
		}
	}
	return err
}

func (s *Store) openPools() error {
	if s.opts.Path == MemoryPath {
		db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
		if err != nil {
			return err
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
		db.SetConnMaxIdleTime(0)
		s.wdb, s.rdb = db, db
		return nil
	}

	dir := filepath.Dir(s.opts.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("store: create data folder: %w", fsErr(err))
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("store: secure data folder: %w", fsErr(err))
	}
	f, err := os.OpenFile(s.opts.Path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("store: create database file: %w", fsErr(err))
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("store: secure database file: %w", fsErr(err))
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Leftover sidecars from an earlier run keep their old mode otherwise.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(s.opts.Path+suffix, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("store: secure database file: %w", fsErr(err))
		}
	}

	base := (&url.URL{Path: s.opts.Path}).EscapedPath()
	// auto_vacuum must be set before the first table exists, so it comes first.
	common := "_pragma=auto_vacuum(2)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	wdb, err := sql.Open("sqlite", "file:"+base+"?"+common)
	if err != nil {
		return err
	}
	wdb.SetMaxOpenConns(1)
	wdb.SetMaxIdleConns(1)
	wdb.SetConnMaxLifetime(0)
	wdb.SetConnMaxIdleTime(0)

	rdb, err := sql.Open("sqlite", "file:"+base+"?"+common+"&_pragma=query_only(1)")
	if err != nil {
		_ = wdb.Close()
		return err
	}
	rdb.SetMaxOpenConns(s.opts.ReaderConns)
	rdb.SetMaxIdleConns(s.opts.ReaderConns)
	rdb.SetConnMaxLifetime(0)
	rdb.SetConnMaxIdleTime(0)
	s.wdb, s.rdb = wdb, rdb
	return nil
}

// enter registers an operation. The returned context is cancelled when ctx is
// or when the store starts closing; done must be called when finished.
func (s *Store) enter(ctx context.Context) (context.Context, func(), error) {
	if s.closing.Load() {
		return nil, nil, ErrClosed
	}
	s.life.RLock()
	if s.closed || s.closing.Load() {
		s.life.RUnlock()
		return nil, nil, ErrClosed
	}
	cctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.baseCtx, cancel)
	return cctx, func() {
		stop()
		cancel()
		s.life.RUnlock()
	}, nil
}

// mapErr turns a cancellation caused by Close into ErrClosed.
func (s *Store) mapErr(err error) error {
	if err != nil && s.baseCtx.Err() != nil && errors.Is(err, context.Canceled) {
		return ErrClosed
	}
	return err
}

// writeTx runs fn inside BEGIN IMMEDIATE .. COMMIT on the writer connection.
func (s *Store) writeTx(ctx context.Context, fn func(*sql.Conn) error) error {
	s.sqlCalls.Add(1)
	return runTx(ctx, s.wdb, "BEGIN IMMEDIATE", fn)
}

// readTx runs fn inside one deferred read transaction (a consistent snapshot)
// on a reader connection.
func (s *Store) readTx(ctx context.Context, fn func(*sql.Conn) error) error {
	s.sqlCalls.Add(1)
	return runTx(ctx, s.rdb, "BEGIN", fn)
}

func runTx(ctx context.Context, db *sql.DB, begin string, fn func(*sql.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, begin); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()
	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	ok = true
	return nil
}

// Close stops the cleanup loop and waits for it, waits for running calls
// (cancelling them), checkpoints and truncates the WAL, and closes both pools.
// It is idempotent. Calls made afterwards return ErrClosed.
func (s *Store) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}

	s.startMu.Lock()
	s.closing.Store(true)
	s.startMu.Unlock()
	s.cancel()
	s.loopWG.Wait()

	s.life.Lock()
	defer s.life.Unlock()

	var first error
	if s.opts.Path != MemoryPath {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := s.wdb.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
		cancel()
		first = err
	}
	if err := s.wdb.Close(); err != nil && first == nil {
		first = err
	}
	if s.rdb != s.wdb {
		if err := s.rdb.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.closed = true
	return first
}

// fsErr drops the path from a filesystem error so error text never carries it.
func fsErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
