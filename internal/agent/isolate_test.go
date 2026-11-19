package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// runIsolated keeps tests off the developer's own global config and data.
func runIsolated(m *testing.M) int {
	home, err := os.MkdirTemp("", "crush-agent-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(home)
	os.Setenv("CRUSH_GLOBAL_CONFIG", filepath.Join(home, "config"))
	os.Setenv("CRUSH_GLOBAL_DATA", filepath.Join(home, "data"))
	// The provider catalog fetch and the Claude Code CLI probe are
	// production startup work, not fixtures: keep the tests offline and
	// off the developer's CLI install.
	os.Setenv("CRUSH_DISABLE_PROVIDER_AUTO_UPDATE", "1")
	os.Setenv("CRUSH_CLAUDE_PATH", filepath.Join(home, "no-claude"))
	return m.Run()
}
