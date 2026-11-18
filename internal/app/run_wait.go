package app

import (
	"context"
	"fmt"
	"log/slog"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent"
)

// runResponse is what the non-interactive command learns from its run.
type runResponse struct {
	result *fantasy.AgentResult
	err    error
	// tree is what the session's tree left behind when the wait ended.
	tree agent.SessionTree
}

// runAndWait runs prompt on sessionID, then waits for the session's tree to
// go idle, so the command does not end while a teammate the run started is
// still working. The response carries the first run's outcome: a teammate's
// report does not wake the session. The wait ends early only when ctx is
// done, which is how a signal still stops the command at once.
//
// Limits are set on the tree before the prompt runs, so the timeout counts
// from here. A tree stopped by a limit refuses new runs and goes idle, so
// the same wait ends it.
func runAndWait(ctx context.Context, coordinator agent.Coordinator, sessionID, prompt string, limits agent.RunLimits) runResponse {
	if limiter, ok := coordinator.(agent.TreeLimiter); ok && limits != (agent.RunLimits{}) {
		limiter.LimitSessionTree(sessionID, limits)
	}
	var response runResponse
	response.result, response.err = coordinator.Run(ctx, sessionID, prompt)
	if response.err != nil {
		response.err = fmt.Errorf("failed to start agent processing stream: %w", response.err)
	}
	watcher, ok := coordinator.(agent.TreeWatcher)
	if !ok {
		return response
	}
	tree, err := watcher.WaitForSessionTree(ctx, sessionID)
	response.tree = tree
	if err != nil {
		slog.Debug("Non-interactive: stopped waiting for the session tree", "session_id", sessionID, "error", err)
		return response
	}
	slog.Info("Session tree is idle", "session_id", sessionID, "undelivered_reports", tree.Team.UnreadByLead)
	return response
}
