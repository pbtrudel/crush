package model

import (
	"fmt"
	"image"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const (
	headerDiag           = "╱"
	minHeaderDiags       = 3
	leftPadding          = 1
	rightPadding         = 1
	diagToDetailsSpacing = 1 // space between diagonal pattern and details section
)

type header struct {
	// compactLogo is the cached "Charm™ CRUSH" label used in compact mode.
	compactLogo string

	com *common.Common
}

// newHeader creates a new header model.
func newHeader(com *common.Common) *header {
	h := &header{
		com: com,
	}
	h.refresh()
	return h
}

// refresh rebuilds cached logo strings using the current styles. Call
// after the theme changes.
func (h *header) refresh() {
	t := h.com.Styles
	isHyper := h.com.IsHyper()
	charm := "Charm™"
	name := "CRUSH"
	if isHyper {
		name = "HYPERCRUSH"
	}
	h.compactLogo = t.Header.Charm.Render(charm) + " " +
		styles.ApplyBoldForegroundGrad(t.Header.LogoGradCanvas, name, t.Header.LogoGradFromColor, t.Header.LogoGradToColor) + " "
}

// drawHeader draws the header for the given session. lspErrorCount comes
// from the UI's memoized LSP state: drawing runs on every frame and must not
// probe the workspace (a synchronous HTTP round-trip in client/server mode).
func (h *header) drawHeader(
	scr uv.Screen,
	area uv.Rectangle,
	session *session.Session,
	compact bool,
	detailsOpen bool,
	width int,
	lspErrorCount int,
	hyperCredits *int,
	gitBranch string,
	busy bool,
	animPhase float64,
	modelName string,
	providerName string,
	contextWindow int64,
	runningSubAgents int,
) {
	t := h.com.Styles
	if !compact {
		logoView := renderLogoAnimated(h.com.Styles, compact, h.com.IsHyper(), width, animPhase)
		uv.NewStyledString(logoView).Draw(scr, area)
		h.drawHUD(scr, area, session, lspErrorCount, gitBranch, busy, animPhase, modelName, providerName, contextWindow)
		return
	}

	if session == nil || session.ID == "" {
		return
	}

	var b strings.Builder
	b.WriteString(h.statusLED(busy, animPhase))
	b.WriteString(" ")
	b.WriteString(h.compactLogo)

	availDetailWidth := width - leftPadding - rightPadding - lipgloss.Width(b.String()) - minHeaderDiags - diagToDetailsSpacing
	details := renderHeaderDetails(
		h.com,
		session,
		lspErrorCount,
		detailsOpen,
		availDetailWidth,
		hyperCredits,
		gitBranch,
		contextWindow,
		runningSubAgents,
	)

	remainingWidth := width -
		lipgloss.Width(b.String()) -
		lipgloss.Width(details) -
		leftPadding -
		rightPadding -
		diagToDetailsSpacing

	if remainingWidth > 0 {
		b.WriteString(t.Header.Diagonals.Render(
			strings.Repeat(headerDiag, max(minHeaderDiags, remainingWidth)),
		))
		b.WriteString(" ")
	}

	b.WriteString(details)

	view := uv.NewStyledString(
		t.Header.Wrapper.Padding(0, rightPadding, 0, leftPadding).Render(b.String()),
	)
	view.Draw(scr, area)
}

// statusLED renders the live status dot. While the agent is working the
// dot breathes between its base color and the accent color.
func (h *header) statusLED(busy bool, animPhase float64) string {
	t := h.com.Styles
	led := t.Chrome.LEDIdle
	if busy {
		led = t.Chrome.LEDBusy
		amount := 0.35 + 0.35*math.Sin(animPhase*2*math.Pi)
		led = led.Foreground(styles.PulseColor(
			led.GetForeground(),
			t.Chrome.TopRuleEdge.GetForeground(),
			amount,
		))
	}
	return led.Render("●")
}

// drawHUD draws the right-aligned status readout over the logo field. It
// shows the agent state, model, context meter, and working directory in a
// single floating block, giving the header a cockpit feel.
func (h *header) drawHUD(
	scr uv.Screen,
	area uv.Rectangle,
	session *session.Session,
	lspErrorCount int,
	gitBranch string,
	busy bool,
	animPhase float64,
	modelName string,
	providerName string,
	contextWindow int64,
) {
	t := h.com.Styles
	const panelWidth = 38
	// Keep enough room for the wordmark on the left; below this the HUD
	// would cover the logo instead of sitting beside it.
	if area.Dx() < panelWidth+50 || area.Dy() < 2 {
		return
	}
	height := min(area.Dy(), 3)
	contentWidth := panelWidth - 3 // rail + horizontal padding

	led := h.statusLED(busy, animPhase)
	status := "READY"
	if busy {
		status = "WORKING"
	}
	if lspErrorCount > 0 {
		led = t.Chrome.LEDError.Render("●")
		status = fmt.Sprintf("%d LSP ERRORS", lspErrorCount)
	}

	if modelName == "" {
		modelName = "…"
	}
	var pct float64
	if session != nil && contextWindow > 0 {
		pct = (float64(session.CompletionTokens+session.PromptTokens) / float64(contextWindow)) * 100
	}

	providerSuffix := ""
	if providerName != "" {
		providerSuffix = "   " + t.Header.GitBranch.Render("via "+providerName)
	}

	branch := ""
	if gitBranch != "" {
		branch = "⎇ " + gitBranch
	}
	cwd := fsext.DirTrim(fsext.PrettyPath(h.com.Workspace.WorkingDir()), 3)

	lines := []string{
		led + " " + t.Header.Charm.Render(status) + "   " +
			t.Header.GitBranch.Render(styles.ModelIcon) + " " + t.Header.GitBranch.Render(modelName),
		common.ContextMeter(t, pct, 10) + " " + t.Header.GitBranch.Render(fmt.Sprintf("%d%%", int(pct))) + providerSuffix,
		t.Header.GitBranch.Render(branch) + " " + t.Header.GitBranch.Render(cwd),
	}
	lines = lines[:min(height, len(lines))]
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], contentWidth, "…")
		if w := lipgloss.Width(lines[i]); w < contentWidth {
			lines[i] += strings.Repeat(" ", contentWidth-w)
		}
	}

	rail := styles.VerticalGradient(
		t.Chrome.SidebarRail,
		len(lines),
		"▌",
		t.Chrome.SidebarRailFrom,
		t.Chrome.SidebarRailTo,
	)
	body := t.Chrome.StatusBar.Padding(0, 1).Width(panelWidth - 1).Render(strings.Join(lines, "\n"))
	panel := lipgloss.JoinHorizontal(lipgloss.Top, rail, body)
	rect := image.Rect(area.Max.X-panelWidth, area.Min.Y, area.Max.X, area.Min.Y+len(lines))
	uv.NewStyledString(panel).Draw(scr, rect)
}

// renderHeaderDetails renders the details section of the header.
func renderHeaderDetails(
	com *common.Common,
	session *session.Session,
	lspErrorCount int,
	detailsOpen bool,
	availWidth int,
	hyperCredits *int,
	gitBranch string,
	contextWindow int64,
	runningSubAgents int,
) string {
	t := com.Styles

	var parts []string

	// Subagents work out of sight in compact mode, so the header carries
	// a live count of how many are still going.
	if runningSubAgents > 0 {
		parts = append(parts, t.Header.Percentage.Render(fmt.Sprintf(
			"%s %d", subAgentSpinner(time.Now()), runningSubAgents,
		)))
	}

	if lspErrorCount > 0 {
		parts = append(parts, t.LSP.ErrorDiagnostic.Render(fmt.Sprintf("%s%d", styles.LSPErrorIcon, lspErrorCount)))
	}

	if contextWindow > 0 {
		percentage := (float64(session.CompletionTokens+session.PromptTokens) / float64(contextWindow)) * 100
		percentageText := fmt.Sprintf("%d%%", int(percentage))
		if session.EstimatedUsage {
			percentageText = "~" + percentageText
		}
		formattedPercentage := t.Header.Percentage.Render(percentageText)
		parts = append(parts, common.ContextMeter(t, percentage, 5)+" "+formattedPercentage)
	}

	if com.IsHyper() && hyperCredits != nil {
		hc := t.Header.HypercreditIcon.Render(styles.HypercreditIcon) + " " + t.Header.Percentage.Render(common.FormatCredits(*hyperCredits))
		parts = append(parts, hc)
	}

	const keystroke = "f2"
	if detailsOpen {
		parts = append(parts, t.Header.Keystroke.Render(keystroke)+t.Header.KeystrokeTip.Render(" close"))
	} else {
		parts = append(parts, t.Header.Keystroke.Render(keystroke)+t.Header.KeystrokeTip.Render(" open "))
	}

	dot := t.Header.Separator.Render(" • ")
	metadata := strings.Join(parts, dot)
	metadata = dot + metadata

	const dirTrimLimit = 4
	workingDir := com.Workspace.WorkingDir()
	cwd := t.Header.WorkingDir.Render(fsext.DirTrim(fsext.PrettyPath(workingDir), dirTrimLimit))

	if gitBranch != "" {
		// Reserve space for the separator between branch and path and
		// at least some of the path.
		branchSep := " "
		metadataWidth := lipgloss.Width(metadata)
		maxBranchWidth := max(0, availWidth-metadataWidth-lipgloss.Width(cwd)-lipgloss.Width(branchSep))
		if maxBranchWidth > 0 {
			truncBranch := ansi.Truncate(gitBranch, maxBranchWidth, "…")
			cwd = t.Header.GitBranch.Render(truncBranch) + branchSep + cwd
		}
	}

	result := cwd + metadata
	return ansi.Truncate(result, max(0, availWidth), "…")
}
