package agent

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/fantasy"
	"golang.org/x/sync/errgroup"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
)

//go:embed templates/agent_tool.md
var agentToolDescription string

type AgentParams struct {
	Prompt string `json:"prompt" description:"The task for the agent to perform"`
	// Agent optionally selects a configured agent preset, which controls
	// the subagent's tools, MCP servers, and context paths.
	Agent  string `json:"agent,omitempty" description:"Optional configured agent preset to use for this subagent. Defaults to the task preset."`
	Access string `json:"access,omitempty" description:"\"read\" (default) for a subagent that can only inspect the codebase, or \"write\" for one that can also edit files"`
}

const (
	AgentToolName = "agent"

	accessRead  = "read"
	accessWrite = "write"
)

// writeAgentConfig derives the read-write variant of the task agent: the same
// agent, given the coder's tools and MCP servers. Subagents never get the
// delegation tools; effectiveToolNames strips them.
func writeAgentConfig(cfg *config.Config) config.Agent {
	task := cfg.Agents[config.AgentTask]
	if coder, ok := cfg.Agents[config.AgentCoder]; ok {
		task.AllowedTools = slices.Clone(coder.AllowedTools)
		task.AllowedMCP = coder.AllowedMCP
	}
	return task
}

// accessOrRead defaults an unset access level to read-only.
func accessOrRead(access string) string {
	if access == "" {
		return accessRead
	}
	return access
}

// readOnlyParent reports whether a parent agent may only delegate to
// read-only subagents. Plan mode promises it cannot change files, and that
// promise has to cover the work it hands off as well as the work it does
// itself.
func readOnlyParent(parentAgentID string) bool {
	return parentAgentID == config.AgentPlan
}

// resolveSubAgent picks the subagent variant for a requested access level,
// returning a message for the model when there is no such variant.
func resolveSubAgent(agents map[string]SessionAgent, access string, readOnly bool) (SessionAgent, string) {
	access = accessOrRead(access)
	if agent, ok := agents[access]; ok {
		return agent, ""
	}
	if readOnly && access == accessWrite {
		return nil, `plan mode subagents are read-only; omit access or pass "read"`
	}
	return nil, `access must be "read" or "write"`
}

// taskAgents builds the variants of the task subagent. Both the synchronous
// agent tool and the detached dispatch tool draw from these. A read-only
// parent gets only the read variant, so the write one is never built rather
// than built and withheld.
func (c *coordinator) taskAgents(ctx context.Context, readOnly bool) (map[string]SessionAgent, error) {
	if _, ok := c.cfg.Config().Agents[config.AgentTask]; !ok {
		return nil, errors.New("task agent not configured")
	}
	accesses := []string{accessRead, accessWrite}
	if readOnly {
		accesses = accesses[:1]
	}

	// The variants share a readiness group of their own and are fully set
	// up before this returns. This runs on every turn, concurrently with
	// other runs waiting on the coordinator's group, so joining that group
	// would race its Wait.
	var ready errgroup.Group
	agents := map[string]SessionAgent{}
	for _, access := range accesses {
		agentCfg, canWrite := c.taskAgentConfig(access)
		agent, err := c.buildTaskAgent(ctx, &ready, agentCfg, canWrite, nil)
		if err != nil {
			return nil, err
		}
		agents[access] = agent
	}
	if err := ready.Wait(); err != nil {
		return nil, err
	}
	return agents, nil
}

// taskAgentConfig returns the task agent's config for an access level, and
// whether it may write.
func (c *coordinator) taskAgentConfig(access string) (config.Agent, bool) {
	if access == accessWrite {
		return writeAgentConfig(c.cfg.Config()), true
	}
	return c.cfg.Config().Agents[config.AgentTask], false
}

// buildTaskAgent schedules one variant of the task subagent into ready; the
// caller waits on the group, so several variants can build concurrently.
// modelsFn, when set, replaces the shared subagent model set; a dispatch
// with a model override uses it to run the variant on the overridden model.
func (c *coordinator) buildTaskAgent(ctx context.Context, ready *errgroup.Group, agentCfg config.Agent, canWrite bool, modelsFn func() ModelSet) (SessionAgent, error) {
	pr, err := taskPrompt(
		prompt.WithWorkingDir(c.cfg.WorkingDir()),
		prompt.WithCanWrite(canWrite),
	)
	if err != nil {
		return nil, err
	}
	return c.buildAgentWithModels(ctx, ready, agentSystemPrompt{Template: pr}, agentCfg, roleSubAgent, modelsFn)
}

func (c *coordinator) agentTool(ctx context.Context, parentAgentID string) (fantasy.AgentTool, error) {
	readOnly := readOnlyParent(parentAgentID)
	agents, err := c.taskAgents(ctx, readOnly)
	if err != nil {
		return nil, err
	}

	description := agentToolDescription
	if presets := availableSubAgentPresets(c.cfg.Config()); len(presets) > 0 {
		description += "\n\nAvailable presets: " + strings.Join(presets, ", ") + "."
	}

	return fantasy.NewParallelAgentTool(
		AgentToolName,
		description,
		func(ctx context.Context, params AgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			// A teammate keeps this tool so its palette matches the Lead's,
			// but nested delegation is not supported: the Team domain has
			// one level, and a teammate's sub-agent would run outside the
			// roster the Lead coordinates.
			if !agentteam.IsLead(ctx) {
				return fantasy.NewTextErrorResponse("nested delegation is not supported inside a teammate; ask the Lead instead"), nil
			}
			if params.Prompt == "" {
				return fantasy.NewTextErrorResponse("prompt is required"), nil
			}

			agent, refusal := c.resolveAgentToolVariant(ctx, params, agents, readOnly)
			if refusal != "" {
				return fantasy.NewTextErrorResponse(refusal), nil
			}

			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}

			return c.runSubAgentMaybeBackground(ctx, AgentToolName, params.Access, subAgentParams{
				Agent:          agent,
				SessionID:      sessionID,
				AgentMessageID: agentMessageID,
				ToolCallID:     call.ID,
				Prompt:         params.Prompt,
				SessionTitle:   "New Agent Session",
			})
		},
	), nil
}

// resolveAgentToolVariant picks the agent a call runs on. A named preset wins
// over the read/write access variants, because a preset already fixes the
// tools the subagent gets; otherwise the access level selects one of the
// task variants built for this turn.
func (c *coordinator) resolveAgentToolVariant(ctx context.Context, params AgentParams, agents map[string]SessionAgent, readOnly bool) (SessionAgent, string) {
	if params.Agent != "" && params.Agent != config.AgentTask {
		preset, err := resolveSubAgentPreset(c.cfg.Config(), params.Agent)
		if err != nil {
			return nil, err.Error()
		}
		// A preset is built on demand, so it gets a readiness group of its
		// own: the coordinator's group is only safe for the constructor.
		var ready errgroup.Group
		agent, err := c.buildTaskAgent(ctx, &ready, preset, !readOnly, nil)
		if err != nil {
			return nil, err.Error()
		}
		if err := ready.Wait(); err != nil {
			return nil, err.Error()
		}
		return agent, ""
	}
	return resolveSubAgent(agents, params.Access, readOnly)
}

// resolveSubAgentPreset returns the configured agent preset a subagent
// should use. An empty name selects the default task preset. Presets that
// are disabled or can spawn subagents themselves are rejected.
func resolveSubAgentPreset(cfg *config.Config, name string) (config.Agent, error) {
	if cfg == nil {
		return config.Agent{}, errors.New("config is not available")
	}
	name = cmp.Or(name, config.AgentTask)
	preset, ok := cfg.Agents[name]
	if !ok {
		available := availableSubAgentPresets(cfg)
		if len(available) == 0 {
			return config.Agent{}, fmt.Errorf("unknown agent preset %q", name)
		}
		return config.Agent{}, fmt.Errorf("unknown agent preset %q; available: %s", name, strings.Join(available, ", "))
	}
	if preset.Disabled {
		return config.Agent{}, fmt.Errorf("agent preset %q is disabled", name)
	}
	if slices.Contains(preset.AllowedTools, AgentToolName) {
		return config.Agent{}, fmt.Errorf("agent preset %q cannot spawn subagents", name)
	}
	return preset, nil
}

// availableSubAgentPresets lists the presets a subagent may use, sorted
// by name.
func availableSubAgentPresets(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Agents))
	for name, preset := range cfg.Agents {
		if preset.Disabled || slices.Contains(preset.AllowedTools, AgentToolName) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
