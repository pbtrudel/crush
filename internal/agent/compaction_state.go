package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// The state block is bounded so a compaction forced by an overflow cannot
// refill the window it just cleared: names only, never file or skill
// contents, and hard caps on every list and on the whole block.
const (
	compactionStateTodoLimit  = 20
	compactionStateTodoBytes  = 200
	compactionStateFileLimit  = 30
	compactionStateSkillLimit = 20
	compactionStateByteLimit  = 4000
)

// compactionStateIntro explains the block to the model in one paragraph.
const compactionStateIntro = "The earlier part of this conversation was compacted. " +
	"This block is the session state at that moment; the summary above describes what happened.\n"

// compactionStateLists is the state block before it is rendered. Each list
// keeps the entries that fit its cap and how many were left out, so the
// rendering can say "and N more" per list.
type compactionStateLists struct {
	todos     []string
	files     []string
	skills    []string
	snapshot  string
	todoMore  int
	fileMore  int
	skillMore int
}

// renderCompactionState renders the hidden state message. It applies the
// per-list caps first and then shrinks the longest list until the whole
// block fits compactionStateByteLimit. It also returns every skill it was
// given, not only the names that fit the cap: the compaction stats carry
// them to the next compaction, and a skill dropped here would be lost for
// good. A session with no state to repeat renders nothing, so no block is
// stored.
func renderCompactionState(todos []session.Todo, snapshot string, files, skills []string) (string, []string) {
	lists := compactionStateLists{
		todos:     compactionStateTodoLines(todos),
		files:     compactionStateFileLines(files),
		skills:    compactionStateSkillLines(skills),
		snapshot:  snapshot,
		todoMore:  max(0, len(todos)-compactionStateTodoLimit),
		fileMore:  max(0, len(files)-compactionStateFileLimit),
		skillMore: max(0, len(skills)-compactionStateSkillLimit),
	}
	if len(lists.todos) == 0 && lists.snapshot == "" && len(lists.files) == 0 && len(lists.skills) == 0 {
		return "", nil
	}
	full := slices.Clone(skills)
	for {
		block := lists.render()
		if len(block) <= compactionStateByteLimit {
			return block, full
		}
		if !lists.shrink() {
			return block, full
		}
	}
}

// compactionStateTodoLines renders the todo list, one line per item, with
// each item's content capped at compactionStateTodoBytes.
func compactionStateTodoLines(todos []session.Todo) []string {
	lines := make([]string, 0, min(len(todos), compactionStateTodoLimit))
	for _, todo := range todos[:min(len(todos), compactionStateTodoLimit)] {
		content := truncateCompactionStateText(todo.Content, compactionStateTodoBytes)
		lines = append(lines, fmt.Sprintf("- [%s] %s", todo.Status, content))
	}
	return lines
}

// compactionStateFileLines keeps the changed file paths that fit the list
// cap.
func compactionStateFileLines(files []string) []string {
	return slices.Clone(files[:min(len(files), compactionStateFileLimit)])
}

// compactionStateSkillLines keeps the skill names that fit the list cap.
func compactionStateSkillLines(skills []string) []string {
	return slices.Clone(skills[:min(len(skills), compactionStateSkillLimit)])
}

// truncateCompactionStateText keeps at most maxBytes of s without cutting a
// UTF-8 sequence in half.
func truncateCompactionStateText(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// render writes the block. Every list is followed by "and N more" when it
// was cut.
func (l *compactionStateLists) render() string {
	var sb strings.Builder
	sb.WriteString(prompt.CompactionStatePrefix + "\n")
	sb.WriteString(compactionStateIntro)

	writeList := func(heading string, items []string, more int, prefix, trailer string) {
		if len(items) == 0 {
			return
		}
		sb.WriteString("\n" + heading + "\n")
		for _, item := range items {
			sb.WriteString(prefix + item + "\n")
		}
		if more > 0 {
			fmt.Fprintf(&sb, "and %d more\n", more)
		}
		if trailer != "" {
			sb.WriteString(trailer + "\n")
		}
	}

	writeList("## Todo list", l.todos, l.todoMore, "", "Keep tracking these with the `todos` tool.")
	if l.snapshot != "" {
		sb.WriteString("\n" + l.snapshot + "\n")
	}
	writeList("## Files changed in this session", l.files, l.fileMore, "- ", "")
	writeList("## Skills summarized away", l.skills, l.skillMore, "- ", "Read them again before relying on them.")
	sb.WriteString("</compaction_state>")
	return sb.String()
}

// shrink drops one entry from the longest list and counts it as omitted.
// It reports false when nothing but the snapshot is left to drop.
func (l *compactionStateLists) shrink() bool {
	candidates := []struct {
		lines *[]string
		more  *int
	}{
		{&l.todos, &l.todoMore},
		{&l.files, &l.fileMore},
		{&l.skills, &l.skillMore},
	}
	best := 0
	var target *[]string
	var more *int
	for _, candidate := range candidates {
		size := 0
		for _, line := range *candidate.lines {
			size += len(line)
		}
		if len(*candidate.lines) > 0 && size > best {
			best, target, more = size, candidate.lines, candidate.more
		}
	}
	if target != nil {
		*target = (*target)[:len(*target)-1]
		*more++
		return true
	}
	if l.snapshot != "" {
		l.snapshot = ""
		return true
	}
	return false
}

// compactionStateSkills returns the names of the skills a compaction
// summarizes away: one that a tool result before the cut carries with no
// result after it, plus the names the previous block listed and nothing has
// read since. The result is sorted so the block stays byte-stable.
func compactionStateSkills(msgs []message.Message, cutIndex int, carried []string) []string {
	before := map[string]bool{}
	after := map[string]bool{}
	for i, msg := range msgs {
		names := after
		if i < cutIndex {
			names = before
		}
		for _, part := range msg.Parts {
			result, ok := part.(message.ToolResult)
			if !ok || result.Metadata == "" {
				continue
			}
			var meta tools.ViewResponseMetadata
			if err := json.Unmarshal([]byte(result.Metadata), &meta); err != nil {
				continue
			}
			if meta.ResourceType != tools.ViewResourceSkill || meta.ResourceName == "" {
				continue
			}
			names[meta.ResourceName] = true
		}
	}
	dropped := make([]string, 0, len(before)+len(carried))
	for name := range before {
		if !after[name] {
			dropped = append(dropped, name)
		}
	}
	for _, name := range carried {
		if !after[name] && !slices.Contains(dropped, name) {
			dropped = append(dropped, name)
		}
	}
	slices.Sort(dropped)
	return dropped
}

// compactionStateText renders the hidden message that re-attaches the
// session state after a compaction, and returns the skills it lists. The
// environment snapshot follows the same rule as the one the first turn
// stores: none for sub-agents and for agents without a config store.
func (a *sessionAgent) compactionStateText(ctx context.Context, sessionID string, todos []session.Todo, skills []string) (string, []string) {
	var snapshot string
	if !a.isSubAgent && a.cfg != nil {
		snapshot = prompt.SessionEnvSnapshot(ctx, a.cfg.WorkingDir(), a.now())
	}
	state, listed := renderCompactionState(todos, snapshot, a.compactionStateFiles(ctx, sessionID), skills)
	return withRoleBlock(a.role, state), listed
}

// createCompactionStateMessage stores the state block as a hidden user
// message, so replay places it after the summary and the preserved tail and
// before the next prompt.
func (a *sessionAgent) createCompactionStateMessage(ctx context.Context, sessionID, text string) (message.Message, error) {
	return a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text, Hidden: true}},
	})
}

// compactionStateCarriedSkills returns the skills the previous state block
// listed. A skill whose instructions an earlier compaction summarized away
// is no longer visible in the messages this compaction reads, so the stats
// of that summary are what keeps it listed until something reads it again.
func (a *sessionAgent) compactionStateCarriedSkills(ctx context.Context, summaryID string) []string {
	if summaryID == "" {
		return nil
	}
	msg, err := a.messages.Get(ctx, summaryID)
	if err != nil {
		return nil
	}
	stats, ok := msg.CompactionStatsPart()
	if !ok {
		return nil
	}
	return stats.ReloadSkills
}

// compactionStateFiles lists the files the session changed, most recent
// first and relative to the working directory, so the model recognizes them
// the way it wrote them. A nil history leaves the list out.
func (a *sessionAgent) compactionStateFiles(ctx context.Context, sessionID string) []string {
	if a.history == nil {
		return nil
	}
	files, err := a.history.ListLatestSessionFiles(ctx, sessionID)
	if err != nil {
		return nil
	}
	slices.SortFunc(files, func(x, y history.File) int {
		if x.UpdatedAt != y.UpdatedAt {
			return int(y.UpdatedAt - x.UpdatedAt)
		}
		return strings.Compare(x.Path, y.Path)
	})
	paths := make([]string, 0, min(len(files), compactionStateFileLimit))
	for _, file := range files {
		paths = append(paths, a.relativePath(file.Path))
	}
	return paths
}

// relativePath renders path relative to the working directory when it is
// inside it.
func (a *sessionAgent) relativePath(path string) string {
	workingDir := ""
	if a.cfg != nil {
		workingDir = a.cfg.WorkingDir()
	}
	if workingDir == "" {
		return path
	}
	rel, err := filepath.Rel(workingDir, path)
	if err != nil || !filepath.IsLocal(rel) {
		// A locality test, not a prefix test: a name that starts with
		// two dots is a file in the directory, not a step above it.
		return path
	}
	return rel
}
