package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/shell"
)

const (
	JobKillToolName = "job_kill"
)

//go:embed job_kill.md
var jobKillDescription string

type JobKillParams struct {
	ShellID string `json:"shell_id" description:"The ID of the background shell to terminate"`
}

type JobKillResponseMetadata struct {
	ShellID     string `json:"shell_id"`
	Command     string `json:"command"`
	Description string `json:"description"`
}

// alreadyGoneResponse reports a kill that had nothing left to do. A shell
// can finish and be reaped before the model's kill arrives, and that race
// should not surface as a failed tool call.
func alreadyGoneResponse(shellID string) fantasy.ToolResponse {
	return fantasy.NewTextResponse(fmt.Sprintf("Background shell %s is not running; it may have already finished or been reaped.", shellID))
}

func NewJobKillTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		JobKillToolName,
		jobKillDescription,
		func(ctx context.Context, params JobKillParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.ShellID == "" {
				return fantasy.NewTextErrorResponse("missing shell_id"), nil
			}

			bgManager := shell.GetBackgroundShellManager()

			bgShell, ok := bgManager.Get(params.ShellID)
			if !ok {
				return alreadyGoneResponse(params.ShellID), nil
			}

			metadata := JobKillResponseMetadata{
				ShellID:     params.ShellID,
				Command:     bgShell.Command,
				Description: bgShell.Description,
			}
			bgShell.MarkKilledByModel()

			err := bgManager.Kill(params.ShellID)
			if err != nil {
				// The shell can be reaped between Get and Kill, so a
				// missing ID here is the same harmless race as above.
				if errors.Is(err, shell.ErrBackgroundShellNotFound) {
					return alreadyGoneResponse(params.ShellID), nil
				}
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			result := fmt.Sprintf("Background shell %s terminated successfully", params.ShellID)
			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(result), metadata), nil
		},
	)
}
