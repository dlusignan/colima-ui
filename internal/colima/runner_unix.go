//go:build unix

package colima

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the child in its own process group and makes
// cancellation kill the whole group. Colima shells out to limactl, and killing
// only the colima PID could leave a grandchild holding the output pipes open.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
