package prompt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fixedSnapshotTime() time.Time {
	ts, _ := time.Parse("1/2/2006", "1/1/2025")
	return ts
}

func TestSessionEnvSnapshotGitRepo(t *testing.T) {
	t.Parallel()

	dir := newRepo(t, "feature/thing")

	got := SessionEnvSnapshot(context.Background(), dir, fixedSnapshotTime())
	require.True(t, strings.HasPrefix(got, EnvSnapshotPrefix),
		"the snapshot must open with the marker so Run can recognize it on retries")
	require.Contains(t, got, "Today's date: 1/1/2025")
	require.Contains(t, got, "Current branch: feature/thing")
	require.True(t, strings.HasSuffix(got, "</env_snapshot>"),
		"the snapshot must be a closed block")
}

func TestSessionEnvSnapshotNonGitDir(t *testing.T) {
	t.Parallel()

	got := SessionEnvSnapshot(context.Background(), t.TempDir(), fixedSnapshotTime())
	require.Contains(t, got, "Today's date: 1/1/2025")
	require.NotContains(t, got, "Git status")
}

func TestSessionEnvSnapshotDeterministic(t *testing.T) {
	t.Parallel()

	dir := newRepo(t, "main")
	ctx := context.Background()
	first := SessionEnvSnapshot(ctx, dir, fixedSnapshotTime())
	second := SessionEnvSnapshot(ctx, dir, fixedSnapshotTime())
	require.Equal(t, first, second,
		"same clock and repo state must render identical bytes")
}

// A .git directory that is not a real repository satisfies isGitRepo but
// makes every git command fail. The snapshot must degrade instead of
// failing the turn: the branch line is omitted and the status section is
// best-effort (the status command pipes through head, so a failing git is
// not reported as an error).
func TestSessionEnvSnapshotBrokenGit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))

	got := SessionEnvSnapshot(context.Background(), dir, fixedSnapshotTime())
	require.Contains(t, got, "Today's date: 1/1/2025")
	require.NotContains(t, got, "Current branch")
}
