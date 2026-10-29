package agent

import (
	"context"
	_ "embed"
	"fmt"

	"charm.land/fantasy"
)

//go:embed templates/subagent_output.md
var subagentOutputDescription string

const SubagentOutputToolName = "subagent_output"

type SubagentOutputParams struct {
	SubagentID string `json:"subagent_id" description:"The subagent id returned by the agent tool"`
	Wait       bool   `json:"wait" description:"If true, block until the subagent finishes"`
}

func (c *coordinator) subagentOutputTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		SubagentOutputToolName,
		subagentOutputDescription,
		func(ctx context.Context, params SubagentOutputParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.SubagentID == "" {
				return fantasy.NewTextErrorResponse("subagent_id is required"), nil
			}
			if c.subagents == nil {
				return fantasy.NewTextErrorResponse("no background subagents are running"), nil
			}
			run, ok := c.subagents.get(params.SubagentID)
			if !ok {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("subagent not found: %s", params.SubagentID)), nil
			}
			if params.Wait {
				select {
				case <-run.done:
				case <-ctx.Done():
					return fantasy.NewTextErrorResponse("timed out waiting for subagent"), nil
				}
			}
			output, done, found, err := c.subagents.result(params.SubagentID)
			if !found {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("subagent not found: %s", params.SubagentID)), nil
			}
			if !done {
				return fantasy.NewTextResponse(fmt.Sprintf("Subagent %s is still running. You will be notified when it finishes.", params.SubagentID)), nil
			}
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			if output == "" {
				return fantasy.NewTextResponse(fmt.Sprintf("Subagent %s finished with no text output.", params.SubagentID)), nil
			}
			return fantasy.NewTextResponse(output), nil
		},
	)
}
