//go:build !windows

package agentteam

import "os"

// renameStateFile replaces to with from.
func renameStateFile(from, to string) error {
	return os.Rename(from, to)
}
