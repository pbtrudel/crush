package dialog

import (
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

const testGoalDescription = "Crush works toward the objective turn after turn until it verifies completion."

func newGoalArgumentsForTest(t *testing.T) *Arguments {
	t.Helper()
	sty := styles.CharmtonePantera()
	com := &common.Common{Styles: &sty}
	return NewArguments(com, "Set Goal", testGoalDescription, []commands.Argument{{
		ID:       "objective",
		Title:    "Objective",
		Required: true,
	}}, ActionGoal{})
}

func drawArgumentsForTest(a *Arguments) ([]string, *int) {
	scr := uv.NewScreenBuffer(100, 30)
	cur := a.Draw(scr, scr.Bounds())
	lines := strings.Split(scr.String(), "\n")
	if cur == nil {
		return lines, nil
	}
	y := cur.Y
	return lines, &y
}

// TestArgumentsGoalLongTextWrappedAndCursorOnInputRow guards the /goal
// dialog: a long objective must wrap inside a wide dialog with its
// beginning still visible, and the cursor must sit on the input row, not
// on the label row above it.
func TestArgumentsGoalLongTextWrappedAndCursorOnInputRow(t *testing.T) {
	t.Parallel()

	a := newGoalArgumentsForTest(t)
	a.inputs[0].SetValue("implement the following workflow end to end and keep the beginning of this very long objective visible while typing near its end")

	lines, cursorY := drawArgumentsForTest(a)
	require.NotNil(t, cursorY, "dialog should return a cursor")
	require.Greater(t, *cursorY, 0)

	// The label sits directly above the input row.
	labelRow := -1
	for i, line := range lines {
		if strings.Contains(line, "Objective") {
			labelRow = i
			break
		}
	}
	require.NotEqual(t, -1, labelRow, "dialog should render an Objective label")
	require.Contains(t, lines[labelRow+1], "> ", "prompt should render on the row below the label")

	// Cursor must be on an input row, not on the label row above it, and
	// the visible tail of the objective must be under it.
	require.NotContains(t, lines[*cursorY], "Objective", "cursor should not sit on the label row")
	require.GreaterOrEqual(t, *cursorY, labelRow+1, "cursor should sit on an input row")
	require.Contains(t, lines[*cursorY], "end")

	// The beginning of the objective stays visible: a single-line input
	// scrolls it away.
	require.True(t, slices.ContainsFunc(lines, func(l string) bool {
		return strings.Contains(l, "implement the following")
	}), "beginning of the objective should stay visible")

	// The dialog has to be wide enough for a worked objective.
	borderRow := -1
	for i, line := range lines {
		if strings.Contains(line, "╭") {
			borderRow = i
			break
		}
	}
	require.NotEqual(t, -1, borderRow, "dialog should draw a top border")
	require.GreaterOrEqual(t, lipgloss.Width(strings.TrimSpace(lines[borderRow])), 60)
}

// TestArgumentsCursorStaysInsideLongInputAfterResize covers a long value
// plus terminal resizes, including height-only shrinks: the textarea and
// the dialog have to keep the caret on a text row, not on a blank row or
// outside the dialog.
func TestArgumentsCursorStaysInsideLongInputAfterResize(t *testing.T) {
	t.Parallel()

	a := newGoalArgumentsForTest(t)
	a.inputs[0].SetValue(strings.Repeat("word ", 120))

	sizes := [][2]int{{100, 30}, {100, 20}, {100, 18}, {70, 30}, {50, 20}, {100, 30}}
	for _, size := range sizes {
		scr := uv.NewScreenBuffer(size[0], size[1])
		cur := a.Draw(scr, scr.Bounds())
		require.NotNil(t, cur, "dialog should return a cursor at %dx%d", size[0], size[1])
		require.GreaterOrEqual(t, cur.Y, 0, "cursor should stay on screen at %dx%d", size[0], size[1])
		require.Less(t, cur.Y, size[1], "cursor should stay on screen at %dx%d", size[0], size[1])
		lines := strings.Split(scr.String(), "\n")
		require.Contains(t, lines[cur.Y], "word",
			"cursor should stay on the wrapped text after resizing to %dx%d", size[0], size[1])
	}
}

// TestArgumentsNoCursorWhenFieldViewportCollapses covers a terminal too
// short to render any field rows: no cursor should be reported outside the
// dialog.
func TestArgumentsNoCursorWhenFieldViewportCollapses(t *testing.T) {
	t.Parallel()

	a := newGoalArgumentsForTest(t)
	a.inputs[0].SetValue("short goal")

	scr := uv.NewScreenBuffer(100, 10)
	require.Nil(t, a.Draw(scr, scr.Bounds()))
}

// TestArgumentsSideQuestionCursorOnInputRow covers the other single-argument
// command that opens this dialog, with the value from the reported screenshot.
func TestArgumentsSideQuestionCursorOnInputRow(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	com := &common.Common{Styles: &sty}
	a := NewArguments(com, "Side Question", "Ask about this session without adding the exchange to the conversation.", []commands.Argument{{
		ID:       "question",
		Title:    "Question",
		Required: true,
	}}, ActionAskSideQuestion{})
	a.inputs[0].SetValue("my text input")

	lines, cursorY := drawArgumentsForTest(a)
	require.NotNil(t, cursorY, "dialog should return a cursor")
	require.Contains(t, lines[*cursorY], "> my text input")
	require.Contains(t, lines[*cursorY-1], "Question")
}

// TestArgumentsCursorOnInputRowWithShortValue guards the /goal dialog's
// cursor placement: even with a short value the cursor belongs on the
// input row, directly below the label.
func TestArgumentsCursorOnInputRowWithShortValue(t *testing.T) {
	t.Parallel()

	a := newGoalArgumentsForTest(t)
	a.inputs[0].SetValue("implement the follow")

	lines, cursorY := drawArgumentsForTest(a)
	require.NotNil(t, cursorY, "dialog should return a cursor")
	require.Greater(t, *cursorY, 0)

	require.Contains(t, lines[*cursorY], "> implement the follow")
	require.Contains(t, lines[*cursorY-1], "Objective")
}
