package chat

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// ActivityGroupID returns the id of the activity row that starts with the
// tool call of the given id.
func ActivityGroupID(firstToolID string) string {
	return firstToolID + ":activity"
}

// ActivityGroupItem collapses a run of consecutive tool calls into one
// summary row such as "Ran 7 tools (3 edits, 2 reads, 2 bash) in 41s". It is
// used when options.tui.verbosity is low. Tool calls that need the user's
// eye (a failure, a pending permission prompt, a call still running) are
// rendered under the row even while it is collapsed.
type ActivityGroupItem struct {
	ver   *list.Versioned
	sty   *styles.Styles
	items []ToolMessageItem

	expanded bool
	focused  bool

	// startedAt and endedAt bound the run on the live path; a group built
	// from stored messages leaves them zero and claims no duration.
	startedAt time.Time
	endedAt   time.Time
}

var (
	_ MessageItem         = (*ActivityGroupItem)(nil)
	_ ItemContainer       = (*ActivityGroupItem)(nil)
	_ Expandable          = (*ActivityGroupItem)(nil)
	_ Animatable          = (*ActivityGroupItem)(nil)
	_ KeyEventHandler     = (*ActivityGroupItem)(nil)
	_ list.Focusable      = (*ActivityGroupItem)(nil)
	_ list.MouseClickable = (*ActivityGroupItem)(nil)
	_ cacheClearable      = (*ActivityGroupItem)(nil)
	_ animRestyler        = (*ActivityGroupItem)(nil)
)

// NewActivityGroupItem groups the given tool calls into one row.
func NewActivityGroupItem(sty *styles.Styles, items ...ToolMessageItem) *ActivityGroupItem {
	return &ActivityGroupItem{ver: list.NewVersioned(), sty: sty, items: items}
}

// groupable reports whether an item may join an activity row. Agents and
// other containers keep their own rows because their nested transcript is
// the point of showing them.
func groupable(item MessageItem) (ToolMessageItem, bool) {
	tool, ok := item.(ToolMessageItem)
	if !ok {
		return nil, false
	}
	if _, container := item.(NestedToolContainer); container {
		return nil, false
	}
	return tool, true
}

// GroupToolActivity replaces every run of two or more consecutive groupable
// tool calls with an [ActivityGroupItem]. Other items pass through as is.
func GroupToolActivity(items []MessageItem) []MessageItem {
	out := make([]MessageItem, 0, len(items))
	var run []ToolMessageItem
	flush := func() {
		switch len(run) {
		case 0:
		case 1:
			out = append(out, run[0])
		default:
			out = append(out, NewActivityGroupItem(nil, run...))
		}
		run = nil
	}
	for _, item := range items {
		if tool, ok := groupable(item); ok {
			run = append(run, tool)
			continue
		}
		flush()
		out = append(out, item)
	}
	flush()
	for _, item := range out {
		if g, ok := item.(*ActivityGroupItem); ok {
			g.sty = toolStyles(g.items[0])
		}
	}
	return out
}

// toolStyles recovers the shared styles pointer from a tool item so a group
// recolors with the rest of the transcript on a theme switch.
func toolStyles(item ToolMessageItem) *styles.Styles {
	if s, ok := item.(interface{ itemStyles() *styles.Styles }); ok {
		return s.itemStyles()
	}
	sty := styles.CharmtonePantera()
	return &sty
}

// Append adds a tool call to the end of the run and extends its measured
// span to now, starting the clock on the first call.
func (g *ActivityGroupItem) Append(item ToolMessageItem, now time.Time) {
	g.items = append(g.items, item)
	if g.startedAt.IsZero() {
		g.startedAt = now
	}
	g.endedAt = now
	g.Bump()
}

// ChildItems returns the grouped tool calls in transcript order.
func (g *ActivityGroupItem) ChildItems() []ToolMessageItem {
	return g.items
}

// Expanded reports whether the user expanded the row.
func (g *ActivityGroupItem) Expanded() bool {
	return g.expanded
}

// ID implements [Identifiable].
func (g *ActivityGroupItem) ID() string {
	return ActivityGroupID(g.items[0].ID())
}

// Version implements [list.Item]. It folds in the children's versions so a
// result landing on a grouped tool call re-renders the row.
func (g *ActivityGroupItem) Version() uint64 {
	v := g.ver.Version()
	for _, item := range g.items {
		v += item.Version()
	}
	return v
}

// Bump invalidates the row's rendered output.
func (g *ActivityGroupItem) Bump() {
	g.ver.Bump()
}

// SetFocused implements [list.Focusable].
func (g *ActivityGroupItem) SetFocused(focused bool) {
	if g.focused != focused {
		g.focused = focused
		g.Bump()
	}
}

// ToggleExpanded implements [Expandable].
func (g *ActivityGroupItem) ToggleExpanded() bool {
	g.expanded = !g.expanded
	g.Bump()
	return g.expanded
}

// HandleKeyEvent implements [KeyEventHandler]: enter toggles the row.
func (g *ActivityGroupItem) HandleKeyEvent(key tea.KeyMsg) (bool, tea.Cmd) {
	if key.String() == "enter" {
		g.ToggleExpanded()
		return true, nil
	}
	return false, nil
}

// HandleMouseClick implements [list.MouseClickable] so a click toggles the
// row through the chat's delayed-click path.
func (g *ActivityGroupItem) HandleMouseClick(btn ansi.MouseButton, x, y int) bool {
	return btn == ansi.MouseLeft
}

// Spinning implements [Animatable].
func (g *ActivityGroupItem) Spinning() bool {
	for _, item := range g.items {
		if a, ok := item.(Animatable); ok && a.Spinning() {
			return true
		}
	}
	return false
}

// Advance implements [Animatable].
func (g *ActivityGroupItem) Advance() bool {
	changed := false
	for _, item := range g.items {
		if a, ok := item.(Animatable); ok && a.Spinning() && a.Advance() {
			changed = true
		}
	}
	return changed
}

// clearCache implements cacheClearable for the grouped tool calls, which
// are not list entries of their own.
func (g *ActivityGroupItem) clearCache() {
	for _, item := range g.items {
		if cc, ok := item.(cacheClearable); ok {
			cc.clearCache()
		}
		if v, ok := item.(interface{ Bump() }); ok {
			v.Bump()
		}
	}
	g.Bump()
}

// restyleAnim implements animRestyler for the grouped tool calls, whose
// spinners bake their colors into pre-rendered frames. A spinning call is
// visible even while the row is collapsed, so a theme switch has to reach it.
func (g *ActivityGroupItem) restyleAnim() {
	for _, item := range g.items {
		if ar, ok := item.(animRestyler); ok {
			ar.restyleAnim()
		}
	}
}

// effectiveStatus is the tool status with the recorded result applied.
func effectiveStatus(item ToolMessageItem) ToolStatus {
	if s, ok := item.(interface{ computeStatus() ToolStatus }); ok {
		return s.computeStatus()
	}
	return item.Status()
}

// needsAttention reports whether a tool call must stay visible while its
// row is collapsed.
func needsAttention(item ToolMessageItem) bool {
	switch effectiveStatus(item) {
	case ToolStatusError, ToolStatusAwaitingPermission:
		return true
	}
	a, ok := item.(Animatable)
	return ok && a.Spinning()
}

// RawRender implements [list.RawRenderable].
func (g *ActivityGroupItem) RawRender(width int) string {
	parts := []string{g.summary()}
	for _, item := range g.items {
		if g.expanded || needsAttention(item) {
			parts = append(parts, item.RawRender(width))
		}
	}
	return strings.Join(parts, "\n\n")
}

// Render implements [list.Item].
func (g *ActivityGroupItem) Render(width int) string {
	prefix := g.sty.Messages.ToolCallBlurred.Render()
	if g.focused {
		prefix = g.sty.Messages.ToolCallFocused.Render()
	}
	parts := []string{prefix + g.summary()}
	for _, item := range g.items {
		if g.expanded || needsAttention(item) {
			parts = append(parts, item.Render(width))
		}
	}
	return strings.Join(parts, "\n\n")
}

// summary renders the one-line description of the run.
func (g *ActivityGroupItem) summary() string {
	failed := 0
	var order []string
	counts := map[string]int{}
	for _, item := range g.items {
		kind := activityKind(item.ToolCall().Name)
		if counts[kind] == 0 {
			order = append(order, kind)
		}
		counts[kind]++
		if effectiveStatus(item) == ToolStatusError {
			failed++
		}
	}
	kinds := make([]string, len(order))
	for i, kind := range order {
		kinds[i] = pluralKind(kind, counts[kind])
	}

	arrow := "▸"
	if g.expanded {
		arrow = "▾"
	}
	icon := g.sty.Tool.IconSuccess.Render()
	if failed > 0 {
		icon = g.sty.Tool.IconError.Render()
	}
	head := g.sty.Tool.NameNormal.Render(fmt.Sprintf("Ran %d tools", len(g.items)))
	detail := fmt.Sprintf("(%s)", strings.Join(kinds, ", "))
	if d := g.endedAt.Sub(g.startedAt); !g.startedAt.IsZero() && d >= time.Second {
		detail += " in " + d.Round(time.Second).String()
	}
	out := fmt.Sprintf("%s %s %s %s", arrow, icon, head, g.sty.Tool.ParamMain.Render(detail))
	if failed > 0 {
		out += g.sty.Tool.ParamMain.Render(fmt.Sprintf(", %d failed", failed))
	}
	return out
}

// activityKind buckets a tool name for the summary row.
func activityKind(name string) string {
	switch name {
	case tools.EditToolName, tools.MultiEditToolName, tools.WriteToolName:
		return "edit"
	case tools.ViewToolName, tools.ReadToolName:
		return "read"
	case tools.BashToolName, tools.JobOutputToolName, tools.JobKillToolName:
		return "bash"
	case tools.GrepToolName, tools.GlobToolName, tools.LSToolName:
		return "search"
	}
	return name
}

func pluralKind(kind string, n int) string {
	switch {
	case n == 1, kind == "bash":
		return fmt.Sprintf("%d %s", n, kind)
	case kind == "search":
		return fmt.Sprintf("%d searches", n)
	case kind == "edit", kind == "read":
		return fmt.Sprintf("%d %ss", n, kind)
	}
	return fmt.Sprintf("%d %s", n, kind)
}
