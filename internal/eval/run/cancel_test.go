//go:build unix

package run

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// TestRunCancelledKillsTheProcessGroup cancels the run's context while the
// child sleeps and has a child of its own. The whole process group must go:
// a Ctrl-C that leaves crush and its tools running for another half hour is
// not a stop.
func TestRunCancelledKillsTheProcessGroup(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")
	f.env["FAKE_SLEEP_MS"] = "600000"
	f.task.Limits.Timeout = task.Duration(time.Hour)
	pidFile := filepath.Join(t.TempDir(), "pid")
	childFile := filepath.Join(t.TempDir(), "child.pid")
	f.env["FAKE_PID"] = pidFile
	f.env["FAKE_CHILD_OUT"] = childFile

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		// Cancel once the child of the child is up, so the group has two
		// members to kill.
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(childFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()

	_, err := Run(ctx, f.options())
	<-stopped
	require.ErrorIs(t, err, context.Canceled, "a cancelled run reports the cancellation")

	pid := readPID(t, pidFile)
	child := readPID(t, childFile)
	require.NotZero(t, pid)
	require.NotZero(t, child)
	require.Eventually(t, func() bool {
		return syscall.Kill(-pid, 0) != nil && syscall.Kill(child, 0) != nil
	}, 10*time.Second, 20*time.Millisecond, "the process group and its child survived the cancel")
}

// readPID reads a pid the fake binary wrote.
func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	return pid
}
