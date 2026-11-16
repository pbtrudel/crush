package dialog

import (
	"testing"

	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/stretchr/testify/require"
)

func TestGoalAndLoopCommandsAlwaysVisible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		hasSession bool
	}{
		{name: "without_session"},
		{name: "with_session", hasSession: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			com := &common.Common{Workspace: &rcCommandsWorkspace{}}
			c := &Commands{
				com:         com,
				hasSession:  tt.hasSession,
				list:        list.NewFilterableList(),
				windowWidth: 200,
			}

			ids := make([]string, 0, len(c.defaultCommands()))
			for _, it := range c.defaultCommands() {
				ids = append(ids, it.ID())
			}

			require.Contains(t, ids, "goal")
			require.Contains(t, ids, "loop")
		})
	}
}
