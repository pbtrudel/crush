package logo

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func testOpts() Opts {
	return Opts{
		FieldColor:     lipgloss.Color("#ff00ff"),
		TitleColorA:    lipgloss.Color("#ff0000"),
		TitleColorB:    lipgloss.Color("#0000ff"),
		CharmColor:     lipgloss.Color("#00ff00"),
		VersionColor:   lipgloss.Color("#ffff00"),
		HighlightColor: lipgloss.Color("#ffffff"),
		Width:          120,
	}
}

func TestRenderHeroDimensions(t *testing.T) {
	t.Parallel()

	out := Render(lipgloss.NewStyle(), "v1.2.3", false, testOpts())
	require.Equal(t, 3, lipgloss.Height(out))
	require.Greater(t, lipgloss.Width(out), 20)
	require.Contains(t, ansi.Strip(out), "terminal coding agent")
}

func TestRenderCompactDimensions(t *testing.T) {
	t.Parallel()

	out := Render(lipgloss.NewStyle(), "v1.2.3", true, testOpts())
	require.Equal(t, 4, lipgloss.Height(out))
	require.Contains(t, ansi.Strip(out), "Charm™")
}

func TestRenderHyperWordmark(t *testing.T) {
	t.Parallel()

	opts := testOpts()
	opts.Hyper = true
	out := Render(lipgloss.NewStyle(), "v1.2.3", true, opts)
	require.Contains(t, ansi.Strip(out), "HYPERCRUSH")
}

func TestRenderHeroTruncatesToWidth(t *testing.T) {
	t.Parallel()

	opts := testOpts()
	opts.Width = 20
	out := Render(lipgloss.NewStyle(), "v1.2.3", false, opts)
	for _, line := range strings.Split(ansi.Strip(out), "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 20)
	}
}

func TestBlockWordmarkRowsSameWidth(t *testing.T) {
	t.Parallel()

	rows := strings.Split(renderBlockWord("CRUSH"), "\n")
	require.Len(t, rows, 3)
	for i, row := range rows {
		require.Equal(t, 19, ansi.StringWidth(row), "wordmark row %d", i)
	}
}
