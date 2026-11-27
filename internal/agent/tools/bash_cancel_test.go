package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCancelledBashWaitsForItsJob pins M1: a bash call in a task worktree
// whose context is cancelled waits, bounded, for the job it started to end,
// so the scope check that follows sees a write a detached child makes after
// the kill.
func TestCancelledBashWaitsForItsJob(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the test uses a detached POSIX child")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is not on PATH")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(WithTaskWorktree(t.Context()))
	defer cancel()
	started := filepath.Join(dir, "started")
	go func() {
		// Cancel only once the detached child runs, so a slow machine
		// cannot cancel the call before the write is even scheduled.
		for ctx.Err() == nil {
			if _, err := os.Stat(started); err == nil {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// The detached child shares the job's output pipes, so the job is not
	// done until the child exits, half a second after the kill.
	command := `sh -c 'setsid sh -c "touch ` + started + `; sleep 0.4; echo late > late.txt" & sleep 30'`
	_, err := executeBashCommand(ctx, BashParams{Command: command}, dir, dir, nil, "", 10, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.FileExists(t, filepath.Join(dir, "late.txt"),
		"the call waited for the job, so its detached write landed before the check")
}
