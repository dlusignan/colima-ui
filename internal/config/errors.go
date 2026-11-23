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
