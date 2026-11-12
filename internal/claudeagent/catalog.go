package claudeagent

import (
	"strings"

	"charm.land/catwalk/v3/pkg/catwalk"
)

// defaultContextWindow is used when neither the CLI nor the Anthropic catalog
// reports a model's window.
const defaultContextWindow = 200_000

// longContextWindow is the window of the 1M-context model variants.
const longContextWindow = 1_000_000

// ProviderEntry builds the catalog entry Crush shows in its model picker.
//
// known looks a model up in the Anthropic catalog by its canonical name, so
// the Claude Agent models get the same versioned names, context windows, and
// prices. It is nil when the Anthropic catalog is not available.
func ProviderEntry(catalog *Catalog, known func(id string) (catwalk.Model, bool)) catwalk.Provider {
	provider := catwalk.Provider{
		ID:   catwalk.InferenceProvider(Name),
		Name: DisplayName,
	}
	if catalog == nil {
		return provider
	}
	for _, model := range catalog.Models {
		match := anthropicModel(model, known)
		provider.Models = append(provider.Models, catwalk.Model{
			ID:            model.ID,
			Name:          modelName(model, match),
			ContextWindow: contextWindow(model, match),
			Reasoning:     reasoning(model),
			Capabilities:  catwalk.Capabilities{Vision: true},
			Pricing:       match.Pricing,
		})
	}
	if len(provider.Models) > 0 {
		provider.DefaultLargeModelID = provider.Models[0].ID
		for _, model := range provider.Models {
			if model.ID == "sonnet" {
				provider.DefaultSmallModelID = model.ID
				break
			}
		}
	}
	return provider
}

// modelName takes the version from the Anthropic catalog, since the CLI
// reports a bare family name such as "Opus". The "Claude " prefix is dropped
// because the provider is already labeled Claude Agent.
func modelName(model CatalogModel, match catwalk.Model) string {
	if match.Name != "" {
		return strings.TrimPrefix(match.Name, "Claude ")
	}
	return model.Name
}

// contextWindow prefers what the CLI reports. The 1M suffix is checked before
// the Anthropic catalog because the suffixed variant matches the base model's
// entry, which can list the smaller window.
func contextWindow(model CatalogModel, match catwalk.Model) int64 {
	if model.ContextWindow > 0 {
		return model.ContextWindow
	}
	if strings.HasSuffix(model.ID, "[1m]") || strings.HasSuffix(model.Resolved, "[1m]") {
		return longContextWindow
	}
	if match.ContextWindow > 0 {
		return match.ContextWindow
	}
	return defaultContextWindow
}

func reasoning(model CatalogModel) catwalk.Reasoning {
	if len(model.Efforts) == 0 {
		return catwalk.Reasoning{Thinking: catwalk.ThinkingAlways}
	}
	return catwalk.Reasoning{
		Thinking:           catwalk.ThinkingToggleable,
		EffortLevels:       catwalk.NewEffortLevels(model.Efforts...),
		DefaultEffortLevel: model.Efforts[0],
	}
}

// anthropicModel looks the model up in the Anthropic catalog by its canonical
// name. The 1M-context variant shares the base model's entry, and Bedrock and
// Vertex list the same model under a prefix or a version suffix.
func anthropicModel(model CatalogModel, known func(string) (catwalk.Model, bool)) catwalk.Model {
	if known == nil {
		return catwalk.Model{}
	}
	name := model.Resolved
	if name == "" {
		name = model.ID
	}
	candidates := []string{
		name,
		trimContextSuffix(name),
		trimProviderPrefix(name),
		trimContextSuffix(trimProviderPrefix(name)),
	}
	for _, candidate := range candidates {
		if match, ok := known(candidate); ok {
			return match
		}
	}
	return catwalk.Model{}
}

func trimContextSuffix(name string) string {
	if len(name) > 4 && name[len(name)-4:] == "[1m]" {
		return name[:len(name)-4]
	}
	return name
}

func trimProviderPrefix(name string) string {
	const prefix = "anthropic."
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		return name[len(prefix):]
	}
	return name
}
