package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/spf13/cobra"
)

var (
	teamStatusJSON bool
	teamWatchEvery time.Duration
)

var teamCmd = &cobra.Command{
	Use:     "team",
	Aliases: []string{"teams"},
	Short:   "Inspect the Agent Teams roster and shared task board",
	Long:    "Inspect the Agent Teams roster, member availability, and shared task board. Use --json for machine-readable output.",
	RunE:    runTeamStatus,
}

var teamStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the roster and shared task board",
	Long:  "Show the roster and shared task board. Use --json for machine-readable output.",
	RunE:  runTeamStatus,
}

var teamWatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch the roster and task board live",
	Long:  "Re-render the roster and shared task board on an interval until interrupted.",
	RunE:  runTeamWatch,
}

var teamSessionCmd = &cobra.Command{
	Use:   "session <member>",
	Short: "Print the command to open a teammate session",
	Long:  "Print the Crush command that resumes a teammate's session.",
	Args:  cobra.ExactArgs(1),
	RunE:  runTeamSession,
}

func init() {
	teamCmd.Flags().BoolVar(&teamStatusJSON, "json", false, "output in JSON format")
	teamStatusCmd.Flags().BoolVar(&teamStatusJSON, "json", false, "output in JSON format")
	teamWatchCmd.Flags().DurationVar(&teamWatchEvery, "interval", time.Second, "refresh interval")
	teamCmd.AddCommand(teamStatusCmd)
	teamCmd.AddCommand(teamWatchCmd)
	teamCmd.AddCommand(teamSessionCmd)
}

func teamStatePath(cmd *cobra.Command) (string, error) {
	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return "", err
	}
	dataDir, _ := cmd.Flags().GetString("data-dir")
	cfg, err := config.Init(cmd.Context(), cwd, dataDir, false)
	if err != nil {
		return "", fmt.Errorf("failed to initialize config: %w", err)
	}
	if dataDir == "" {
		dataDir = cfg.Config().Options.DataDirectory
	}
	return filepath.Join(dataDir, "agent-teams", "team.json"), nil
}

func loadTeamState(cmd *cobra.Command) (agentteam.State, error) {
	path, err := teamStatePath(cmd)
	if err != nil {
		return agentteam.State{}, err
	}
	return agentteam.LoadState(path)
}

func runTeamStatus(cmd *cobra.Command, _ []string) error {
	state, err := loadTeamState(cmd)
	if err != nil {
		return err
	}
	if teamStatusJSON {
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
	}
	fmt.Fprint(cmd.OutOrStdout(), renderTeam(state))
	return nil
}

func runTeamWatch(cmd *cobra.Command, _ []string) error {
	if teamWatchEvery < 100*time.Millisecond {
		return fmt.Errorf("interval must be at least 100ms")
	}
	out := cmd.OutOrStdout()
	for {
		state, err := loadTeamState(cmd)
		if err != nil {
			return err
		}
		// Clear the screen and home the cursor, then redraw.
		fmt.Fprint(out, "\x1b[H\x1b[2J")
		fmt.Fprint(out, renderTeam(state))
		time.Sleep(teamWatchEvery)
	}
}

func runTeamSession(cmd *cobra.Command, args []string) error {
	state, err := loadTeamState(cmd)
	if err != nil {
		return err
	}
	name := args[0]
	for _, member := range state.Members {
		if member.Name != name {
			continue
		}
		if member.SessionID == "" {
			return fmt.Errorf("teammate %q has no session yet", name)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "crush --session %s\n", member.SessionID)
		return nil
	}
	return fmt.Errorf("teammate %q not found", name)
}

// renderTeam renders the roster and shared task board.
func renderTeam(state agentteam.State) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Agent Team %s\n", state.TeamID)
	fmt.Fprintf(&sb, "Members (%d)\n", len(state.Members)+1)
	sb.WriteString("  ◐ lead                  running       Team Lead\n")
	for _, member := range state.Members {
		fmt.Fprintf(&sb, "  %s %-20s %-13s %s\n",
			statusGlyph(member.Status), member.Name, member.Status, member.Description)
		for _, diagnostic := range member.Diagnostics {
			fmt.Fprintf(&sb, "      ! %s\n", diagnostic)
		}
	}

	tasks := agentteam.TaskViews(state)
	active := 0
	for _, task := range tasks {
		if task.Status != agentteam.TaskDeleted {
			active++
		}
	}
	fmt.Fprintf(&sb, "\nShared tasks (%d)\n", active)
	if active == 0 {
		sb.WriteString("  No shared tasks yet.\n")
		return sb.String()
	}
	for _, task := range tasks {
		if task.Status == agentteam.TaskDeleted {
			continue
		}
		fmt.Fprintf(&sb, "  %s %-8s %-12s %s\n",
			taskGlyph(task), task.ID, task.Status, task.Subject)
		if task.OwnerName != "" {
			fmt.Fprintf(&sb, "      owner %s\n", task.OwnerName)
		}
		if len(task.BlockedBy) > 0 {
			fmt.Fprintf(&sb, "      blocked by %s\n", strings.Join(task.BlockedBy, ", "))
		}
		if len(task.WriteScopes) > 0 {
			fmt.Fprintf(&sb, "      write scopes %s\n", strings.Join(task.WriteScopes, ", "))
		}
		for _, warning := range task.WriteScopeWarnings {
			fmt.Fprintf(&sb, "      ! %s\n", warning)
		}
	}
	return sb.String()
}

func statusGlyph(status agentteam.Status) string {
	switch status {
	case agentteam.StatusRunning, agentteam.StatusProvisioning:
		return "◐"
	case agentteam.StatusFailed:
		return "✗"
	default:
		return "○"
	}
}

func taskGlyph(task agentteam.TaskView) string {
	switch {
	case task.Status == agentteam.TaskCompleted:
		return "✓"
	case task.Status == agentteam.TaskInProgress:
		return "◐"
	case task.Ready:
		return "○"
	default:
		return "!"
	}
}
