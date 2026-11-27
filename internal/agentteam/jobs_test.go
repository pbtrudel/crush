package agentteam

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/shell"
)

// settleRecord remembers the settle callbacks a Service made.
type settleRecord struct {
	mu      sync.Mutex
	members []string
	session string
}

func (r *settleRecord) add(member, session string) {
	r.mu.Lock()
	r.members = append(r.members, member)
	r.session = session
	r.mu.Unlock()
}

func (r *settleRecord) calls() ([]string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.members...), r.session
}

// newJobSettleService is a Service whose settle callback stops the member's
// background jobs and waits for their checks, the way the coordinator does.
// Every run of its runner is in session, so one test's settle never reaches
// another test's job.
func newJobSettleService(t *testing.T, repo, session string) (*Service, *fakeRunner, *settleRecord) {
	t.Helper()
	runner := newFakeRunnerInSession(session)
	record := &settleRecord{}
	var service *Service
	cfg := Config{
		WorkingDir: repo,
		Autostart:  func() bool { return false },
		SettleMemberJobs: func(ctx context.Context, member string) {
			session := service.MemberSession(member)
			record.add(member, session)
			if session == "" {
				return
			}
			shell.GetBackgroundShellManager().KillSession(ctx, session)
		},
	}
	service, err := New(filepath.Join(t.TempDir(), "team.json"), cfg, runner)
	require.NoError(t, err)
	t.Cleanup(service.Shutdown)
	return service, runner, record
}

// startStrayJob starts a background job in worktree that writes stray.txt,
// out of scope, at once and then waits. Its finisher removes the file the
// way the scope check would.
func startStrayJob(t *testing.T, worktree, session string) *shell.BackgroundShell {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	command := "echo stray > stray.txt && touch " + ready + " && sleep 30"
	job, err := shell.GetBackgroundShellManager().StartWithSession(t.Context(), nil, worktree, nil, command, "stray", session, 10)
	require.NoError(t, err)
	job.SetFinisher(func() string {
		_ = os.Remove(filepath.Join(worktree, "stray.txt"))
		return "Restored: stray.txt."
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = shell.GetBackgroundShellManager().Kill(ctx, job.ID)
	})
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 10*time.Second, 20*time.Millisecond)
	return job
}

// branchTree lists the files at the tip of branch, or "" when it has no
// commit. It never fails the test, so it suits a wait loop.
func branchTree(repo, branch string) string {
	out, err := exec.Command("git", "-C", repo, "ls-tree", "-r", "--name-only", branch).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestSettleStopsBackgroundJobsBeforeCommitting pins W17: a turn that ends
// with a background job still writing in the worktree does not have that
// job's out-of-scope change committed to the task branch.
func TestSettleStopsBackgroundJobsBeforeCommitting(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	service, runner, record := newJobSettleService(t, repo, "session-stray-settle")
	spawnMember(t, service, "impl")
	view := scopedTask(t, service, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/wip.md", "wip\n")
	session := memberByID(t, service, "impl").SessionID
	startStrayJob(t, view.Worktree, session)

	runner.finish("impl")

	require.Eventually(t, func() bool { return strings.Contains(branchTree(repo, view.Branch), "docs/wip.md") },
		10*time.Second, 20*time.Millisecond, "the settle committed the turn's work")
	files := branchTree(repo, view.Branch)
	require.Contains(t, files, "docs/wip.md")
	require.NotContains(t, files, "stray.txt", "the commit waited for the job and its restore")
	require.NoFileExists(t, filepath.Join(view.Worktree, "stray.txt"))
	require.Empty(t, gitIn(t, view.Worktree, "status", "--porcelain"))

	members, recorded := record.calls()
	require.Contains(t, members, "impl")
	require.Equal(t, session, recorded, "the callback names the member and its session")
}

// TestCompleteStopsBackgroundJobsBeforeCommitting pins the other half of
// W17: a complete during a running background job commits only the work
// inside the scopes.
func TestCompleteStopsBackgroundJobsBeforeCommitting(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	service, _, record := newJobSettleService(t, repo, "session-stray-complete")
	spawnMember(t, service, "impl")
	view := scopedTask(t, service, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/wip.md", "wip\n")
	session := memberByID(t, service, "impl").SessionID
	startStrayJob(t, view.Worktree, session)

	act(t, service, "impl", view.ID, "complete", nil)

	require.Equal(t, TaskInReview, taskByID(t, service, view.ID).Status)
	files := branchTree(repo, view.Branch)
	require.Contains(t, files, "docs/wip.md")
	require.NotContains(t, files, "stray.txt", "the commit waited for the job and its restore")
	require.NoFileExists(t, filepath.Join(view.Worktree, "stray.txt"))
	require.Empty(t, gitIn(t, view.Worktree, "status", "--porcelain"))
	members, _ := record.calls()
	require.Contains(t, members, "impl")
}
