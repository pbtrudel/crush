package agentteam

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigPipelineDefaults(t *testing.T) {
	t.Parallel()
	require.True(t, Config{}.autostart(), "a nil Autostart means on")
	require.True(t, Config{}.roleExists("anything"), "a nil RoleExists accepts every role")
	require.False(t, Config{Autostart: func() bool { return false }}.autostart())
	require.False(t, Config{RoleExists: func(string) bool { return false }}.roleExists("gone"))
	require.NotNil(t, Config{Autostart: func() bool { return true }}.withDefaults().Autostart, "defaults keep the func")

	// The defaulted Config must still answer through the func fields.
	require.True(t, Config{}.withDefaults().autostart(), "defaults leave autostart on")
	require.False(t, Config{Autostart: func() bool { return false }}.withDefaults().autostart(), "defaults keep a false func")
	require.True(t, Config{}.withDefaults().roleExists("anything"), "defaults accept every role")
	require.False(t, Config{RoleExists: func(string) bool { return false }}.withDefaults().roleExists("gone"), "defaults keep a false RoleExists")
}
