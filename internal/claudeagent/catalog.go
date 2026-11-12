package claudeagent

import (
	"charm.land/catwalk/v3/pkg/catwalk"
)

// defaultContextWindow is used when the CLI has not reported a model's window.
const defaultContextWindow = 200_000

// ProviderEntry builds the catalog entry Crush shows in its model picker.
//
// prices resolves a model's Anthropic pricing by its canonical name. It is nil
// for a subscription login, which covers usage and therefore costs nothing at
// the point of use.
func ProviderEntry(catalog *Catalog, prices func(resolved string) (catwalk.Model, bool)) catwalk.Provider {
	provider := catwalk.Provider{
		ID:   catwalk.InferenceProvider(Name),
		Name: DisplayName,
	}
	if catalog == nil {
		return provider
	}
	for _, model := range catalog.Models {
		provider.Models = append(provider.Models, catwalk.Model{
			ID:            model.ID,
			Name:          model.Name,
			ContextWindow: contextWindow(model),
			Reasoning:     reasoning(model),
			Capabilities:  catwalk.Capabilities{Vision: true},
			Pricing:       pricing(model, prices),
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

func contextWindow(model CatalogModel) int64 {
	if model.ContextWindow > 0 {
		return model.ContextWindow
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

// pricing looks up the model's Anthropic prices by its canonical name. The
// 1M-context variant shares the base model's tiers, and Bedrock and Vertex list
// the same model under a prefix or a version suffix.
func pricing(model CatalogModel, prices func(string) (catwalk.Model, bool)) catwalk.Pricing {
	if prices == nil {
		return catwalk.Pricing{}
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
		if priced, ok := prices(candidate); ok {
			return priced.Pricing
		}
	}
	return catwalk.Pricing{}
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
