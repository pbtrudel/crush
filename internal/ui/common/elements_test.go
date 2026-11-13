package common

import (
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestFormatTokensAndCostPrefixesEstimatedUsage(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	rendered := formatTokensAndCost(&sty, 120, 1000, 0, true)
	actual := ansi.Strip(rendered)

	require.Contains(t, actual, "~12%")
	require.Contains(t, actual, "(120)")
	require.Contains(t, actual, "$0.00")
	require.True(t, strings.Contains(rendered, sty.ModelInfo.TokenPercentage.Render("~12%")))
}

func TestModelInfoRendersCachedPercentage(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	context := &ModelContextInfo{
		ContextUsed:  500,
		ModelContext: 1000,
		CachePercent: 43,
	}
	actual := ansi.Strip(ModelInfo(&sty, "Model", "provider", "", context, 80, nil, nil))

	require.Contains(t, actual, "43% cached")
}

func TestModelInfoOmitsCachedPercentageWhenZero(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	context := &ModelContextInfo{
		ContextUsed:  500,
		ModelContext: 1000,
		CachePercent: 0,
	}
	actual := ansi.Strip(ModelInfo(&sty, "Model", "provider", "", context, 80, nil, nil))

	require.NotContains(t, actual, "cached")
}

func TestFormatTokensAndCostOmitsEstimatedPrefix(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	actual := ansi.Strip(formatTokensAndCost(&sty, 120, 1000, 0, false))

	require.Contains(t, actual, "12%")
	require.NotContains(t, actual, "~12%")
}

func TestModelInfoRendersTPSStat(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	context := &ModelContextInfo{
		ContextUsed:  500,
		ModelContext: 1000,
		TPSLive:      12.3,
		TPSAverage:   8.1,
	}
	actual := ansi.Strip(ModelInfo(&sty, "Model", "provider", "", context, 80, nil, nil))

	require.Contains(t, actual, "TPS: 12.3")
	require.Contains(t, actual, "TPS_AVG 8.1")
}

func TestModelInfoAlwaysRendersTPSStat(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	context := &ModelContextInfo{
		ContextUsed:  500,
		ModelContext: 1000,
	}
	actual := ansi.Strip(ModelInfo(&sty, "Model", "provider", "", context, 80, nil, nil))

	require.Contains(t, actual, "TPS: 0.0")
}

func TestModelInfoRendersCopilotCreditsUsed(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	used := 2568
	actual := ansi.Strip(ModelInfo(&sty, "Model", "GitHub Copilot", "", nil, 80, nil, &used))

	require.Contains(t, actual, "2,568 AI credits used")
}

func TestModelInfoOmitsCopilotCreditsWhenUnknown(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	actual := ansi.Strip(ModelInfo(&sty, "Model", "GitHub Copilot", "", nil, 80, nil, nil))

	require.NotContains(t, actual, "AI credits used")
}

func TestContextMeter(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	require.Equal(t, 6, ansi.StringWidth(ContextMeter(&sty, 0, 6)))
	require.Equal(t, "▱▱▱▱▱▱", ansi.Strip(ContextMeter(&sty, 0, 6)))
	require.Equal(t, "▰▰▰▰▰▰", ansi.Strip(ContextMeter(&sty, 100, 6)))
	require.Equal(t, "▰▰▰▱▱▱", ansi.Strip(ContextMeter(&sty, 50, 6)))
	require.Equal(t, "", ContextMeter(&sty, 50, 0))
}
