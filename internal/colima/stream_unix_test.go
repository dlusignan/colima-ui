//go:build unix

package colima

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// pidRunning reports whether pid is a live process. A killed process can
// linger as a zombie until init reaps it, which counts as dead here.
func pidRunning(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	stat := strings.TrimSpace(string(out))
	return stat != "" && !strings.HasPrefix(stat, "Z")
}

func killPid(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
