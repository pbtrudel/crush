package shell

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundedCapture_SmallOutputIsExact(t *testing.T) {
	t.Parallel()

	c := newBoundedCapture(nil)
	var want strings.Builder
	for range 50 {
		chunk := "a line of output\n"
		want.WriteString(chunk)
		n, err := c.Write([]byte(chunk))
		require.NoError(t, err)
		require.Equal(t, len(chunk), n)
	}
	require.Equal(t, want.String(), c.String())
}

// A stream past the retain cap must cost a bounded amount of memory and
// still show the agent the beginning and the end of what the command
// printed.
func TestBoundedCapture_OverflowStaysBounded(t *testing.T) {
	t.Parallel()

	c := newBoundedCapture(nil)
	first := "FIRST LINE\n"
	_, err := c.Write([]byte(first))
	require.NoError(t, err)

	huge := strings.Repeat("x", 64<<10) + "\n"
	for range maxCaptureRetain/(64<<10) + 2 {
		_, err := c.Write([]byte(huge))
		require.NoError(t, err)
	}
	_, err = c.Write([]byte("LAST LINE\n"))
	require.NoError(t, err)

	got := c.String()
	require.LessOrEqual(t, len(got), maxPersistedBytes, "the rendered overflow must fit the persist budget")
	require.True(t, withinBudget(got), "the persist path must leave an already-bounded rendering untouched")
	require.True(t, strings.HasPrefix(got, "FIRST LINE"), got[:40])
	// The rendered tail carries no trailing newline, matching
	// TruncateForPersist's own output shape.
	require.True(t, strings.HasSuffix(got, "LAST LINE"), got[len(got)-40:])
	require.Contains(t, got, "omitted")
}

// Interleaved writes from stdout and stderr are safe: the capture is used
// as a shared io.Writer.
func TestBoundedCapture_ConcurrentWrites(t *testing.T) {
	t.Parallel()

	c := newBoundedCapture(nil)
	chunk := strings.Repeat("y", 4096) + "\n"
	done := make(chan struct{})
	// Push past the retain cap so the overflow path is exercised under
	// concurrent writers.
	for range 8 {
		go func() {
			for range 500 {
				_, _ = c.Write([]byte(chunk))
			}
			done <- struct{}{}
		}()
	}
	for range 8 {
		<-done
	}
	require.LessOrEqual(t, len(c.String()), maxPersistedBytes)
}

// Every byte still reaches the live progress sink even when the capture
// has stopped retaining the middle: the UI shows the command as it runs.
func TestBoundedCapture_ProgressSeesEveryChunk(t *testing.T) {
	t.Parallel()

	var seen strings.Builder
	c := newBoundedCapture(func(s string) { seen.WriteString(s) })

	body := strings.Repeat("z", 1<<10) + "\n"
	for range maxCaptureRetain/len(body) + 3 {
		_, err := c.Write([]byte(body))
		require.NoError(t, err)
	}
	require.Contains(t, seen.String(), "zzzz")
	require.Greater(t, seen.Len(), maxCaptureRetain)
}

// ANSI content must not bleed styling into the chat around the cut
// points, mirroring why TruncateForPersist strips when it truncates.
func TestBoundedCapture_OverflowStripsAnsi(t *testing.T) {
	t.Parallel()

	c := newBoundedCapture(nil)
	_, err := c.Write([]byte("\x1b[31mFIRST\n"))
	require.NoError(t, err)
	chunk := "\x1b[38;5;208m" + strings.Repeat("q", 1<<10) + "\x1b[0m\n"
	for range maxCaptureRetain/len(chunk) + 3 {
		_, err := c.Write([]byte(chunk))
		require.NoError(t, err)
	}
	_, err = c.Write([]byte("\x1b[32mLAST\n"))
	require.NoError(t, err)

	got := c.String()
	require.NotContains(t, got, "\x1b", "a truncated rendering must not carry escape sequences")
}

// The rendering is what the session stores, so the line count also has to
// stay inside the persist budget.
func TestBoundedCapture_OverflowLineBudget(t *testing.T) {
	t.Parallel()

	c := newBoundedCapture(nil)
	line := fmt.Sprintf("%s\n", strings.Repeat("w", 200))
	for range (maxCaptureRetain / len(line)) + 5 {
		_, err := c.Write([]byte(line))
		require.NoError(t, err)
	}
	got := c.String()
	require.LessOrEqual(t, strings.Count(got, "\n")+1, maxPersistedLines)
}

func TestRunAndCaptureStream_SmallOutputUnchanged(t *testing.T) {
	t.Parallel()

	var progress strings.Builder
	res, err := RunAndCaptureStream(
		context.Background(),
		RunOptions{Command: "echo hello", Cwd: t.TempDir()},
		func(s string) { progress.WriteString(s) },
	)
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Contains(t, res.Output, "hello")
	require.Contains(t, progress.String(), "hello")
}
