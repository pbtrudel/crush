package model

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// agentDiscoveryWorkspace provides the workspace calls the catalog needs
// without pulling in the full workspace implementation.
type agentDiscoveryWorkspace struct {
	workspace.Workspace
}

func (agentDiscoveryWorkspace) WorkingDir() string { return "/tmp/crush-test" }

func (agentDiscoveryWorkspace) CreateAgentToolSessionID(messageID, toolCallID string) string {
	return messageID + "$$" + toolCallID
}

func TestAgentDescriptorsMergesAndScopesSources(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.session = &session.Session{ID: "root"}
	started := time.Unix(100, 0)
	u.agentRuns = []agentrun.Run{
		{ID: "child", SessionID: "child", ParentSessionID: "root", Name: "run", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning, StartedAt: started},
		{ID: "other", SessionID: "other", ParentSessionID: "other-root", Name: "other", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning},
	}
	u.teamState = agentteam.State{Members: []agentteam.Member{
		{ID: "member-1", Name: "reviewer", SessionID: "team-child", ParentID: "root", Status: agentteam.StatusRunning},
		{ID: "member-2", Name: "old", SessionID: "old-child", ParentID: "root", Status: agentteam.StatusInactive},
	}}

	got := u.agentDescriptors()

	require.Len(t, got, 2)
	require.Equal(t, "child", got[0].SessionID)
	require.Equal(t, agentrun.StatusRunning, got[0].Status)
	require.Equal(t, "team-child", got[1].SessionID)
	require.Equal(t, agentrun.KindTeammate, got[1].Kind)
}

func TestAgentDescriptorsUsesDispatchSessionIDAndRetention(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = agentDiscoveryWorkspace{}
	u.session = &session.Session{ID: "root"}
	call := message.ToolCall{ID: "call-1", Name: agent.AgentDispatchToolName, Input: `{"label":"review","prompt":"inspect"}`}
	item := chat.NewToolMessageItem(u.com.Styles, "parent-message", call, nil, false, u.com.Workspace.WorkingDir()).(*chat.AgentToolMessageItem)
	u.chat.SetMessages(item)

	got := u.agentDescriptors()

	require.Len(t, got, 1)
	require.Equal(t, u.com.Workspace.CreateAgentToolSessionID(item.MessageID(), "call-1"), got[0].SessionID)
	require.False(t, subAgentStatusRetained(chat.DispatchStatus{
		Finished: time.Now().Add(-subAgentIdleRetention - time.Minute),
	}, time.Now()))
}

func TestAgentDescriptorsOrdersRunningFirstAndStable(t *testing.T) {
	t.Parallel()

	t.Run("running then settled", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.session = &session.Session{ID: "root"}
		u.agentRuns = []agentrun.Run{
			{ID: "run-late", SessionID: "run-late", ParentSessionID: "root", Name: "late", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning, StartedAt: time.Unix(200, 0)},
			{ID: "run-zero", SessionID: "run-zero", ParentSessionID: "root", Name: "zero", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning},
			{ID: "done-old", SessionID: "done-old", ParentSessionID: "root", Name: "old", Kind: agentrun.KindSubagent, Status: agentrun.StatusDone, StartedAt: time.Unix(10, 0), FinishedAt: time.Unix(100, 0)},
			{ID: "done-new", SessionID: "done-new", ParentSessionID: "root", Name: "new", Kind: agentrun.KindSubagent, Status: agentrun.StatusDone, StartedAt: time.Unix(20, 0), FinishedAt: time.Unix(300, 0)},
		}

		got := u.agentDescriptors()

		require.Len(t, got, 4)
		require.Equal(t, []string{"run-late", "run-zero", "done-new", "done-old"},
			[]string{got[0].SessionID, got[1].SessionID, got[2].SessionID, got[3].SessionID})
	})

	t.Run("ties break by session id", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.session = &session.Session{ID: "root"}
		started := time.Unix(100, 0)
		finished := time.Unix(200, 0)
		u.agentRuns = []agentrun.Run{
			{ID: "run-b", SessionID: "run-b", ParentSessionID: "root", Name: "b", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning, StartedAt: started},
			{ID: "run-a", SessionID: "run-a", ParentSessionID: "root", Name: "a", Kind: agentrun.KindSubagent, Status: agentrun.StatusRunning, StartedAt: started},
			{ID: "done-b", SessionID: "done-b", ParentSessionID: "root", Name: "b", Kind: agentrun.KindSubagent, Status: agentrun.StatusDone, FinishedAt: finished},
			{ID: "done-a", SessionID: "done-a", ParentSessionID: "root", Name: "a", Kind: agentrun.KindSubagent, Status: agentrun.StatusDone, FinishedAt: finished},
		}

		got := u.agentDescriptors()

		require.Len(t, got, 4)
		require.Equal(t, []string{"run-a", "run-b", "done-a", "done-b"},
			[]string{got[0].SessionID, got[1].SessionID, got[2].SessionID, got[3].SessionID})
	})
}
