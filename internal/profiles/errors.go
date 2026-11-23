package profiles

import "errors"

// ErrNotFound means no source knows the profile, or its name is not valid.
// Callers map it to a 404.
var ErrNotFound = errors.New("profile not found")

// ColimaError means Colima could not be queried and no profile data could be
// built from the configuration files either. Callers map it to a 502. It wraps
// the adapter error, so errors.As still finds the adapter's typed errors.
type ColimaError struct {
	Err error
}

func (e *ColimaError) Error() string { return "colima could not be queried" }
func (e *ColimaError) Unwrap() error { return e.Err }
