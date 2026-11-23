package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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

func TestSetSkillDisabledConfigOnlyTouchesScopeFile(t *testing.T) {
	t.Parallel()

	globalPath := filepath.Join(t.TempDir(), "crush.json")
	require.NoError(t, os.WriteFile(globalPath, []byte(`{"options":{"disabled_skills":["global-one","pair"]}}`), 0o600))

	// The merged config also carries a project-only entry, which must not
	// leak into the global file.
	store := &ConfigStore{
		config:         &Config{Options: &Options{DisabledSkills: []string{"global-one", "pair", "project-only"}}},
		globalDataPath: globalPath,
	}

	require.NoError(t, store.SetSkillDisabledConfig(ScopeGlobal, "charmtone", true))
	require.NoError(t, store.SetSkillDisabledConfig(ScopeGlobal, "pair", false))

	data, err := os.ReadFile(globalPath)
	require.NoError(t, err)
	var got []string
	for _, name := range gjson.GetBytes(data, "options.disabled_skills").Array() {
		got = append(got, name.String())
	}
	require.Equal(t, []string{"charmtone", "global-one"}, got)
}
