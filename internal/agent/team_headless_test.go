package agent

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/permission"
)

// recordingApprovals records the sessions a coordinator auto-approves.
type recordingApprovals struct {
	permission.Service
	mu       sync.Mutex
	approved []string
}

func (r *recordingApprovals) AutoApproveSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.approved = append(r.approved, sessionID)
}

// A teammate of a run with no user must not stop on a permission prompt
// nobody can answer, now that `crush run` waits for it. An interactive
// Lead's teammate still asks.
func TestTeammateOfAHeadlessRunIsAutoApproved(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		want        []string
	}{
		{name: "headless", want: []string{"team-member-1"}},
		{name: "interactive", interactive: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coord := newGateTestCoordinator(t, tt.interactive)
			require.NoError(t, coord.readyWg.Wait())
			approvals := &recordingApprovals{Service: coord.permissions}
			coord.permissions = approvals
			// Without a coder agent the turn fails right after its session
			// is set up, so no model is called.
			coord.agents = map[string]SessionAgent{}

			lead, err := coord.sessions.Create(t.Context(), "Lead")
			require.NoError(t, err)
			runner := &teamRunner{c: coord}
			_, err = runner.Start(t.Context(), agentteam.Member{
				ID: "member-1", Name: "worker", Role: agentteam.RoleTeammate, ParentID: lead.ID,
			}, "Work.")
			require.ErrorContains(t, err, "coder agent not configured")

			approvals.mu.Lock()
			defer approvals.mu.Unlock()
			require.Equal(t, tt.want, approvals.approved)
		})
	}
}
