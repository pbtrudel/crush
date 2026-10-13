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
	actual := ansi.Strip(ModelInfo(&sty, "Model", "provider", "", context, 80, nil))

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
	actual := ansi.Strip(ModelInfo(&sty, "Model", "provider", "", context, 80, nil))

	require.NotContains(t, actual, "cached")
}

func TestFormatTokensAndCostOmitsEstimatedPrefix(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	actual := ansi.Strip(formatTokensAndCost(&sty, 120, 1000, 0, false))

	require.Contains(t, actual, "12%")
	require.NotContains(t, actual, "~12%")
}
