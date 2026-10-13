package dialog

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

type rcCommandsWorkspace struct {
	workspace.Workspace
}

func (w *rcCommandsWorkspace) Config() *config.Config {
	return &config.Config{Options: &config.Options{TUI: &config.TUIOptions{}}}
}

func (w *rcCommandsWorkspace) WorkingDir() string { return "/tmp" }

func TestRemoteControlCommandVisibleWithoutSession(t *testing.T) {
	t.Parallel()

	com := &common.Common{Workspace: &rcCommandsWorkspace{}}
	c := &Commands{
		com:         com,
		hasSession:  false,
		list:        list.NewFilterableList(),
		windowWidth: 200,
	}

	ids := make([]string, 0, len(c.defaultCommands()))
	for _, it := range c.defaultCommands() {
		ids = append(ids, it.ID())
	}

	require.Contains(t, ids, "remote_control")
}
