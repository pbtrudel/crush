package workspace

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/charmbracelet/crush/internal/config"
)

// Errors a model switch can answer with. Frontends turn them into their
// own messages, so they stay free of user-facing wording beyond the
// reason itself.
var (
	// ErrAgentBusy means the agent is working and a switch would race the
	// run.
	ErrAgentBusy = errors.New("the agent is busy")
	// ErrNoConfig means the workspace has no configuration to switch.
	ErrNoConfig = errors.New("configuration not found")
	// ErrModelUnavailable means the model is not one the current
	// credentials can serve, so choosing it would need a sign-in first.
	ErrModelUnavailable = errors.New("model is not available without signing in")
)

// ModelChoice is one model the user can switch to without signing in.
type ModelChoice struct {
	Provider      string   `json:"provider"`
	ProviderName  string   `json:"provider_name"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ContextWindow int64    `json:"context_window"`
	CanReason     bool     `json:"can_reason"`
	EffortLevels  []string `json:"effort_levels,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
}

// ModelChoices lists the models the configured, enabled providers can
// serve right now, grouped by provider id and in config order within each
// provider. Providers the model dialog hides (unconfigured or disabled)
// are left out, so a choice always skips the sign-in flow.
func ModelChoices(cfg *config.Config) []ModelChoice {
	if cfg == nil || cfg.Providers == nil {
		return nil
	}

	ids := make([]string, 0, cfg.Providers.Len())
	for id := range cfg.Providers.Seq2() {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var choices []ModelChoice
	for _, id := range ids {
		if !cfg.ProviderListed(id, false) {
			continue
		}
		providerConfig, ok := cfg.Providers.Get(id)
		if !ok {
			continue
		}
		for _, model := range providerConfig.Models {
			if !cfg.IsModelAvailable(id, model.ID) {
				continue
			}
			choices = append(choices, ModelChoice{
				Provider:      id,
				ProviderName:  cmp.Or(providerConfig.Name, id),
				ID:            model.ID,
				Name:          cmp.Or(model.Name, model.ID),
				ContextWindow: model.ContextWindow,
				CanReason:     model.CanReason(),
				EffortLevels:  model.ReasoningEffortLevels(),
				DefaultEffort: model.Reasoning.DefaultEffortLevel,
			})
		}
	}
	return choices
}

// SelectModel switches the model of the given type after checking that
// the agent is idle and that the credentials can serve it, then rebuilds
// the agent's models. Frontends with their own pre-checks (the TUI keeps
// them, sign-in included) call ApplyModel directly.
func SelectModel(ctx context.Context, ws Workspace, typ config.SelectedModelType, m config.SelectedModel) error {
	cfg := ws.Config()
	if cfg == nil {
		return ErrNoConfig
	}
	if ws.AgentIsBusy() {
		return ErrAgentBusy
	}
	if !slices.ContainsFunc(ModelChoices(cfg), func(choice ModelChoice) bool {
		return choice.Provider == m.Provider && choice.ID == m.Model
	}) {
		return ErrModelUnavailable
	}
	return ApplyModel(ctx, ws, typ, m)
}

// SetReasoningEffort sets the coder model's reasoning effort and rebuilds
// the agent's models. An effort the model does not offer is refused.
func SetReasoningEffort(ctx context.Context, ws Workspace, effort string) error {
	cfg := ws.Config()
	if cfg == nil {
		return ErrNoConfig
	}
	if ws.AgentIsBusy() {
		return ErrAgentBusy
	}

	typ, current, err := coderModel(cfg)
	if err != nil {
		return err
	}
	model := cfg.GetModel(current.Provider, current.Model)
	if model == nil || !slices.Contains(model.ReasoningEffortLevels(), effort) {
		return fmt.Errorf("unknown reasoning effort %q", effort)
	}
	current.ReasoningEffort = effort
	return ApplyModel(ctx, ws, typ, current)
}

// SetThinking turns the coder model's thinking mode on or off and
// rebuilds the agent's models.
func SetThinking(ctx context.Context, ws Workspace, on bool) error {
	if ws.Config() == nil {
		return ErrNoConfig
	}
	if ws.AgentIsBusy() {
		return ErrAgentBusy
	}
	return ApplyThinking(ctx, ws, on)
}

// ApplyModel records the model choice and rebuilds the agent's models. It
// performs no busy or availability checks, which lets a frontend that
// already made them switch both without repeating them.
func ApplyModel(ctx context.Context, ws Workspace, typ config.SelectedModelType, m config.SelectedModel) error {
	if err := ApplyPreferredModel(ws, typ, m); err != nil {
		return err
	}
	return ReloadAgentModel(ctx, ws)
}

// ApplyPreferredModel records a model choice in the config without
// touching the running agent. Frontends that interleave their own work
// between the write and the rebuild use it with ReloadAgentModel; every
// other caller uses ApplyModel.
func ApplyPreferredModel(ws Workspace, typ config.SelectedModelType, m config.SelectedModel) error {
	return ws.UpdatePreferredModel(config.ScopeGlobal, typ, m)
}

// ReloadAgentModel rebuilds the agent's models from the current config.
func ReloadAgentModel(ctx context.Context, ws Workspace) error {
	return ws.UpdateAgentModel(ctx)
}

// ApplyThinking turns the coder model's thinking mode on or off and
// rebuilds the agent's models, without a busy check so the TUI keeps its
// long-standing unguarded toggle.
func ApplyThinking(ctx context.Context, ws Workspace, on bool) error {
	typ, model, err := coderModel(ws.Config())
	if err != nil {
		return err
	}
	model.Think = on
	return ApplyModel(ctx, ws, typ, model)
}

// coderModel returns the coder agent's model type and its selected model.
func coderModel(cfg *config.Config) (config.SelectedModelType, config.SelectedModel, error) {
	if cfg == nil {
		return "", config.SelectedModel{}, ErrNoConfig
	}
	agentCfg, ok := cfg.Agents[config.AgentCoder]
	if !ok {
		return "", config.SelectedModel{}, errors.New("agent configuration not found")
	}
	return agentCfg.Model, cfg.Models[agentCfg.Model], nil
}
