package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func hasSkill(list []*Skill, name string) bool {
	for _, s := range list {
		if s.Name == name {
			return true
		}
	}
	return false
}

// TestManagerReplace verifies a reload swaps every observable snapshot
// (skills, states, paths) and publishes the new states to subscribers.
func TestManagerReplace(t *testing.T) {
	t.Parallel()

	mgr := NewManager(
		[]*Skill{{Name: "old"}},
		[]*Skill{{Name: "old"}},
		[]*SkillState{{Name: "old"}},
	)
	t.Cleanup(mgr.Shutdown)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events := mgr.SubscribeEvents(ctx)

	mgr.Replace(
		[]*Skill{{Name: "new"}},
		[]*Skill{{Name: "new"}},
		[]*SkillState{{Name: "new"}},
		[]string{"/tmp/skills"},
		"/tmp/work",
	)

	require.Equal(t, "new", mgr.AllSkills()[0].Name)
	require.Equal(t, "new", mgr.ActiveSkills()[0].Name)
	require.Equal(t, "new", mgr.States()[0].Name)
	require.Equal(t, []string{"/tmp/skills"}, mgr.ResolvedPaths())
	require.Equal(t, "/tmp/work", mgr.WorkingDir())

	select {
	case ev := <-events:
		require.Len(t, ev.Payload.States, 1)
		require.Equal(t, "new", ev.Payload.States[0].Name)
	case <-time.After(time.Second):
		t.Fatal("no skills event published by Replace")
	}
}

// TestReloadPicksUpDiskChanges verifies Reload re-runs discovery so
// skills added to disk between runs become visible, and that config
// filters apply to the new result.
func TestReloadPicksUpDiskChanges(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	writeSkill := func(name string) {
		dir := filepath.Join(tmp, name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, SkillFileName),
			[]byte("---\nname: "+name+"\ndescription: skill for reload tests.\n---\nBody.\n"),
			0o644,
		))
	}

	// Start from an empty skills dir: only builtins are discovered.
	cfg := DiscoveryConfig{SkillsPaths: []string{tmp}}
	all, active, states := DiscoverFromConfig(cfg)
	mgr := NewManager(all, active, states)
	t.Cleanup(mgr.Shutdown)

	require.False(t, hasSkill(mgr.ActiveSkills(), "fresh-skill"))

	// A new skill appears on disk; a reload must pick it up.
	writeSkill("fresh-skill")
	Reload(mgr, cfg)
	require.True(t, hasSkill(mgr.AllSkills(), "fresh-skill"), "all skills must include the new skill")
	require.True(t, hasSkill(mgr.ActiveSkills(), "fresh-skill"), "active skills must include the new skill")

	// Disabling the skill yields an active set without it, while the
	// full set keeps it for inventory purposes.
	cfg.DisabledSkills = []string{"fresh-skill"}
	Reload(mgr, cfg)
	require.True(t, hasSkill(mgr.AllSkills(), "fresh-skill"), "all keeps disabled skills")
	require.False(t, hasSkill(mgr.ActiveSkills(), "fresh-skill"), "disabled skill must drop out of active")
}
