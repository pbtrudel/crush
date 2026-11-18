package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestInjectionsByKind(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.session("s2", "title")
	hidden := func(session, text string) {
		log.add(session, message.User, message.TextContent{Text: text, Hidden: true})
	}
	hidden("s1", "<env_snapshot>\nToday's date: 1/1/2026")
	log.add("s1", message.User, message.TextContent{Text: "a prompt"})
	hidden("s1", "<system_reminder>abc</system_reminder>")
	// Characters, not bytes: "é" is one character.
	hidden("s1", "<system_reminder>dé</system_reminder>")
	hidden("s1", "<background_job_notice>x</background_job_notice>")
	log.add("s1", message.User, message.SubAgentReport{Label: "l", Output: "o"})
	// A goal opens with no tag: it is a hidden turn, not an injection.
	hidden("s1", "You are pursuing a standing goal across multiple turns.")
	// A tag the report does not know is no injection, and its name is
	// never printed.
	hidden("s1", "<a_tag_from_a_later_build>x")
	// A report is only an injection on a user row.
	log.add("s1", message.Assistant, message.SubAgentReport{Label: "l", Output: "o"})
	hidden("s2", "<system_reminder>abc</system_reminder>")

	require.Equal(t, []Injection{
		{Kind: "system_reminder", Rows: 3, Sessions: 2, Chars: 38 + 37 + 38},
		{Kind: "background_job_notice", Rows: 1, Sessions: 1, Chars: 48},
		{Kind: "env_snapshot", Rows: 1, Sessions: 1, Chars: 37},
		// The report as the model reads it, tags included.
		{Kind: "subagent_report", Rows: 1, Sessions: 1, Chars: 48},
	}, log.report(Options{}).Injections.ByKind)
}

func TestClassifyUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts []message.ContentPart
		row   userRow
		kind  string
	}{
		{"typed", []message.ContentPart{message.TextContent{Text: "fix it"}}, visibleRow, ""},
		{"attachment only", []message.ContentPart{message.TextContent{}, message.BinaryContent{Path: "a.png"}}, visibleRow, ""},
		{"visible beside hidden", []message.ContentPart{message.TextContent{Text: "<system_reminder>x", Hidden: true}, message.TextContent{Text: "fix it"}}, visibleRow, ""},
		{"reminder", []message.ContentPart{message.TextContent{Text: "<system_reminder>x", Hidden: true}}, injectionRow, "system_reminder"},
		{"retired notice", []message.ContentPart{message.TextContent{Text: "<background_subagent_notice>x", Hidden: true}}, injectionRow, "background_subagent_notice"},
		{"report", []message.ContentPart{message.SubAgentReport{Label: "l"}}, injectionRow, "subagent_report"},
		{"plan handoff", []message.ContentPart{message.TextContent{Text: "Implement the plan.", Hidden: true}}, hiddenTurnRow, ""},
		{"tag not at the start", []message.ContentPart{message.TextContent{Text: "see <system_reminder>", Hidden: true}}, hiddenTurnRow, ""},
		{"bang-mode shell", []message.ContentPart{message.ShellCommand{Command: "ls"}}, otherRow, ""},
	}
	for _, tt := range tests {
		row, kind, _ := classifyUser(&message.Message{Role: message.User, Parts: tt.parts})
		require.Equal(t, tt.row, row, tt.name)
		require.Equal(t, tt.kind, kind, tt.name)
	}
}
