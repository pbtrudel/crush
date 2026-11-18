package logreport

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// marker is placed in everything private a log holds: prompts, answers,
// tool input and output, paths, titles and ids. It must appear in neither
// output format, whatever sections the report has.
const marker = "PRIVATE-7f3a9c"

func TestReportNeverPrintsPrivateText(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	session := "session-" + marker
	path := "/home/" + marker + "/a.go"
	log.session(session, "title "+marker)
	log.add(session, message.User, message.TextContent{Text: "prompt " + marker})
	log.add(session, message.User, message.TextContent{Text: "<system_reminder>" + marker, Hidden: true})
	log.add(session, message.Assistant,
		message.ReasoningContent{Thinking: "thinking " + marker},
		message.TextContent{Text: "answer " + marker},
		message.ToolCall{ID: "call-" + marker + "-1", Name: "bash", Input: `{"command":"echo ` + marker + `"}`},
		message.ToolCall{ID: "call-" + marker + "-2", Name: "edit", Input: `{"file_path":"` + path + `","old_string":"` + marker + `"}`},
		message.ToolCall{ID: "call-" + marker + "-3", Name: "view", Input: `{"file_path":"` + path + `","limit":5}`},
		message.ToolCall{ID: "call-" + marker + "-4", Name: "invented_" + marker, Input: `{}`},
		message.ToolCall{ID: "call-" + marker + "-5", Name: "invented_" + marker, Input: `{}`},
		message.Finish{Reason: message.FinishReasonToolUse, Message: marker, Details: marker, Build: "0.100.0-fork.120"},
	)
	log.add(session, message.Tool, message.ToolResult{
		ToolCallID: "call-" + marker + "-1", Name: "bash", Content: "output " + marker,
		Metadata: `{"exit_code":1,"output":"` + marker + `","description":"` + marker + `","working_directory":"` + path + `"}`,
	})
	log.add(session, message.Tool, message.ToolResult{
		ToolCallID: "call-" + marker + "-2", Name: "edit", Content: "error at " + path, IsError: true,
		Metadata: `{"error_kind":"not_read"}`,
	})
	log.add(session, message.Tool, message.ToolResult{ToolCallID: "call-" + marker + "-3", Name: "view", Content: marker})
	log.add(session, message.Tool, message.ToolResult{
		ToolCallID: "call-" + marker + "-4", Name: "invented_" + marker, Content: "no such tool", IsError: true,
		Metadata: `{"error_kind":"unknown_tool"}`,
	})
	// An interrupted result names the call it never answered, so the
	// name it carries is not proof of a tool either.
	log.add(session, message.Tool, message.ToolResult{
		ToolCallID: "call-" + marker + "-5", Name: "invented_" + marker, Content: "interrupted " + marker, IsError: true,
		Metadata: `{"error_kind":"interrupted"}`,
	})
	log.add(session, message.Assistant, message.SubAgentReport{Label: marker, Output: marker},
		message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.120"})
	// A hidden row with a tag the report does not know, a subagent
	// report, a compaction summary and a child session.
	log.add(session, message.User, message.TextContent{Text: "<" + marker + ">", Hidden: true})
	log.add(session, message.User, message.SubAgentReport{Label: marker, Output: marker, Model: marker})
	log.summary(session, message.TextContent{Text: marker},
		message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.120"},
		message.CompactionStats{TokensBefore: 10, Trigger: "guard"})
	child := "team-" + marker
	log.child(child, session)
	log.add(child, message.User, message.TextContent{Text: "brief " + marker})
	log.add(child, message.Assistant, message.TextContent{Text: marker},
		message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.120", TurnEnd: message.TurnEndCompleted})

	r := log.report(Options{})
	require.Equal(t, 14, r.Window.Messages, "the fixture is read")
	require.Zero(t, r.Window.Skipped, "every part of the fixture decodes")
	var text, js bytes.Buffer
	require.NoError(t, r.WriteText(&text))
	require.NoError(t, r.WriteJSON(&js))
	require.NotContains(t, text.String(), marker)
	require.NotContains(t, js.String(), marker)
}
