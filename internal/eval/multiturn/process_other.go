//go:build !unix

package multiturn

import "os/exec"

// processGroup returns the function that kills the child. Without process
// groups only the child itself is killed.
func processGroup(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
