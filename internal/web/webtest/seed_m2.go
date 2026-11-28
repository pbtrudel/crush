package webtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

// The M2 sessions and the ids the browser specs reach for.
const (
	M2ChatSession   = "s-m2-chat"
	M2LongSession   = "s-m2-long"
	M2StreamSession = "s-m2-stream"
	// M2ChildSession is the agent call's subagent session, built the way
	// session.Service.CreateAgentToolSessionID builds it.
	M2ChildSession  = M2ChatSession + "-a2$$tc-agent"
	M2StreamMessage = M2StreamSession + "-a"
	// M2LongMessages is more than the 100 a snapshot holds, so the long
	// session has an older page to load.
	M2LongMessages = 130
	// M2StreamLines is how many lines the streamed reply grows by.
	M2StreamLines = 24
	m2Model       = "claude-sonnet"
)

const fence = "```"

// m2Markdown exercises the renderer: headings, lists, a task list, a
// table, a safe and an unsafe link, an image, a Go fence, inline and
// display math, and raw HTML that must stay text.
var m2Markdown = strings.Join([]string{
	"## Chat rendering",
	"",
	"- **Bold** and *italic* with `inline code`",
	"- [x] A finished task",
	"- [ ] An open task",
	"",
	"See [Charm](https://charm.land) but not [a trap](javascript:alert(1)).",
	"",
	"![the logo](https://example.com/logo.png)",
	"",
	"| Tool | Exit |",
	"| :--- | ---: |",
	"| bash | 1 |",
	"",
	fence + "go",
	"func main() {",
	"\tfmt.Println(\"hello\")",
	"}",
	fence,
	"",
	"The area is $$\\pi r^2$$ for a circle.",
	"",
	"$$",
	"\\int_0^1 x^2\\,dx = \\frac{1}{3}",
	"$$",
	"",
	`<img src="https://example.com/x.png" onerror="alert(1)"> and <script>alert(1)</script> stay text.`,
}, "\n")

// m2BashOutput is a failed test run long enough for the terminal's 200-line
// clip, so the row offers a "Show all" button: the focusable control the
// paging specs reach for. The escapes and the Exit code line are what the
// bash tool stores, not a mistake, and the last line has no newline because
// the tool appends the working directory itself.
func m2BashOutput() string {
	var out strings.Builder
	fmt.Fprintln(&out, "\x1b[31mFAIL\x1b[0m parser_test.go:12")
	fmt.Fprintln(&out, "\x1b[32mok\x1b[0m   lexer")
	for i := 3; i <= 250; i++ {
		fmt.Fprintf(&out, "parser/parser.go:%d: token %d did not match\n", i, i)
	}
	fmt.Fprint(&out, "\nExit code 1")
	return out.String()
}

// m2StreamTurns is how many short finished turns sit between the bash turn
// and the stream. The virtualizer keeps six rows of overscan around the
// window; at 1280x720 the tall streamed reply fills the window, so at most
// the reply row and its prompt row are visible, and this many rows put the
// bash turn far outside the overscan the focus spec must defeat.
const m2StreamTurns = 6

// SeedM2 gives the fake the sessions the M2 browser specs drive. Call it
// before the gateway starts; PlayM2Stream then streams the busy session's
// reply.
func SeedM2(f *Fake, clock time.Time) {
	at := clock.Unix()
	f.AddSession(session.Session{
		ID: M2ChatSession, Title: "Render the chat", UpdatedAt: at,
		PromptTokens: 42000, CompletionTokens: 1800, Cost: 0.1234,
	})
	f.AddSession(session.Session{ID: M2StreamSession, Title: "Stream a reply", UpdatedAt: at - 60})
	f.AddSession(session.Session{ID: M2LongSession, Title: "Long history", UpdatedAt: at - 120})
	f.AddSession(session.Session{
		ID: M2ChildSession, ParentSessionID: M2ChatSession, Title: "Map the parser package", UpdatedAt: at - 600,
	})
	seedM2Chat(f, at)
	seedM2Child(f, at)
	seedM2Long(f, at)
	seedM2Stream(f, at)
	f.SetSessionBusy(M2StreamSession, true)
}

// metadata is a tool result's metadata string.
func metadata(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func m2Assistant(id string, createdAt int64, parts ...message.ContentPart) message.Message {
	return message.Message{
		ID: id, SessionID: M2ChatSession, Role: message.Assistant, CreatedAt: createdAt, UpdatedAt: createdAt,
		Model: m2Model, Provider: "anthropic", Parts: parts,
	}
}

func m2StreamAssistant(id string, createdAt int64, parts ...message.ContentPart) message.Message {
	return message.Message{
		ID: id, SessionID: M2StreamSession, Role: message.Assistant, CreatedAt: createdAt, UpdatedAt: createdAt,
		Model: m2Model, Provider: "anthropic", Parts: parts,
	}
}

func m2User(sessionID, id string, createdAt int64, text string) message.Message {
	return message.Message{
		ID: id, SessionID: sessionID, Role: message.User, CreatedAt: createdAt, UpdatedAt: createdAt,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}
}

var m2Todos = []map[string]string{
	{"content": "Port the parser", "status": "completed", "active_form": "Porting the parser"},
	{"content": "Write the tests", "status": "in_progress", "active_form": "Writing the tests"},
	{"content": "Update the docs", "status": "pending", "active_form": "Updating the docs"},
}

func seedM2Chat(f *Fake, at int64) {
	f.AddMessage(m2User(M2ChatSession, M2ChatSession+"-u1", at-100, "Show me everything"))
	f.AddMessage(m2Assistant(M2ChatSession+"-a1", at-99,
		message.ReasoningContent{Thinking: "Plan the reply.\nShow every block the renderer knows.", StartedAt: at - 99, FinishedAt: at - 97},
		message.TextContent{Text: m2Markdown},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: at - 95},
	))

	f.AddMessage(m2User(M2ChatSession, M2ChatSession+"-u2", at-90, "Now use every tool"))
	todos := metadata(map[string]any{"todos": m2Todos})
	calls := []message.ToolCall{
		{ID: "tc-bash", Name: "bash", Input: `{"command":"go test ./...","description":"Run the tests"}`},
		{ID: "tc-view", Name: "view", Input: `{"file_path":"/tmp/project/parser.go"}`},
		{ID: "tc-edit", Name: "edit", Input: `{"file_path":"/tmp/project/parser.go","old_string":"func Parse() {}","new_string":"func Parse() error { return nil }"}`},
		{ID: "tc-grep", Name: "grep", Input: `{"pattern":"func Parse","path":"/tmp/project"}`},
		{ID: "tc-glob", Name: "glob", Input: `{"pattern":"**/*.go"}`},
		{ID: "tc-fetch", Name: "fetch", Input: `{"url":"https://charm.land","format":"markdown"}`},
		{ID: "tc-todos", Name: "todos", Input: todos},
		{ID: "tc-mcp", Name: "mcp_github_search_issues", Input: `{"query":"parser bug"}`},
		{ID: "tc-agent", Name: "agent", Input: `{"prompt":"Map the parser package"}`},
	}
	parts := []message.ContentPart{message.TextContent{Text: "Running every tool."}}
	for _, call := range calls {
		call.Finished = true
		parts = append(parts, call)
	}
	parts = append(parts, message.Finish{Reason: message.FinishReasonToolUse, Time: at - 85})
	f.AddMessage(m2Assistant(M2ChatSession+"-a2", at-89, parts...))

	results := []message.ToolResult{
		{ToolCallID: "tc-bash", Name: "bash", Content: m2BashOutput() + "\n\n<cwd>/tmp/project</cwd>", Metadata: metadata(map[string]any{
			"start_time":        (at - 90) * 1000,
			"end_time":          (at - 89) * 1000,
			"output":            m2BashOutput(),
			"exit_code":         1,
			"description":       "Run the tests",
			"working_directory": "/tmp/project",
		})},
		{ToolCallID: "tc-view", Name: "view", Content: "<file>\n     1|package parser\n</file>", Metadata: metadata(map[string]any{
			"file_path": "/tmp/project/parser.go",
			"content":   "package parser\n\nfunc Parse() {}",
		})},
		{ToolCallID: "tc-edit", Name: "edit", Content: "<result>\nContent replaced in file: /tmp/project/parser.go\n</result>\n", Metadata: metadata(map[string]any{
			"additions":   1,
			"removals":    1,
			"old_content": "package parser\n\nfunc Parse() {}\n",
			"new_content": "package parser\n\nfunc Parse() error { return nil }\n",
		})},
		{ToolCallID: "tc-grep", Name: "grep", Content: "Found 1 matches\n/tmp/project/parser.go:\n  Line 3: func Parse() {}", Metadata: metadata(map[string]any{
			"number_of_matches": 1, "truncated": false,
		})},
		{ToolCallID: "tc-glob", Name: "glob", Content: "/tmp/project/parser.go\n/tmp/project/lexer.go", Metadata: metadata(map[string]any{
			"number_of_files": 2, "truncated": false,
		})},
		// The fetch tool wraps a markdown answer in one fence, which the
		// view unwraps before rendering it.
		{ToolCallID: "tc-fetch", Name: "fetch", Content: fence + "\n# Charm\n\nWe build tools for the terminal.\n" + fence},
		{ToolCallID: "tc-todos", Name: "todos", Content: "Todo list updated successfully.\n\nStatus: 1 pending, 1 in progress, 1 completed\nTodos have been modified successfully.", Metadata: metadata(map[string]any{
			"is_new": false, "todos": m2Todos, "just_completed": []string{"Port the parser"},
			"just_started": "Writing the tests", "completed": 1, "total": 3,
		})},
		{ToolCallID: "tc-mcp", Name: "mcp_github_search_issues", Content: "#12 Parser drops comments"},
		{ToolCallID: "tc-agent", Name: "agent", Content: "The parser has two files."},
	}
	answers := make([]message.ContentPart, 0, len(results))
	for _, result := range results {
		answers = append(answers, result)
	}
	f.AddMessage(message.Message{
		ID: M2ChatSession + "-t2", SessionID: M2ChatSession, Role: message.Tool, CreatedAt: at - 84, UpdatedAt: at - 84,
		Parts: answers,
	})
	f.AddMessage(m2Assistant(M2ChatSession+"-a3", at-83,
		message.TextContent{Text: "Every tool answered."},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: at - 80},
	))

	// A canceled turn whose call never answered: the tab must show it
	// interrupted, never running.
	f.AddMessage(m2User(M2ChatSession, M2ChatSession+"-u4", at-70, "Run the slow command"))
	f.AddMessage(m2Assistant(M2ChatSession+"-a4", at-69,
		message.TextContent{Text: "Starting it."},
		message.ToolCall{ID: "tc-slow", Name: "bash", Input: `{"command":"sleep 600","description":"Wait ten minutes"}`, Finished: true},
		message.Finish{Reason: message.FinishReasonCanceled, Time: at - 60},
	))
}

// seedM2Stream seeds the busy session. A finished bash turn with a clipped
// output gives the log the focusable "Show all" control, the short turns
// below push that row past the virtualizer's overscan, and the unfinished
// reply PlayM2Stream grows is the live end.
func seedM2Stream(f *Fake, at int64) {
	f.AddMessage(m2User(M2StreamSession, M2StreamSession+"-u1", at-100, "Run the tests"))
	f.AddMessage(m2StreamAssistant(M2StreamSession+"-a1", at-99,
		message.TextContent{Text: "Running the parser tests."},
		message.ToolCall{ID: "tc-stream-bash", Name: "bash", Input: `{"command":"go test ./...","description":"Run the tests"}`, Finished: true},
		message.Finish{Reason: message.FinishReasonToolUse, Time: at - 99},
	))
	f.AddMessage(message.Message{
		ID: M2StreamSession + "-t1", SessionID: M2StreamSession, Role: message.Tool, CreatedAt: at - 98, UpdatedAt: at - 98,
		Parts: []message.ContentPart{message.ToolResult{
			ToolCallID: "tc-stream-bash",
			Name:       "bash",
			Content:    m2BashOutput() + "\n\n<cwd>/tmp/project</cwd>",
			Metadata: metadata(map[string]any{
				"start_time":        (at - 100) * 1000,
				"end_time":          (at - 99) * 1000,
				"output":            m2BashOutput(),
				"exit_code":         1,
				"description":       "Run the tests",
				"working_directory": "/tmp/project",
			}),
		}},
	})
	f.AddMessage(m2StreamAssistant(M2StreamSession+"-a2", at-97,
		message.TextContent{Text: "The parser tests fail on the new token stream."},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: at - 97},
	))
	for i := range m2StreamTurns {
		created := at - 90 + int64(i*2)
		f.AddMessage(m2User(M2StreamSession, fmt.Sprintf("%s-u%d", M2StreamSession, i+2), created, fmt.Sprintf("Follow-up %d", i+1)))
		f.AddMessage(m2StreamAssistant(fmt.Sprintf("%s-a%d", M2StreamSession, i+3), created+1,
			message.TextContent{Text: fmt.Sprintf("Short answer %d.", i+1)},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: created + 1},
		))
	}
	f.AddMessage(message.Message{
		ID: M2StreamSession + "-u", SessionID: M2StreamSession, Role: message.User, CreatedAt: at - 30, UpdatedAt: at - 30,
		Parts: []message.ContentPart{message.TextContent{Text: "Write a long reply, one line at a time"}},
	})
	f.AddMessage(m2StreamReply(at, 0))
}

func seedM2Child(f *Fake, at int64) {
	child := func(m message.Message) {
		m.SessionID = M2ChildSession
		m.UpdatedAt = m.CreatedAt
		if m.Role == message.Assistant {
			m.Model, m.Provider = m2Model, "anthropic"
		}
		f.AddMessage(m)
	}
	child(m2User(M2ChildSession, M2ChildSession+"-u", at-88, "Map the parser package"))
	child(message.Message{ID: M2ChildSession + "-a1", Role: message.Assistant, CreatedAt: at - 87, Parts: []message.ContentPart{
		message.ToolCall{ID: "tc-child-view", Name: "view", Input: `{"file_path":"/tmp/project/lexer.go"}`, Finished: true},
		message.Finish{Reason: message.FinishReasonToolUse, Time: at - 87},
	}})
	child(message.Message{ID: M2ChildSession + "-t1", Role: message.Tool, CreatedAt: at - 86, Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "tc-child-view", Name: "view", Content: "package parser", Metadata: metadata(map[string]any{
			"file_path": "/tmp/project/lexer.go", "content": "package parser\n\nfunc Lex() {}",
		})},
	}})
	child(message.Message{ID: M2ChildSession + "-a2", Role: message.Assistant, CreatedAt: at - 85, Parts: []message.ContentPart{
		message.TextContent{Text: "The parser has two files."},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: at - 85},
	}})
}

func seedM2Long(f *Fake, at int64) {
	for i := range M2LongMessages {
		created := at - 10_000 + int64(i)
		// Five one-line paragraphs: the 30-row older page is several
		// screens tall at 1280x720, so its prepend is a real scroll.
		text := fmt.Sprintf(
			"History message %d\n\nContext: the parser keeps its own token stream.\n\nNote: this fixture is older than the session.\n\nMore: it makes an older page several screens tall.\n\nEnd of the entry.",
			i,
		)
		if i%2 == 0 {
			f.AddMessage(m2User(M2LongSession, fmt.Sprintf("%s-m%03d", M2LongSession, i), created, text))
			continue
		}
		// Every assistant turn is finished, or the fake's settle step would
		// stamp each one interrupted.
		f.AddMessage(message.Message{
			ID: fmt.Sprintf("%s-m%03d", M2LongSession, i), SessionID: M2LongSession, Role: message.Assistant,
			CreatedAt: created, UpdatedAt: created, Model: m2Model, Provider: "anthropic",
			Parts: []message.ContentPart{
				message.TextContent{Text: text},
				message.Finish{Reason: message.FinishReasonEndTurn, Time: created},
			},
		})
	}
}

// m2StreamReply is the stream session's reply after lines lines. Every
// line is a paragraph, so the row grows by a visible step each time.
func m2StreamReply(at int64, lines int) message.Message {
	var text strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&text, "Stream line %d: the tokens keep arriving.\n\n", i)
	}
	return message.Message{
		ID: M2StreamMessage, SessionID: M2StreamSession, Role: message.Assistant,
		CreatedAt: at - 29, UpdatedAt: at - 29 + int64(lines), Model: m2Model, Provider: "anthropic",
		Parts: []message.ContentPart{message.TextContent{Text: text.String()}},
	}
}

// PlayM2Stream grows the stream session's reply one line per step, then
// ends the turn the way a run does: the finish part, the session idle, and
// the run_complete event the gateway recomputes the status on.
func PlayM2Stream(ctx context.Context, f *Fake, clock time.Time, step time.Duration) {
	at := clock.Unix()
	for lines := 1; lines <= M2StreamLines; lines++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(step):
		}
		reply := m2StreamReply(at, lines)
		f.UpsertMessage(reply)
		f.Emit(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: reply})
	}
	reply := m2StreamReply(at, M2StreamLines)
	reply.Parts = append(reply.Parts, message.Finish{Reason: message.FinishReasonEndTurn, Time: reply.UpdatedAt})
	f.UpsertMessage(reply)
	f.SetSessionBusy(M2StreamSession, false)
	f.Emit(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: reply})
	f.Emit(pubsub.Event[notify.RunComplete]{Type: pubsub.CreatedEvent, Payload: notify.RunComplete{
		SessionID: M2StreamSession, MessageID: reply.ID,
	}})
}
