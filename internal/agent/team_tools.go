package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
)

// Team tool names. They match the experimental DSH Agent Teams surface.
const (
	SpawnTeammateToolName  = "spawn_teammate"
	SendMessageToolName    = "send_message"
	ListAgentsToolName     = "list_agents"
	WaitAgentToolName      = "wait_agent"
	InterruptAgentToolName = "interrupt_agent"
	TeamTaskCreateToolName = "team_task_create"
	TeamTaskListToolName   = "team_task_list"
	TeamTaskGetToolName    = "team_task_get"
	TeamTaskUpdateToolName = "team_task_update"
)

// noActivePeerMessage explains why a wait cannot make progress.
const noActivePeerMessage = "No other Team member is running or provisioning. wait_agent cannot make progress or wake inactive teammates. Re-list with list_agents and team_task_list, then use send_message to wake each required inactive teammate before waiting again."

type spawnTeammateParams struct {
	Name        string `json:"name" description:"Unique lower-kebab-case teammate name."`
	Description string `json:"description" description:"Short description of the delegated responsibility."`
	Prompt      string `json:"prompt" description:"Complete initial task for the teammate."`
	Context     string `json:"context,omitempty" description:"fresh starts without Lead history; fork inherits completed Lead turns. Defaults to fresh."`
}

type sendMessageParams struct {
	Target  string `json:"target" description:"Member target returned by spawn_teammate or list_agents, including lead."`
	Message string `json:"message" description:"Self-contained message for the target."`
}

type waitAgentParams struct {
	TimeoutMS *int `json:"timeout_ms,omitempty" description:"Wait duration in milliseconds, from 10000 through 3600000. Defaults to 30000."`
}

type interruptAgentParams struct {
	Target string `json:"target" description:"Teammate target returned by spawn_teammate or list_agents."`
}

type teamTaskCreateParams struct {
	Subject     string   `json:"subject" description:"Concise task title."`
	Description string   `json:"description" description:"Complete task details and acceptance criteria."`
	BlockedBy   []string `json:"blocked_by,omitempty" description:"Task ids that must complete first."`
	WriteScopes []string `json:"write_scopes,omitempty" description:"Advisory workspace-relative file or directory prefixes this task expects to modify."`
}

type teamTaskListParams struct {
	Status *string `json:"status,omitempty" description:"Optional exact status filter."`
	Owner  *string `json:"owner,omitempty" description:"Optional member target from spawn_teammate or list_agents, matching ownerName; use unowned for tasks without an owner."`
	Ready  *bool   `json:"ready,omitempty" description:"Optional readiness filter."`
	Cursor *int    `json:"cursor,omitempty" description:"Zero-based result offset. Defaults to 0."`
	Limit  *int    `json:"limit,omitempty" description:"Number of rows, 1 through 100. Defaults to 50."`
}

type teamTaskGetParams struct {
	TaskID string `json:"task_id" description:"Shared task id."`
}

type teamTaskUpdateParams struct {
	TaskID           string   `json:"task_id" description:"Shared task id."`
	ExpectedRevision int      `json:"expected_revision" description:"Current task revision used as the CAS precondition."`
	Action           string   `json:"action" description:"Task transition to apply."`
	Subject          *string  `json:"subject,omitempty" description:"Replacement title for edit."`
	Description      *string  `json:"description,omitempty" description:"Replacement details for edit."`
	BlockedBy        []string `json:"blocked_by,omitempty" description:"Complete blocker list for set_dependencies."`
	WriteScopes      []string `json:"write_scopes,omitempty" description:"Replacement advisory write scopes for edit."`
	Owner            *string  `json:"owner,omitempty" description:"Member target from spawn_teammate or list_agents for Lead-only reassign; omit to unassign."`
}

// teamTools returns the nine Team-scoped tools bound to one caller.
func (c *coordinator) teamTools(caller string) []fantasy.AgentTool {
	return []fantasy.AgentTool{
		c.spawnTeammateTool(caller),
		c.sendMessageTool(caller),
		c.listAgentsTool(caller),
		c.waitAgentTool(caller),
		c.interruptAgentTool(caller),
		c.teamTaskCreateTool(caller),
		c.teamTaskListTool(caller),
		c.teamTaskGetTool(caller),
		c.teamTaskUpdateTool(caller),
	}
}

// teamResponse renders a compact JSON result and appends pending peer
// messages, which is how Team inbox delivery reaches the model.
func (c *coordinator) teamResponse(caller string, value any) fantasy.ToolResponse {
	data, err := json.Marshal(value)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error())
	}
	text := string(data)
	if c.team == nil {
		return fantasy.NewTextResponse(text)
	}
	pending := c.team.DrainInbox(caller)
	if len(pending) == 0 {
		return fantasy.NewTextResponse(text)
	}
	var sb strings.Builder
	sb.WriteString(text)
	for _, message := range pending {
		fmt.Fprintf(&sb, "\n\nMessage from %s: %s", message.From, message.Text)
	}
	return fantasy.NewTextResponse(sb.String())
}

func (c *coordinator) teamError(err error) fantasy.ToolResponse {
	return fantasy.NewTextErrorResponse(err.Error())
}

func (c *coordinator) spawnTeammateTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		SpawnTeammateToolName,
		"Create one named, durable teammate. Only the Team Lead may call this tool.",
		func(ctx context.Context, params spawnTeammateParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			mode := agentteam.ContextMode(params.Context)
			if mode == "" {
				mode = agentteam.ContextFresh
			}
			view, err := c.team.SpawnTeammate(ctx, caller, agentteam.SpawnRequest{
				Name:        params.Name,
				Description: params.Description,
				Prompt:      params.Prompt,
				Context:     mode,
				Provider:    "crush",
				ParentID:    tools.GetSessionFromContext(ctx),
			})
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(caller, map[string]any{"member": view}), nil
		},
	)
}

func (c *coordinator) sendMessageTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		SendMessageToolName,
		"Send one message to another Team member. A running target receives it at the nearest step boundary; an inactive target starts or resumes a turn.",
		func(ctx context.Context, params sendMessageParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			err := c.team.SendMessage(ctx, caller, agentteam.SendRequest{
				Target:  params.Target,
				Message: params.Message,
			})
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(caller, map[string]bool{"sent": true}), nil
		},
	)
}

func (c *coordinator) listAgentsTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ListAgentsToolName,
		"List the Lead and every durable teammate with an addressable target and current availability. inactive means no turn is executing, not a task result. provisioning and failed describe member creation.",
		func(_ context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			members, err := c.team.ListMembers(caller)
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(caller, members), nil
		},
	)
}

func (c *coordinator) waitAgentTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		WaitAgentToolName,
		"Wait for the next teammate status, message, or shared-task change after this call starts. This never wakes inactive members and returns noProgress immediately when no other member is running or provisioning. Re-list after wakeup or timeout instead of polling.",
		func(ctx context.Context, params waitAgentParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			timeout := 30 * time.Second
			if params.TimeoutMS != nil {
				ms := *params.TimeoutMS
				if ms < 10000 || ms > 3600000 {
					return fantasy.NewTextErrorResponse("timeout_ms must be from 10000 through 3600000"), nil
				}
				timeout = time.Duration(ms) * time.Millisecond
			}
			if !c.team.HasActivePeer(caller) {
				return c.teamResponse(caller, map[string]any{
					"timedOut": false,
					"noProgress": map[string]string{
						"reason":  "no-active-peer",
						"message": noActivePeerMessage,
					},
				}), nil
			}
			result, err := c.team.WaitForChange(ctx, caller, timeout)
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(caller, map[string]bool{"timedOut": result.TimedOut}), nil
		},
	)
}

func (c *coordinator) interruptAgentTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		InterruptAgentToolName,
		"Interrupt one teammate's current turn while preserving its pending inbox. Team Lead only.",
		func(_ context.Context, params interruptAgentParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			previous, err := c.team.Interrupt(caller, params.Target)
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(caller, map[string]string{"previousStatus": string(previous)}), nil
		},
	)
}

func (c *coordinator) teamTaskCreateTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamTaskCreateToolName,
		"Create one unowned pending task on the shared Team task board.",
		func(_ context.Context, params teamTaskCreateParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			view, err := c.team.CreateTask(caller, agentteam.CreateTaskRequest{
				Subject:     params.Subject,
				Description: params.Description,
				BlockedBy:   params.BlockedBy,
				WriteScopes: params.WriteScopes,
			})
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(caller, view), nil
		},
	)
}

func (c *coordinator) teamTaskListTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamTaskListToolName,
		"List shared tasks, including readiness, owner, revision, blockers, and write-scope warnings.",
		func(_ context.Context, params teamTaskListParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			filter := agentteam.TaskFilter{Ready: params.Ready}
			if params.Status != nil {
				status := agentteam.TaskStatus(*params.Status)
				filter.Status = &status
			}
			if params.Owner != nil {
				filter.Owner = *params.Owner
			}
			if params.Cursor != nil {
				filter.Cursor = *params.Cursor
			}
			if params.Limit != nil {
				filter.Limit = *params.Limit
			}
			list, err := c.team.ListTasks(caller, filter)
			if err != nil {
				return c.teamError(err), nil
			}
			result := map[string]any{"tasks": list.Tasks}
			if list.NextCursor > 0 {
				result["nextCursor"] = list.NextCursor
			}
			return c.teamResponse(caller, result), nil
		},
	)
}

func (c *coordinator) teamTaskGetTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamTaskGetToolName,
		"Read the complete latest value of one shared task before changing or executing it.",
		func(_ context.Context, params teamTaskGetParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			view, err := c.team.GetTask(caller, params.TaskID)
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(caller, view), nil
		},
	)
}

func (c *coordinator) teamTaskUpdateTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamTaskUpdateToolName,
		"Compare-and-set a shared task action using the latest revision from team_task_get or team_task_list.",
		func(_ context.Context, params teamTaskUpdateParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			request := agentteam.UpdateTaskRequest{
				TaskID:           params.TaskID,
				ExpectedRevision: params.ExpectedRevision,
				Action:           params.Action,
				BlockedBy:        params.BlockedBy,
				WriteScopes:      params.WriteScopes,
			}
			if params.Subject != nil {
				request.Subject = *params.Subject
			}
			if params.Description != nil {
				request.Description = *params.Description
			}
			if params.Owner != nil {
				request.Owner = *params.Owner
			}
			view, err := c.team.UpdateTask(caller, request)
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(caller, view), nil
		},
	)
}
