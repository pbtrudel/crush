package workspace

import (
	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
)

// AuthChoice is the sign-in a provider gets when the user picks it.
type AuthChoice int

const (
	AuthChoiceAPIKey AuthChoice = iota // ask for an API key
	AuthChoiceOAuth                    // run the provider's OAuth flow
	AuthChoicePick                     // let the user pick OAuth or a key
)

// DefaultAuthChoice is the TUI's sign-in choice for a provider. placeholder
// marks the OpenAI and xAI sign-in entry, which names no model.
func DefaultAuthChoice(providerID string, pc config.ProviderConfig, hasAPIKey, placeholder bool) AuthChoice {
	switch providerID {
	case "hyper", string(catwalk.InferenceProviderCopilot):
		return AuthChoiceOAuth
	case string(catwalk.InferenceProviderOpenAI), string(catwalk.InferenceProviderXAI):
		switch {
		case placeholder || pc.OAuthToken != nil:
			return AuthChoiceOAuth
		case !hasAPIKey:
			return AuthChoicePick
		}
	}
	return AuthChoiceAPIKey
}

// SignInTarget is one provider the browser can sign in to.
type SignInTarget struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Configured bool     `json:"configured"`
	Methods    []string `json:"methods"`              // "oauth", "api_key"; the first is the default
	OAuthKind  string   `json:"oauth_kind,omitempty"` // "device" or "browser"
}

// SignInTargets lists every known provider the picker can list, in the
// known order (Hyper first), with how it signs in.
func SignInTargets(cfg *config.Config, known []catwalk.Provider) []SignInTarget {
	targets := make([]SignInTarget, 0, len(known))
	for _, provider := range known {
		id := string(provider.ID)
		var providerCfg config.ProviderConfig
		configured := false
		if cfg != nil && cfg.Providers != nil {
			// A config without a provider map is a reload in flight, not a
			// workspace that lists nothing: every known provider stays
			// signable, as it does before the first config loads.
			if !cfg.ProviderListed(id, true) {
				continue
			}
			providerCfg, configured = cfg.Providers.Get(id)
		}
		methods, kind := signInMethods(id)
		targets = append(targets, SignInTarget{
			ID:         id,
			Name:       signInName(id, providerCfg, provider, configured),
			Configured: configured,
			Methods:    methods,
			OAuthKind:  kind,
		})
	}
	return targets
}

// signInMethods returns the ways a provider signs in, the default first,
// and the OAuth challenge kind when it has one.
func signInMethods(id string) ([]string, string) {
	switch id {
	case "hyper", string(catwalk.InferenceProviderCopilot):
		return []string{"oauth"}, "device"
	case string(catwalk.InferenceProviderOpenAI), string(catwalk.InferenceProviderXAI):
		return []string{"oauth", "api_key"}, "browser"
	}
	return []string{"api_key"}, ""
}

// signInName prefers the configured provider's name, then the catalog's,
// then the id, so a custom provider keeps the name the user gave it.
func signInName(id string, providerCfg config.ProviderConfig, provider catwalk.Provider, configured bool) string {
	if configured && providerCfg.Name != "" {
		return providerCfg.Name
	}
	if provider.Name != "" {
		return provider.Name
	}
	return id
}
