package model

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func TestSidebarRebuildMemoization(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.com = common.DefaultCommon(stubConfigWorkspace{})
	u.session = &session.Session{ID: "s1", Title: "first"}
	u.layout.sidebar = uv.Rect(0, 0, 30, 40)

	u.updateSidebarScrollState()
	require.Equal(t, 1, u.sidebarBuilds)
	require.Contains(t, u.sidebarContent, "first")

	// Same inputs: no rebuild.
	u.updateSidebarScrollState()
	require.Equal(t, 1, u.sidebarBuilds)

	// A session change rebuilds.
	u.session = &session.Session{ID: "s1", Title: "second"}
	u.updateSidebarScrollState()
	require.Equal(t, 2, u.sidebarBuilds)
	require.Contains(t, u.sidebarContent, "second")

	// A version bump (LSP/MCP/skills state replaced) rebuilds.
	u.lspVer++
	u.updateSidebarScrollState()
	require.Equal(t, 3, u.sidebarBuilds)

	// A size change rebuilds.
	u.layout.sidebar = uv.Rect(0, 0, 30, 20)
	u.updateSidebarScrollState()
	require.Equal(t, 4, u.sidebarBuilds)
}

func TestPillsRebuildMemoization(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.com = common.DefaultCommon(stubConfigWorkspace{})
	u.todoSpinner = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	u.session = &session.Session{ID: "s1", Todos: []session.Todo{
		{Content: "write tests", Status: session.TodoStatusPending},
	}}
	u.layout.pills = uv.Rect(0, 0, 40, 2)

	u.renderPills()
	require.Equal(t, 1, u.pillsBuilds)
	require.NotEmpty(t, u.pillsView)
	firstView := u.pillsView

	// Same inputs: no rebuild, and the memo hit keeps the content.
	u.renderPills()
	require.Equal(t, 1, u.pillsBuilds)
	require.Equal(t, firstView, u.pillsView)

	// Expanding rebuilds.
	u.pillsExpanded = true
	u.renderPills()
	require.Equal(t, 2, u.pillsBuilds)

	// While spinning the memo is bypassed.
	u.todoIsSpinning = true
	u.renderPills()
	u.renderPills()
	require.Equal(t, 4, u.pillsBuilds)

	// Stopping the spinner rebuilds (the key changed).
	u.todoIsSpinning = false
	u.renderPills()
	require.Equal(t, 5, u.pillsBuilds)
}

func BenchmarkSidebarRebuildMemoized(b *testing.B) {
	u := newTestUI()
	u.com = common.DefaultCommon(stubConfigWorkspace{})
	u.session = &session.Session{ID: "s1", Title: "bench"}
	u.layout.sidebar = uv.Rect(0, 0, 30, 40)
	u.updateSidebarScrollState() // prime the memo

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		u.updateSidebarScrollState()
	}
}
