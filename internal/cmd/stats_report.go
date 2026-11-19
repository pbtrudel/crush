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
results in the window, and "unstamped" otherwise. A refusal by the read
guard is known by its kind alone, so rows written before the kinds existed
show none. A refusal the session has not followed up inside the window,
because the window ends first or its call falls before --since, counts as
no retry.

Tools whose calls never ran under a known name are listed as
"unrecognized". Errors are counted per result in the window and calls
per call in the window, so when --since cuts a session a tool can show
more errors than calls.

A child session whose creating call falls before --since reads
"unknown". A compaction a hook blocked or the user cancelled leaves no
row; one written before compactions carried statistics counts under
"unstamped" but not in the medians.

Injections are the hidden user rows that open with a tag the report knows,
and subagent reports. Any other hidden user row is a hidden turn: a goal,
a loop iteration, a plan handoff.

A prompt is a visible user row in a top-level session; in a child session
such a row is its brief, or a message sent to it. A row that follows a
compaction of a step stamped context_guard or context_overflow continues
that prompt; when a cancel dropped the queued prompt, the next prompt is
counted as a continuation. Without the stamp a continuation counts as a
prompt, and runs by turn end read "unstamped", counted per prompt.

Child sessions' steps, teammates' included, count toward the prompt open
in their top-level session when they were written. Steps before a
session's first prompt in the window, and child steps whose prompt falls
before --since, belong to no prompt; a continuation whose stamped step
falls before --since counts as a prompt.

By model and By build count a prompt under the model and build of its
first step, so a prompt that caused no step is in Turns but in neither,
and a model used only in child sessions shows no prompts; their median
steps leave child sessions out. Their tool calls and results come from
every session, each counted by the step that made the call, and a result
whose call falls before --since is skipped, so their totals can fall
short of Tools, Edits and Shell.`,
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
		cfg, err := config.Init(ctx, cwd, "", false, config.WithoutProviderUpdate())
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
