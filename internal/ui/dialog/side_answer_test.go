package dialog

import (
	"testing"

	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestSideAnswerContent(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	s := NewSideAnswer(&common.Common{Styles: &sty}, "what is pooling?", "Pooling keeps connections warm.")

	content := ansi.Strip(s.content(60))
	require.Contains(t, content, "what is pooling?")
	require.Contains(t, content, "Pooling keeps connections warm.")
	require.Contains(t, content, "Not added to the conversation.")
}

func TestSideAnswerEmptyAnswer(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	s := NewSideAnswer(&common.Common{Styles: &sty}, "why?", "")

	require.Contains(t, ansi.Strip(s.content(60)), "(the model returned no answer)")
}
