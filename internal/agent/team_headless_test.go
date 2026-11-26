package agent

import (
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/permission"
)

// recordingApprovals records the sessions a coordinator auto-approves and
// answers from that same list, so a test sees both what the coordinator
// approved and what it inherited.
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

func (r *recordingApprovals) IsSessionAutoApproved(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.approved, sessionID)
}

// A teammate of a run with no user must not stop on a permission prompt
// nobody can answer, now that `crush run` waits for it. It inherits the
// approval of its Lead, so a Lead that is not approved, such as a client of
// the server, passes nothing on. An interactive Lead's teammate still asks.
func TestTeammateInheritsTheApprovalOfAHeadlessLead(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		approveLead bool
		wantMember  bool
	}{
		{name: "headless, Lead approved", approveLead: true, wantMember: true},
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
			runner := &teamRunner{c: coord}
			run, err := runner.Start(t.Context(), agentteam.Member{
				ID: "member-1", Name: "worker", Role: agentteam.RoleTeammate, ParentID: lead.ID,
			}, "Work.")
			require.NoError(t, err)
			// The approval is recorded before Start returns. The provider is
			// a closed port, so the turn only has to be stopped here.
			run.Cancel()
			<-run.Done()

			var want []string
			if tt.approveLead {
				want = append(want, lead.ID)
			}
			if tt.wantMember {
				want = append(want, "team-member-1")
			}
			approvals.mu.Lock()
			defer approvals.mu.Unlock()
			require.Equal(t, want, approvals.approved)
		})
	}
}
