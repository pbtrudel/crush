package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/permission"
)

// subAgentSessionParams returns the parameters the agent tool hands
// createSubAgentSession for a run whose parent session is parentID.
func subAgentSessionParams(parentID string) subAgentParams {
	return subAgentParams{
		SessionID:      parentID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		SessionTitle:   "Subagent",
	}
}

// A subagent runs in a session of its own, so in a run with no user it must
// inherit the approval of its Lead the way a teammate does; otherwise its
// first tool that needs permission waits on a prompt nobody can answer. A
// Lead that is not approved, such as a client of the server, passes nothing
// on, and an interactive run's subagent still asks.
func TestSubAgentInheritsTheApprovalOfAHeadlessLead(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		approveLead bool
		wantSub     bool
	}{
		{name: "headless, Lead approved", approveLead: true, wantSub: true},
		{name: "headless, Lead not approved"},
		{name: "interactive, Lead approved", interactive: true, approveLead: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coord := newGateTestCoordinator(t, tt.interactive)
			require.NoError(t, coord.readyWg.Wait())
			approvals := &recordingApprovals{Service: coord.permissions}
			coord.permissions = approvals

			lead, err := coord.sessions.Create(t.Context(), "Lead")
			require.NoError(t, err)
			if tt.approveLead {
				approvals.AutoApproveSession(lead.ID)
			}

			sess, err := coord.createSubAgentSession(t.Context(), coord.sessions.CreateAgentToolSessionID("msg-1", "call-1"), subAgentSessionParams(lead.ID))
			require.NoError(t, err)
			require.Equal(t, coord.sessions.CreateAgentToolSessionID("msg-1", "call-1"), sess.ID)

			var want []string
			if tt.approveLead {
				want = append(want, lead.ID)
			}
			if tt.wantSub {
				want = append(want, sess.ID)
			}
			approvals.mu.Lock()
			defer approvals.mu.Unlock()
			require.Equal(t, want, approvals.approved)
		})
	}
}

// A subagent's tool call in a run with no user is answered without a
// prompt: the real permission service grants it because the subagent
// inherited its Lead's approval. The short deadline stands in for the signal
// that is the only thing that ends the wait in a real run.
func TestSubAgentPermissionIsAnsweredInAHeadlessRun(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())
	coord.permissions.SetPermissionMode(permission.PermissionModeNormal)

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	coord.permissions.AutoApproveSession(lead.ID)

	sess, err := coord.createSubAgentSession(t.Context(), coord.sessions.CreateAgentToolSessionID("msg-1", "call-1"), subAgentSessionParams(lead.ID))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	granted, err := coord.permissions.Request(ctx, permission.CreatePermissionRequest{
		SessionID:   sess.ID,
		ToolCallID:  "call-1",
		ToolName:    "write",
		Description: "Write file.txt",
		Action:      "create",
		Path:        "file.txt",
	})
	require.NoError(t, err, "the request waited on a prompt nobody can answer")
	require.True(t, granted)
}
