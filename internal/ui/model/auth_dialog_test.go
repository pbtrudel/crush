package model

import (
	"sync/atomic"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/stretchr/testify/require"
)

// countingResolver records how often a provider API key template is
// resolved, so a test can prove a sign-in dialog never runs $(...).
type countingResolver struct {
	calls atomic.Int64
}

func (r *countingResolver) ResolveValue(value string) (string, error) {
	r.calls.Add(1)
	return value, nil
}

// authResolverWorkspace gives the UI a workspace whose resolver counts.
type authResolverWorkspace struct {
	*testWorkspace
	resolver config.VariableResolver
}

func (w *authResolverWorkspace) Resolver() config.VariableResolver { return w.resolver }

func newAuthDialogUI(t *testing.T, providerID, apiKey string) (*UI, *countingResolver) {
	t.Helper()
	cfg := &config.Config{
		Options: &config.Options{},
		Providers: csync.NewMapFrom(map[string]config.ProviderConfig{
			providerID: {ID: providerID, Name: providerID, APIKey: apiKey},
		}),
	}
	resolver := &countingResolver{}
	ws := &authResolverWorkspace{testWorkspace: &testWorkspace{cfg: cfg}, resolver: resolver}
	u := newTestUI()
	u.com = common.DefaultCommon(ws)
	u.dialog = dialog.NewOverlay()
	return u, resolver
}

// TestAuthDialogDoesNotResolveAKeyForOtherProviders pins that opening a
// sign-in dialog for a provider whose choice does not read the key never
// shell-expands a $(...) key template.
func TestAuthDialogDoesNotResolveAKeyForOtherProviders(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"anthropic", "hyper", string(catwalk.InferenceProviderCopilot)} {
		u, resolver := newAuthDialogUI(t, id, "$(touch /tmp/crush-auth-dialog-pwned)")
		u.openAuthenticationDialog(
			catwalk.Provider{ID: catwalk.InferenceProvider(id), Name: id},
			config.SelectedModel{Provider: id, Model: "m"},
			config.SelectedModelTypeMain,
		)
		require.Zero(t, resolver.calls.Load(), "%s must not resolve the key template", id)
	}
}

// TestAuthDialogResolvesTheKeyForOpenAIAndXAI pins that the providers whose
// choice reads it still resolve it, as the dialog did before the shared
// helper existed.
func TestAuthDialogResolvesTheKeyForOpenAIAndXAI(t *testing.T) {
	t.Parallel()
	for _, id := range []string{string(catwalk.InferenceProviderOpenAI), string(catwalk.InferenceProviderXAI)} {
		u, resolver := newAuthDialogUI(t, id, "$(echo key)")
		u.openAuthenticationDialog(
			catwalk.Provider{ID: catwalk.InferenceProvider(id), Name: id},
			config.SelectedModel{Provider: id, Model: "m"},
			config.SelectedModelTypeMain,
		)
		require.EqualValues(t, 1, resolver.calls.Load(), "%s reads the key to pick its dialog", id)
	}
}
