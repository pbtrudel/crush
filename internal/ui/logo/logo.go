// Package logo renders a Crush wordmark in a stylized way.
package logo

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// letterform represents a letterform. It can be stretched horizontally by
// a given amount via the boolean argument.
type letterform func(bool) string

// Opts are the options for rendering the Crush title art.
type Opts struct {
	FieldColor   color.Color // diagonal lines
	TitleColorA  color.Color // left gradient ramp point
	TitleColorB  color.Color // right gradient ramp point
	CharmColor   color.Color // Charm™ text color
	VersionColor color.Color // version text color
	Width        int         // width of the rendered logo, used for truncation
	Hyper        bool        // whether it is Crush or Hypercrush

	// Animate adds a moving highlight to the wordmark gradient. AnimPhase
	// is the highlight position in the range [0,1) and HighlightColor is
	// the color it blends toward.
	Animate        bool
	AnimPhase      float64
	HighlightColor color.Color
}

// blockLetters is a 3-row block font. Each glyph is three cells wide;
// renderBlockWord adds the one-cell gap between letters.
var blockLetters = map[rune][3]string{
	'C': {"█▀▀", "█  ", "▀▀▀"},
	'R': {"█▀▀", "█▀▀", "▀ ▀"},
	'U': {"█ █", "█ █", " ▀ "},
	'S': {"█▀▀", "▀▀█", "▀▀▀"},
	'H': {"█ █", "█▀█", "▀ ▀"},
	'Y': {"█ █", " ▀ ", " ▀ "},
	'P': {"█▀▀", "█▀▀", "▀  "},
	'E': {"█▀▀", "█▀▀", "▀▀▀"},
}

// Render renders the Crush mark. The compact form is a small stacked
// badge for the sidebar; the wide form is the hero shown above the
// landing screen.
func Render(base lipgloss.Style, version string, compact bool, o Opts) string {
	name := "CRUSH"
	if o.Hyper {
		name = "HYPERCRUSH"
	}
	if compact {
		return renderCompact(base, name, version, o)
	}
	return renderHero(base, name, version, o)
}

// renderBlockWord renders name in the 3-row block font.
func renderBlockWord(name string) string {
	rows := make([]strings.Builder, 3)
	for _, r := range name {
		glyph, ok := blockLetters[r]
		if !ok {
			continue
		}
		for i := range 3 {
			rows[i].WriteString(glyph[i])
			rows[i].WriteString(" ")
		}
	}
	lines := make([]string, 3)
	for i := range 3 {
		lines[i] = strings.TrimRight(rows[i].String(), " ")
	}
	return strings.Join(lines, "\n")
}

// renderBlockGradient paints the block wordmark with the theme gradient
// and, when animated, a moving highlight.
func renderBlockGradient(base lipgloss.Style, wordmark string, o Opts) string {
	lines := strings.Split(wordmark, "\n")
	for i, line := range lines {
		if o.Animate {
			lines[i] = styles.ApplyShimmerGrad(base.Bold(true), line, o.TitleColorA, o.TitleColorB, o.HighlightColor, o.AnimPhase)
			continue
		}
		lines[i] = styles.ApplyBoldForegroundGrad(base, line, o.TitleColorA, o.TitleColorB)
	}
	return strings.Join(lines, "\n")
}

// renderTextWordmark is the fallback for names too wide for the block
// font in the sidebar.
func renderTextWordmark(base lipgloss.Style, name string, o Opts) string {
	if o.Animate {
		return styles.ApplyShimmerGrad(base.Bold(true), name, o.TitleColorA, o.TitleColorB, o.HighlightColor, o.AnimPhase)
	}
	return styles.ApplyBoldForegroundGrad(base, name, o.TitleColorA, o.TitleColorB)
}

// renderHero renders the wide header mark: the block wordmark beside the
// tagline and version line.
func renderHero(base lipgloss.Style, name, version string, o Opts) string {
	wordmark := renderBlockGradient(base, renderBlockWord(name), o)
	tagline := lipgloss.NewStyle().Foreground(o.CharmColor).Render("terminal coding agent")
	meta := lipgloss.NewStyle().Foreground(o.VersionColor).Render("Charm™ · " + version)
	right := lipgloss.JoinVertical(lipgloss.Left, tagline, meta)
	mark := lipgloss.JoinHorizontal(lipgloss.Center, wordmark, "  ", right)
	return truncateLines(mark, o.Width)
}

// renderCompact renders the sidebar badge: the block wordmark, its meta
// line, and a gradient rule underneath.
func renderCompact(base lipgloss.Style, name, version string, o Opts) string {
	wordmark := renderBlockWord(name)
	if lipgloss.Width(wordmark) > 28 {
		wordmark = renderTextWordmark(base, name, o)
	} else {
		wordmark = renderBlockGradient(base, wordmark, o)
	}
	meta := lipgloss.NewStyle().Foreground(o.VersionColor).Render("Charm™ " + version)
	mark := lipgloss.JoinVertical(lipgloss.Left, wordmark, meta)
	return truncateLines(mark, o.Width)
}

// truncateLines clips every line of s to width cells. A zero or negative
// width leaves the mark untouched.
func truncateLines(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}

// SmallRender renders a smaller version of the Crush logo, suitable for
// smaller windows or sidebar usage.
func SmallRender(t *styles.Styles, width int, o Opts) string {
	name := "Crush"
	if o.Hyper {
		name = "HYPERCRUSH"
	}
	charm := "Charm™"
	title := t.Logo.SmallCharm.Render(charm)
	title = fmt.Sprintf("%s %s", title, styles.ApplyBoldForegroundGrad(t.Logo.GradCanvas, name, t.Logo.SmallGradFromColor, t.Logo.SmallGradToColor))
	remainingWidth := width - lipgloss.Width(title) - 1 // 1 for the space after the name
	if remainingWidth > 0 {
		lines := strings.Repeat("╱", remainingWidth)
		title = fmt.Sprintf("%s %s", title, t.Logo.SmallDiagonals.Render(lines))
	}
	return title
}
