package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSkillsDiscoveryConfig(t *testing.T) {
	t.Parallel()

	store, err := Init(t.Context(), t.TempDir(), "", false)
	require.NoError(t, err)

	store.Config().Options.SkillsPaths = []string{"~/skills", "$HOME/more"}
	store.Config().Options.DisabledSkills = []string{"skip-me"}

	dc := SkillsDiscoveryConfig(store)

	require.Equal(t, []string{"~/skills", "$HOME/more"}, dc.SkillsPaths)
	require.Equal(t, []string{"skip-me"}, dc.DisabledSkills)
	require.Equal(t, store.WorkingDir(), dc.WorkingDir)
	if store.Resolver() != nil {
		require.NotNil(t, dc.Resolver, "resolver must be wired when the store has one")
	}
}
