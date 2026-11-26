package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"golang.org/x/sync/errgroup"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/roles"
)

// mainModel returns the model the main agent runs on, or the zero Model
// before one is set.
func (c *coordinator) mainModel() Model {
	if agent, _ := c.activeAgent(); agent != nil {
		return agent.Model()
	}
	return Model{}
}

// resolveModelOverride turns the dispatch tool's model-override params into
// the model selection a subagent should run on. ok is false when no
// override was requested. A non-empty refusal means one was requested but
// cannot be served; it goes back to the model so the call can be retried.
func (c *coordinator) resolveModelOverride(params AgentDispatchParams, parentModel Model) (sel config.SelectedModel, ok bool, refusal string) {
	provider := strings.TrimSpace(params.Provider)
	model := strings.TrimSpace(params.Model)
	reasoning := strings.TrimSpace(params.Reasoning)
	switch {
	case provider == "" && model == "" && reasoning == "":
		return config.SelectedModel{}, false, ""
	case provider != "" && model == "":
		return config.SelectedModel{}, false, "provider is given without model; pass model as well"
	case model == "":
		// Reasoning alone rides the subagent's default selection.
		sub := c.subAgentModels().Large
		if sub.Model == nil {
			return config.SelectedModel{}, false, "no subagent model is configured"
		}
		sel = sub.ModelCfg
	default:
		if provider == "" {
			if parentModel.Model == nil {
				return config.SelectedModel{}, false, "the parent agent has no model to take a provider from"
			}
			provider = parentModel.ModelCfg.Provider
		}
		// A fresh selection: everything else is the model's own default.
		sel = config.SelectedModel{Provider: provider, Model: model}
	}
	catwalkModel, refusal := c.availableModel(sel)
	if refusal == "" {
		refusal = applyReasoning(&sel, catwalkModel, reasoning)
	}
	return sel, true, refusal
}

// availableModel finds a selection's model among what the catalog would list.
func (c *coordinator) availableModel(sel config.SelectedModel) (catwalk.Model, string) {
	cfg := c.cfg.Config()
	provider, ok := cfg.Providers.Get(sel.Provider)
	if !ok || provider.Disable {
		ids := make([]string, 0)
		for _, p := range usableProviders(cfg) {
			ids = append(ids, p.ID)
		}
		return catwalk.Model{}, fmt.Sprintf("provider %q is not available; use one of: %s", sel.Provider, strings.Join(ids, ", "))
	}
	models := provider.UsableModels()
	idx := slices.IndexFunc(models, func(m catwalk.Model) bool { return m.ID == sel.Model })
	if idx < 0 {
		return catwalk.Model{}, fmt.Sprintf("model %q is not available from provider %q; use one of: %s", sel.Model, sel.Provider, modelIDList(models))
	}
	return models[idx], ""
}

// reasoningOptions lists the values a model takes for reasoning: its effort
// levels, or on and off for a model that can only toggle thinking.
func reasoningOptions(m catwalk.Model) []string {
	if levels := m.ReasoningEffortLevels(); len(levels) > 0 {
		return levels
	}
	if m.Reasoning.Thinking == catwalk.ThinkingToggleable {
		return []string{"on", "off"}
	}
	return nil
}

// applyReasoning sets the selection's effort level or thinking toggle,
// whichever the model reasons with.
func applyReasoning(sel *config.SelectedModel, m catwalk.Model, reasoning string) string {
	if reasoning == "" {
		return ""
	}
	options := reasoningOptions(m)
	if !slices.Contains(options, reasoning) {
		if len(options) == 0 {
			return fmt.Sprintf("model %q does not support reasoning", m.ID)
		}
		return fmt.Sprintf("model %q takes reasoning %s", m.ID, strings.Join(options, ", "))
	}
	if len(m.ReasoningEffortLevels()) > 0 {
		sel.ReasoningEffort = reasoning
	} else {
		sel.Think = reasoning == "on"
	}
	return ""
}

// catalogModelsPerProvider keeps one huge catalog from crowding out the prompt.
const catalogModelsPerProvider = 25

// usableProviders lists enabled providers by ID, so the catalog text is stable across turns.
func usableProviders(cfg *config.Config) []config.ProviderConfig {
	providers := cfg.EnabledProviders()
	slices.SortFunc(providers, func(a, b config.ProviderConfig) int { return strings.Compare(a.ID, b.ID) })
	return providers
}

func modelIDList(models []catwalk.Model) string {
	listed := models[:min(len(models), catalogModelsPerProvider)]
	ids := make([]string, 0, len(listed))
	for _, m := range listed {
		if options := reasoningOptions(m); len(options) > 0 {
			ids = append(ids, m.ID+" ["+strings.Join(options, "|")+"]")
		} else {
			ids = append(ids, m.ID)
		}
	}
	list := strings.Join(ids, ", ")
	if extra := len(models) - len(listed); extra > 0 {
		list += fmt.Sprintf(", and %d more", extra)
	}
	return list
}

// modelCatalog renders the provider and model catalog, one line per usable
// provider, for the [models] section of crush_info. It used to sit in the
// descriptions of agent_dispatch and spawn_teammate, which sent it twice with
// every request and rewrote the cached tool definitions whenever provider
// metadata changed; read through crush_info it costs nothing until asked for.
// Empty when no provider is usable.
func modelCatalog(cfg *config.Config) string {
	providers := usableProviders(cfg)
	lines := make([]string, 0, len(providers))
	for _, p := range providers {
		if models := p.UsableModels(); len(models) > 0 {
			lines = append(lines, fmt.Sprintf("- `%s`: %s", p.ID, modelIDList(models)))
		}
	}
	return strings.Join(lines, "\n")
}

// modelDisplayName names a model as provider/model.
func modelDisplayName(m Model) string {
	return m.ModelCfg.Provider + "/" + m.ModelCfg.Model
}

// overrideDispatchAgent resolves a dispatch's model override and, when one
// was requested, builds the SessionAgent it should run on. Without an
// override the caller keeps the shared agent taskAgents built; with one,
// the entry keeps the dedicated agent so reopens run on the same model.
func (c *coordinator) overrideDispatchAgent(ctx context.Context, access string, params AgentDispatchParams, parentModel Model) (SessionAgent, config.SelectedModel, bool, string) {
	sel, ok, refusal := c.resolveModelOverride(params, parentModel)
	if refusal != "" || !ok {
		return nil, config.SelectedModel{}, false, refusal
	}
	agent, refusal := c.overrideTaskAgent(ctx, access, sel)
	if refusal != "" {
		return nil, sel, true, refusal
	}
	return agent, sel, true, ""
}

// overrideTaskAgent builds a dedicated task subagent variant running on an
// overridden model selection. The model function serves the selection's
// current build, so a model refresh reaches a running subagent on its next
// turn.
func (c *coordinator) overrideTaskAgent(ctx context.Context, access string, sel config.SelectedModel) (SessionAgent, string) {
	entry, err := c.overrideModel(sel)
	if err != nil {
		return nil, "failed to set up the requested model: " + err.Error()
	}
	agentCfg, canWrite := c.taskAgentConfig(access)
	// A group of its own: this build joins no one, and is fully set up
	// before the dispatch registers the subagent.
	var ready errgroup.Group
	agent, err := c.buildTaskAgent(ctx, &ready, agentCfg, canWrite, func() ModelSet {
		return ModelSet{Large: entry.current(c), Small: c.subAgentModels().Small}
	})
	if err != nil {
		return nil, "failed to start a subagent on the requested model: " + err.Error()
	}
	if err := ready.Wait(); err != nil {
		return nil, "failed to start a subagent on the requested model: " + err.Error()
	}
	return agent, ""
}

// overrideRecord is what a transcript records of an override: only the
// settings the call asked for, with the provider it resolved to.
func overrideRecord(params AgentDispatchParams, sel config.SelectedModel) (model, provider, reasoning string) {
	if strings.TrimSpace(params.Model) != "" {
		model, provider = sel.Model, sel.Provider
	}
	return model, provider, strings.TrimSpace(params.Reasoning)
}

// sendOnNewModel moves an idle subagent to another model and reopens it with
// the message, keeping its session.
func (c *coordinator) sendOnNewModel(ctx context.Context, entry *dispatchedAgent, params AgentSendParams, parentModel Model) fantasy.ToolResponse {
	if entry.stopping() {
		entry.awaitSettled(10 * time.Second)
	}
	running := fmt.Sprintf("subagent %q is running; stop it with agent_stop before moving it to another model", entry.Label)
	if entry.running() {
		return fantasy.NewTextErrorResponse(running)
	}
	override := AgentDispatchParams{Model: params.Model, Provider: params.Provider, Reasoning: params.Reasoning}
	agent, sel, _, refusal := c.overrideDispatchAgent(ctx, entry.Access, override, parentModel)
	if refusal != "" {
		return fantasy.NewTextErrorResponse(refusal)
	}
	if !c.reopen(entry, params.Message, agent) {
		return fantasy.NewTextErrorResponse(running)
	}
	meta := AgentSendResponseMetadata{Label: entry.Label, Reopened: true, RunModel: modelDisplayName(agent.Model())}
	meta.Model, meta.Provider, meta.Reasoning = overrideRecord(override, sel)
	return fantasy.WithResponseMetadata(fantasy.NewTextResponse(fmt.Sprintf(
		"Moved %q to %s and delivered the message. It will answer in a new report.",
		entry.Label, meta.RunModel,
	)), meta)
}

// roleAccess picks the subagent variant for a role. A role that allows no
// file-editing tool runs read-only; access may narrow a write role to read
// but never widen a read-only one.
func roleAccess(role *roles.Role, requested string) (string, string) {
	readOnly := role.ReadOnly(config.SubAgentWriteTools())
	switch requested {
	case "":
		if readOnly {
			return accessRead, ""
		}
		return accessWrite, ""
	case accessRead:
		return accessRead, ""
	case accessWrite:
		if readOnly {
			return "", fmt.Sprintf("role %q is read-only; access %q is not allowed", role.Name, requested)
		}
		return accessWrite, ""
	default:
		return "", fmt.Sprintf("unknown access %q", requested)
	}
}

// roleTaskAgent builds a task subagent for a role. It runs on one of the
// two existing variants, so its tools block and system prompt match an
// agent without a role; the role's gate and block ride on top. The model
// is the dispatch's override if it has one, then the role's, then the
// shared subagent model.
func (c *coordinator) roleTaskAgent(ctx context.Context, role *roles.Role, access string, params AgentDispatchParams, parentModel Model) (SessionAgent, config.SelectedModel, bool, string) {
	access, refusal := roleAccess(role, access)
	if refusal != "" {
		return nil, config.SelectedModel{}, false, refusal
	}
	explicit := strings.TrimSpace(params.Model) != "" || strings.TrimSpace(params.Provider) != "" || strings.TrimSpace(params.Reasoning) != ""
	if !explicit && role.PinsModel() {
		params = roleModelParams(role)
	}
	sel, hasOverride, refusal := c.resolveModelOverride(params, parentModel)
	if refusal != "" {
		return nil, config.SelectedModel{}, false, fmt.Sprintf("role %q: %s", role.Name, refusal)
	}
	var modelsFn func() ModelSet
	if hasOverride {
		entry, err := c.overrideModel(sel)
		if err != nil {
			return nil, sel, true, "failed to set up the requested model: " + err.Error()
		}
		modelsFn = func() ModelSet {
			return ModelSet{Large: entry.current(c), Small: c.subAgentModels().Small}
		}
	}
	agentCfg, canWrite := c.taskAgentConfig(access)
	agentCfg.Role = role
	var ready errgroup.Group
	agent, err := c.buildTaskAgent(ctx, &ready, agentCfg, canWrite, modelsFn)
	if err != nil {
		return nil, sel, hasOverride, "failed to start a subagent for role " + role.Name + ": " + err.Error()
	}
	if err := ready.Wait(); err != nil {
		return nil, sel, hasOverride, "failed to start a subagent for role " + role.Name + ": " + err.Error()
	}
	return agent, sel, hasOverride, ""
}

// planModeRoleRefusal refuses a role that would write from a read-only
// parent, as resolveSubAgent refuses access=write there.
func planModeRoleRefusal(readOnly bool, role *roles.Role, access string) string {
	if !readOnly {
		return ""
	}
	if want, _ := roleAccess(role, access); want == accessWrite {
		return fmt.Sprintf(`plan mode subagents are read-only; role %q can write, so pass access "read" or pick a read-only role`, role.Name)
	}
	return ""
}
