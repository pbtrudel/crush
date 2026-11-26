package webtest

import (
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
)

// logReportPath is the report the log layout case opens. The tool summary
// truncates at 120 characters before the browser sees it, and what is left
// is still wider than any log column.
const logReportPath = "/Users/dev/workspace/crush/artifacts/web/performance-audit/2026-11-25/exports/archive/final-database-redundancy-report-with-appendix-supporting-charts-raw-measurements-and-query-plans.md"

// logReportLine is one unbroken line from that report. A READ output can
// hold a minified or encoded line wider than any log column.
var logReportLine = strings.Repeat("Zm9vYmFy", 40)

// SeedLogLayout adds the assistant message the log layout case opens: one
// paragraph and a view call whose path is far wider than the log column,
// then the call's output with one unbroken line. Only WEBFAKE_LONG_PATH
// applies it, so no baseline sees the message.
func SeedLogLayout(f *Fake, clock time.Time) {
	at := clock.Unix()
	f.AddMessage(message.Message{
		ID: "log-layout-a", SessionID: "s1", Role: message.Assistant,
		CreatedAt: at, UpdatedAt: at, Model: "claude-sonnet", Provider: "anthropic",
		Parts: []message.ContentPart{
			message.TextContent{Text: "Checking the audit report before the next step."},
			message.ToolCall{ID: "log-layout-tc", Name: "view", Input: `{"file_path":"` + logReportPath + `"}`},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: at},
		},
	})
	f.AddMessage(message.Message{
		ID: "log-layout-t", SessionID: "s1", Role: message.Tool,
		CreatedAt: at + 1, UpdatedAt: at + 1,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "log-layout-tc", Name: "view", Content: logReportLine},
		},
	})
}
