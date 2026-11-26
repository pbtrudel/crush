package dialog

import (
	"context"
	"testing"

	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/stretchr/testify/require"
)

// commandsWebStub is a web launcher whose running state the test sets.
type commandsWebStub struct {
	running bool
}

func (s *commandsWebStub) Start(context.Context) (string, error) {
	return "http://127.0.0.1:1/#t=x", nil
}

func (s *commandsWebStub) Stop(context.Context) error {
	s.running = false
	return nil
}

func (s *commandsWebStub) OpenBrowser() error { return nil }

func (s *commandsWebStub) SetInputMode(bool, uint64) {}

func (s *commandsWebStub) ReportInputModeRefused(string) {}

func (s *commandsWebStub) Running() bool { return s.running }

// TestStopWebCommandShowsOnlyWhileRunning checks that the palette offers
// Stop Web Mode exactly while a gateway runs, and keeps Open Web Mode
// listed either way so a closed tab can be reopened.
func TestStopWebCommandShowsOnlyWhileRunning(t *testing.T) {
	t.Parallel()

	commands := func(web common.WebLauncher) []*CommandItem {
		com := &common.Common{Workspace: &rcCommandsWorkspace{}, Web: web}
		c := &Commands{com: com}
		return c.defaultCommands()
	}
	ids := func(items []*CommandItem) []string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, item.ID())
		}
		return out
	}

	require.NotContains(t, ids(commands(nil)), "stop_web",
		"web mode unavailable has nothing to stop")
	require.NotContains(t, ids(commands(&commandsWebStub{})), "stop_web",
		"a stopped gateway has nothing to stop")
	require.Contains(t, ids(commands(nil)), "open_web",
		"Open Web Mode stays listed to reopen the tab")

	running := commands(&commandsWebStub{running: true})
	require.Contains(t, ids(running), "stop_web")
	require.Contains(t, ids(running), "open_web",
		"Open Web Mode stays listed to reopen the tab")
	for _, item := range running {
		if item.ID() != "stop_web" {
			continue
		}
		require.Equal(t, "Stop Web Mode", item.title)
		require.IsType(t, ActionStopWeb{}, item.Action())
		require.Equal(t, []string{"web stop", "stop web"}, item.aliases)
	}
}
