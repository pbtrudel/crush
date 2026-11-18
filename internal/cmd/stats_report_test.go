package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// runStatsReportForTest runs the report command on its own, with the root
// flags it reads, so a test does not share the package's command values.
func runStatsReportForTest(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := &cobra.Command{Use: "report", Args: cobra.NoArgs, RunE: runStatsReport}
	cmd.Flags().String("cwd", "", "")
	cmd.Flags().String("data-dir", "", "")
	addStatsReportFlags(cmd)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

func TestStatsReportReadsTheProjectLog(t *testing.T) {
	require.Contains(t, statsCmd.Commands(), statsReportCmd, "crush stats report")

	dir := t.TempDir()
	conn, err := db.Connect(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dir) })
	_, err = conn.ExecContext(t.Context(), `INSERT INTO sessions (id, title, created_at, updated_at) VALUES ('s1', 't', 0, 0)`)
	require.NoError(t, err)
	// 2026-11-10 12:00 UTC, which is 2026-11-10 in every time zone.
	_, err = conn.ExecContext(t.Context(), `INSERT INTO messages (id, session_id, role, parts, created_at, updated_at)
		VALUES ('m1', 's1', 'user', '[]', 1794312000, 1794312000)`)
	require.NoError(t, err)

	out, err := runStatsReportForTest(t, "--data-dir", dir, "--json", "--since", "2026-10-21", "--until", "2026-11-17")
	require.NoError(t, err)
	var report struct {
		Schema int `json:"schema"`
		Window struct {
			Messages int `json:"messages"`
		} `json:"window"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.Equal(t, 1, report.Schema)
	require.Equal(t, 1, report.Window.Messages)

	out, err = runStatsReportForTest(t, "--data-dir", dir)
	require.NoError(t, err)
	require.Contains(t, out, "Window")
}

func TestStatsReportRefusesBadInput(t *testing.T) {
	dir := t.TempDir()

	_, err := runStatsReportForTest(t, "--data-dir", dir)
	require.ErrorContains(t, err, "no session log")
	require.NoFileExists(t, filepath.Join(dir, "crush.db"), "the report never creates a database")

	_, err = runStatsReportForTest(t, "--data-dir", dir, "--since", "last week")
	require.ErrorContains(t, err, "invalid --since")

	_, err = runStatsReportForTest(t, "--data-dir", dir, "--since", "2026-11-17", "--until", "2026-11-09")
	require.ErrorContains(t, err, "--since must be before --until")
}
