package agentteam

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// pipelineSession is the Lead session the pipeline tests run as.
const pipelineSession = "session-a"

// newBareService opens a Team with no runner, so nothing ever starts.
func newBareService(t *testing.T) *Service {
	t.Helper()
	service, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, nil)
	require.NoError(t, err)
	return service
}

func createTask(t *testing.T, s *Service, req CreateTaskRequest) TaskView {
	t.Helper()
	view, err := s.CreateTask(leadName, pipelineSession, req)
	require.NoError(t, err)
	return view
}

func taskByID(t *testing.T, s *Service, id string) Task {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.state.Tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("task %s not found", id)
	return Task{}
}

// act applies action at the task's current revision.
func act(t *testing.T, s *Service, caller, taskID, action string, edit func(*UpdateTaskRequest)) TaskView {
	t.Helper()
	view, err := s.UpdateTask(caller, updateRequest(s, taskID, action, edit))
	require.NoError(t, err)
	return view
}

func actErr(s *Service, caller, taskID, action string, edit func(*UpdateTaskRequest)) error {
	_, err := s.UpdateTask(caller, updateRequest(s, taskID, action, edit))
	return err
}

func updateRequest(s *Service, taskID, action string, edit func(*UpdateTaskRequest)) UpdateTaskRequest {
	s.mu.Lock()
	revision := 0
	for _, task := range s.state.Tasks {
		if task.ID == taskID {
			revision = task.Revision
		}
	}
	s.mu.Unlock()
	req := UpdateTaskRequest{TaskID: taskID, ExpectedRevision: revision, Action: action, SessionID: pipelineSession}
	if edit != nil {
		edit(&req)
	}
	return req
}

func TestTaskVerifyDefaults(t *testing.T) {
	t.Parallel()
	on, off := true, false
	require.True(t, Task{Role: "implementer"}.needsVerification())
	require.False(t, Task{Role: "reviewer"}.needsVerification())
	require.False(t, Task{}.needsVerification())
	require.False(t, Task{Role: "implementer", Verify: &off}.needsVerification())
	require.True(t, Task{Role: "researcher", Verify: &on}.needsVerification())
	require.False(t, Task{Role: "implementer", Verifies: "task-1", Verify: &on}.needsVerification(),
		"a verification task is never verified itself")
}

func TestCreateTaskRecordsRoleAndVerify(t *testing.T) {
	t.Parallel()
	s := newBareService(t)
	off := false
	view := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: " implementer "})
	require.Equal(t, "implementer", view.Role)
	require.True(t, view.Verify, "an implementer task is verified by default")

	view = createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer", Verify: &off})
	require.False(t, view.Verify)

	_, err := s.CreateTask(leadName, pipelineSession, CreateTaskRequest{Subject: "x", Description: "d", Role: "Not A Role"})
	require.Equal(t, CodeInvalidArgument, ErrorCode(err))
}

func TestEditTaskRoleAndVerify(t *testing.T) {
	t.Parallel()
	s := newBareService(t)
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d"})
	role, on := "researcher", true
	view := act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Role = &role; r.Verify = &on })
	require.Equal(t, "researcher", view.Role)
	require.True(t, view.Verify)

	empty := ""
	view = act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Role = &empty })
	require.Empty(t, view.Role, "an empty role clears it")
}

func TestLeadActionsClearEscalation(t *testing.T) {
	t.Parallel()
	s := newBareService(t)
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "researcher"})
	escalate := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i := range s.state.Tasks {
			if s.state.Tasks[i].ID == task.ID {
				s.state.Tasks[i].Escalated = true
				s.state.Tasks[i].Attempts = 3
			}
		}
	}
	cleared := func(view TaskView) {
		t.Helper()
		require.False(t, view.Escalated)
		require.Zero(t, view.Attempts)
	}

	escalate()
	subject := "Build it"
	cleared(act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Subject = subject }))

	escalate()
	cleared(act(t, s, leadName, task.ID, "reassign", nil))

	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	escalate()
	cleared(act(t, s, leadName, task.ID, "reopen", nil))
}

// TestOldTeamFileLoads pins compatibility: a team.json written before the
// pipeline loads, and a role-less task neither gains view keys nor goes to
// review.
func TestOldTeamFileLoads(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	old := `{"version":1,"team_id":"team-1","members":[],"messages":[],"next_task":1,
"tasks":[{"id":"task-1","revision":1,"subject":"Old","description":"d","status":"pending",
"blocked_by":[],"write_scopes":[],"session_id":"session-a"}]}`
	require.NoError(t, os.WriteFile(path, []byte(old), 0o644))
	s, err := New(path, Config{}, nil)
	require.NoError(t, err)

	view, err := s.GetTask(leadName, pipelineSession, "task-1")
	require.NoError(t, err)
	data, err := json.Marshal(view)
	require.NoError(t, err)
	for _, key := range []string{`"role"`, `"verify"`, `"verifies"`, `"attempts"`, `"escalated"`} {
		require.NotContains(t, string(data), key)
	}

	act(t, s, leadName, "task-1", "claim", nil)
	require.Equal(t, TaskCompleted, act(t, s, leadName, "task-1", "complete", nil).Status)
}

// TestOwnerCannotChangeRoleOrVerify pins that a teammate owning a task cannot
// change the two fields that decide whether its work is checked: only the
// Lead may, so an owner cannot turn verification off or rename its role.
func TestOwnerCannotChangeRoleOrVerify(t *testing.T) {
	t.Parallel()
	// Autostart off: the role task below is claimed by hand, and the
	// dispatcher would otherwise claim it for an implementer first.
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	_, err := s.SpawnTeammate(context.Background(), leadName, SpawnRequest{
		Name: "worker", Description: "d", Prompt: "p", ParentID: pipelineSession,
	})
	require.NoError(t, err)

	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	claimed := act(t, s, "worker", task.ID, "claim", nil)
	require.Equal(t, "worker", claimed.OwnerName)

	off := false
	require.Equal(t, CodeLeadRequired, ErrorCode(actErr(s, "worker", task.ID, "edit",
		func(r *UpdateTaskRequest) { r.Verify = &off })))
	role := "researcher"
	require.Equal(t, CodeLeadRequired, ErrorCode(actErr(s, "worker", task.ID, "edit",
		func(r *UpdateTaskRequest) { r.Role = &role })))

	unchanged := taskByID(t, s, task.ID)
	require.Nil(t, unchanged.Verify, "a refused edit does not store a verify setting")
	require.Equal(t, "implementer", unchanged.Role)
	require.Equal(t, claimed.Revision, unchanged.Revision, "a refused edit does not bump the revision")

	view := act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) {
		r.Verify = &off
		r.Role = &role
	})
	require.False(t, view.Verify)
	require.Equal(t, "researcher", view.Role)

	view = act(t, s, "worker", task.ID, "edit", func(r *UpdateTaskRequest) { r.Subject = "Build it" })
	require.Equal(t, "Build it", view.Subject, "the owner still edits subject and description")
}

func TestClaimedWriteScopes(t *testing.T) {
	t.Parallel()
	s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, newFakeRunner())
	require.NoError(t, err)
	_, err = s.SpawnTeammate(context.Background(), leadName, SpawnRequest{Name: "impl", Description: "d", Prompt: "p", ParentID: pipelineSession})
	require.NoError(t, err)
	a := createTask(t, s, CreateTaskRequest{Subject: "A", Description: "d", WriteScopes: []string{"internal/agent", "docs"}})
	b := createTask(t, s, CreateTaskRequest{Subject: "B", Description: "d", WriteScopes: []string{"docs", "README.md"}})
	createTask(t, s, CreateTaskRequest{Subject: "C", Description: "d", WriteScopes: []string{"internal/ui"}})

	require.Empty(t, s.ClaimedWriteScopes("impl"), "no claimed task, no scopes")
	act(t, s, "impl", a.ID, "claim", nil)
	act(t, s, "impl", b.ID, "claim", nil)
	require.Equal(t, []string{"internal/agent", "docs", "README.md"}, s.ClaimedWriteScopes("impl"))
	require.Empty(t, s.ClaimedWriteScopes(leadName))
	require.Empty(t, s.ClaimedWriteScopes("nobody"))
}
