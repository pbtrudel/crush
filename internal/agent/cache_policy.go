package agent

import (
	"strings"

	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openrouter"
	"charm.land/fantasy/providers/vercel"
)

// cachePolicy says where explicit prompt-cache breakpoints belong for one
// model: on the tool definitions, on the last system message, and on how
// many trailing messages. A zero policy sends no markers at all, which is
// right for providers that cache the prefix automatically and for gateways
// whose upstream ignores or misreads the markers.
type cachePolicy struct {
	Tools  bool
	System bool
	Tail   int
}

var (
	// cachePolicyFull is the layout Anthropic's own clients use and the
	// one every Anthropic model keeps: the tools block, the system
	// prompt, and the last two messages.
	cachePolicyFull = cachePolicy{Tools: true, System: true, Tail: 2}
	// cachePolicyGemini covers every Gemini route. Gemini honours only
	// the last breakpoint, so a tail marker writes a fresh cache on every
	// step; one marker on the system prompt stays put.
	cachePolicyGemini = cachePolicy{System: true}
	// cachePolicyQwen covers Qwen models reached through a gateway. Qwen
	// ignores markers on tool definitions and caches the tools together
	// with the system prompt, so the system marker covers both and a
	// single tail marker still lets a turn extend the cache.
	cachePolicyQwen = cachePolicy{System: true, Tail: 1}
	// cachePolicyNone sends no breakpoints.
	cachePolicyNone = cachePolicy{}
)

// cachePolicyFor returns the breakpoint layout for a model served by
// providerType, whose value is the provider name the request travels
// through (see servingProvider). Gateway model IDs keep their upstream
// prefix, so "anthropic/claude-sonnet-4.5" through OpenRouter or Vercel
// follows the Anthropic rule while any other upstream there gets nothing.
func cachePolicyFor(providerType, modelID string) cachePolicy {
	switch providerType {
	case anthropic.Name, bedrock.Name:
		return cachePolicyFull
	case google.Name:
		if strings.Contains(modelID, "claude") {
			return cachePolicyFull
		}
		return cachePolicyGemini
	case openrouter.Name, vercel.Name:
		return gatewayCachePolicy(modelID)
	}
	return cachePolicyNone
}

// gatewayCachePolicy maps a gateway model ID onto its upstream's rule.
func gatewayCachePolicy(modelID string) cachePolicy {
	switch {
	case strings.HasPrefix(modelID, "anthropic/"):
		return cachePolicyFull
	case strings.HasPrefix(modelID, "qwen/"), strings.HasPrefix(modelID, "alibaba/qwen"):
		return cachePolicyQwen
	case strings.Contains(modelID, "gemini"):
		return cachePolicyGemini
	}
	return cachePolicyNone
}

// cachePolicyForModel resolves the policy of a built model, preferring the
// provider that actually serves the request and falling back to the
// configured provider id for models assembled outside the coordinator.
func cachePolicyForModel(model Model) cachePolicy {
	providerType := model.ServingProvider
	if providerType == "" {
		providerType = model.ModelCfg.Provider
	}
	return cachePolicyFor(providerType, model.ModelCfg.Model)
}
