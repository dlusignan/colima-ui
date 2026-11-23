//go:build !unix

package colima

import "os/exec"

// configureProcessGroup is a no-op where process groups are unavailable; the
// default CommandContext kill and WaitDelay still apply.
func configureProcessGroup(*exec.Cmd) {}
