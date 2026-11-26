package model

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

// sidebarMouseWorkspace gives the sidebar build a config with Options, which
// the skills section reads.
type sidebarMouseWorkspace struct {
	agentViewerWorkspace
}

func (w *sidebarMouseWorkspace) Config() *config.Config {
	return &config.Config{
		Options:   &config.Options{},
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}
}

func newSidebarMouseUI(t *testing.T) *UI {
	t.Helper()
	u, _ := newAgentViewerUITest(t)
	u.com.Workspace = &sidebarMouseWorkspace{agentViewerWorkspace: agentViewerWorkspace{messages: make(map[string][]message.Message)}}
	return u
}

func sidebarAgentLine(t *testing.T, u *UI, name string) int {
	t.Helper()
	for i, line := range strings.Split(u.sidebarContent, "\n") {
		if strings.Contains(line, name) {
			return i
		}
	}
	require.Failf(t, "agent row not found in sidebar", "%s", name)
	return -1
}

func TestSidebarAgentHitMapAlignsWithContent(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	u.agentRuns = []agentrun.Run{
		{ID: "a", SessionID: "child-a", ParentSessionID: "main", Name: "alpha-agent", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning},
		{ID: "b", SessionID: "child-b", ParentSessionID: "main", Name: "beta-agent", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning},
	}
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()

	require.NotEmpty(t, u.sidebarAgentHits)
	for name, wantSession := range map[string]string{"alpha-agent": "child-a", "beta-agent": "child-b"} {
		line := sidebarAgentLine(t, u, name)
		hit, ok := u.sidebarAgentHits[line]
		require.Truef(t, ok, "line %d (%s) must map to an agent", line, name)
		require.Equal(t, wantSession, hit.SessionID)
	}
}

func TestSidebarAgentClickOpensViewer(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	u.agentRuns = []agentrun.Run{
		{ID: "a", SessionID: "child-a", ParentSessionID: "main", Name: "alpha-agent", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning},
	}
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()

	line := sidebarAgentLine(t, u, "alpha-agent")
	contentMinY := u.layout.sidebar.Min.Y + lipgloss.Height(u.sidebarDrawLogo)
	y := contentMinY + (line - u.sidebarOffset)

	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: u.layout.sidebar.Min.X, Y: y, Button: uv.MouseLeft}))

	require.NotNil(t, u.agentViewer)
	require.Equal(t, "child-a", u.agentViewer.descriptor.SessionID)
}

func TestSidebarNonAgentClickDoesNothing(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	u.agentRuns = []agentrun.Run{
		{ID: "a", SessionID: "child-a", ParentSessionID: "main", Name: "alpha-agent", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning},
	}
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()

	// The first content line is the session title, not an agent row.
	contentMinY := u.layout.sidebar.Min.Y + lipgloss.Height(u.sidebarDrawLogo)
	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: u.layout.sidebar.Min.X, Y: contentMinY, Button: uv.MouseLeft}))

	require.Nil(t, u.agentViewer)
}

func TestSidebarAgentClickRespectsScrollOffset(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	runs := make([]agentrun.Run, 40)
	for i := range runs {
		runs[i] = agentrun.Run{
			ID:              fmt.Sprintf("r%d", i),
			SessionID:       fmt.Sprintf("child-%d", i),
			ParentSessionID: "main",
			Name:            fmt.Sprintf("agent-%02d", i),
			Kind:            agentrun.KindSubagent,
			Status:          agentrun.StatusRunning,
		}
	}
	u.agentRuns = runs
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()
	require.True(t, u.sidebarScrollable)

	line := sidebarAgentLine(t, u, "agent-25")
	require.GreaterOrEqual(t, line, 3)
	u.sidebarOffset = line - 3
	contentMinY := u.layout.sidebar.Min.Y + lipgloss.Height(u.sidebarDrawLogo)

	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: u.layout.sidebar.Min.X, Y: contentMinY + 3, Button: uv.MouseLeft}))

	require.NotNil(t, u.agentViewer)
	require.Equal(t, "child-25", u.agentViewer.descriptor.SessionID)
}

// TestSidebarAgentClickShortSidebarSmallLogo pins that a short sidebar (height
// below the logo breakpoint, so the small logo is drawn) still maps a click to
// the right agent: contentMinY derives from sidebarDrawLogo, which holds the
// small logo.
func TestSidebarAgentClickShortSidebarSmallLogo(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	u.agentRuns = []agentrun.Run{
		{ID: "a", SessionID: "child-a", ParentSessionID: "main", Name: "alpha-agent", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning},
	}
	u.width, u.height = 140, 30
	u.updateLayoutAndSize()
	require.Positive(t, u.layout.sidebar.Dy())
	require.Less(t, u.layout.sidebar.Dy(), 30, "short sidebar must use the small logo")
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()

	line := sidebarAgentLine(t, u, "alpha-agent")
	u.sidebarOffset = min(line, u.sidebarMaxOffsetVal)
	row := line - u.sidebarOffset
	require.Less(t, row, u.sidebarContentHeight, "agent row must be visible")
	contentMinY := u.layout.sidebar.Min.Y + lipgloss.Height(u.sidebarDrawLogo)

	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: u.layout.sidebar.Min.X, Y: contentMinY + row, Button: uv.MouseLeft}))

	require.NotNil(t, u.agentViewer)
	require.Equal(t, "child-a", u.agentViewer.descriptor.SessionID)
}
