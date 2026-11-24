package webtest

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/sshaskpass"
	"github.com/stretchr/testify/require"
)

func TestSeedM4(t *testing.T) {
	t.Parallel()
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	seams := SeedM4(f, done)

	// The M3 seeds come along with the M4 data.
	cfg := f.Config()
	require.NotNil(t, cfg)
	require.Equal(t, config.SelectedModel{Provider: "p1", Model: "m1"}, cfg.Models[config.SelectedModelTypeMain])

	known, err := seams.KnownProviders(cfg)
	require.NoError(t, err)
	ids := make([]string, 0, len(known))
	for _, p := range known {
		ids = append(ids, string(p.ID))
	}
	require.Equal(t, []string{"hyper", "copilot", "openai", "xai", "anthropic"}, ids)
	require.Equal(t, []catwalk.Model{{ID: "claude-sonnet", Name: "Claude Sonnet"}}, known[4].Models)

	flow, err := seams.SignInFlow("copilot")
	require.NoError(t, err)
	copilot, ok := flow.(*FakeFlow)
	require.True(t, ok)
	require.Equal(t, "WDJB-MJHT", copilot.Challenge.UserCode)
	require.Equal(t, "https://github.com/login/device", copilot.Challenge.VerificationURL)
	require.Equal(t, 900, copilot.Challenge.ExpiresIn)

	require.Error(t, seams.TestAPIKey(t.Context(), catwalk.Provider{}, "sk-wrong", config.IdentityResolver()))
	require.NoError(t, seams.TestAPIKey(t.Context(), catwalk.Provider{}, "sk-valid", config.IdentityResolver()))

	confirm, ok := SecurePromptFromEnv("confirm")
	require.True(t, ok)
	confirmReq, ok := confirm.(sshaskpass.PromptRequest)
	require.True(t, ok)
	require.Equal(t, sshaskpass.KindConfirm, confirmReq.Kind)

	touch, ok := SecurePromptFromEnv("touch")
	require.True(t, ok)
	touchReq, ok := touch.(sshaskpass.PromptRequest)
	require.True(t, ok)
	require.Equal(t, sshaskpass.KindTouch, sshaskpass.ParsePrompt(touchReq.Prompt).Kind)
	require.Equal(t, "Tap your security key to continue.", sshaskpass.Describe(touchReq).Message)
}

func TestSecurePromptFromEnv(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"pinentry", "pin", "ssh", "confirm", "touch"} {
		req, ok := SecurePromptFromEnv(kind)
		require.True(t, ok, kind)
		require.NotNil(t, req, kind)
	}
	_, ok := SecurePromptFromEnv("bogus")
	require.False(t, ok)
}
