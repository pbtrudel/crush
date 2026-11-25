package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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

	// Model override. When none of these is given the teammate runs on
	// the subagent model with its configured settings.
	Model     string `json:"model,omitempty" description:"The model ID for this teammate, as used by the provider API. Omit provider to use the parent agent's provider. Other settings default to the model's own"`
	Provider  string `json:"provider,omitempty" description:"The provider ID for the overridden model; requires model. Defaults to the parent agent's provider when only model is given"`
	Reasoning string `json:"reasoning,omitempty" description:"How hard the teammate reasons: one of the values the [models] section of crush_info lists in brackets for its model. Defaults to the model's own"`
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

// teamTools returns the nine Team-scoped tools bound to one caller. The
// parent model is what a spawn's model-only override takes its provider
// from, the same rule agent_dispatch follows.
func (c *coordinator) teamTools(caller string, parentModel func() Model) []fantasy.AgentTool {
	return []fantasy.AgentTool{
		c.spawnTeammateTool(caller, parentModel),
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
// messages, which is how Team inbox delivery reaches the model. The inbox
// is the caller's own: its session says which team the mail belongs to.
// The service is the one the call ran on, not a fresh read of the
// coordinator's pointer: a reload that retires the Team mid-turn must not
// lose or switch the mailbox the answer belongs to.
func (c *coordinator) teamResponse(ctx context.Context, team *agentteam.Service, caller string, value any) fantasy.ToolResponse {
	data, err := json.Marshal(value)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error())
	}
	text := string(data)
	if team == nil {
		return fantasy.NewTextResponse(text)
	}
	pending := team.DrainInbox(caller, tools.GetSessionFromContext(ctx))
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

// teamErrorWithState returns a refused task call with the state a retry
// needs: the current task when it still exists, otherwise the live tasks the
// model can pick from. A failure the state cannot help with falls back to the
// bare message.
func (c *coordinator) teamErrorWithState(ctx context.Context, caller string, err error, taskID string) fantasy.ToolResponse {
	text := err.Error()
	if agentteam.ErrorCode(err) == agentteam.CodeInvalidWriteScope {
		text += "\n\nwrite scopes must be inside the workspace; leave scratch paths out"
	}
	team := c.teamService()
	if team == nil {
		return fantasy.NewTextErrorResponse(text)
	}
	sessionID := tools.GetSessionFromContext(ctx)
	if taskID != "" {
		if view, getErr := team.GetTask(caller, sessionID, taskID); getErr == nil {
			if data, marshalErr := json.Marshal(view); marshalErr == nil {
				return fantasy.NewTextErrorResponse(text + "\n\nCurrent task:\n" + string(data))
			}
		}
	}
	list, listErr := team.ListTasks(caller, sessionID, agentteam.TaskFilter{Limit: 10})
	if listErr != nil || len(list.Tasks) == 0 {
		return fantasy.NewTextErrorResponse(text)
	}
	var sb strings.Builder
	sb.WriteString(text)
	sb.WriteString("\n\nLive tasks:")
	for _, task := range list.Tasks {
		fmt.Fprintf(&sb, "\n- %s: %s", task.ID, task.Subject)
	}
	return fantasy.NewTextErrorResponse(sb.String())
}

// relativizeWriteScopes rewrites absolute write scopes that sit inside the
// workspace to their relative form, which is the only form the validator
// accepts. It reports each rewrite so the response can name it.
func (c *coordinator) relativizeWriteScopes(scopes []string) ([]string, []string) {
	if len(scopes) == 0 || c.cfg == nil {
		return scopes, nil
	}
	workspace := c.cfg.WorkingDir()
	if workspace == "" {
		return scopes, nil
	}
	rewritten := make([]string, len(scopes))
	var changed []string
	for i, scope := range scopes {
		relative, ok := workspaceRelativeScope(workspace, scope)
		rewritten[i] = scope
		if !ok {
			continue
		}
		rewritten[i] = relative
		changed = append(changed, fmt.Sprintf("%s -> %s", scope, relative))
	}
	return rewritten, changed
}

// workspaceRelativeScope returns the relative form of an absolute scope that
// lies inside the workspace.
func workspaceRelativeScope(workspace, scope string) (string, bool) {
	if !filepath.IsAbs(scope) {
		return "", false
	}
	relative, err := filepath.Rel(workspace, scope)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func (c *coordinator) spawnTeammateTool(caller string, parentModel func() Model) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		SpawnTeammateToolName,
		"Create one named, durable teammate. Only the Team Lead may call this tool."+
			" `model`, `provider` and `reasoning` work as in agent_dispatch; the [models] section of crush_info lists what each provider can serve.",
		func(ctx context.Context, params spawnTeammateParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if refusal, off := c.refuseAgentsOff(); off {
				return refusal, nil
			}
			team := c.teamService()
			if team == nil {
				return c.teamError(ErrAgentTeamsDisabled), nil
			}
			mode := agentteam.ContextMode(params.Context)
			if mode == "" {
				mode = agentteam.ContextFresh
			}
			request := agentteam.SpawnRequest{
				Name:        params.Name,
				Description: params.Description,
				Prompt:      params.Prompt,
				Context:     mode,
				Provider:    "crush",
				ParentID:    tools.GetSessionFromContext(ctx),
			}
			// Resolve the override first, so a refused model leaves no
			// roster entry behind and the call can be retried.
			sel, hasOverride, refusal := c.resolveModelOverride(AgentDispatchParams{
				Model:     params.Model,
				Provider:  params.Provider,
				Reasoning: params.Reasoning,
			}, parentModel())
			if refusal != "" {
				return fantasy.NewTextErrorResponse(refusal), nil
			}
			if hasOverride {
				request.Model = sel.Model
				request.ModelProvider = sel.Provider
				request.Reasoning = strings.TrimSpace(params.Reasoning)
			}
			view, err := team.SpawnTeammate(ctx, caller, request)
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(ctx, team, caller, map[string]any{"member": view}), nil
		},
	)
}

func (c *coordinator) sendMessageTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		SendMessageToolName,
		"Send one message to another Team member. A running target receives it at the nearest step boundary; an inactive target starts or resumes a turn.",
		func(ctx context.Context, params sendMessageParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if refusal, off := c.refuseAgentsOff(); off {
				return refusal, nil
			}
			team := c.teamService()
			if team == nil {
				return c.teamError(ErrAgentTeamsDisabled), nil
			}
			err := team.SendMessage(ctx, caller, tools.GetSessionFromContext(ctx), agentteam.SendRequest{
				Target:  params.Target,
				Message: params.Message,
			})
			if err != nil {
				// The name may belong to a dispatched subagent instead.
				if agentteam.ErrorCode(err) == agentteam.CodeMemberNotFound {
					if resp, handled := c.deliverToSubagent(ctx, params.Target, params.Message); handled {
						return resp, nil
					}
					return fantasy.NewTextErrorResponse(fmt.Sprintf(
						"no teammate or subagent %q; teammates: %s; subagents: %s",
						params.Target, c.teammateNames(ctx),
						c.runningDispatchLabels(tools.GetSessionFromContext(ctx)),
					)), nil
				}
				return c.teamError(err), nil
			}
			return c.teamResponse(ctx, team, caller, map[string]bool{"sent": true}), nil
		},
	)
}

func (c *coordinator) listAgentsTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ListAgentsToolName,
		"List the Lead and every durable teammate with an addressable target and current availability. inactive means no turn is executing, not a task result. provisioning and failed describe member creation.",
		func(ctx context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			team := c.teamService()
			if team == nil {
				return c.teamError(ErrAgentTeamsDisabled), nil
			}
			members, err := team.ListMembers(caller, tools.GetSessionFromContext(ctx))
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(ctx, team, caller, members), nil
		},
	)
}

func (c *coordinator) waitAgentTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		WaitAgentToolName,
		"Wait for the next teammate status, message, or shared-task change after this call starts. Returns the current roster and live task board with every result. This never wakes inactive members and returns noProgress immediately when no other member is running or provisioning.",
		func(ctx context.Context, params waitAgentParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			team := c.teamService()
			if team == nil {
				return c.teamError(ErrAgentTeamsDisabled), nil
			}
			timeout := 30 * time.Second
			if params.TimeoutMS != nil {
				ms := *params.TimeoutMS
				if ms < 10000 || ms > 3600000 {
					return fantasy.NewTextErrorResponse("timeout_ms must be from 10000 through 3600000"), nil
				}
				timeout = time.Duration(ms) * time.Millisecond
			}
			if !team.HasActivePeer(caller, tools.GetSessionFromContext(ctx)) {
				return c.teamResponse(ctx, team, caller, map[string]any{
					"timedOut": false,
					"noProgress": map[string]string{
						"reason":  "no-active-peer",
						"message": noActivePeerMessage,
					},
					"progress": team.Progress(caller, tools.GetSessionFromContext(ctx)),
				}), nil
			}
			result, err := team.WaitForChange(ctx, caller, tools.GetSessionFromContext(ctx), timeout)
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(ctx, team, caller, result), nil
		},
	)
}

func (c *coordinator) interruptAgentTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		InterruptAgentToolName,
		"Interrupt one teammate's current turn while preserving its pending inbox. Team Lead only.",
		func(ctx context.Context, params interruptAgentParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if refusal, off := c.refuseAgentsOff(); off {
				return refusal, nil
			}
			team := c.teamService()
			if team == nil {
				return c.teamError(ErrAgentTeamsDisabled), nil
			}
			previous, err := team.Interrupt(caller, tools.GetSessionFromContext(ctx), params.Target)
			if err != nil {
				return c.teamError(err), nil
			}
			return c.teamResponse(ctx, team, caller, map[string]string{"previousStatus": string(previous)}), nil
		},
	)
}

func (c *coordinator) teamTaskCreateTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamTaskCreateToolName,
		"Create one unowned pending task on the shared Team task board.",
		func(ctx context.Context, params teamTaskCreateParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if refusal, off := c.refuseAgentsOff(); off {
				return refusal, nil
			}
			team := c.teamService()
			if team == nil {
				return c.teamError(ErrAgentTeamsDisabled), nil
			}
			scopes, rewritten := c.relativizeWriteScopes(params.WriteScopes)
			view, err := team.CreateTask(caller, tools.GetSessionFromContext(ctx), agentteam.CreateTaskRequest{
				Subject:     params.Subject,
				Description: params.Description,
				BlockedBy:   params.BlockedBy,
				WriteScopes: scopes,
			})
			if err != nil {
				return c.teamErrorWithState(ctx, caller, err, ""), nil
			}
			response := c.teamResponse(ctx, team, caller, view)
			if len(rewritten) > 0 {
				response.Content += "\n\nWrite scopes were rewritten relative to the workspace: " + strings.Join(rewritten, ", ")
			}
			return response, nil
		},
	)
}

func (c *coordinator) teamTaskListTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamTaskListToolName,
		"List shared tasks, including readiness, owner, revision, blockers, and write-scope warnings.",
		func(ctx context.Context, params teamTaskListParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			team := c.teamService()
			if team == nil {
				return c.teamError(ErrAgentTeamsDisabled), nil
			}
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
			list, err := team.ListTasks(caller, tools.GetSessionFromContext(ctx), filter)
			if err != nil {
				return c.teamError(err), nil
			}
			result := map[string]any{"tasks": list.Tasks}
			if list.NextCursor > 0 {
				result["nextCursor"] = list.NextCursor
			}
			return c.teamResponse(ctx, team, caller, result), nil
		},
	)
}

func (c *coordinator) teamTaskGetTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamTaskGetToolName,
		"Read the complete latest value of one shared task before changing or executing it.",
		func(ctx context.Context, params teamTaskGetParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			team := c.teamService()
			if team == nil {
				return c.teamError(ErrAgentTeamsDisabled), nil
			}
			view, err := team.GetTask(caller, tools.GetSessionFromContext(ctx), params.TaskID)
			if err != nil {
				return c.teamErrorWithState(ctx, caller, err, params.TaskID), nil
			}
			return c.teamResponse(ctx, team, caller, view), nil
		},
	)
}

func (c *coordinator) teamTaskUpdateTool(caller string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamTaskUpdateToolName,
		"Compare-and-set a shared task action using the latest revision from team_task_get or team_task_list.",
		func(ctx context.Context, params teamTaskUpdateParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			team := c.teamService()
			if team == nil {
				return c.teamError(ErrAgentTeamsDisabled), nil
			}
			scopes, rewritten := c.relativizeWriteScopes(params.WriteScopes)
			request := agentteam.UpdateTaskRequest{
				TaskID:           params.TaskID,
				ExpectedRevision: params.ExpectedRevision,
				Action:           params.Action,
				BlockedBy:        params.BlockedBy,
				WriteScopes:      scopes,
				SessionID:        tools.GetSessionFromContext(ctx),
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
			view, err := team.UpdateTask(caller, request)
			if err != nil {
				return c.teamErrorWithState(ctx, caller, err, params.TaskID), nil
			}
			response := c.teamResponse(ctx, team, caller, view)
			if len(rewritten) > 0 {
				response.Content += "\n\nWrite scopes were rewritten relative to the workspace: " + strings.Join(rewritten, ", ")
			}
			return response, nil
		},
	)
}
