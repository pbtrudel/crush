//go:build windows

package agentteam

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// The state file is replaced by rename, and Windows refuses to replace a
// file another process holds open. A read-only consumer such as the TUI
// sidebar opens it every second, so a refused rename is retried briefly
// instead of failing the write.
const (
	renameAttempts = 10
	renameDelay    = 10 * time.Millisecond
)

// renameStateFile replaces to with from, retrying while another process has
// the destination open.
func renameStateFile(from, to string) error {
	var err error
	for attempt := range renameAttempts {
		err = os.Rename(from, to)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) &&
			!errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		if attempt < renameAttempts-1 {
			time.Sleep(renameDelay)
		}
	}
	return err
}
