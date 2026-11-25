package webtest

import (
	"context"
	"errors"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/pinentry"
	"github.com/charmbracelet/crush/internal/sshaskpass"
)

// M4Seams are the gateway seams the M4 scenario scripts. webfake copies
// them into web.Options; webtest cannot import web, whose tests import
// webtest.
type M4Seams struct {
	KnownProviders func(*config.Config) ([]catwalk.Provider, error)
	TestAPIKey     func(context.Context, catwalk.Provider, string, config.VariableResolver) error
	SignInFlow     func(string) (signin.Flow, error)

	// DockerMCPAvailable answers whether Docker MCP can be enabled. SeedM4
	// leaves it nil; SeedM4b fills it.
	DockerMCPAvailable func() bool
	// Automation answers a /goal or /loop command the way the terminal's
	// automation does. SeedM4 leaves it nil; SeedM4b fills it.
	Automation func(ctx context.Context, sessionID, name, args string) (string, error)
}

// m4Providers is the catalog the M4 picker lists: the OAuth providers plus
// Anthropic, whose one model the model selector shows.
func m4Providers() []catwalk.Provider {
	return []catwalk.Provider{
		{ID: "hyper", Name: "Hyper"},
		{ID: catwalk.InferenceProviderCopilot, Name: "GitHub Copilot"},
		{ID: catwalk.InferenceProviderOpenAI, Name: "OpenAI"},
		{ID: catwalk.InferenceProviderXAI, Name: "xAI"},
		{
			ID: catwalk.InferenceProviderAnthropic, Name: "Anthropic",
			Models: []catwalk.Model{{ID: "claude-sonnet", Name: "Claude Sonnet"}},
		},
	}
}

// SeedM4 adds what the M4 browser surfaces show: the M3 seeds, a sign-in
// provider catalog, an API key check and the scripted OAuth flows. done
// releases a flow still waiting when webfake shuts down, and clock dates
// the seeded sessions.
func SeedM4(f *Fake, done <-chan struct{}, clock time.Time) (M4Seams, error) {
	if err := SeedM3(f, clock); err != nil {
		return M4Seams{}, err
	}
	known := m4Providers()
	f.SetCatalog(known)
	return M4Seams{
		KnownProviders: func(*config.Config) ([]catwalk.Provider, error) { return known, nil },
		TestAPIKey: func(_ context.Context, _ catwalk.Provider, key string, _ config.VariableResolver) error {
			if key == "sk-valid" {
				return nil
			}
			return errors.New("the provider did not accept this key")
		},
		SignInFlow: func(providerID string) (signin.Flow, error) {
			return m4Flow(providerID, done), nil
		},
	}, nil
}

// m4Flow scripts one provider's sign-in. A device flow blocks until it is
// cancelled or completed by WEBFAKE_SIGNIN_MS; a browser flow waits for a
// pasted code or a cancel.
func m4Flow(providerID string, done <-chan struct{}) *FakeFlow {
	flow := NewFakeFlowFor(done)
	switch providerID {
	case string(catwalk.InferenceProviderCopilot):
		flow.FlowName = "GitHub Copilot"
		flow.Challenge = signin.Challenge{
			UserCode: "WDJB-MJHT", VerificationURL: "https://github.com/login/device", ExpiresIn: 900,
		}
		flow.Token = &oauth.Token{AccessToken: "webfake-copilot-token"}
	case string(catwalk.InferenceProviderOpenAI):
		flow.FlowName = "ChatGPT"
		flow.Challenge = signin.Challenge{VerificationURL: "http://127.0.0.1:9/start"}
		flow.Token = &oauth.Token{AccessToken: "webfake-openai-token"}
	case string(catwalk.InferenceProviderXAI):
		flow.FlowName = "Grok"
		flow.HasCodeEntry = true
		flow.CodeOK = "good-code"
		flow.Challenge = signin.Challenge{VerificationURL: "http://127.0.0.1:9/start"}
		flow.Token = &oauth.Token{AccessToken: "webfake-xai-token"}
	case "hyper":
		flow.FlowName = "Hyper"
		flow.Challenge = signin.Challenge{
			UserCode: "HYPR-CODE", VerificationURL: "https://hyper.charm.land/device", ExpiresIn: 900,
		}
		flow.Token = &oauth.Token{AccessToken: "webfake-hyper-token"}
	default:
		flow.FlowName = "Fake provider"
	}
	return flow
}

// SecurePromptFromEnv builds the prompt event a WEBFAKE_SECURE_PROMPT value
// names, so a Playwright run can exercise one dialog kind at a time.
func SecurePromptFromEnv(kind string) (any, bool) {
	switch kind {
	case "pinentry":
		return pinentry.PromptRequest{
			ID: "secure-pinentry", Kind: pinentry.KindPassphrase,
			Prompt: "Please enter the passphrase for your GPG key:",
			Key:    pinentry.Key{Fingerprint: "ABCDEF0123456789", Holder: "Dev <dev@example.com>"},
		}, true
	case "pin":
		return pinentry.PromptRequest{
			ID: "secure-pin", Kind: pinentry.KindPIN,
			Prompt: "Please enter the PIN for your security key:",
			Key:    pinentry.Key{CardNumber: "0005 12345678"},
		}, true
	case "ssh":
		return sshaskpass.PromptRequest{
			ID: "secure-ssh", Kind: sshaskpass.KindPassword,
			Prompt: "git@github.com's password: ",
		}, true
	case "confirm":
		return sshaskpass.PromptRequest{
			ID: "secure-confirm", Kind: sshaskpass.KindConfirm,
			Prompt: "The authenticity of host 'github.com' can't be established. Are you sure you want to continue connecting (yes/no/[fingerprint])?",
		}, true
	case "touch":
		return sshaskpass.PromptRequest{
			ID: "secure-touch", Kind: sshaskpass.KindTouch,
			Prompt: "Confirm user presence for key ED25519-SK SHA256:abc",
		}, true
	}
	return nil, false
}
