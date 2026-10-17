package prompt

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// EnvSnapshotPrefix opens the per-session environment snapshot message.
// Run uses it to recognize the message when a first turn is retried, so the
// snapshot is never duplicated.
const EnvSnapshotPrefix = "<env_snapshot>"

// SessionEnvSnapshot renders the session's volatile environment data (date,
// git status) as the text of a hidden first user message.
//
// This data used to live in the system prompt, which is rebuilt once per
// process: every resume rebuilt it with a fresh date and git status, so the
// first request after a restart re-read the entire conversation even when the
// provider still held the cached prefix. A stored message replays
// byte-identically across restarts, keeping the cache warm, and being created
// per session it is accurate for this session instead of frozen at process
// start.
func SessionEnvSnapshot(ctx context.Context, workingDir string, now time.Time) string {
	var sb strings.Builder
	sb.WriteString(EnvSnapshotPrefix + "\n")
	sb.WriteString("Today's date: " + now.Format("1/2/2006") + "\n")
	if isGitRepo(workingDir) {
		status, err := getGitStatus(ctx, workingDir)
		if err != nil {
			// The model can run git itself; a broken git must not fail the
			// turn.
			slog.Warn("Failed to snapshot git status", "error", err)
		} else if status != "" {
			sb.WriteString("\nGit status (snapshot at conversation start - may be outdated):\n")
			sb.WriteString(status + "\n")
		}
	}
	sb.WriteString("</env_snapshot>")
	return sb.String()
}
