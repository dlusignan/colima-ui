package colima

import (
	"fmt"
	"strings"
)

// BinaryNotFoundError reports that the colima binary could not be located or
// started. It unwraps to the underlying cause (typically exec.ErrNotFound).
// Its text never includes a filesystem path.
type BinaryNotFoundError struct {
	Err error
}

func (e *BinaryNotFoundError) Error() string { return "colima binary not found" }
func (e *BinaryNotFoundError) Unwrap() error { return e.Err }

// ExitError reports that colima ran and exited with a non-zero status. Stderr
// is kept for logs but is deliberately left out of Error(), because it can
// contain absolute paths.
type ExitError struct {
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("colima %s exited with code %d", strings.Join(e.Args, " "), e.ExitCode)
}

// ParseError reports that colima output did not have a recognised shape. Raw
// keeps the offending output for logs; Error() does not include it.
type ParseError struct {
	Op  string // "list", "status" or "version"
	Raw string
	Err error
}

func (e *ParseError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("colima %s: unrecognised output", e.Op)
	}
	return fmt.Sprintf("colima %s: unrecognised output: %v", e.Op, e.Err)
}
func (e *ParseError) Unwrap() error { return e.Err }

// InvalidProfileError reports a profile name that failed validation.
type InvalidProfileError struct {
	Name   string
	Reason string
}

func (e *InvalidProfileError) Error() string {
	return fmt.Sprintf("invalid profile name %q: %s", e.Name, e.Reason)
}

// InvalidOptionError reports a start option that failed validation. Field is
// the colima flag name without dashes. The rejected value is deliberately not
// kept or printed, because it is caller-supplied text.
type InvalidOptionError struct {
	Field  string
	Reason string
}

func (e *InvalidOptionError) Error() string {
	return fmt.Sprintf("invalid start option %s: %s", e.Field, e.Reason)
}
