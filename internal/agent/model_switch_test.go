package agent

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

func TestNeedsModelSwitchCompaction(t *testing.T) {
	t.Parallel()
	large := Model{CatwalkCfg: catwalk.Model{ContextWindow: 200_000}}
	small := Model{CatwalkCfg: catwalk.Model{ContextWindow: 32_000}}

	require.True(t, needsModelSwitchCompaction(large, small, 20_000, 0.5))
	require.False(t, needsModelSwitchCompaction(large, small, 10_000, 0.5))
	require.False(t, needsModelSwitchCompaction(large, large, 100_000, 0.5))
	require.False(t, needsModelSwitchCompaction(small, large, 100_000, 0.5))
	require.False(t, needsModelSwitchCompaction(large, small, 20_000, 0))
}

func TestSetModelsTracksPreviousPair(t *testing.T) {
	t.Parallel()
	large := Model{CatwalkCfg: catwalk.Model{ContextWindow: 200_000}}
	small := Model{CatwalkCfg: catwalk.Model{ContextWindow: 32_000}}
	agent := NewSessionAgent(SessionAgentOptions{Models: func() ModelSet { return ModelSet{Large: large, Small: small} }}).(*sessionAgent)

	nextLarge := Model{CatwalkCfg: catwalk.Model{ContextWindow: 16_000}}
	nextSmall := Model{CatwalkCfg: catwalk.Model{ContextWindow: 8_000}}
	agent.SetModels(nextLarge, nextSmall)

	require.True(t, agent.modelSwitchPending)
	require.Equal(t, large, agent.previousLarge)
	require.Equal(t, small, agent.previousSmall)
	require.Equal(t, nextLarge, agent.Model())
}
