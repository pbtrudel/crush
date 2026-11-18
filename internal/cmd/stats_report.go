package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/logreport"
	"github.com/spf13/cobra"
)

var statsReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Print counts and rates from this project's session log",
	Long: `Print counts and rates from this project's session log, as text or
as JSON. The database is opened read-only, so a running session is not
disturbed. The output never contains text from a message, a title, a path
or a session id.

--since and --until take a date such as 2026-11-09 (the start of that day,
local time) or an age such as 7d or 12h. --since is included and --until
is excluded: --since 2026-10-21 --until 2026-11-17 ends at midnight before
November 17.

Foreground shell commands are the shell results that carry an exit code or
the interrupted mark, including a background command that finished before
it was moved. A command moved to the background carries neither and is not
counted.

An error result without a kind reads "other" when the build that made the
call stamps kinds, which the report learns from that build's stamped
results in the window, and "unstamped" otherwise.

Tools whose calls never ran under a known name are listed as
"unrecognized". Errors are counted per result in the window and calls
per call in the window, so when --since cuts a session a tool can show
more errors than calls.`,
	Args: cobra.NoArgs,
	RunE: runStatsReport,
}

func init() {
	addStatsReportFlags(statsReportCmd)
	statsCmd.AddCommand(statsReportCmd)
}

func addStatsReportFlags(cmd *cobra.Command) {
	cmd.Flags().String("since", "", "Start of the window: a date such as 2026-11-09 or an age such as 7d")
	cmd.Flags().String("until", "", "End of the window, excluded: a date or an age")
	cmd.Flags().Bool("json", false, "Output in JSON format")
}

func runStatsReport(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	now := time.Now()
	var opts logreport.Options
	var err error
	since, _ := cmd.Flags().GetString("since")
	if opts.Since, err = logreport.ParseBound(since, now); err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}
	until, _ := cmd.Flags().GetString("until")
	if opts.Until, err = logreport.ParseBound(until, now); err != nil {
		return fmt.Errorf("invalid --until: %w", err)
	}
	if !opts.Since.IsZero() && !opts.Until.IsZero() && !opts.Since.Before(opts.Until) {
		return errors.New("--since must be before --until")
	}

	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return err
	}
	dataDir, _ := cmd.Flags().GetString("data-dir")
	if dataDir == "" {
		cfg, err := config.Init(ctx, cwd, "", false)
		if err != nil {
			return fmt.Errorf("failed to initialize config: %w", err)
		}
		dataDir = cfg.Config().Options.DataDirectory
	}
	dbPath := filepath.Join(dataDir, "crush.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("no session log at %s: %w", dbPath, err)
	}
	conn, err := db.ConnectReadOnly(ctx, dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	// A reader can meet SQLITE_BUSY while another connection recovers or
	// closes the WAL; wait for it instead of failing at once.
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("set busy timeout: %w", err)
	}

	report, err := logreport.Run(ctx, conn, opts)
	if err != nil {
		return err
	}
	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		return report.WriteJSON(cmd.OutOrStdout())
	}
	return report.WriteText(cmd.OutOrStdout())
}
