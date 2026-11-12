package config

import (
	"charm.land/catwalk/v3/pkg/catwalk"
)

// Copilot Fusion model IDs. They are catalog-only entries: the real model is
// chosen per turn by the Copilot router, so each ID stands for one of the
// three Fusion tiers rather than for a concrete model.
const (
	CopilotFusionStandardModel = "fusion-standard"
	CopilotFusionHighModel     = "fusion-high"
	CopilotFusionMaxModel      = "fusion-max"
)

// copilotFusionTiers maps each Fusion model ID to the tier the router expects.
var copilotFusionTiers = map[string]string{
	CopilotFusionStandardModel: "standard",
	CopilotFusionHighModel:     "high",
	CopilotFusionMaxModel:      "max",
}

// copilotFusionWindow is the prompt budget advertised for the Fusion entries.
// The router can pick any Copilot model for a turn, and the smallest default
// tier among them is 200k tokens, so the entries stay within that bound.
const copilotFusionWindow = 200_000

// IsCopilotFusionModel reports whether id is one of the Fusion tier entries.
func IsCopilotFusionModel(id string) bool {
	_, ok := copilotFusionTiers[id]
	return ok
}

// CopilotFusionTier returns the router tier for a Fusion model ID.
func CopilotFusionTier(id string) (string, bool) {
	tier, ok := copilotFusionTiers[id]
	return tier, ok
}

// withCopilotFusionModels appends the three Fusion tier entries to the Copilot
// catalog. A provider that already lists an entry keeps its own definition.
func withCopilotFusionModels(models []catwalk.Model) []catwalk.Model {
	existing := make(map[string]bool, len(models))
	for _, model := range models {
		existing[model.ID] = true
	}
	out := make([]catwalk.Model, 0, len(models)+len(copilotFusionTiers))
	out = append(out, models...)
	for _, model := range []catwalk.Model{
		fusionModel(CopilotFusionStandardModel, "Copilot Fusion (Standard)"),
		fusionModel(CopilotFusionHighModel, "Copilot Fusion (High)"),
		fusionModel(CopilotFusionMaxModel, "Copilot Fusion (Max)"),
	} {
		if existing[model.ID] {
			continue
		}
		out = append(out, model)
	}
	return out
}

func fusionModel(id, name string) catwalk.Model {
	return catwalk.Model{
		ID:               id,
		Name:             name,
		ContextWindow:    copilotFusionWindow,
		DefaultMaxTokens: 32_000,
		Reasoning:        catwalk.Reasoning{Thinking: catwalk.ThinkingNever},
		Capabilities:     catwalk.Capabilities{Vision: true},
	}
}
