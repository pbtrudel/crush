package styles

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestApplyShimmerGradPreservesText(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle()
	input := "CRUSH terminal"
	out := ApplyShimmerGrad(
		base,
		input,
		lipgloss.Color("#ff0000"),
		lipgloss.Color("#0000ff"),
		lipgloss.Color("#ffffff"),
		0.4,
	)
	require.Equal(t, ansi.StringWidth(input), ansi.StringWidth(out))
	require.Equal(t, input, ansi.Strip(out))
}

func TestApplyShimmerGradMovesHighlight(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle()
	input := "0123456789abcdef"
	from := lipgloss.Color("#ff0000")
	to := lipgloss.Color("#0000ff")
	highlight := lipgloss.Color("#ffffff")

	first := ApplyShimmerGrad(base, input, from, to, highlight, 0.1)
	second := ApplyShimmerGrad(base, input, from, to, highlight, 0.7)
	require.NotEqual(t, first, second, "the highlight must move with the phase")
}

func TestVerticalGradientHeight(t *testing.T) {
	t.Parallel()

	out := VerticalGradient(
		lipgloss.NewStyle(),
		5,
		"┃",
		lipgloss.Color("#ff0000"),
		lipgloss.Color("#0000ff"),
	)
	require.Equal(t, 5, lipgloss.Height(out))
	require.Equal(t, 1, lipgloss.Width(out))
}

func TestPulseColorEndpoints(t *testing.T) {
	t.Parallel()

	base := lipgloss.Color("#00ff00")
	highlight := lipgloss.Color("#ffffff")
	require.Equal(t, base, PulseColor(base, highlight, 0))
	require.Equal(t, highlight, PulseColor(base, highlight, 1))
}
