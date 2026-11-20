package config

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

// TestLoadKeepsCatalogModelType covers a model the user lists in the provider
// configuration without a type. The user entry replaces the catalog entry, so
// without a correction the model loses the type the catalog gave it and a
// Responses model silently falls back to the provider's chat-completions
// endpoint. A type the user states explicitly still wins.
func TestLoadKeepsCatalogModelType(t *testing.T) {
	// Not parallel: swaps package-level provider state and sets
	// environment variables.
	workDir, dataDir := isolateLoadEnv(t)
	newCatwalkStub(t, http.StatusOK, []catwalk.Provider{{
		Name:                "GitHub Copilot",
		ID:                  catwalk.InferenceProviderCopilot,
		Type:                catwalk.TypeCompletions,
		DefaultLargeModelID: "gpt-5.6-sol",
		DefaultSmallModelID: "gpt-5.6-sol",
		Models: []catwalk.Model{
			{ID: "gpt-5.6-sol", Name: "GPT 5.6 Sol", Type: catwalk.TypeResponses},
			{ID: "gpt-5.6-luna", Name: "GPT 5.6 Luna", Type: catwalk.TypeResponses},
		},
	}})
	resetProviderState()
	t.Cleanup(resetProviderState)
	withoutClaudeAgent(t)

	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "crush.json"),
		[]byte(`{"providers":{"copilot":{"api_key":"ghu_test","models":[
			{"id":"gpt-5.6-sol","name":"My Sol"},
			{"id":"gpt-5.6-luna","name":"My Luna","type":"completions"}
		]}}}`),
		0o600,
	))

	store, err := Load(t.Context(), workDir, dataDir, false)
	require.NoError(t, err)

	provider, ok := store.Config().Providers.Get(string(catwalk.InferenceProviderCopilot))
	require.True(t, ok)

	models := make(map[string]catwalk.Model, len(provider.Models))
	for _, model := range provider.Models {
		models[model.ID] = model
	}

	sol, ok := models["gpt-5.6-sol"]
	require.True(t, ok)
	require.Equal(t, catwalk.TypeResponses, sol.Type, "a listed model without a type keeps the catalog type")
	require.Equal(t, "My Sol", sol.Name, "the user entry still replaces the catalog entry")

	luna, ok := models["gpt-5.6-luna"]
	require.True(t, ok)
	require.Equal(t, catwalk.TypeCompletions, luna.Type, "an explicit type in the user entry wins")
}
