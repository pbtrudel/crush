//go:build unix

package multiturn

import (
	"os/exec"
	"syscall"
)

// processGroup puts the child in its own process group and returns the
// function that kills the whole group.
func processGroup(cmd *exec.Cmd) func() error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
