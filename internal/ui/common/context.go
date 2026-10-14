package common

import (
	"context"
	"time"
)

// WorkspaceOpTimeout bounds UI-initiated workspace operations: session
// list/delete/save, model switches, and MCP server toggles. Without a
// deadline, a stalled backend (especially the remote client/server mode)
// leaves dialogs and toggles waiting forever with no feedback.
const WorkspaceOpTimeout = 30 * time.Second

// WorkspaceOpContext returns a context with a bounded deadline for
// UI-initiated workspace operations. Callers must always cancel when done.
func WorkspaceOpContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), WorkspaceOpTimeout)
}
