package sampler

import (
	"log/slog"
	"sync"
)

type logKey struct{ profile, what string }

// logOnce logs a problem on its first occurrence and a recovery line when it
// ends, and nothing in between, so an outage is one warning and not one per
// tick.
type logOnce struct {
	log *slog.Logger
	mu  sync.Mutex
	bad map[logKey]bool
}

func newLogOnce(l *slog.Logger) *logOnce { return &logOnce{log: l, bad: map[logKey]bool{}} }

// set reports the current state of one condition. bad moving from false to
// true warns, and from true to false logs the recovery. It returns true when
// a new problem started.
func (l *logOnce) set(k logKey, bad bool, msg string, err error) bool {
	l.mu.Lock()
	was := l.bad[k]
	if bad {
		l.bad[k] = true
	} else {
		delete(l.bad, k)
	}
	l.mu.Unlock()
	switch {
	case bad && !was:
		attrs := []any{"profile", k.profile, "source", k.what}
		if err != nil {
			attrs = append(attrs, "error", err)
		}
		l.log.Warn(msg, attrs...)
	case !bad && was:
		l.log.Info("recovered: "+msg, "profile", k.profile, "source", k.what)
	}
	return bad && !was
}

// forget drops the state of a profile without logging.
func (l *logOnce) forget(profile string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.bad {
		if k.profile == profile {
			delete(l.bad, k)
		}
	}
}
