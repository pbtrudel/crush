package agent

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
)

// titleTestModel builds a Model for the title helpers.
func titleTestModel(provider, id string, thinking catwalk.Thinking, maxTokens int64) Model {
	return Model{
		ModelCfg: config.SelectedModel{Provider: provider, Model: id},
		CatwalkCfg: catwalk.Model{
			ID:               id,
			DefaultMaxTokens: maxTokens,
			Reasoning:        catwalk.Reasoning{Thinking: thinking},
		},
	}
}

func TestTitleTokenBudget(t *testing.T) {
	t.Parallel()

	t.Run("plain model keeps the small cap", func(t *testing.T) {
		t.Parallel()
		m := titleTestModel("neuralwatt", "glm-5.3", "", 128000)
		require.Equal(t, plainTitleTokens, titleTokenBudget(m))
	})

	t.Run("reasoning model gets room to think", func(t *testing.T) {
		t.Parallel()
		m := titleTestModel("neuralwatt", "glm-5.3-flash", catwalk.ThinkingToggleable, 0)
		require.Greater(t, titleTokenBudget(m), plainTitleTokens)
		require.Equal(t, reasoningTitleTokens, titleTokenBudget(m))
	})

	t.Run("reasoning model keeps a larger declared budget", func(t *testing.T) {
		t.Parallel()
		m := titleTestModel("neuralwatt", "glm-5.3-flash", catwalk.ThinkingToggleable, 4096)
		require.Equal(t, int64(4096), titleTokenBudget(m))
	})
}

func TestTitleAttempts(t *testing.T) {
	t.Parallel()

	t.Run("distinct models are both tried", func(t *testing.T) {
		t.Parallel()
		small := titleTestModel("neuralwatt", "glm-5.3-flash", catwalk.ThinkingToggleable, 0)
		large := titleTestModel("neuralwatt", "glm-5.3", catwalk.ThinkingToggleable, 0)
		attempts := titleAttempts(small, large)
		require.Len(t, attempts, 2)
		require.Equal(t, "small", attempts[0].name)
		require.Equal(t, "main", attempts[1].name)
	})

	t.Run("the same model is not retried", func(t *testing.T) {
		t.Parallel()
		small := titleTestModel("neuralwatt", "glm-5.3", catwalk.ThinkingToggleable, 0)
		large := titleTestModel("neuralwatt", "glm-5.3", catwalk.ThinkingToggleable, 0)
		attempts := titleAttempts(small, large)
		require.Len(t, attempts, 1)
		require.Equal(t, "small", attempts[0].name)
	})
}
