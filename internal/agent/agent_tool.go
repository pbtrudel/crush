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

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
)

//go:embed templates/agent_tool.md
var agentToolDescription string

type AgentParams struct {
	Prompt string `json:"prompt" description:"The task for the agent to perform"`
	// Agent optionally selects a configured agent preset, which controls
	// the subagent's tools, MCP servers, and context paths.
	Agent string `json:"agent,omitempty" description:"Optional configured agent preset to use for this subagent. Defaults to the task preset."`
	// RunInBackground defaults to the background_subagents option. Set
	// false to wait for the result in this turn.
	RunInBackground *bool `json:"run_in_background,omitempty" description:"Set false to wait for the result in this turn. Defaults to the background_subagents option."`
}

const (
	AgentToolName = "agent"
)

func (c *coordinator) agentTool(ctx context.Context) (fantasy.AgentTool, error) {
	cfg := c.cfg.Config()
	agentCfg, ok := cfg.Agents[config.AgentTask]
	if !ok {
		return nil, errors.New("task agent not configured")
	}
	systemPrompt, err := taskPrompt(prompt.WithWorkingDir(c.cfg.WorkingDir()))
	if err != nil {
		return nil, err
	}

	defaultAgent, err := c.buildAgent(ctx, systemPrompt, agentCfg, true)
	if err != nil {
		return nil, err
	}

	description := agentToolDescription
	if presets := availableSubAgentPresets(cfg); len(presets) > 0 {
		description += "\n\nAvailable presets: " + strings.Join(presets, ", ") + "."
	}

	return fantasy.NewParallelAgentTool(
		AgentToolName,
		description,
		func(ctx context.Context, params AgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Prompt == "" {
				return fantasy.NewTextErrorResponse("prompt is required"), nil
			}

			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}

			agent := defaultAgent
			if name := cmp.Or(params.Agent, config.AgentTask); name != config.AgentTask {
				preset, err := resolveSubAgentPreset(cfg, name)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
				agent, err = c.buildAgent(ctx, systemPrompt, preset, true)
				if err != nil {
					return fantasy.NewTextErrorResponse(err.Error()), nil
				}
			}

			sp := subAgentParams{
				Agent:          agent,
				SessionID:      sessionID,
				AgentMessageID: agentMessageID,
				ToolCallID:     call.ID,
				Prompt:         params.Prompt,
				SessionTitle:   "New Agent Session",
			}
			background := true
			if c.cfg != nil && c.cfg.Config() != nil {
				background = c.cfg.Config().Options.GetBackgroundSubagents()
			}
			if params.RunInBackground != nil {
				background = *params.RunInBackground
			}
			if background {
				return c.startBackgroundSubAgent(ctx, sp)
			}
			return c.runSubAgent(ctx, sp)
		},
	), nil
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
