package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// ErrInvalidProfileName is returned when a profile name is not allowed or its
// folder would resolve outside the Colima base folder.
var ErrInvalidProfileName = errors.New("invalid profile name")

// ErrProfileNotFound is returned when a profile has no colima.yaml. It wraps
// fs.ErrNotExist so errors.Is(err, fs.ErrNotExist) also matches. The text never
// contains a filesystem path.
var ErrProfileNotFound = fmt.Errorf("profile not found: %w", fs.ErrNotExist)

// ParseError reports malformed YAML. It carries a message and a line number
// only, never a path.
type ParseError struct {
	Line    int // 1-based; 0 when unknown
	Message string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("malformed profile YAML at line %d: %s", e.Line, e.Message)
	}
	return "malformed profile YAML: " + e.Message
}

// TypeError reports one or more keys whose value has the wrong YAML kind, for
// example `cpu: "four"`.
type TypeError struct {
	Problems []string
}

func (e *TypeError) Error() string {
	return "profile has values of the wrong type: " + strings.Join(e.Problems, "; ")
}

// ReadError wraps an I/O failure other than "not found". The underlying error
// is stripped of the file path.
type ReadError struct {
	Err error
}

func (e *ReadError) Error() string { return "read profile: " + e.Err.Error() }
func (e *ReadError) Unwrap() error { return e.Err }

// FieldError is one validation failure, keyed by the YAML field path.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// ErrProfileBusy is the errors.Is target of *BusyError: a job is running on the
// profile, or another write holds it.
var ErrProfileBusy = errors.New("profile is busy")

// BusyError reports that the exclusive hold on a profile was not granted.
type BusyError struct {
	Profile string
}

func (e *BusyError) Error() string        { return "profile " + e.Profile + " is busy" }
func (e *BusyError) Is(target error) bool { return target == ErrProfileBusy }

// ErrConflict means the file changed on disk while an edit was being prepared.
// Nothing was written; the caller should re-read and retry.
var ErrConflict = errors.New("profile file changed on disk; re-read and retry")

// ErrMultiDocument is returned when the file holds more than one YAML document.
// Writing would silently drop the extra documents, so nothing is written.
var ErrMultiDocument = errors.New("profile file has more than one YAML document")

// ErrUnwritableTarget is returned when colima.yaml is a dangling symlink,
// resolves to something that is not a regular file, or has more than one hard
// link (a rename would detach the other names).
var ErrUnwritableTarget = errors.New("profile file is not a writable regular file")

// ErrNoGate is returned when a Service built without WithGate is asked to
// write. Writing unguarded is never allowed.
var ErrNoGate = errors.New("config service has no exclusion gate configured")

// ValidationError carries every field that failed validation. Nothing was
// written.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Error()
	}
	return "invalid profile change: " + strings.Join(parts, "; ")
}

// WriteError wraps an I/O failure on the write path. The underlying error is
// stripped of file paths.
type WriteError struct {
	Err error
}

func (e *WriteError) Error() string { return "write profile: " + e.Err.Error() }
func (e *WriteError) Unwrap() error { return e.Err }

// ErrAliasedValue is returned when an edit would also change another field
// through a shared YAML anchor. Nothing is written.
var ErrAliasedValue = errors.New("edit would change other fields through a shared YAML anchor")
