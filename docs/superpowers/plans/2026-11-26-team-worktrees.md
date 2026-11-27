# Agent Team Worktrees, Bash Scopes and Pipeline Follow-ups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every scoped team task its own git worktree that its teammate works in, merge verified work into the Lead's tree on its own, hold `bash` to the claimed write scopes, and ship four pipeline fixes (LSP path resolution, durable failure notes, the Shutdown window, fork context after compaction).

**Architecture:** All git work is driven through `internal/gitutil` (new commit, merge, prefix and file snapshot/restore helpers) and runs with no service lock held: the service validates under `s.mu`, runs git under its own `gitMu`, then records the result in a compare-and-set write. In `internal/agent`, a new `worktreeTool` wrapper resolves the calling teammate's worktree on every call and hands it to the tool on the context (`tools.WithWorkingDir`), so each tool reads its working directory per call and no tool `Info()`, tool list, tool order or system prompt changes. The scope gate also snapshots the worktree around each teammate `bash` call (and each background job) and restores out-of-scope changes.

**Tech Stack:** Go (module `github.com/charmbracelet/crush`), `charm.land/fantasy`, the git CLI through `internal/gitutil`, `internal/shell` background jobs, testify `require`.

**Spec:** `docs/superpowers/specs/2026-11-26-team-worktrees-design.md`. The spec's line numbers are at `main` a52ea5f16; this plan was written at a167f920a. Line numbers drift as tasks land: find every anchor by the quoted code, not the number.

## Global Constraints

- Prompt caching is first. No tool `Info()`, tool list, tool order or system prompt may differ between the Lead and any teammate, in a worktree or not. The worktree path travels in messages (the brief, the task view, notices) and on the tool call's context only.
- The only system prompt difference stays the existing `teamIdentity` suffix; `agentteam.Policy` is not changed by this plan.
- Every wrapper (`roleGatedTool`, `worktreeTool`, `scopeGatedTool`, `hookedTool`) forwards `Info()`, `ProviderOptions()`, `SetProviderOptions()` and `MCP()` to its inner tool.
- A teammate tool is wrapped, outermost first: role gate, worktree wrapper, scope gate, hook wrapper, tool.
- `Runner.Start` (and so `s.launch`) and every git command never run while `s.mu`, the in-process write slot or the file lock is held. Team service git commands that touch the repository (create, commit, merge, remove) also take `s.gitMu`, which is never held with those locks. Never call `s.write` with `s.mu` held.
- New durable fields (`Task.Worktree`, `Task.Branch`, `Task.LastFailure`, `Member.ContextSummary`) and new `TaskView` fields are `omitempty`, so an old `team.json` loads unchanged and a task without a worktree serializes exactly as today.
- Outside a git repository, or for a task without write scopes, behavior is exactly as after PR #193.
- No new module dependencies.
- Format every Go file with `gofumpt -w <file>` (fall back to `goimports`, then `gofmt`).
- No em dashes in source, comments, tests, docs or commit messages.
- Log messages start with a capital letter (`scripts/check_log_capitalization.sh`).
- Comments on their own line start with a capital letter, end with a period, and wrap at 78 columns.
- Tests use testify `require`, `t.Parallel()` where the package already does, `t.TempDir()` for directories, `t.Setenv()` for environment. File permissions in octal (`0o644`, `0o755`).
- A test that needs git creates a real repository in `t.TempDir()` (`git init -b main`, local `user.name`, `user.email`, `commit.gpgsign false`, one initial commit) and skips when `git` is not on `PATH`.
- Semantic commit subjects, one line, no attribution trailers.

## Review Focus

1. The Lead's tree is dirty where the branch changes it, or on a detached `HEAD`, when a task passes: the merge must not run or must be aborted, the Lead's files must be untouched, and the task must stay `in_review`, escalated, with a notice naming the branch; after the Lead fixes the tree, `complete` merges it (Task 9 test `TestMergeRefusedOnADirtyOrDetachedLeadTree`).
2. One teammate `bash` command edits a file inside its scopes and one outside: only the outside file is restored and listed, the inside edit stays (Task 10 test `TestBashScopeCheckKeepsInScopeEditsOfAMixedCommand`).
3. A task that went to review is reopened: it keeps its worktree, and the next implementer (a different member) is started in it and sees the prior commit (Task 9 test `TestReopenedTaskShowsPriorWorkToTheNextImplementer`).
4. Worktree creation fails (for example the sibling worktrees path is a file): the claim is released with no attempt charged, the auto-spawned member is free again rather than stuck provisioning, the Lead is told once, and the dispatcher does not loop on it until the Lead edits the task (Task 7 test `TestWorktreeCreationFailureReleasesWithoutAnAttempt`).
5. Shutdown lands while a member's held settle is waiting for its resume: the held task goes back to pending with no attempt and no owner, and the drained mail is pending again, both when the resume is refused by shutdown and when it fails after shutdown began (Task 3 tests `TestShutdownDuringAHeldResumeReleasesTheTask` and `TestRefusedResumeAfterShutdownReleasesTheTask`).

---

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/agent/tools/lsp_helpers.go` | `resolveToolPath`; used by the three LSP tools (Task 1) |
| `internal/agent/tools/lsp_replace_symbol.go`, `lsp_symbols.go`, `diagnostics.go` | Resolve `file_path` against the working directory (Task 1) |
| `internal/agentteam/team.go` | `Task.LastFailure`, `Task.Worktree`, `Task.Branch`, `Member.ContextSummary`, `TaskView` fields, `Config.WorkingDir`, `CodeTaskGit`, Service fields, held-release in `watch`, `launch` records the summary, `UpdateTask` git phases, settle changes |
| `internal/agentteam/verify.go` | Failure notes, verdict result, verification description with the worktree |
| `internal/agentteam/dispatch.go` | `taskBrief` (notes, worktree), `dispatchStart.task`, worktree placement before `launch` |
| `internal/agentteam/worktree.go` (create) | Repo detection, `Workspace`, `MemberWorkspace`, create, record, discard, remove, creation failure release |
| `internal/agentteam/merge.go` (create) | Commit before complete, commit held work, merge, settle merge, Lead accept |
| `internal/gitutil/plumbing.go` (create) | `RepoPrefix`, `CommitAll`, `MergeNoFF`, `MergeError` |
| `internal/gitutil/filesnapshot.go` (create) | `SnapshotFiles`, `ChangedPaths`, `RestorePaths` and their types |
| `internal/agent/tools/workdir.go` (create) | `WithWorkingDir`, `WorkingDirFrom` |
| `internal/agent/tools/{bash,download,edit,multiedit,write,view,glob,grep,ls,apply_patch}.go` | Read the working directory per call |
| `internal/agent/worktree_tool.go` (create) | `worktreeTool`, `placeToolsInWorktrees`, workspace on the context, LSP refusal |
| `internal/agent/coordinator.go` | Wrap teammate tools; pass the working directory to the Team service; diagnostics constructor |
| `internal/agent/scope_gate.go` | Scope gate reads the per-call working directory; bash check |
| `internal/agent/team.go` | `teammatePromptFor`, `teamRun.ContextSummary` |
| `internal/shell/background.go` | `SetFinisher`, note on job output |
| `docs/agent-teams.md`, `docs/hooks/README.md` | Documentation |

## Spec interpretations

These are the places the spec is silent or ambiguous; every task below follows them.

- **Per-call working directory.** The spec says `teamRunner.Start` builds the teammate's tools with the worktree as their working directory, and that a member's tools follow the worktree of its current scoped task. A teammate the Lead spawned claims its task during its turn, after its tools were built, so a working directory fixed at build time would leave it editing the Lead's tree. The build therefore installs a per-call resolver: `buildTools` wraps every teammate tool in `worktreeTool`, which asks the service for the member's workspace on each call and passes it to the tool on the context. Tool construction, `Info()` and order are unchanged.
- **Unverified scoped tasks merge too.** The spec merges on `pass` and on Lead accept. A scoped task with `verify` false never sees either, so its branch would never reach the Lead. Such a task's `complete` (or settled turn) moves it to `in_review` and merges at once; the merge outcome completes it or leaves it escalated in review, exactly like `pass`.
- **Pass order.** The verdict write records the pass and leaves the original `in_review`; the merge runs after it with no lock held; a compare-and-set write then completes the original or escalates it. The Lead accept path validates read-only, merges, then applies the normal `complete` write. Both match the spec's "validate, git, CAS write" rule; the pass path validates in its own write so no merge runs for a verdict the service would refuse.
- **Creation failure does not loop.** A released task is immediately dispatchable again, so a persistent failure would notice the Lead on every pass. The service remembers, in memory, the revision a task was released at; the dispatcher skips it until its revision changes (any Lead edit) or the process restarts.
- **Lead in a subdirectory.** When the Lead's working directory is a subdirectory of the repository, the teammate's working directory is the same subdirectory inside the worktree (`git rev-parse --show-prefix`), and git status paths (repository-relative) are mapped to scope paths (working-directory-relative) before the bash check compares them.
- **Status format.** The spec names `--porcelain=v1`; the existing `gitutil.Snapshot` reads `--porcelain=v2 -z --untracked-files=all`, which lists the same paths. The bash check reuses it and adds the content hashes.
- **HEAD moving inside bash.** A command that commits removes its files from the status list. When `HEAD` moved, the files the new commits touched count as changed, and a clean file is restored from the `HEAD` taken before the call.
- **Snapshot failure.** If the worktree cannot be recorded before a scoped `bash` call, the call is refused with the error (fail closed), since an unchecked call would break the guarantee.
- **Recording the worktree does not bump the revision.** It is service bookkeeping; the brief's `expected_revision` stays valid.
- **A merged task forgets its worktree.** A successful merge removes the worktree and clears `Worktree` and `Branch`, so a completed task the Lead reopens later gets a fresh worktree instead of a deleted path.
- **Hooks.** The hook runner's `cwd` is the Lead's working directory for every agent (`hooks.NewRunner` takes it once). A teammate's hooks keep that `cwd`; relative paths in its `tool_input` resolve against its worktree. The docs say so; the payload is unchanged.
- **`release` keeps the worktree** like `reopen` and `fail`; only `delete` and a successful merge remove it.

## Task order and parallelism

Tasks 1, 5 and 6 touch disjoint files and may run in parallel with each other and with Tasks 2 to 4. Tasks 2, 3, 4, 7 and 9 all edit `internal/agentteam/team.go` and must run in that order. Task 4 also edits `internal/agent/team.go`. Task 7 needs Task 5 (`RepoPrefix`). Tasks 1 and 8 both edit `internal/agent/coordinator.go`; run 8 after 1. Task 8 needs Task 7 (`Workspace`, `MemberWorkspace`, `TaskView.Worktree`). Task 9 needs Tasks 5 and 7. Task 10 needs Tasks 6 and 8 and edits `scope_gate.go` after Task 8. Task 11 needs everything; Task 12 is last.

---

### Task 1: LSP tools resolve relative paths against the working directory

**Files:**
- Modify: `internal/agent/tools/lsp_helpers.go` (add `resolveToolPath` after `findLSPClient`)
- Modify: `internal/agent/tools/lsp_replace_symbol.go` (`NewReplaceSymbolTool` closure, the `params.FilePath == ""` check)
- Modify: `internal/agent/tools/lsp_symbols.go` (`NewSymbolsTool` closure)
- Modify: `internal/agent/tools/diagnostics.go` (`NewDiagnosticsTool` signature and closure)
- Modify: `internal/agent/coordinator.go` (`tools.NewDiagnosticsTool(c.lspManager)` in `buildTools`)
- Test: `internal/agent/tools/lsp_helpers_test.go`

**Interfaces:**
- Consumes: `filepathext.SmartJoin`, `home.Long`, `config.ConfigStore.WorkingDir()`.
- Produces:
  - `func resolveToolPath(workingDir, path string) string` (unexported, package `tools`)
  - `func configWorkingDir(cfg *config.ConfigStore) string` (unexported)
  - `func NewDiagnosticsTool(cfg *config.ConfigStore, lspManager *lsp.Manager) fantasy.AgentTool` (new first parameter)

- [ ] **Step 1: Write the failing tests**

Append to `internal/agent/tools/lsp_helpers_test.go` (it already imports `os`, `path/filepath`, `testing`, `fantasy`, `config`, `lsp` and `require`; add any that are missing):

```go
// TestResolveToolPath pins the path rule the LSP tools share with edit: a
// relative path joins the working directory, an absolute one is kept, and
// an empty one stays empty so diagnostics still means the whole project.
func TestResolveToolPath(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	require.Equal(t, filepath.Join(wd, "a.go"), resolveToolPath(wd, "a.go"))
	require.Equal(t, filepath.Join(wd, "sub", "a.go"), resolveToolPath(wd, "./sub/a.go"))
	abs := filepath.Join(t.TempDir(), "b.go")
	require.Equal(t, abs, resolveToolPath(wd, abs))
	require.Empty(t, resolveToolPath(wd, ""))
}

// TestLSPToolsResolveRelativePaths pins the follow-up from the pipeline
// branch: lsp_symbols and lsp_replace_symbol used a relative file_path as
// is, so it resolved against the process directory and a workspace file
// read as outside the workspace.
func TestLSPToolsResolveRelativePaths(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	cfg := newLSPMissStore(t, workspace)
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "a.zzz"), []byte("zzz\n"), 0o644))
	want := "no LSP client handles file " + filepath.Join(workspace, "a.zzz") + ": no language server is configured for .zzz files"

	symbols := NewSymbolsTool(cfg, lsp.NewManager(cfg))
	resp, err := symbols.Run(t.Context(), fantasy.ToolCall{ID: "s1", Name: SymbolsToolName, Input: `{"file_path":"a.zzz"}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, want)

	replace := NewReplaceSymbolTool(cfg, lsp.NewManager(cfg), nil, nil, nil)
	resp, err = replace.Run(t.Context(), fantasy.ToolCall{
		ID: "r1", Name: ReplaceSymbolToolName,
		Input: `{"symbol":"X","file_path":"a.zzz","replacement":"y"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, want)
}

// TestDiagnosticsToolTakesTheConfig pins the constructor the coordinator
// calls: diagnostics needs the working directory to resolve file_path.
func TestDiagnosticsToolTakesTheConfig(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	cfg := newLSPMissStore(t, workspace)
	tool := NewDiagnosticsTool(cfg, lsp.NewManager(cfg))
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "d1", Name: DiagnosticsToolName, Input: `{"file_path":"a.go"}`})
	require.NoError(t, err)
	require.False(t, resp.IsError)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/tools/ -run 'TestResolveToolPath|TestLSPToolsResolveRelativePaths|TestDiagnosticsToolTakesTheConfig'`
Expected: FAIL to compile with `undefined: resolveToolPath` and `too many arguments in call to NewDiagnosticsTool`.

- [ ] **Step 3: Add the helpers**

In `internal/agent/tools/lsp_helpers.go`, add to the imports `"github.com/charmbracelet/crush/internal/config"`, `"github.com/charmbracelet/crush/internal/filepathext"` and `"github.com/charmbracelet/crush/internal/home"`, and add after `findLSPClient`:

```go
// resolveToolPath resolves a file_path argument the way edit does: a
// leading ~ expands to the home directory and a relative path joins
// workingDir. An empty path stays empty, so a tool that reads it as "the
// whole project" keeps doing so.
func resolveToolPath(workingDir, path string) string {
	if path == "" {
		return ""
	}
	return filepathext.SmartJoin(workingDir, home.Long(path))
}

// configWorkingDir is the workspace the LSP tools resolve paths against, or
// "" without a config, which leaves a relative path as it is.
func configWorkingDir(cfg *config.ConfigStore) string {
	if cfg == nil {
		return ""
	}
	return cfg.WorkingDir()
}
```

- [ ] **Step 4: Resolve `file_path` in the three tools**

In `internal/agent/tools/lsp_symbols.go`, inside the `NewSymbolsTool` closure, replace:

```go
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}
			lspManager.Start(ctx, params.FilePath)
```

with:

```go
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}
			params.FilePath = resolveToolPath(configWorkingDir(cfg), params.FilePath)
			lspManager.Start(ctx, params.FilePath)
```

In `internal/agent/tools/lsp_replace_symbol.go`, inside the `NewReplaceSymbolTool` closure, replace:

```go
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			action := params.Action
```

with:

```go
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}
			// Every later use (the LSP lookup, the read, the permission
			// request, the write) needs the same resolved path.
			params.FilePath = resolveToolPath(configWorkingDir(cfg), params.FilePath)

			action := params.Action
```

In `internal/agent/tools/diagnostics.go`, add `"github.com/charmbracelet/crush/internal/config"` to the imports and replace `NewDiagnosticsTool` with:

```go
func NewDiagnosticsTool(cfg *config.ConfigStore, lspManager *lsp.Manager) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		DiagnosticsToolName,
		diagnosticsDescription,
		func(ctx context.Context, params DiagnosticsParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			// The diagnostics a server reports are keyed by absolute path,
			// so a relative file_path never matched its own file.
			filePath := resolveToolPath(configWorkingDir(cfg), params.FilePath)
			notifyLSPs(ctx, lspManager, filePath)
			output := getDiagnostics(filePath, lspManager)
			return fantasy.NewTextResponse(output), nil
		},
	)
}
```

In `internal/agent/coordinator.go`, in `buildTools`, replace `tools.NewDiagnosticsTool(c.lspManager),` with `tools.NewDiagnosticsTool(c.cfg, c.lspManager),`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `gofumpt -w internal/agent/tools/lsp_helpers.go internal/agent/tools/lsp_symbols.go internal/agent/tools/lsp_replace_symbol.go internal/agent/tools/diagnostics.go internal/agent/tools/lsp_helpers_test.go internal/agent/coordinator.go && go test ./internal/agent/tools/ && go build ./...`
Expected: PASS, and the build succeeds.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/tools/lsp_helpers.go internal/agent/tools/lsp_symbols.go internal/agent/tools/lsp_replace_symbol.go internal/agent/tools/diagnostics.go internal/agent/tools/lsp_helpers_test.go internal/agent/coordinator.go
git commit -m "fix: resolve LSP tool paths against the working directory"
```

---

### Task 2: Verification failure notes survive on the task

**Files:**
- Modify: `internal/agentteam/team.go` (`Task` struct, after `RoleReported`; `applyTaskUpdateLocked` `case "edit":`)
- Modify: `internal/agentteam/verify.go` (`applyVerdictLocked`)
- Modify: `internal/agentteam/dispatch.go` (`taskBrief`)
- Test: `internal/agentteam/verify_test.go`

**Interfaces:**
- Consumes: `truncateText(s string, n int) string` (`team.go`), the test helpers `act`, `taskByID`, `verificationTaskFor`, `refusingStartRunner`, `runRecorded`, `memberIDOrEmpty`.
- Produces:
  - `Task.LastFailure string` with JSON key `last_failure,omitempty`
  - `const maxFailureNotes = 8 << 10`
  - `taskBrief` appends `"\n\nThe last verification failed:\n\n" + task.LastFailure` when set

- [ ] **Step 1: Write the failing tests**

Append to `internal/agentteam/verify_test.go` (add `"encoding/json"` to its imports):

```go
// TestFailVerdictKeepsItsNotesOnTheTask pins that a fail verdict's notes are
// stored on the original, capped, and cleared by a pass and by a Lead edit.
func TestFailVerdictKeepsItsNotesOnTheTask(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, check.ID, "claim", nil)
	act(t, s, leadName, check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "TestX fails" })
	got := taskByID(t, s, task.ID)
	require.Equal(t, "TestX fails", got.LastFailure)
	require.Contains(t, taskBrief(got), "\n\nThe last verification failed:\n\nTestX fails")

	act(t, s, leadName, task.ID, "complete", nil)
	check = verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, check.ID, "claim", nil)
	act(t, s, leadName, check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = strings.Repeat("x", 9000) })
	require.Len(t, taskByID(t, s, task.ID).LastFailure, maxFailureNotes, "the notes are capped at 8 KiB")

	act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Description = "Build it right." })
	require.Empty(t, taskByID(t, s, task.ID).LastFailure, "a Lead edit clears the notes")

	act(t, s, leadName, task.ID, "complete", nil)
	check = verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, check.ID, "claim", nil)
	act(t, s, leadName, check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "Still failing" })
	act(t, s, leadName, task.ID, "complete", nil)
	check = verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, check.ID, "claim", nil)
	act(t, s, leadName, check.ID, "pass", nil)
	got = taskByID(t, s, task.ID)
	require.Equal(t, TaskCompleted, got.Status)
	require.Empty(t, got.LastFailure, "a pass clears the notes")
	require.NotContains(t, taskBrief(got), "The last verification failed")
}

// TestFailureNotesReachTheNextImplementer pins the follow-up: the owner gets
// the notes as mail, and when its resume fails the mail is gone with it, so
// the implementer the dispatcher starts next must read them in its brief.
func TestFailureNotesReachTheNextImplementer(t *testing.T) {
	t.Parallel()
	runner := &refusingStartRunner{fakeRunner: newFakeRunner()}
	s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, runner)
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)

	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	require.Eventually(t, func() bool { return runRecorded(s, "implementer-1") }, time.Second, 10*time.Millisecond)
	act(t, s, "implementer-1", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, ownerName(t, s, check.ID), check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "TestX fails" })

	runner.failFor("implementer-1")
	runner.finish("implementer-1")
	require.Eventually(t, func() bool {
		got := taskByID(t, s, task.ID)
		return got.OwnerID != "" && got.OwnerID == memberIDOrEmpty(s, "implementer-2") && runRecorded(s, "implementer-2")
	}, time.Second, 10*time.Millisecond, "the released task reaches a fresh implementer")
	require.Contains(t, runner.lastPrompt(), "The last verification failed:\n\nTestX fails")
}

// TestLastFailureIsOmittedWhenEmpty pins that an old team.json and a task
// that never failed serialize the same as before.
func TestLastFailureIsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(Task{ID: "task-1"})
	require.NoError(t, err)
	require.NotContains(t, string(data), "last_failure")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agentteam/ -run 'TestFailVerdictKeepsItsNotesOnTheTask|TestFailureNotesReachTheNextImplementer|TestLastFailureIsOmittedWhenEmpty'`
Expected: FAIL to compile with `got.LastFailure undefined` and `undefined: maxFailureNotes`.

- [ ] **Step 3: Add the field**

In `internal/agentteam/team.go`, in the `Task` struct, after the `RoleReported` field, add:

```go
	// LastFailure holds the notes of the latest failed verification, so the
	// next implementer's brief carries them even when the mail that took
	// them to the owner is gone. A pass and a Lead edit clear it.
	LastFailure string `json:"last_failure,omitempty"`
```

In the same file, in `applyTaskUpdateLocked`, at the end of `case "edit":` (after the `if req.Verify != nil { ... }` block), add:

```go
		if lead {
			// The Lead rewrote the task, so the old verdict no longer
			// describes it.
			next.LastFailure = ""
		}
```

- [ ] **Step 4: Store and clear the notes in the verdict**

In `internal/agentteam/verify.go`, add after the imports:

```go
// maxFailureNotes caps the failure notes a task keeps for its next brief.
const maxFailureNotes = 8 << 10
```

In `applyVerdictLocked`, replace:

```go
	original.Revision++
	if verdict == "pass" {
		original.Status = TaskCompleted
		s.state.Tasks[index] = original
		return original.Subject, nil, nil, ""
	}
	original.Attempts++
```

with:

```go
	original.Revision++
	if verdict == "pass" {
		original.LastFailure = ""
		original.Status = TaskCompleted
		s.state.Tasks[index] = original
		return original.Subject, nil, nil, ""
	}
	original.LastFailure = truncateText(notes, maxFailureNotes)
	original.Attempts++
```

- [ ] **Step 5: Put the notes in the brief**

In `internal/agentteam/dispatch.go`, replace `taskBrief` with:

```go
// taskBrief is the message a dispatched member starts from. It is per-task
// text in a message, so the role block before it on a first turn stays the
// shared prefix of every sibling with that role.
func taskBrief(task Task) string {
	last := fmt.Sprintf("Complete it with team_task_update (action \"complete\", expected_revision %d) when the acceptance criteria are met.", task.Revision)
	if task.Verifies != "" {
		last = `Record your verdict with team_task_update, action "pass", or action "fail" with notes describing what failed.`
	}
	brief := fmt.Sprintf("Task %s on the shared board was claimed for you: %s\n\n%s\n\nWrite scopes: %s\n%s",
		task.ID, task.Subject, task.Description, scopesText(task), last)
	if task.LastFailure != "" {
		brief += "\n\nThe last verification failed:\n\n" + task.LastFailure
	}
	return brief
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `gofumpt -w internal/agentteam/team.go internal/agentteam/verify.go internal/agentteam/dispatch.go internal/agentteam/verify_test.go && go test ./internal/agentteam/`
Expected: PASS, including the existing `TestDispatchSpawnsAMemberForARoleTask` exact-prompt assertion (a task with no notes gets the old brief byte for byte).

- [ ] **Step 7: Commit**

```bash
git add internal/agentteam/team.go internal/agentteam/verify.go internal/agentteam/dispatch.go internal/agentteam/verify_test.go
git commit -m "fix: keep verification failure notes on the task for the next brief"
```

---

### Task 3: Shutdown during a held settle releases the held tasks

**Files:**
- Modify: `internal/agentteam/team.go` (`watch`: the `errors.Is(err, errShutdown)` branch after `s.launch(...)`, and the resume-failure write's `if s.closed { closed = true; return false, nil }`; add two helpers after `resumeFailedNotice`)
- Test: `internal/agentteam/shutdown_test.go`

**Interfaces:**
- Consumes: `gatedRunner`, `gatedRun`, `newGatedRunner` (`shutdown_test.go`), `LoadState`.
- Produces:
  - `func (s *Service) releaseHeldLocked(memberID string)`
  - `func (s *Service) requeueLocked(messages []Message)`

- [ ] **Step 1: Write the failing tests**

Append to `internal/agentteam/shutdown_test.go` (add `"errors"` to its imports):

```go
// gatedRefusingRunner is a gatedRunner whose gated start fails once it is
// released, the way a model that went away does, so the resume-failure
// write runs after Shutdown closed the Team.
type gatedRefusingRunner struct {
	*gatedRunner
}

func (r *gatedRefusingRunner) Start(ctx context.Context, member Member, prompt string) (Run, error) {
	r.mu.Lock()
	refuse := r.gateName == member.Name
	r.mu.Unlock()
	run, err := r.gatedRunner.Start(ctx, member, prompt)
	if refuse {
		return nil, errors.New("model unavailable")
	}
	return run, err
}

// heldShutdown runs the held-settle window: alpha owns a task, its turn
// ends with mail queued, and Shutdown closes the Team while alpha's resume
// is held inside the runner. It returns the durable state Shutdown left.
func heldShutdown(t *testing.T, runner Runner, gate *gatedRunner) (State, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "team.json")
	service, err := New(path, Config{}, runner)
	require.NoError(t, err)
	ctx := context.Background()
	for _, name := range []string{"alpha", "beta"} {
		_, err := service.SpawnTeammate(ctx, "lead", SpawnRequest{Name: name, Description: "d", Prompt: "p", ParentID: "session-a"})
		require.NoError(t, err)
	}
	task, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "Build", Description: "d"})
	require.NoError(t, err)
	_, err = service.UpdateTask("alpha", UpdateTaskRequest{TaskID: task.ID, ExpectedRevision: task.Revision, Action: "claim", SessionID: "session-a"})
	require.NoError(t, err)
	require.NoError(t, service.SendMessage(ctx, "lead", "session-a", SendRequest{Target: "alpha", Message: "keep going"}))

	gate.gate("alpha")
	gate.finish("alpha")
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("alpha's resume never reached the runner")
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		service.Shutdown()
	}()
	select {
	case name := <-gate.canceled:
		require.Equal(t, "beta", name, "Shutdown is closed once beta is canceled")
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown never canceled beta's run")
	}
	close(gate.release)
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return after the held resume was released")
	}
	state, err := LoadState(path)
	require.NoError(t, err)
	return state, task.ID
}

func requireHeldReleased(t *testing.T, state State, taskID string) {
	t.Helper()
	task, ok := findTask(state, taskID)
	require.True(t, ok)
	require.Equal(t, TaskPending, task.Status, "the held task goes back to the board")
	require.Empty(t, task.OwnerID)
	require.Zero(t, task.Attempts, "a shutdown charges no attempt")
	queued := 0
	for _, message := range state.Messages {
		if message.To == "alpha" && message.Text == "keep going" {
			queued++
			require.False(t, message.Delivered, "the drained mail is pending again")
		}
	}
	require.Equal(t, 1, queued)
	for _, member := range state.Members {
		if member.Name == "alpha" {
			require.False(t, member.Failed, "a shutdown fails no member")
		}
	}
}

// TestShutdownDuringAHeldResumeReleasesTheTask pins Review Focus 5 for a
// resume that Shutdown refuses.
func TestShutdownDuringAHeldResumeReleasesTheTask(t *testing.T) {
	t.Parallel()
	runner := newGatedRunner()
	state, taskID := heldShutdown(t, runner, runner)
	requireHeldReleased(t, state, taskID)
}

// TestRefusedResumeAfterShutdownReleasesTheTask pins Review Focus 5 for a
// resume whose start fails on its own after Shutdown closed the Team.
func TestRefusedResumeAfterShutdownReleasesTheTask(t *testing.T) {
	t.Parallel()
	gate := newGatedRunner()
	state, taskID := heldShutdown(t, &gatedRefusingRunner{gatedRunner: gate}, gate)
	requireHeldReleased(t, state, taskID)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agentteam/ -run 'TestShutdownDuringAHeldResumeReleasesTheTask|TestRefusedResumeAfterShutdownReleasesTheTask'`
Expected: FAIL: the first with `the held task goes back to the board` (status `in_progress`), the second with the same assertion and, before it, the mail still delivered.

- [ ] **Step 3: Add the helpers**

In `internal/agentteam/team.go`, add after `resumeFailedNotice`:

```go
// releaseHeldLocked hands back the in-progress tasks memberID kept for a
// resume that will not run, without charging an attempt: the turn that
// would have finished them was stopped by shutdown, not by the work.
// Callers must hold s.mu.
func (s *Service) releaseHeldLocked(memberID string) {
	for i := range s.state.Tasks {
		task := &s.state.Tasks[i]
		if task.OwnerID != memberID || task.Status != TaskInProgress {
			continue
		}
		task.Status = TaskPending
		task.OwnerID = ""
		task.Revision++
	}
}

// requeueLocked marks drained mail pending again, so a later Service
// delivers it. Callers must hold s.mu.
func (s *Service) requeueLocked(messages []Message) {
	for _, message := range messages {
		for i := range s.state.Messages {
			if s.state.Messages[i].ID == message.ID {
				s.state.Messages[i].Delivered = false
			}
		}
	}
}
```

- [ ] **Step 4: Release the held tasks in both shutdown paths of `watch`**

In `watch`, replace the `errShutdown` branch:

```go
		if errors.Is(err, errShutdown) {
			// Shutdown took the member after this turn drained its inbox.
			// Hand the messages back so a later Service delivers them.
			_ = s.write(mustApply, func() (bool, error) {
				for _, message := range pending {
					for i := range s.state.Messages {
						if s.state.Messages[i].ID == message.ID {
							s.state.Messages[i].Delivered = false
						}
					}
				}
				s.notifyLocked()
				return true, nil
			})
			return
		}
```

with:

```go
		if errors.Is(err, errShutdown) {
			// Shutdown took the member after this turn drained its inbox
			// and held its tasks for the resume. Hand both back in one
			// write, so a later Service delivers the mail and the tasks do
			// not stay in progress under a member nothing runs. Shutdown
			// waits for this write: the watcher is counted.
			_ = s.write(mustApply, func() (bool, error) {
				s.requeueLocked(pending)
				s.releaseHeldLocked(member.ID)
				s.notifyLocked()
				return true, nil
			})
			return
		}
```

In the resume-failure write just below, replace:

```go
			if s.closed {
				closed = true
				return false, nil
			}
```

with:

```go
			if s.closed {
				// Shutdown began while the refused start ran. It is not
				// this member's failure to record, but the mail and the
				// held tasks must not be stranded.
				closed = true
				s.requeueLocked(pending)
				s.releaseHeldLocked(member.ID)
				s.notifyLocked()
				return true, nil
			}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `gofumpt -w internal/agentteam/team.go internal/agentteam/shutdown_test.go && go test ./internal/agentteam/ && go test -race ./internal/agentteam/ -run 'Shutdown|Resume'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agentteam/team.go internal/agentteam/shutdown_test.go
git commit -m "fix: release held tasks when shutdown stops a resume"
```

---

### Task 4: Fork context is re-sent after the teammate session is compacted

**Files:**
- Modify: `internal/agentteam/team.go` (`Member` struct; `launch`'s member loop in its `s.write`; add `contextSummaryRun` after the `Runner` interface)
- Modify: `internal/agent/team.go` (`teammatePrompt`, `teamRun`, `Start`)
- Test: `internal/agentteam/team_test.go`, `internal/agent/team_sibling_prefix_test.go`

**Interfaces:**
- Consumes: `session.Session.SummaryMessageID` (`internal/session/session.go`).
- Produces:
  - `Member.ContextSummary string` with JSON key `context_summary,omitempty`
  - unexported `interface contextSummaryRun { ContextSummary() (summary string, sent bool) }` in `agentteam`
  - `func (r *teamRunner) teammatePromptFor(ctx context.Context, member agentteam.Member, task string, first bool, summary string) (string, bool)`
  - `func (r *teamRun) ContextSummary() (string, bool)`

- [ ] **Step 1: Write the failing agentteam test**

Append to `internal/agentteam/team_test.go` (add `"encoding/json"` to its imports if missing):

```go
// summaryRun is a fakeRun that reports the session summary its fork prefix
// was rendered against.
type summaryRun struct {
	*fakeRun
	summary string
	sent    bool
}

func (r *summaryRun) ContextSummary() (string, bool) { return r.summary, r.sent }

type summaryRunner struct {
	summary string
	sent    bool
}

func (r *summaryRunner) Start(_ context.Context, _ Member, _ string) (Run, error) {
	run := &fakeRun{done: make(chan struct{})}
	var once sync.Once
	run.cancel = func() { once.Do(func() { close(run.done) }) }
	return &summaryRun{fakeRun: run, summary: r.summary, sent: r.sent}, nil
}

// TestLaunchRecordsTheContextSummary pins that the write that records a run
// also records the summary its fork prefix was sent against, and leaves it
// alone when the turn sent no prefix.
func TestLaunchRecordsTheContextSummary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sent bool
		want string
	}{
		{"sent", true, "message-7"},
		{"not sent", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, &summaryRunner{summary: "message-7", sent: tc.sent})
			require.NoError(t, err)
			t.Cleanup(s.Shutdown)
			_, err = s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
				Name: "forked", Description: "d", Prompt: "p", Context: ContextFork, ParentID: "session-a",
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, memberByID(t, s, "forked").ContextSummary)
		})
	}
}

func TestContextSummaryIsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(Member{ID: "member-1", Name: "a"})
	require.NoError(t, err)
	require.NotContains(t, string(data), "context_summary")
}
```

- [ ] **Step 2: Write the failing agent test**

Append to `internal/agent/team_sibling_prefix_test.go`:

```go
// TestForkPrefixResentAfterCompaction pins the follow-up: compaction
// replaces a fork teammate's history, inherited Lead context included, with
// a summary. The first turn after it carries the inherited context again,
// without the role block, and the turn after that does not.
func TestForkPrefixResentAfterCompaction(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	_, err = coord.messages.Create(t.Context(), lead.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "fix the failing tests"}},
	})
	require.NoError(t, err)
	member := agentteam.Member{
		ID: "member-1", Name: "db-layer", Role: agentteam.RoleTeammate,
		ParentID: lead.ID, Context: agentteam.ContextFork, RoleName: "implementer",
	}
	inherited := runner.forkPrefix(t.Context(), member)
	require.Contains(t, inherited, "fix the failing tests")

	first, sent := runner.teammatePromptFor(t.Context(), member, "Start.", true, "")
	require.True(t, sent)
	require.True(t, strings.HasPrefix(first, inherited))

	resent, sent := runner.teammatePromptFor(t.Context(), member, "Next.", false, "message-summary")
	require.True(t, sent, "a new summary means the inherited context is gone from the history")
	require.Equal(t, inherited+teammateTaskMessage(member, "Next."), resent, "the role block is first-turn only")

	member.ContextSummary = "message-summary"
	later, sent := runner.teammatePromptFor(t.Context(), member, "Again.", false, "message-summary")
	require.False(t, sent)
	require.Equal(t, teammateTaskMessage(member, "Again."), later)

	fresh := member
	fresh.Context = agentteam.ContextFresh
	fresh.ContextSummary = ""
	plain, sent := runner.teammatePromptFor(t.Context(), fresh, "x", false, "message-summary")
	require.False(t, sent, "a fresh member inherits nothing")
	require.Equal(t, teammateTaskMessage(fresh, "x"), plain)

	run := &teamRun{contextSummary: "message-summary", forkSent: true}
	summary, ok := run.ContextSummary()
	require.True(t, ok)
	require.Equal(t, "message-summary", summary)
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/agentteam/ -run 'TestLaunchRecordsTheContextSummary|TestContextSummaryIsOmittedWhenEmpty' ; go test ./internal/agent/ -run TestForkPrefixResentAfterCompaction`
Expected: FAIL to compile with `ContextSummary undefined` and `runner.teammatePromptFor undefined`.

- [ ] **Step 4: Add the member field and record it in `launch`**

In `internal/agentteam/team.go`, in the `Member` struct, after `Diagnostics`, add:

```go
	// ContextSummary is the summary message id of the teammate session when
	// its fork prefix was last sent. A different id at the next start means
	// the session was compacted since, and the prefix is sent again.
	ContextSummary string `json:"context_summary,omitempty"`
```

After the `Runner` interface, add:

```go
// contextSummaryRun is a Run that reports the session summary its fork
// prefix was rendered against; sent is false when the turn sent none.
type contextSummaryRun interface {
	ContextSummary() (summary string, sent bool)
}
```

In `launch`, inside its `s.write`, replace the member loop:

```go
		for i := range s.state.Members {
			if s.state.Members[i].Name != member.Name {
				continue
			}
			s.state.Members[i].SessionID = run.SessionID()
			s.state.Members[i].Failed = false
			s.state.Members[i].Status = StatusRunning
		}
```

with:

```go
		for i := range s.state.Members {
			if s.state.Members[i].Name != member.Name {
				continue
			}
			s.state.Members[i].SessionID = run.SessionID()
			s.state.Members[i].Failed = false
			s.state.Members[i].Status = StatusRunning
			// Recorded in the write that starts the run, so the next
			// start compares against the summary this turn was sent on.
			if reporter, ok := run.(contextSummaryRun); ok {
				if summary, sent := reporter.ContextSummary(); sent {
					s.state.Members[i].ContextSummary = summary
				}
			}
		}
```

- [ ] **Step 5: Decide the prefix per summary in the runner**

In `internal/agent/team.go`, replace `teammatePrompt` with:

```go
// teammatePrompt is teammatePromptFor with the summary the member last got
// its fork prefix on, which never re-sends it.
func (r *teamRunner) teammatePrompt(ctx context.Context, member agentteam.Member, task string, first bool) string {
	prompt, _ := r.teammatePromptFor(ctx, member, task, first, member.ContextSummary)
	return prompt
}

// teammatePromptFor is the message a teammate turn starts from. On the
// member's first turn only, it carries its role block; every turn carries
// its name, then the task. A fork member also gets the inherited context on
// its first turn and on the first turn after its session was compacted:
// summary is the session's summary message id, and one that differs from
// the member's recorded value means the summary replaced the copy in its
// history. It reports whether the inherited context was sent.
//
// The inherited context goes first because it is the same bytes for every fork
// sibling of one Lead. The role block follows because it is the same bytes for
// every sibling with that role; the per-sibling name comes after both, so the
// shared prefix runs through the whole role block.
func (r *teamRunner) teammatePromptFor(ctx context.Context, member agentteam.Member, task string, first bool, summary string) (string, bool) {
	task = teammateTaskMessage(member, task)
	if first && member.RoleName != "" {
		if role, refusal := resolveRole(r.c.cfg.Config(), member.RoleName); refusal == "" {
			task = roles.Block(*role) + task
		}
	}
	if member.Context == agentteam.ContextFork && (first || summary != member.ContextSummary) {
		return r.forkPrefix(ctx, member) + task, true
	}
	return task, false
}
```

Replace the `teamRun` struct and add its method:

```go
// teamRun is one live teammate turn.
type teamRun struct {
	sessionID string
	done      chan struct{}
	output    string
	err       error
	cancel    context.CancelFunc
	// contextSummary is the session's summary id when the turn started;
	// forkSent says the turn's message carried the fork prefix.
	contextSummary string
	forkSent       bool
}
```

and after `func (r *teamRun) Cancel()`:

```go
// ContextSummary reports the summary the turn's fork prefix was sent on.
func (r *teamRun) ContextSummary() (string, bool) { return r.contextSummary, r.forkSent }
```

In `Start`, replace:

```go
	promptText = r.teammatePrompt(memberCtx, member, promptText, teammateFirstTurn(created, sess))

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(memberCtx), 30*time.Minute)
	run := &teamRun{
		sessionID: sess.ID,
		done:      make(chan struct{}),
		cancel:    cancel,
	}
```

with:

```go
	promptText, forkSent := r.teammatePromptFor(memberCtx, member, promptText, teammateFirstTurn(created, sess), sess.SummaryMessageID)

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(memberCtx), 30*time.Minute)
	run := &teamRun{
		sessionID:      sess.ID,
		done:           make(chan struct{}),
		cancel:         cancel,
		contextSummary: sess.SummaryMessageID,
		forkSent:       forkSent,
	}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `gofumpt -w internal/agentteam/team.go internal/agentteam/team_test.go internal/agent/team.go internal/agent/team_sibling_prefix_test.go && go test ./internal/agentteam/ && go test ./internal/agent/ -run 'Fork|RoleTeammate|FirstTurn|Prefix'`
Expected: PASS, including the existing `TestForkPrefixFirstTurnOnly` and `role_teammate_test.go` prompt tests.

- [ ] **Step 7: Commit**

```bash
git add internal/agentteam/team.go internal/agentteam/team_test.go internal/agent/team.go internal/agent/team_sibling_prefix_test.go
git commit -m "fix: resend a fork teammate's inherited context after compaction"
```

---
### Task 5: Git plumbing for commit, merge and repository prefix

**Files:**
- Create: `internal/gitutil/plumbing.go`
- Test: `internal/gitutil/plumbing_test.go`

**Interfaces:**
- Consumes: `gitOutput(ctx, dir string, args ...string) (string, error)` (`worktree.go`), `runGit`, `gitAvailable` (`branch_test.go`), `CreateWorktree`.
- Produces:
  - `func RepoPrefix(ctx context.Context, dir string) (string, error)`: `dir`'s path inside its repository in slash form with a trailing slash, `""` at the root
  - `func CommitAll(ctx context.Context, dir, message string) (bool, error)`: `git add -A`, then commit when anything is staged; `false, nil` for an empty change set
  - `type MergeError struct { Reason, Output string }` with `Error() string`
  - `func MergeNoFF(ctx context.Context, dir, branch, message string) error`: refuses a detached `HEAD`; aborts a merge that stopped; returns `*MergeError` for every refusal

- [ ] **Step 1: Write the failing tests**

Create `internal/gitutil/plumbing_test.go`:

```go
package gitutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// initPlumbingRepo is a repository whose commits never ask for a signature,
// whatever the user's global git config says.
func initPlumbingRepo(t *testing.T) string {
	t.Helper()
	if !gitAvailable() {
		t.Skip("git is not available")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644))
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

// gitText runs git in dir and returns its trimmed output.
func gitText(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func mergeInProgressForTest(dir string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "-C", dir, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return cmd.Run() == nil
}

func TestRepoPrefix(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "sub", "inner"), 0o755))

	prefix, err := RepoPrefix(context.Background(), repo)
	require.NoError(t, err)
	require.Empty(t, prefix)
	prefix, err = RepoPrefix(context.Background(), filepath.Join(repo, "sub", "inner"))
	require.NoError(t, err)
	require.Equal(t, "sub/inner/", prefix)
	_, err = RepoPrefix(context.Background(), t.TempDir())
	require.Error(t, err)
}

func TestCommitAll(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	ctx := context.Background()

	committed, err := CommitAll(ctx, repo, "Nothing")
	require.NoError(t, err)
	require.False(t, committed, "an empty change set makes no commit")
	require.Equal(t, "initial", gitText(t, repo, "log", "-1", "--format=%s"))

	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o644))
	committed, err = CommitAll(ctx, repo, "Add things")
	require.NoError(t, err)
	require.True(t, committed)
	require.Equal(t, "Add things", gitText(t, repo, "log", "-1", "--format=%s"))
	require.Equal(t, "Test", gitText(t, repo, "log", "-1", "--format=%an"), "the author comes from the repository config")
	require.Empty(t, gitText(t, repo, "status", "--porcelain"))
}

// branchWithCommit makes a worktree on a new branch, writes name there, and
// commits it.
func branchWithCommit(t *testing.T, repo, name, content string) string {
	t.Helper()
	path, branch, err := CreateWorktree(context.Background(), repo, "feature")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(path, name), []byte(content), 0o644))
	committed, err := CommitAll(context.Background(), path, "Feature work")
	require.NoError(t, err)
	require.True(t, committed)
	return branch
}

func TestMergeNoFFMakesAMergeCommit(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "feature.txt", "feature\n")

	require.NoError(t, MergeNoFF(context.Background(), repo, branch, "Merge it"))
	require.FileExists(t, filepath.Join(repo, "feature.txt"))
	require.Equal(t, "Merge it", gitText(t, repo, "log", "-1", "--format=%s"))
	require.Len(t, strings.Fields(gitText(t, repo, "rev-list", "--parents", "-n", "1", "HEAD")), 3,
		"a fast-forward was possible, and --no-ff still made a merge commit")
}

func TestMergeNoFFAbortsAConflict(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "README.md", "branch\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("main\n"), 0o644))
	runGit(t, repo, "commit", "-am", "main change")

	err := MergeNoFF(context.Background(), repo, branch, "Merge it")
	var mergeErr *MergeError
	require.True(t, errors.As(err, &mergeErr), "%v", err)
	require.Contains(t, mergeErr.Reason, "conflict")
	require.Contains(t, mergeErr.Output, "README.md")
	require.False(t, mergeInProgressForTest(repo), "the merge was aborted")
	require.Empty(t, gitText(t, repo, "status", "--porcelain"))
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	require.NoError(t, err)
	require.Equal(t, "main\n", string(data))
}

func TestMergeNoFFRefusesADirtyOverlap(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "README.md", "branch\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("local\n"), 0o644))

	err := MergeNoFF(context.Background(), repo, branch, "Merge it")
	var mergeErr *MergeError
	require.True(t, errors.As(err, &mergeErr), "%v", err)
	require.False(t, mergeInProgressForTest(repo))
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	require.NoError(t, err)
	require.Equal(t, "local\n", string(data), "the Lead's uncommitted change is kept")
}

func TestMergeNoFFRefusesADetachedHead(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "feature.txt", "feature\n")
	runGit(t, repo, "checkout", "--detach")

	err := MergeNoFF(context.Background(), repo, branch, "Merge it")
	var mergeErr *MergeError
	require.True(t, errors.As(err, &mergeErr), "%v", err)
	require.Contains(t, mergeErr.Reason, "detached")
	require.NoFileExists(t, filepath.Join(repo, "feature.txt"))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gitutil/ -run 'TestRepoPrefix|TestCommitAll|TestMergeNoFF'`
Expected: FAIL to compile with `undefined: RepoPrefix`, `undefined: CommitAll`, `undefined: MergeNoFF`, `undefined: MergeError`.

- [ ] **Step 3: Implement the plumbing**

Create `internal/gitutil/plumbing.go`:

```go
package gitutil

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// RepoPrefix returns dir's path inside its repository, in slash form with a
// trailing slash, or "" at the repository root. It is what git rev-parse
// --show-prefix prints.
func RepoPrefix(ctx context.Context, dir string) (string, error) {
	out, err := gitOutput(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-prefix: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// CommitAll stages every change in the work tree at dir, ignored files
// excepted, and commits it with message. It reports false with no error
// when nothing was staged. Author, committer, signing and hooks follow the
// repository's git config, so a failing hook refuses the commit.
func CommitAll(ctx context.Context, dir, message string) (bool, error) {
	if _, err := gitOutput(ctx, dir, "add", "-A"); err != nil {
		return false, fmt.Errorf("git add -A: %w", err)
	}
	// diff --quiet exits 1 when something is staged and 0 when nothing is.
	err := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--cached", "--quiet").Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return false, fmt.Errorf("git diff --cached: %w", err)
	}
	if _, err := gitOutput(ctx, dir, "commit", "-m", message); err != nil {
		return false, fmt.Errorf("git commit: %w", err)
	}
	return true, nil
}

// MergeError is a merge git refused or could not finish. The work tree is
// as it was before: a merge that started is aborted.
type MergeError struct {
	// Reason says why, in words a notice can quote.
	Reason string
	// Output is git's own output for the failed step.
	Output string
}

func (e *MergeError) Error() string {
	if e.Output == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Output
}

// MergeNoFF merges branch into the branch checked out at dir with a merge
// commit, even when a fast-forward is possible. A detached HEAD is refused
// before anything runs. A merge that stops on a conflict is aborted, and
// one git refuses to start (a dirty file it would overwrite) changes
// nothing; both return a *MergeError carrying git's output.
func MergeNoFF(ctx context.Context, dir, branch, message string) error {
	if _, err := gitOutput(ctx, dir, "symbolic-ref", "-q", "HEAD"); err != nil {
		return &MergeError{Reason: "the Lead's HEAD is detached, so there is no branch to merge into"}
	}
	out, err := gitOutput(ctx, dir, "merge", "--no-ff", "-m", message, "--end-of-options", branch)
	if err == nil {
		return nil
	}
	output := strings.TrimSpace(out)
	if mergeInProgress(ctx, dir) {
		if abortOut, abortErr := gitOutput(ctx, dir, "merge", "--abort"); abortErr != nil {
			output += "\n\ngit merge --abort failed: " + strings.TrimSpace(abortOut)
		}
		return &MergeError{Reason: "the merge has conflicts and was aborted", Output: output}
	}
	return &MergeError{Reason: "git refused the merge", Output: output}
}

// mergeInProgress reports whether a merge stopped half-way at dir.
func mergeInProgress(ctx context.Context, dir string) bool {
	_, err := gitOutput(ctx, dir, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return err == nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofumpt -w internal/gitutil/plumbing.go internal/gitutil/plumbing_test.go && go test ./internal/gitutil/`
Expected: PASS (or SKIP for every git test when git is absent).

- [ ] **Step 5: Commit**

```bash
git add internal/gitutil/plumbing.go internal/gitutil/plumbing_test.go
git commit -m "feat: add git commit, merge and prefix helpers"
```

---

### Task 6: Git file snapshots that can undo a change

**Files:**
- Create: `internal/gitutil/filesnapshot.go`
- Test: `internal/gitutil/filesnapshot_test.go`

**Interfaces:**
- Consumes: `Snapshot(ctx, dir string) (WorktreeState, error)` (`snapshot.go`), `gitOutput`; test helpers `initPlumbingRepo`, `gitText`, `runGit` (Task 5 and `branch_test.go`).
- Produces:
  - `type SnapshotCaps struct { PerFile, Total int64 }`
  - `type SnapshotFile struct { Exists, Tracked, Saved bool; Hash string; Content []byte; Mode fs.FileMode }`
  - `type FileSnapshot struct { Head string; Files map[string]SnapshotFile }`
  - `func SnapshotFiles(ctx context.Context, root string, caps SnapshotCaps) (FileSnapshot, error)`
  - `func ChangedPaths(ctx context.Context, root string, before, after FileSnapshot) ([]string, error)`
  - `type RestoreResult struct { Restored, Unrestored []string }`
  - `func RestorePaths(ctx context.Context, root string, before FileSnapshot, paths []string) RestoreResult`

- [ ] **Step 1: Write the failing tests**

Create `internal/gitutil/filesnapshot_test.go`:

```go
package gitutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var testCaps = SnapshotCaps{PerFile: 1 << 20, Total: 32 << 20}

func writeRepoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}

func snapshot(t *testing.T, root string, caps SnapshotCaps) FileSnapshot {
	t.Helper()
	snap, err := SnapshotFiles(context.Background(), root, caps)
	require.NoError(t, err)
	return snap
}

func changed(t *testing.T, root string, before, after FileSnapshot) []string {
	t.Helper()
	paths, err := ChangedPaths(context.Background(), root, before, after)
	require.NoError(t, err)
	return paths
}

func TestSnapshotFilesListsChangedPaths(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, ".gitignore", "build/\n")
	writeRepoFile(t, repo, "gone.txt", "bye\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")

	writeRepoFile(t, repo, "README.md", "dirty\n")
	writeRepoFile(t, repo, "docs/new.md", "new\n")
	writeRepoFile(t, repo, "build/out.bin", "ignored\n")
	require.NoError(t, os.Remove(filepath.Join(repo, "gone.txt")))

	snap := snapshot(t, repo, testCaps)
	require.Equal(t, gitText(t, repo, "rev-parse", "HEAD"), snap.Head)
	require.ElementsMatch(t, []string{"README.md", "docs/new.md", "gone.txt"}, keys(snap.Files), "ignored files are not listed")
	require.True(t, snap.Files["README.md"].Tracked)
	require.True(t, snap.Files["README.md"].Saved)
	require.Equal(t, "dirty\n", string(snap.Files["README.md"].Content))
	require.False(t, snap.Files["docs/new.md"].Tracked)
	require.False(t, snap.Files["gone.txt"].Exists)
}

func keys(files map[string]SnapshotFile) []string {
	out := make([]string, 0, len(files))
	for path := range files {
		out = append(out, path)
	}
	return out
}

func TestChangedPathsSeesEveryKindOfChange(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "clean.txt", "clean\n")
	writeRepoFile(t, repo, "reverted.txt", "base\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	writeRepoFile(t, repo, "README.md", "dirty\n")
	writeRepoFile(t, repo, "reverted.txt", "dirty\n")
	before := snapshot(t, repo, testCaps)

	writeRepoFile(t, repo, "clean.txt", "edited\n")   // Clean before, now listed.
	writeRepoFile(t, repo, "README.md", "dirtier\n")  // Listed both times, new hash.
	writeRepoFile(t, repo, "reverted.txt", "base\n")  // Listed before, left the list.
	writeRepoFile(t, repo, "fresh.txt", "fresh\n")    // New and untracked.
	after := snapshot(t, repo, SnapshotCaps{})

	require.Equal(t, []string{"README.md", "clean.txt", "fresh.txt", "reverted.txt"}, changed(t, repo, before, after))
	require.Empty(t, changed(t, repo, after, snapshot(t, repo, SnapshotCaps{})), "nothing changed since")
}

func TestChangedPathsFollowsAMovedHead(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	before := snapshot(t, repo, testCaps)
	writeRepoFile(t, repo, "README.md", "committed\n")
	runGit(t, repo, "commit", "-am", "inside the command")
	after := snapshot(t, repo, SnapshotCaps{})
	require.Empty(t, after.Files, "a committed file leaves the status list")
	require.Equal(t, []string{"README.md"}, changed(t, repo, before, after))

	result := RestorePaths(context.Background(), repo, before, []string{"README.md"})
	require.Equal(t, []string{"README.md"}, result.Restored)
	require.Equal(t, "hello\n", readRepoFile(t, repo, "README.md"), "restored from the HEAD taken before")
}

func TestRestorePathsUndoesEachKind(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "dirty.txt", "base\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	writeRepoFile(t, repo, "dirty.txt", "mine\n")
	writeRepoFile(t, repo, "untracked.txt", "kept\n")
	writeRepoFile(t, repo, "big.bin", strings.Repeat("x", 2<<20))
	before := snapshot(t, repo, testCaps)
	require.False(t, before.Files["big.bin"].Saved, "a file over the per-file cap is hashed, not saved")

	writeRepoFile(t, repo, "README.md", "theirs\n")    // Clean tracked.
	writeRepoFile(t, repo, "dirty.txt", "theirs\n")    // Dirty tracked.
	writeRepoFile(t, repo, "untracked.txt", "theirs\n") // Untracked that existed.
	writeRepoFile(t, repo, "new/added.txt", "new\n")    // New untracked.
	runGit(t, repo, "add", "new/added.txt")             // Even when staged.
	writeRepoFile(t, repo, "big.bin", "small now\n")    // Over the cap.

	paths := changed(t, repo, before, snapshot(t, repo, SnapshotCaps{}))
	result := RestorePaths(context.Background(), repo, before, paths)
	require.ElementsMatch(t, []string{"README.md", "dirty.txt", "untracked.txt", "new/added.txt"}, result.Restored)
	require.Equal(t, []string{"big.bin"}, result.Unrestored)

	require.Equal(t, "hello\n", readRepoFile(t, repo, "README.md"))
	require.Equal(t, "mine\n", readRepoFile(t, repo, "dirty.txt"), "a dirty file gets its saved content back, not HEAD's")
	require.Equal(t, "kept\n", readRepoFile(t, repo, "untracked.txt"))
	require.NoFileExists(t, filepath.Join(repo, "new", "added.txt"))
	require.Equal(t, "small now\n", readRepoFile(t, repo, "big.bin"), "a path over the cap is reported, not restored")
	require.NotContains(t, gitText(t, repo, "status", "--porcelain"), "new/added.txt", "the staged new file left the index")
}

func TestRestorePathsTreatsPathspecMagicLiterally(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "*.md", "star\n")
	writeRepoFile(t, repo, "other.md", "other\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	before := snapshot(t, repo, testCaps)
	writeRepoFile(t, repo, "*.md", "changed\n")
	writeRepoFile(t, repo, "other.md", "changed\n")

	result := RestorePaths(context.Background(), repo, before, []string{"*.md"})
	require.Equal(t, []string{"*.md"}, result.Restored)
	require.Equal(t, "star\n", readRepoFile(t, repo, "*.md"))
	require.Equal(t, "changed\n", readRepoFile(t, repo, "other.md"), "the glob matched only its own name")
}
```

On Windows a file cannot be named `*.md`; add at the top of `TestRestorePathsTreatsPathspecMagicLiterally`, after `t.Parallel()`:

```go
	if runtime.GOOS == "windows" {
		t.Skip("Windows file names cannot hold *")
	}
```

and add `"runtime"` to the imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gitutil/ -run 'TestSnapshotFiles|TestChangedPaths|TestRestorePaths'`
Expected: FAIL to compile with `undefined: SnapshotCaps`, `undefined: SnapshotFiles`, `undefined: ChangedPaths`, `undefined: RestorePaths`.

- [ ] **Step 3: Implement the snapshot**

Create `internal/gitutil/filesnapshot.go`:

```go
package gitutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SnapshotCaps bound how much content one FileSnapshot saves. A file over
// PerFile, or one that would take the snapshot past Total, is hashed but
// not saved: a change to it can be seen and reported, not undone. Zero caps
// save nothing but empty files.
type SnapshotCaps struct {
	PerFile int64
	Total   int64
}

// SnapshotFile is one path git status listed.
type SnapshotFile struct {
	// Exists is false for a path listed as deleted.
	Exists bool
	// Tracked is false for an untracked path.
	Tracked bool
	// Hash identifies the content: a sha256 of the bytes, the link target
	// of a symlink, or "absent".
	Hash string
	// Saved reports whether Content holds the bytes. A deleted path needs
	// none and counts as saved; a symlink or special file never is.
	Saved   bool
	Content []byte
	Mode    fs.FileMode
}

// FileSnapshot is the changed-file state of a work tree: its HEAD and every
// path git status lists there. Ignored files are not listed.
type FileSnapshot struct {
	Head  string
	Files map[string]SnapshotFile
}

// SnapshotFiles records root's status and each listed path. root must be
// the top of a work tree, since the paths are relative to it.
func SnapshotFiles(ctx context.Context, root string, caps SnapshotCaps) (FileSnapshot, error) {
	state, err := Snapshot(ctx, root)
	if err != nil {
		return FileSnapshot{}, err
	}
	paths := make([]string, 0, len(state.Entries))
	for path, entry := range state.Entries {
		paths = append(paths, path)
		// A rename names its original path only inside its entry.
		if _, orig, ok := strings.Cut(entry, " <- "); ok && orig != "" {
			paths = append(paths, orig)
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)

	snap := FileSnapshot{Head: state.Head, Files: make(map[string]SnapshotFile, len(paths))}
	var saved int64
	for _, path := range paths {
		entry, listed := state.Entries[path]
		file := SnapshotFile{Tracked: !listed || !strings.HasPrefix(entry, "? ")}
		if err := readSnapshotFile(filepath.Join(root, filepath.FromSlash(path)), &file, caps, &saved); err != nil {
			return FileSnapshot{}, fmt.Errorf("snapshot %s: %w", path, err)
		}
		snap.Files[path] = file
	}
	return snap, nil
}

// readSnapshotFile fills file from the path on disk, saving its bytes while
// the caps allow.
func readSnapshotFile(full string, file *SnapshotFile, caps SnapshotCaps, saved *int64) error {
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		file.Hash, file.Saved = "absent", true
		return nil
	}
	if err != nil {
		return err
	}
	file.Exists = true
	file.Mode = info.Mode().Perm()
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(full)
		if err != nil {
			return err
		}
		file.Hash = "link:" + target
		return nil
	case !info.Mode().IsRegular():
		file.Hash = "special:" + info.Mode().Type().String()
		return nil
	}
	if info.Size() <= caps.PerFile && *saved+info.Size() <= caps.Total {
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		file.Hash = hex.EncodeToString(sum[:])
		file.Content, file.Saved = data, true
		*saved += int64(len(data))
		return nil
	}
	hash, err := hashFile(full)
	if err != nil {
		return err
	}
	file.Hash = hash
	return nil
}

// hashFile is the sha256 of a file too large to keep, read as a stream.
func hashFile(full string) (string, error) {
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// ChangedPaths lists, sorted, the paths whose state differs between two
// snapshots of root: listed in one and not the other, or listed in both
// with other content. When HEAD moved, every path the new commits touched
// counts too, since a committed file leaves the status list.
func ChangedPaths(ctx context.Context, root string, before, after FileSnapshot) ([]string, error) {
	set := map[string]bool{}
	for path, file := range after.Files {
		if prior, ok := before.Files[path]; !ok || prior.Hash != file.Hash {
			set[path] = true
		}
	}
	for path := range before.Files {
		if _, ok := after.Files[path]; !ok {
			set[path] = true
		}
	}
	if knownHead(before.Head) && knownHead(after.Head) && before.Head != after.Head {
		out, err := gitLiteral(ctx, root, "diff", "--name-only", "-z", before.Head, after.Head)
		if err != nil {
			return nil, fmt.Errorf("git diff --name-only: %w", err)
		}
		for path := range strings.SplitSeq(out, "\x00") {
			if path != "" {
				set[path] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set)), nil
}

// knownHead reports whether head names a commit; an unborn branch reports
// "(initial)".
func knownHead(head string) bool {
	return head != "" && head != "(initial)"
}

// RestoreResult reports what RestorePaths did with each path.
type RestoreResult struct {
	Restored []string
	// Unrestored lists paths whose content was not saved (over the caps, a
	// symlink, a special file) and paths whose restore failed.
	Unrestored []string
}

// RestorePaths puts each path under root back to its state in before. A
// path before did not list was clean: it is restored from before.Head when
// that commit has it, and removed (from the index too) when it is new. A
// listed path is rewritten from its saved content, or removed when it did
// not exist.
func RestorePaths(ctx context.Context, root string, before FileSnapshot, paths []string) RestoreResult {
	var result RestoreResult
	for _, path := range paths {
		if err := restorePath(ctx, root, before, path); err != nil {
			result.Unrestored = append(result.Unrestored, path)
			continue
		}
		result.Restored = append(result.Restored, path)
	}
	return result
}

// errNotSaved marks a path whose content the snapshot did not keep.
var errNotSaved = errors.New("content not saved")

func restorePath(ctx context.Context, root string, before FileSnapshot, path string) error {
	full := filepath.Join(root, filepath.FromSlash(path))
	file, listed := before.Files[path]
	if !listed {
		if knownHead(before.Head) && inCommit(ctx, root, before.Head, path) {
			_, err := gitLiteral(ctx, root, "restore", "--source="+before.Head, "--staged", "--worktree", "--", path)
			return err
		}
		if _, err := gitLiteral(ctx, root, "rm", "--cached", "--quiet", "--ignore-unmatch", "--", path); err != nil {
			return err
		}
		return removeIfPresent(full)
	}
	if !file.Exists {
		return removeIfPresent(full)
	}
	if !file.Saved {
		return errNotSaved
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	// Whatever now stands in the way (a symlink, a directory) is replaced,
	// never written through.
	if info, err := os.Lstat(full); err == nil && !info.Mode().IsRegular() {
		if err := os.RemoveAll(full); err != nil {
			return err
		}
	}
	if err := os.WriteFile(full, file.Content, file.Mode); err != nil {
		return err
	}
	return os.Chmod(full, file.Mode)
}

func removeIfPresent(full string) error {
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// inCommit reports whether commit has a file at path.
func inCommit(ctx context.Context, root, commit, path string) bool {
	_, err := gitOutput(ctx, root, "cat-file", "-e", commit+":"+path)
	return err == nil
}

// gitLiteral runs git in dir with pathspec magic off, so a path read from a
// status listing only ever means that path.
func gitLiteral(ctx context.Context, dir string, args ...string) (string, error) {
	return gitOutput(ctx, dir, append([]string{"--literal-pathspecs"}, args...)...)
}
```

`gitOutput` builds `git -C <dir> <args>`, so the call is `git -C <dir> --literal-pathspecs <subcommand> ...`; both are global options and git accepts them in that order.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofumpt -w internal/gitutil/filesnapshot.go internal/gitutil/filesnapshot_test.go && go test ./internal/gitutil/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gitutil/filesnapshot.go internal/gitutil/filesnapshot_test.go
git commit -m "feat: add git file snapshots that restore changed paths"
```

---
### Task 7: Service worktree lifecycle

**Files:**
- Create: `internal/agentteam/worktree.go`
- Modify: `internal/agentteam/team.go` (`Config`, error codes, `Task`, `TaskView`, `Service` fields and `New`, `taskUpdate`, `UpdateTask`, `applyTaskUpdateLocked` `claim`/`reassign`/`delete`, `needsVerification` neighbor, `projectTaskView`)
- Modify: `internal/agentteam/dispatch.go` (`dispatchStart`, `dispatchPass` start loop, `planDispatchLocked`, `hasDispatchableLocked`, `taskBrief`)
- Test: `internal/agentteam/worktree_test.go` (create)

**Interfaces:**
- Consumes: `gitutil.RepoRoot`, `gitutil.RepoPrefix` (Task 5), `gitutil.CreateWorktreeFrom`, `gitutil.RemoveWorktree`, `gitutil.DeleteBranch`, `gitutil.CurrentBranch`, `gitutil.ListWorktrees`; `taskBrief` from Task 2.
- Produces:
  - `Config.WorkingDir string` (empty turns worktrees off)
  - `const CodeTaskGit = "TEAM_TASK_GIT"`
  - `Task.Worktree`, `Task.Branch` (`worktree,omitempty`, `branch,omitempty`); `TaskView.Worktree`, `TaskView.Branch` (same keys)
  - `func (t Task) wantsWorktree() bool`
  - `type Workspace struct { Root, Dir, Branch string }`
  - `func (s *Service) MemberWorkspace(member string) (Workspace, bool)`
  - `func gitContext() (context.Context, context.CancelFunc)`, `const gitTimeout`
  - `func (s *Service) repo() (root, prefix string, ok bool)`
  - `func (s *Service) createWorktree(ctx context.Context, taskID string) (path, branch string, err error)`
  - `func (s *Service) recordWorktree(taskID, sessionID, ownerID string, revision int, path, branch string) bool`
  - `func (s *Service) discardWorktree(ctx context.Context, path, branch string)`
  - `type worktreeRemoval struct { taskID, sessionID, path, branch string; forceBranch bool }`
  - `func (s *Service) removeTaskWorktree(ctx context.Context, r worktreeRemoval)`
  - `func (s *Service) placeStart(start dispatchStart) (dispatchStart, bool)`
  - `func (s *Service) placeClaim(view TaskView) (TaskView, error)`
  - `func (s *Service) holdsWorktreeLocked(memberID, except string) bool`
  - `dispatchStart.task Task`; `taskUpdate.place bool`; `taskUpdate.removal *worktreeRemoval`
  - Service fields `gitMu sync.Mutex`, `repoOnce sync.Once`, `repoRoot`, `repoPrefix string`, `repoOK bool`, `worktreeRefused map[string]int`

- [ ] **Step 1: Write the failing tests**

Create `internal/agentteam/worktree_test.go`:

```go
package agentteam

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/gitutil"
)

// initTeamRepo is the Lead's tree for the worktree tests: a repository with
// one commit whose commits never ask for a signature.
func initTeamRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		gitIn(t, dir, args...)
	}
	writeTreeFile(t, dir, "README.md", "hello\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-m", "initial")
	return dir
}

// gitIn runs git in dir and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func writeTreeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func branchExists(repo, branch string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// worktreesDir is where gitutil.CreateWorktreeFrom puts the worktrees of
// repo.
func worktreesDir(repo string) string {
	return filepath.Join(filepath.Dir(repo), "."+filepath.Base(repo)+"-worktrees")
}

func newWorktreeService(t *testing.T, workingDir string, autostart bool) (*Service, *fakeRunner) {
	t.Helper()
	cfg := Config{WorkingDir: workingDir}
	if !autostart {
		cfg.Autostart = func() bool { return false }
	}
	return newPipelineService(t, cfg)
}

func spawnMember(t *testing.T, s *Service, name string) {
	t.Helper()
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{Name: name, Description: "d", Prompt: "p", ParentID: pipelineSession})
	require.NoError(t, err)
}

func TestDispatchGivesAScopedTaskItsWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, true)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "Write docs.", Role: "implementer", WriteScopes: []string{"docs"}})

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, "crush/team-"+task.ID, got.Branch)
	require.Equal(t, filepath.Join(worktreesDir(repo), "team-"+task.ID), got.Worktree)
	require.DirExists(t, got.Worktree)
	require.Equal(t, got.Branch, gitutil.CurrentBranch(got.Worktree))
	require.Equal(t, gitIn(t, repo, "rev-parse", "HEAD"), gitIn(t, got.Worktree, "rev-parse", "HEAD"), "the worktree starts at the Lead's HEAD")
	require.Equal(t, 2, got.Revision, "recording the worktree is bookkeeping, not a new revision")
	require.Contains(t, runner.lastPrompt(), fmt.Sprintf("Worktree: %s (branch %s).", got.Worktree, got.Branch))
	require.Contains(t, runner.lastPrompt(), "relative paths resolve inside it")
	require.Contains(t, runner.lastPrompt(), "expected_revision 2", "the brief's revision is still the one to complete with")
}

func TestTasksWithoutScopesOrRepoGetNoWorktree(t *testing.T) {
	t.Parallel()
	t.Run("no scopes", func(t *testing.T) {
		t.Parallel()
		s, runner := newWorktreeService(t, initTeamRepo(t), true)
		task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
		require.Empty(t, taskByID(t, s, task.ID).Worktree)
		require.NotContains(t, runner.lastPrompt(), "Worktree:")
	})
	t.Run("not a repository", func(t *testing.T) {
		t.Parallel()
		s, runner := newWorktreeService(t, t.TempDir(), true)
		task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})
		got := taskByID(t, s, task.ID)
		require.Equal(t, TaskInProgress, got.Status, "the task starts in the shared tree, as before")
		require.Empty(t, got.Worktree)
		require.Equal(t, []string{"implementer-1"}, runner.memberNames())
	})
}

// TestWorktreeCreationFailureReleasesWithoutAnAttempt pins Review Focus 4.
func TestWorktreeCreationFailureReleasesWithoutAnAttempt(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	// A file where the worktrees directory goes makes git worktree add fail.
	require.NoError(t, os.WriteFile(worktreesDir(repo), []byte("not a directory"), 0o644))
	s, runner := newWorktreeService(t, repo, true)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Empty(t, got.OwnerID)
	require.Zero(t, got.Attempts, "the work never started, so nothing is charged")
	require.False(t, got.Escalated)
	require.Empty(t, got.Worktree)
	require.Empty(t, runner.memberNames(), "nothing starts without its worktree")
	require.Equal(t, StatusInactive, memberStatus(s, "implementer-1"), "the member made for the claim is free, not stuck provisioning")
	notices := leadNotices(t, s, pipelineSession)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0], "could not get its git worktree")

	// Another write runs a dispatch pass; the refused task is left alone.
	createTask(t, s, CreateTaskRequest{Subject: "Other", Description: "d"})
	require.Len(t, leadNotices(t, s, pipelineSession), 1, "a refused task waits for the Lead instead of looping")

	// The Lead edits it, and the dispatcher tries again.
	act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Description = "Write the docs." })
	require.Len(t, leadNotices(t, s, pipelineSession), 2)
	got = taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Zero(t, got.Attempts)
	require.Empty(t, runner.memberNames())
}

func TestManualClaimPlacesTheWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "rev")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}, Verify: new(true)})

	view := act(t, s, "impl", task.ID, "claim", nil)
	require.Equal(t, "crush/team-"+task.ID, view.Branch)
	require.DirExists(t, view.Worktree)
	ws, ok := s.MemberWorkspace("impl")
	require.True(t, ok)
	require.Equal(t, Workspace{Root: view.Worktree, Dir: view.Worktree, Branch: view.Branch}, ws)
	_, ok = s.MemberWorkspace("rev")
	require.False(t, ok, "a member with no scoped task works in the Lead tree")
	_, ok = s.MemberWorkspace(leadName)
	require.False(t, ok, "the Lead never leaves its tree")

	act(t, s, "impl", task.ID, "complete", nil)
	_, ok = s.MemberWorkspace("impl")
	require.False(t, ok, "work in review holds no worktree for its implementer")
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, "rev", check.ID, "claim", nil)
	ws, ok = s.MemberWorkspace("rev")
	require.True(t, ok, "the verifier runs in the worktree of the work it checks")
	require.Equal(t, view.Worktree, ws.Root)
}

func TestLeadAssignPlacesTheWorktree(t *testing.T) {
	t.Parallel()
	s, _ := newWorktreeService(t, initTeamRepo(t), false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, leadName, task.ID, "reassign", func(r *UpdateTaskRequest) { r.Owner = "impl" })
	require.DirExists(t, view.Worktree)

	own := createTask(t, s, CreateTaskRequest{Subject: "Mine", Description: "d", WriteScopes: []string{"internal"}})
	require.Empty(t, act(t, s, leadName, own.ID, "claim", nil).Worktree, "the Lead's own claim stays in its tree")
}

func TestMemberWorkspaceKeepsTheLeadSubdirectory(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	writeTreeFile(t, repo, "sub/keep.txt", "keep\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "sub")
	s, _ := newWorktreeService(t, filepath.Join(repo, "sub"), false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)

	ws, ok := s.MemberWorkspace("impl")
	require.True(t, ok)
	require.Equal(t, view.Worktree, ws.Root)
	require.Equal(t, filepath.Join(view.Worktree, "sub"), ws.Dir, "the teammate starts where the Lead works")
	require.FileExists(t, filepath.Join(ws.Dir, "keep.txt"))
}

func TestReleasedTaskKeepsItsWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "impl2")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	first := act(t, s, "impl", task.ID, "claim", nil)
	act(t, s, "impl", task.ID, "release", nil)
	require.Equal(t, first.Worktree, taskByID(t, s, task.ID).Worktree)

	second := act(t, s, "impl2", task.ID, "claim", nil)
	require.Equal(t, first.Worktree, second.Worktree, "nothing is recreated")
	worktrees, err := gitutil.ListWorktrees(context.Background(), repo)
	require.NoError(t, err)
	require.Len(t, worktrees, 2, "the Lead's tree and the one task worktree")
}

func TestReopenedTaskKeepsItsWorktree(t *testing.T) {
	t.Parallel()
	s, _ := newWorktreeService(t, initTeamRepo(t), false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}, Verify: new(true)})
	view := act(t, s, "impl", task.ID, "claim", nil)
	act(t, s, "impl", task.ID, "complete", nil)
	act(t, s, leadName, task.ID, "reopen", nil)
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Equal(t, view.Worktree, got.Worktree)
	require.DirExists(t, view.Worktree)
	require.Equal(t, view.Worktree, act(t, s, "impl", task.ID, "claim", nil).Worktree)
}

func TestDeleteRemovesTheWorktreeAndBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)
	writeTreeFile(t, view.Worktree, "docs/draft.md", "unfinished\n")

	act(t, s, leadName, task.ID, "delete", nil)
	require.NoDirExists(t, view.Worktree)
	require.False(t, branchExists(repo, view.Branch), "an unmerged branch goes with its deleted task")
	require.Empty(t, leadNotices(t, s, pipelineSession))
}

func TestDeleteRemovalFailureNoticesTheLead(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)
	// A locked worktree refuses git worktree remove --force.
	gitIn(t, repo, "worktree", "lock", view.Worktree)

	act(t, s, leadName, task.ID, "delete", nil)
	require.Equal(t, TaskDeleted, taskByID(t, s, task.ID).Status, "the state change stands")
	notices := leadNotices(t, s, pipelineSession)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0], "Could not remove the git worktree of task "+task.ID)
	require.Contains(t, notices[0], view.Branch)
}

func TestOneScopedWorktreePerMember(t *testing.T) {
	t.Parallel()
	s, _ := newWorktreeService(t, initTeamRepo(t), false)
	spawnMember(t, s, "impl")
	a := createTask(t, s, CreateTaskRequest{Subject: "A", Description: "d", WriteScopes: []string{"docs"}})
	b := createTask(t, s, CreateTaskRequest{Subject: "B", Description: "d", WriteScopes: []string{"internal"}})
	c := createTask(t, s, CreateTaskRequest{Subject: "C", Description: "d"})
	act(t, s, "impl", a.ID, "claim", nil)

	err := actErr(s, "impl", b.ID, "claim", nil)
	require.Equal(t, CodeTaskInvalidTransition, ErrorCode(err))
	require.Contains(t, err.Error(), "already hold")
	require.Empty(t, taskByID(t, s, b.ID).Worktree)
	act(t, s, "impl", c.ID, "claim", nil)
}

func TestPlaceStartDiscardsAWorktreeForAMovedClaim(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})

	// The planned claim is stale: the task is not owned by impl.
	start := dispatchStart{member: memberByID(t, s, "impl"), taskID: task.ID, sessionID: pipelineSession, revision: task.Revision, task: taskByID(t, s, task.ID)}
	_, ok := s.placeStart(start)
	require.False(t, ok, "a claim the board no longer holds does not start")
	require.NoDirExists(t, filepath.Join(worktreesDir(repo), "team-"+task.ID))
	require.False(t, branchExists(repo, "crush/team-"+task.ID))
	require.Empty(t, taskByID(t, s, task.ID).Worktree)
}

func TestWorktreeFieldsAreOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	for _, value := range []any{Task{ID: "task-1"}, TaskView{ID: "task-1"}} {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(data), "worktree")
		require.NotContains(t, string(data), "branch")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agentteam/ -run 'Worktree|TestManualClaimPlaces|TestLeadAssignPlaces|TestReleasedTaskKeeps|TestReopenedTaskKeeps|TestDeleteRemoves|TestDeleteRemovalFailure|TestOneScoped|TestPlaceStart|TestTasksWithoutScopes|TestDispatchGivesAScoped'`
Expected: FAIL to compile with `unknown field WorkingDir in struct literal of type Config`, `got.Worktree undefined`, `undefined: Workspace`, `s.MemberWorkspace undefined`.

- [ ] **Step 3: Add the fields, the code and the Service state**

In `internal/agentteam/team.go` (no new imports), in the error code block, after `CodeStateBusy`, add:

```go
	CodeTaskGit               = "TEAM_TASK_GIT"
```

In the `Task` struct, after `LastFailure`, add:

```go
	// Worktree is the absolute path of the task's own git worktree, and
	// Branch its branch. They are set when the task is first given to a
	// teammate inside a git repository and kept through release, reopen
	// and failed verification; a merge or a delete removes the worktree.
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
```

In the `TaskView` struct, after `Escalated`, add:

```go
	Worktree           string     `json:"worktree,omitempty"`
	Branch             string     `json:"branch,omitempty"`
```

In `projectTaskView`, in the `view := TaskView{...}` literal, after `Escalated:   task.Escalated,`, add:

```go
		Worktree:    task.Worktree,
		Branch:      task.Branch,
```

In `Config`, after `RoleExists`, add:

```go
	// WorkingDir is the Lead's working directory. Inside a git repository,
	// a scoped task given to a teammate gets its own worktree; empty, or
	// outside a repository, every member shares this tree.
	WorkingDir string
```

In the `Service` struct, after `leadNotifyHook`, add:

```go
	// gitMu serializes the service's git commands, which share one
	// repository. It is never held with s.mu, the write slot or the file
	// lock.
	gitMu sync.Mutex
	// repoOnce resolves, once and outside every lock, whether WorkingDir is
	// in a git repository and where.
	repoOnce   sync.Once
	repoRoot   string
	repoPrefix string
	repoOK     bool
	// worktreeRefused maps a task (refusalKey) to the revision it was
	// handed back at after its worktree failed, so the dispatcher leaves
	// it until the Lead changes it.
	worktreeRefused map[string]int
```

In `New`, in the returned `&Service{...}` literal, add `worktreeRefused: make(map[string]int),`.

After `needsVerification`, add:

```go
// wantsWorktree reports whether t gets its own git worktree when a teammate
// takes it: it has write scopes and is not a verification task, which runs
// in the worktree of the work it checks.
func (t Task) wantsWorktree() bool {
	return len(t.WriteScopes) > 0 && t.Verifies == ""
}
```

In `taskUpdate`, after `leadSessions`, add:

```go
	// place is set for a claim or assignment that needs its worktree
	// created once the write lands.
	place bool
	// removal is a worktree to remove once the write lands.
	removal *worktreeRemoval
```

- [ ] **Step 4: Create `internal/agentteam/worktree.go`**

```go
package agentteam

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/charmbracelet/crush/internal/gitutil"
)

// gitTimeout bounds one git step of a task transition, so a wedged git
// cannot hold a tool call, a dispatch pass or a settle forever.
const gitTimeout = 2 * time.Minute

// gitContext is the context one git step runs under. Git work outlives the
// tool call that asked for it, so it is never cancelled with that call.
func gitContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), gitTimeout)
}

// Workspace is where a teammate's tools run while it holds a task with a git
// worktree: the worktree's root, the directory inside it that matches the
// Lead's working directory, and the task branch.
type Workspace struct {
	Root   string
	Dir    string
	Branch string
}

// repo reports the repository that holds the Lead's working directory, and
// that directory's prefix inside it. It runs git once per Service, outside
// every lock; ok is false outside a git repository or with no working
// directory, which turns worktrees off.
func (s *Service) repo() (root, prefix string, ok bool) {
	if s.cfg.WorkingDir == "" {
		return "", "", false
	}
	s.repoOnce.Do(func() {
		ctx, cancel := gitContext()
		defer cancel()
		top, err := gitutil.RepoRoot(ctx, s.cfg.WorkingDir)
		if err != nil {
			return
		}
		sub, err := gitutil.RepoPrefix(ctx, s.cfg.WorkingDir)
		if err != nil {
			return
		}
		s.repoRoot, s.repoPrefix, s.repoOK = top, sub, true
	})
	return s.repoRoot, s.repoPrefix, s.repoOK
}

// MemberWorkspace returns the workspace member's tools run in: the worktree
// of the scoped task it holds in progress, or of the task its verification
// task checks. ok is false for the Lead and for a member with no such task;
// they work in the Lead's tree. It holds s.mu only to read the board.
func (s *Service) MemberWorkspace(member string) (Workspace, bool) {
	s.mu.Lock()
	ws, ok := s.memberWorktreeLocked(member)
	s.mu.Unlock()
	if !ok {
		return Workspace{}, false
	}
	_, prefix, _ := s.repo()
	ws.Dir = filepath.Join(ws.Root, filepath.FromSlash(prefix))
	return ws, true
}

// memberWorktreeLocked finds the worktree of name's in-progress task.
// Callers must hold s.mu.
func (s *Service) memberWorktreeLocked(name string) (Workspace, bool) {
	owner, ok := s.memberLocked(name)
	if !ok || owner.Role == RoleLead {
		return Workspace{}, false
	}
	for _, task := range s.state.Tasks {
		if task.OwnerID != owner.ID || task.Status != TaskInProgress {
			continue
		}
		root, branch := task.Worktree, task.Branch
		if task.Verifies != "" {
			if original, found := findTask(s.state, task.Verifies); found {
				root, branch = original.Worktree, original.Branch
			}
		}
		if root != "" {
			return Workspace{Root: root, Branch: branch}, true
		}
	}
	return Workspace{}, false
}

// holdsWorktreeLocked reports whether memberID owns, in progress, a task
// other than except that has its own worktree. A member works in one
// worktree at a time. Callers must hold s.mu.
func (s *Service) holdsWorktreeLocked(memberID, except string) bool {
	for _, task := range s.state.Tasks {
		if task.ID != except && task.OwnerID == memberID && task.Status == TaskInProgress && task.Worktree != "" {
			return true
		}
	}
	return false
}

// refusalKey names a task in worktreeRefused. Task ids repeat across Lead
// sessions in old files, so the session is part of the key.
func refusalKey(sessionID, taskID string) string {
	return sessionID + "/" + taskID
}

// worktreeRefusedLocked reports whether task was handed back for a failed
// worktree at its current revision. Callers must hold s.mu.
func (s *Service) worktreeRefusedLocked(task Task) bool {
	revision, ok := s.worktreeRefused[refusalKey(task.SessionID, task.ID)]
	return ok && revision == task.Revision
}

// createWorktree creates the worktree of taskID from the Lead's HEAD, on
// branch crush/team-<task id>. It returns empty values and no error outside
// a git repository, where tasks share the Lead's tree as before. Callers
// must hold no lock.
func (s *Service) createWorktree(ctx context.Context, taskID string) (path, branch string, err error) {
	if _, _, ok := s.repo(); !ok {
		return "", "", nil
	}
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	return gitutil.CreateWorktreeFrom(ctx, s.cfg.WorkingDir, "team-"+taskID, "HEAD")
}

// recordWorktree stores a new worktree on its task in a compare-and-set
// write against the claim it was made for: same task, owner and revision,
// still in progress, with no worktree yet. The revision does not change, so
// the brief's expected_revision stays valid. It reports whether it stored.
func (s *Service) recordWorktree(taskID, sessionID, ownerID string, revision int, path, branch string) bool {
	recorded := false
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		for i := range s.state.Tasks {
			task := &s.state.Tasks[i]
			if task.ID != taskID || task.SessionID != sessionID {
				continue
			}
			if task.OwnerID != ownerID || task.Status != TaskInProgress || task.Revision != revision || task.Worktree != "" {
				return false, nil
			}
			task.Worktree, task.Branch = path, branch
			recorded = true
			s.notifyLocked()
			return true, nil
		}
		return false, nil
	})
	return recorded
}

// discardWorktree removes a worktree no task names, with its branch. It is
// best effort: a leftover is logged, not noticed, because no task owns it.
func (s *Service) discardWorktree(ctx context.Context, path, branch string) {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	if err := gitutil.RemoveWorktree(ctx, s.cfg.WorkingDir, path, true); err != nil {
		slog.Warn("Could not remove an unused team worktree", "path", path, "error", err)
		return
	}
	if err := gitutil.DeleteBranch(ctx, s.cfg.WorkingDir, branch, true); err != nil {
		slog.Warn("Could not delete an unused team branch", "branch", branch, "error", err)
	}
}

// worktreeRemoval is a task worktree to remove once a write lands.
type worktreeRemoval struct {
	taskID, sessionID string
	path, branch      string
	// forceBranch deletes a branch that was never merged, as a deleted
	// task's is; a merged branch is deleted with the safe form.
	forceBranch bool
}

// removeTaskWorktree removes a task's worktree and branch after the state
// change that ends them landed. It is best effort: a failure is logged and
// told to the Lead, and the state change stands. Callers must hold no lock.
func (s *Service) removeTaskWorktree(ctx context.Context, r worktreeRemoval) {
	s.gitMu.Lock()
	err := gitutil.RemoveWorktree(ctx, s.cfg.WorkingDir, r.path, true)
	if err == nil {
		err = gitutil.DeleteBranch(ctx, s.cfg.WorkingDir, r.branch, r.forceBranch)
	}
	s.gitMu.Unlock()
	if err == nil {
		return
	}
	slog.Warn("Could not remove a team task worktree", "task", r.taskID, "path", r.path, "error", err)
	var (
		leadHook func(sessionID string)
		noted    bool
	)
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		leadHook = s.leadNotifyHook
		s.noteLeadLocked(r.sessionID, fmt.Sprintf(
			"Could not remove the git worktree of task %s at %s (branch %s): %v. Remove it by hand with git worktree remove --force and git branch -D.",
			r.taskID, r.path, r.branch, err))
		s.notifyLocked()
		noted = true
		return true, nil
	})
	if noted {
		wakeLeads(leadHook, []string{r.sessionID})
	}
}

// placeStart gives the scoped task of a planned start its worktree before
// its member starts. A task that has one keeps it, and a task without
// write scopes, or outside a git repository, starts as before. The
// worktree is created with no lock held and recorded against the claim, so
// a claim the Lead changed meanwhile gets nothing and the new worktree is
// removed. It reports whether the start goes ahead, with its brief
// rendered for the worktree.
func (s *Service) placeStart(start dispatchStart) (dispatchStart, bool) {
	if !start.task.wantsWorktree() || start.task.Worktree != "" {
		return start, true
	}
	ctx, cancel := gitContext()
	defer cancel()
	path, branch, err := s.createWorktree(ctx, start.taskID)
	if err != nil {
		s.worktreeFailed(start, err)
		return start, false
	}
	if path == "" {
		return start, true
	}
	if !s.recordWorktree(start.taskID, start.sessionID, start.member.ID, start.revision, path, branch) {
		s.discardWorktree(ctx, path, branch)
		s.abandonStart(start)
		return start, false
	}
	start.task.Worktree, start.task.Branch = path, branch
	start.prompt = taskBrief(start.task)
	return start, true
}

// abandonStart undoes the in-memory marks of a planned start that will not
// run because its claim moved on: the member is no longer starting, and a
// member created for it reads as idle instead of provisioning forever.
func (s *Service) abandonStart(start dispatchStart) {
	_ = s.write(mustApply, func() (bool, error) {
		delete(s.starting, start.member.Name)
		if claim, ok := s.claims[start.member.Name]; ok &&
			claim.taskID == start.taskID && claim.revision == start.revision {
			delete(s.claims, start.member.Name)
		}
		if s.closed {
			return false, nil
		}
		changed := s.idleProvisionedLocked(start.member.Name)
		if changed {
			s.notifyLocked()
		}
		return changed, nil
	})
}

// idleProvisionedLocked marks a member created for a start that never ran
// as inactive. Callers must hold s.mu.
func (s *Service) idleProvisionedLocked(name string) bool {
	for i := range s.state.Members {
		if s.state.Members[i].Name == name && s.state.Members[i].Status == StatusProvisioning {
			s.state.Members[i].Status = StatusInactive
			return true
		}
	}
	return false
}

// worktreeFailed hands back a dispatcher claim whose worktree could not be
// created. No attempt is charged, since the work never started, and the
// member is free again. The Lead is told once: the dispatcher leaves the
// task until the Lead changes it, instead of failing again on every pass.
func (s *Service) worktreeFailed(start dispatchStart, err error) {
	var (
		leadHook func(sessionID string)
		noted    bool
	)
	_ = s.write(mustApply, func() (bool, error) {
		delete(s.starting, start.member.Name)
		if s.closed {
			// Shutdown hands the recorded claim back itself.
			return false, nil
		}
		if claim, ok := s.claims[start.member.Name]; ok &&
			claim.taskID == start.taskID && claim.revision == start.revision {
			delete(s.claims, start.member.Name)
		}
		s.idleProvisionedLocked(start.member.Name)
		noted = s.releaseForWorktreeLocked(start.taskID, start.sessionID, start.member.ID, start.revision, err)
		leadHook = s.leadNotifyHook
		s.notifyLocked()
		return true, nil
	})
	if noted {
		wakeLeads(leadHook, []string{start.sessionID})
	}
}

// releaseForWorktreeLocked puts a claimed task whose worktree failed back
// to pending, uncharged, remembers it as refused at its new revision, and
// tells the Lead. It reports false when the claim moved on and nothing was
// released. Callers must hold s.mu.
func (s *Service) releaseForWorktreeLocked(taskID, sessionID, ownerID string, revision int, err error) bool {
	for i := range s.state.Tasks {
		task := &s.state.Tasks[i]
		if task.ID != taskID || task.SessionID != sessionID {
			continue
		}
		if task.OwnerID != ownerID || task.Status != TaskInProgress || task.Revision != revision {
			return false
		}
		task.Status = TaskPending
		task.OwnerID = ""
		task.Revision++
		s.worktreeRefused[refusalKey(sessionID, taskID)] = task.Revision
		s.noteLeadLocked(sessionID, fmt.Sprintf(
			"Task %s (%s) could not get its git worktree: %v. It is pending again with no attempt counted; the dispatcher tries again after you edit the task.",
			task.ID, task.Subject, err))
		return true
	}
	return false
}

// placeClaim gives a task a teammate just claimed, or was assigned, its
// worktree. It runs after the claim's write, with no lock held. A failure
// hands the claim back uncharged and tells the Lead, and the caller gets
// the error, so a teammate never starts the work in the Lead's tree.
func (s *Service) placeClaim(view TaskView) (TaskView, error) {
	ctx, cancel := gitContext()
	defer cancel()
	path, branch, err := s.createWorktree(ctx, view.ID)
	if err != nil {
		s.releaseUnplaced(view, err)
		return TaskView{}, newError(CodeTaskGit,
			"could not create the git worktree for task %s: %v. The claim was released and the task is pending again", view.ID, err)
	}
	if path == "" {
		return view, nil
	}
	if !s.recordWorktree(view.ID, view.SessionID, view.OwnerID, view.Revision, path, branch) {
		// The board moved the claim on between its write and this one, so
		// the worktree belongs to nobody.
		s.discardWorktree(ctx, path, branch)
		return view, nil
	}
	view.Worktree, view.Branch = path, branch
	return view, nil
}

// releaseUnplaced hands back a claim whose worktree failed.
func (s *Service) releaseUnplaced(view TaskView, err error) {
	var (
		leadHook func(sessionID string)
		noted    bool
	)
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		noted = s.releaseForWorktreeLocked(view.ID, view.SessionID, view.OwnerID, view.Revision, err)
		if !noted {
			return false, nil
		}
		leadHook = s.leadNotifyHook
		s.notifyLocked()
		return true, nil
	})
	if noted {
		wakeLeads(leadHook, []string{view.SessionID})
	}
}
```

- [ ] **Step 5: Claims, assignments and deletes in `applyTaskUpdateLocked`**

Replace the `var` block after `next := current`:

```go
	var (
		review  bool
		verdict string
		notes   string
	)
```

with:

```go
	var (
		review  bool
		verdict string
		notes   string
		place   bool
		removal *worktreeRemoval
	)
```

In `case "claim":`, replace:

```go
		next.Status = TaskInProgress
		next.OwnerID = member.ID
	case "release":
```

with:

```go
		if !lead && current.wantsWorktree() && s.holdsWorktreeLocked(member.ID, current.ID) {
			return taskUpdate{}, newError(CodeTaskInvalidTransition,
				"you already hold a task with its own git worktree; complete or release it before claiming another task with write scopes")
		}
		next.Status = TaskInProgress
		next.OwnerID = member.ID
		place = !lead && current.wantsWorktree() && current.Worktree == ""
	case "release":
```

In `case "reassign":`, replace:

```go
		next.Status = TaskInProgress
		next.OwnerID = assignee.ID
	case "delete":
```

with:

```go
		teammate := assignee.Role != RoleLead
		if teammate && current.wantsWorktree() && s.holdsWorktreeLocked(assignee.ID, current.ID) {
			return taskUpdate{}, newError(CodeTaskInvalidTransition,
				"%s already holds a task with its own git worktree; it works in one at a time", req.Owner)
		}
		next.Status = TaskInProgress
		next.OwnerID = assignee.ID
		place = teammate && current.wantsWorktree() && current.Worktree == ""
	case "delete":
```

In `case "delete":`, replace `next.Status = TaskDeleted` with:

```go
		next.Status = TaskDeleted
		if current.Worktree != "" {
			removal = &worktreeRemoval{
				taskID: current.ID, sessionID: current.SessionID,
				path: current.Worktree, branch: current.Branch, forceBranch: true,
			}
		}
```

Replace `update := taskUpdate{parentSessionID: parentSessionID, completedSubject: completedSubject, taskID: current.ID}` with:

```go
	update := taskUpdate{
		parentSessionID:  parentSessionID,
		completedSubject: completedSubject,
		taskID:           current.ID,
		place:            place,
		removal:          removal,
	}
```

- [ ] **Step 6: Run the claim's git work after the write in `UpdateTask`**

Replace `UpdateTask` with:

```go
// UpdateTask applies one compare-and-set task transition. Git work runs
// after the write, with no lock held: a claim gets its worktree, and a
// deleted task loses its worktree.
func (s *Service) UpdateTask(caller string, req UpdateTaskRequest) (TaskView, error) {
	var (
		applied  taskUpdate
		hook     func(sessionID, subject string)
		leadHook func(sessionID string)
	)
	err := s.write(refusable, func() (bool, error) {
		next, err := s.applyTaskUpdateLocked(caller, req)
		if err != nil {
			return false, err
		}
		applied = next
		hook = s.taskCompletedHook
		leadHook = s.leadNotifyHook
		s.notifyLocked()
		return true, nil
	})
	if err != nil {
		return TaskView{}, err
	}
	// A claim that needs a worktree gets it before anything else reacts to
	// the claim, so the teammate's next tool call already runs there.
	if applied.place {
		view, err := s.placeClaim(applied.view)
		if err != nil {
			return TaskView{}, err
		}
		applied.view = view
	}
	// The hooks run outside every lock, and only once the record is durable.
	if applied.completedSubject != "" && hook != nil {
		hook(applied.parentSessionID, applied.completedSubject)
	}
	wakeLeads(leadHook, applied.leadSessions)
	// Only the verdict path fills wake, so this is the failed-verification
	// wake; the log stays generic in case another path ever does.
	for _, name := range applied.wake {
		if err := s.wakeIfIdle(context.Background(), name); err != nil {
			slog.Warn("Could not wake a teammate", "member", name, "error", err)
			// The verdict handed the task back, but nobody runs to fix
			// it. A Warn log is invisible to the Lead, so say it in its
			// inbox, outside every lock, unless the Team is shut down.
			_ = s.write(mustApply, func() (bool, error) {
				if s.closed {
					return false, nil
				}
				s.noteLeadLocked(applied.parentSessionID, fmt.Sprintf(
					"Could not start %s to fix task %s after a failed verification: %s. The task stays in progress with nobody running it; to continue, %s.",
					name, applied.taskID, err, taskRecoveryActions(TaskInProgress)))
				s.notifyLocked()
				return true, nil
			})
			wakeLeads(leadHook, []string{applied.parentSessionID})
		}
	}
	if applied.removal != nil {
		ctx, cancel := gitContext()
		s.removeTaskWorktree(ctx, *applied.removal)
		cancel()
	}
	s.dispatch(context.Background(), applied.parentSessionID)
	return applied.view, nil
}
```

- [ ] **Step 7: Place the worktree in the dispatch pass and brief**

In `internal/agentteam/dispatch.go`, in `dispatchStart`, after `prompt    string`, add:

```go
	// task is the claimed task as the claim left it; a scoped one gets its
	// worktree before the start.
	task Task
```

In `dispatchPass`, replace:

```go
	for _, start := range starts {
		if _, err := s.launch(ctx, runner, start.member, start.prompt); err != nil {
			s.dispatchFailed(start, err)
		}
	}
```

with:

```go
	for _, start := range starts {
		start, ok := s.placeStart(start)
		if !ok {
			continue
		}
		if _, err := s.launch(ctx, runner, start.member, start.prompt); err != nil {
			s.dispatchFailed(start, err)
		}
	}
```

In `planDispatchLocked` and in `hasDispatchableLocked`, after the `if !scope[task.SessionID] || !dispatchable(s.state, task) { continue }` line, add:

```go
		if s.worktreeRefusedLocked(task) {
			continue
		}
```

In `planDispatchLocked`, replace the `starts = append(starts, dispatchStart{...})` literal with:

```go
		starts = append(starts, dispatchStart{
			member:    member,
			taskID:    claimed.ID,
			sessionID: claimed.SessionID,
			revision:  claimed.Revision,
			prompt:    taskBrief(s.briefTaskLocked(*claimed)),
			task:      *claimed,
		})
```

Replace `taskBrief` (Task 2's version) with:

```go
// taskBrief is the message a dispatched member starts from. It is per-task
// text in a message, so the role block before it on a first turn stays the
// shared prefix of every sibling with that role.
func taskBrief(task Task) string {
	last := fmt.Sprintf("Complete it with team_task_update (action \"complete\", expected_revision %d) when the acceptance criteria are met.", task.Revision)
	if task.Verifies != "" {
		last = `Record your verdict with team_task_update, action "pass", or action "fail" with notes describing what failed.`
	}
	brief := fmt.Sprintf("Task %s on the shared board was claimed for you: %s\n\n%s\n\nWrite scopes: %s\n%s",
		task.ID, task.Subject, task.Description, scopesText(task), last)
	if task.Worktree != "" {
		brief += "\n" + worktreeBrief(task)
	}
	if task.LastFailure != "" {
		brief += "\n\nThe last verification failed:\n\n" + task.LastFailure
	}
	return brief
}

// worktreeBrief tells a dispatched member where its tools run.
func worktreeBrief(task Task) string {
	if task.Verifies != "" {
		return fmt.Sprintf("Worktree: %s (branch %s). Your tools run there, and the change under review is committed on that branch.",
			task.Worktree, task.Branch)
	}
	return fmt.Sprintf("Worktree: %s (branch %s). Your tools run there: relative paths resolve inside it, and the service commits and merges the branch when the task is done.",
		task.Worktree, task.Branch)
}

// briefTaskLocked returns task as its brief describes it: a verification
// task names the worktree of the work it checks. Callers must hold s.mu.
func (s *Service) briefTaskLocked(task Task) Task {
	if task.Verifies != "" {
		if original, ok := findTask(s.state, task.Verifies); ok {
			task.Worktree, task.Branch = original.Worktree, original.Branch
		}
	}
	return task
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `gofumpt -w internal/agentteam/*.go && go test ./internal/agentteam/ && go test -race ./internal/agentteam/ -run 'Worktree|Claim|Dispatch|Shutdown'`
Expected: PASS. The existing dispatch and shutdown tests pass unchanged: their services have no `WorkingDir`, so `placeStart` starts every task as before.

- [ ] **Step 9: Commit**

```bash
git add internal/agentteam/worktree.go internal/agentteam/worktree_test.go internal/agentteam/team.go internal/agentteam/dispatch.go
git commit -m "feat: give scoped team tasks their own git worktree"
```

---

### Task 8: Teammate tools run in the task worktree

**Files:**
- Create: `internal/agent/tools/workdir.go`, `internal/agent/tools/workdir_test.go`
- Modify: `internal/agent/tools/bash.go`, `download.go`, `edit.go`, `multiedit.go`, `write.go`, `view.go`, `glob.go`, `grep.go`, `ls.go`, `apply_patch.go` (the first lines of each `Run` closure)
- Create: `internal/agent/worktree_tool.go`, `internal/agent/worktree_tool_test.go`
- Modify: `internal/agent/coordinator.go` (`buildTools`, the teammate block at the end)
- Modify: `internal/agent/scope_gate.go` (`scopeGatedTool.Run`)
- Modify: `internal/agent/team.go` (`syncAgentTeams`, the `agentteam.Config` literal)
- Modify: `internal/agent/team_cache_test.go` (`TestHookWrapFollowsRole`, `TestTeammateWrapperOrderKeepsLeadPrefix`)

**Interfaces:**
- Consumes: `agentteam.Workspace`, `(*agentteam.Service).MemberWorkspace(member string) (agentteam.Workspace, bool)`, `agentteam.Config.WorkingDir`, `TaskView.Worktree` (Task 7).
- Produces:
  - `func tools.WithWorkingDir(ctx context.Context, dir string) context.Context`
  - `func tools.WorkingDirFrom(ctx context.Context, fallback string) string`
  - `type worktreeTool struct { inner fantasy.AgentTool; workspace func(context.Context) (agentteam.Workspace, bool) }`
  - `func placeToolsInWorktrees(list []fantasy.AgentTool, workspace func(context.Context) (agentteam.Workspace, bool)) []fantasy.AgentTool`
  - `func withWorkspace(ctx context.Context, ws agentteam.Workspace) context.Context`
  - `func workspaceFrom(ctx context.Context) (agentteam.Workspace, bool)`
  - `func (c *coordinator) memberWorkspace(ctx context.Context) (agentteam.Workspace, bool)`
  - `const lspWorktreeRefusal`
  - test helpers `initTeamGitRepo`, `gitInRepo`, `newWorktreeTestCoordinator`, `claimInWorktree` (package `agent`, reused by Task 10)

- [ ] **Step 1: Write the failing tools test**

Create `internal/agent/tools/workdir_test.go`:

```go
package tools

import (
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
)

func TestWorkingDirFrom(t *testing.T) {
	t.Parallel()
	require.Equal(t, "/lead", WorkingDirFrom(t.Context(), "/lead"))
	require.Equal(t, "/tree", WorkingDirFrom(WithWorkingDir(t.Context(), "/tree"), "/lead"))
	require.Equal(t, "/lead", WorkingDirFrom(WithWorkingDir(t.Context(), ""), "/lead"), "an empty directory changes nothing")
}

// TestGlobReadsTheContextWorkingDir pins that a tool reads its working
// directory per call, including for a relative path argument.
func TestGlobReadsTheContextWorkingDir(t *testing.T) {
	t.Parallel()
	base, other := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, "found.txt"), []byte("x"), 0o644))
	tool := NewGlobTool(base, config.ToolGlob{})
	ctx := WithWorkingDir(t.Context(), other)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "g1", Name: GlobToolName, Input: `{"pattern":"*.txt"}`})
	require.NoError(t, err)
	require.Contains(t, resp.Content, "found.txt")

	resp, err = tool.Run(ctx, fantasy.ToolCall{ID: "g2", Name: GlobToolName, Input: `{"pattern":"*.txt","path":"."}`})
	require.NoError(t, err)
	require.Contains(t, resp.Content, "found.txt", "a relative path resolves inside the call's working directory")
}
```

- [ ] **Step 2: Write the failing agent tests**

Create `internal/agent/worktree_tool_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
)

// initTeamGitRepo is a Lead tree in a real repository: one commit, no
// signing, whatever the user's global git config says.
func initTeamGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		gitInRepo(t, dir, args...)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644))
	gitInRepo(t, dir, "add", ".")
	gitInRepo(t, dir, "commit", "-m", "initial")
	return dir
}

func gitInRepo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// newWorktreeTestCoordinator is newTeammateTestCoordinator with a Team whose
// Lead works in a git repository, so scoped claims get worktrees.
func newWorktreeTestCoordinator(t *testing.T) (*coordinator, string) {
	t.Helper()
	repo := initTeamGitRepo(t)
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	coord.hookRunner = hooks.NewRunner(hooks.Static(nil), t.TempDir(), t.TempDir())
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{
		WorkingDir: repo,
		Autostart:  func() bool { return false },
	}, newTeamFakeRunner())
	require.NoError(t, err)
	t.Cleanup(service.Shutdown)
	coord.setTeam(service)
	lead := coord.coderAgent()
	require.NotNil(t, lead)
	leadTools, err := coord.buildTools(t.Context(), coord.cfg.Config().Agents[config.AgentCoder], roleLead, func() Model { return coord.mainModels().Large })
	require.NoError(t, err)
	lead.SetTools(leadTools)
	return coord, repo
}

// claimInWorktree spawns teammate name and has it claim a task scoped to
// scope, which gives the task its worktree.
func claimInWorktree(t *testing.T, coord *coordinator, name, scope string) agentteam.Workspace {
	t.Helper()
	team := coord.teamService()
	_, err := team.SpawnTeammate(t.Context(), agentteam.LeadName, agentteam.SpawnRequest{Name: name, Description: "d", Prompt: "p", ParentID: teamTestSessionID})
	require.NoError(t, err)
	task, err := team.CreateTask(agentteam.LeadName, teamTestSessionID, agentteam.CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{scope}})
	require.NoError(t, err)
	claimed, err := team.UpdateTask(name, agentteam.UpdateTaskRequest{TaskID: task.ID, ExpectedRevision: task.Revision, Action: "claim", SessionID: teamTestSessionID})
	require.NoError(t, err)
	require.NotEmpty(t, claimed.Worktree)
	ws, ok := team.MemberWorkspace(name)
	require.True(t, ok)
	return ws
}

func runIn(t *testing.T, ctx context.Context, tool fantasy.AgentTool, input string) fantasy.ToolResponse {
	t.Helper()
	require.NotNil(t, tool)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: tool.Info().Name, Input: input})
	require.NoError(t, err)
	return resp
}

func TestWorktreeToolForwardsIdentity(t *testing.T) {
	t.Parallel()
	recorder := &recordingTool{name: tools.EditToolName}
	none := func(context.Context) (agentteam.Workspace, bool) { return agentteam.Workspace{}, false }
	tool := placeToolsInWorktrees([]fantasy.AgentTool{recorder}, none)[0]
	require.Equal(t, recorder.Info(), tool.Info(), "the wrapper must not change what the provider sees")
	opts := fantasy.ProviderOptions{anthropic.Name: &anthropic.ProviderCacheControlOptions{CacheControl: anthropic.CacheControl{Type: "ephemeral"}}}
	tool.SetProviderOptions(opts)
	require.Equal(t, opts, recorder.ProviderOptions())
	require.Equal(t, opts, tool.ProviderOptions())
	mcp := &worktreeTool{inner: &roleMCPTool{name: "mcp_x_y", server: "x"}, workspace: none}
	require.Equal(t, "x", mcp.MCP())
}

func TestWorktreeToolPassesTheWorkspaceOn(t *testing.T) {
	t.Parallel()
	ws := agentteam.Workspace{Root: "/tree", Dir: "/tree/sub", Branch: "crush/team-task-1"}
	inside := func(context.Context) (agentteam.Workspace, bool) { return ws, true }
	inner := &fakeTool{name: tools.ViewToolName, resp: fantasy.NewTextResponse("ok")}
	runIn(t, t.Context(), placeToolsInWorktrees([]fantasy.AgentTool{inner}, inside)[0], `{}`)
	require.Equal(t, "/tree/sub", tools.WorkingDirFrom(inner.gotCtx, "/lead"))
	got, ok := workspaceFrom(inner.gotCtx)
	require.True(t, ok)
	require.Equal(t, ws, got)

	lsp := &fakeTool{name: tools.SymbolsToolName}
	resp := runIn(t, t.Context(), placeToolsInWorktrees([]fantasy.AgentTool{lsp}, inside)[0], `{}`)
	require.False(t, lsp.called)
	require.True(t, resp.IsError)
	require.Equal(t, lspWorktreeRefusal, resp.Content)

	outside := func(context.Context) (agentteam.Workspace, bool) { return agentteam.Workspace{}, false }
	plain := &fakeTool{name: tools.SymbolsToolName, resp: fantasy.NewTextResponse("ok")}
	runIn(t, t.Context(), placeToolsInWorktrees([]fantasy.AgentTool{plain}, outside)[0], `{}`)
	require.True(t, plain.called, "with no worktree the tool runs as before")
	require.Equal(t, "/lead", tools.WorkingDirFrom(plain.gotCtx, "/lead"))
}

func TestTeammateToolsRunInTheTaskWorktree(t *testing.T) {
	coord, repo := newWorktreeTestCoordinator(t)
	ws := claimInWorktree(t, coord, "impl", "docs")
	teammate := buildTeammateAgent(t, coord, agentteam.Member{Name: "impl", Role: agentteam.RoleTeammate})
	list := teammate.(*sessionAgent).tools.Copy()
	sess, err := coord.sessions.Create(t.Context(), "Teammate impl")
	require.NoError(t, err)
	ctx := context.WithValue(agentteam.WithMember(t.Context(), "impl"), tools.SessionIDContextKey, sess.ID)

	resp := runIn(t, ctx, toolByName(list, tools.WriteToolName), `{"file_path":"docs/a.md","content":"in the worktree\n"}`)
	require.False(t, resp.IsError, resp.Content)
	require.FileExists(t, filepath.Join(ws.Root, "docs", "a.md"))
	require.NoFileExists(t, filepath.Join(repo, "docs", "a.md"), "the Lead's tree is untouched")

	resp = runIn(t, ctx, toolByName(list, tools.ViewToolName), `{"file_path":"docs/a.md"}`)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "in the worktree")

	resp = runIn(t, ctx, toolByName(list, tools.BashToolName), `{"command":"pwd","description":"where"}`)
	require.Contains(t, resp.Content, ws.Root)

	resp = runIn(t, ctx, toolByName(list, tools.SymbolsToolName), `{"file_path":"README.md"}`)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "LSP tools are not available in a task worktree")
}

// TestTeammateInWorktreeKeepsLeadPrefix extends the cache tests: a teammate
// in a task worktree sends the Lead's tools block byte for byte and the
// Lead's system prompt plus its identity, and no tool definition names a
// working directory.
func TestTeammateInWorktreeKeepsLeadPrefix(t *testing.T) {
	coord, repo := newWorktreeTestCoordinator(t)
	ws := claimInWorktree(t, coord, "impl", "docs")
	lead := coord.coderAgent()
	require.NotNil(t, lead)
	member := agentteam.Member{Name: "impl", Role: agentteam.RoleTeammate}
	teammate := buildTeammateAgent(t, coord, member)

	leadTools := lead.(*sessionAgent).tools.Copy()
	teammateTools := teammate.(*sessionAgent).tools.Copy()
	require.Equal(t, toolNames(leadTools), toolNames(teammateTools))
	for i, tool := range teammateTools {
		want, err := json.Marshal(leadTools[i].Info())
		require.NoError(t, err)
		have, err := json.Marshal(tool.Info())
		require.NoError(t, err)
		require.Equal(t, string(want), string(have), "tool %q", tool.Info().Name)
		for _, dir := range []string{coord.cfg.WorkingDir(), repo, ws.Root} {
			require.NotContains(t, string(want), dir, "Lead tool %q names a working directory", tool.Info().Name)
			require.NotContains(t, string(have), dir, "teammate tool %q names a working directory", tool.Info().Name)
		}
	}
	require.Equal(t, lead.SystemPrompt()+teamIdentity(member), teammate.SystemPrompt())
	require.NotContains(t, teammate.SystemPrompt(), ws.Root)
}
```

If `TestTeammateInWorktreeKeepsLeadPrefix` fails on a Lead tool before any change of this task (a Lead tool description already embeds the working directory), stop and report it: that is an existing cache defect, outside this plan.

- [ ] **Step 3: Update the wrapper-order assertions**

In `internal/agent/team_cache_test.go`, in `TestHookWrapFollowsRole`, replace:

```go
	_, hooked := toolByName(teammate, tools.ViewToolName).(*hookedTool)
	require.True(t, hooked, "a teammate's tools must be hook-wrapped")
```

with:

```go
	place, ok := toolByName(teammate, tools.ViewToolName).(*worktreeTool)
	require.True(t, ok, "a teammate's tools run through the worktree wrapper")
	_, hooked := place.inner.(*hookedTool)
	require.True(t, hooked, "a teammate's tools must be hook-wrapped")
```

In `TestTeammateWrapperOrderKeepsLeadPrefix`, replace:

```go
		scope, ok := gate.inner.(*scopeGatedTool)
		require.True(t, ok, "%s: scope gate is next", role.Name)
```

with:

```go
		place, ok := gate.inner.(*worktreeTool)
		require.True(t, ok, "%s: the worktree wrapper is next", role.Name)
		scope, ok := place.inner.(*scopeGatedTool)
		require.True(t, ok, "%s: scope gate is next", role.Name)
```

and update that test's doc comment's order to "role gate, then worktree wrapper, then scope gate, then hook wrapper".

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/agent/tools/ -run 'TestWorkingDirFrom|TestGlobReadsTheContextWorkingDir' ; go test ./internal/agent/ -run 'Worktree|TestHookWrapFollowsRole|TestTeammateWrapperOrderKeepsLeadPrefix'`
Expected: FAIL to compile with `undefined: WithWorkingDir`, `undefined: placeToolsInWorktrees`, `undefined: worktreeTool`.

- [ ] **Step 5: Add the context helpers**

Create `internal/agent/tools/workdir.go`:

```go
package tools

import "context"

type workingDirKey struct{}

// WithWorkingDir returns ctx carrying dir as the working directory a tool
// call resolves relative paths against and runs in. A teammate working in a
// task worktree gets its worktree this way, on every call, so its tools are
// built exactly like the Lead's and no tool description changes. An empty
// dir leaves ctx as it is.
func WithWorkingDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, workingDirKey{}, dir)
}

// WorkingDirFrom returns the working directory ctx carries, or fallback,
// the directory the tool was built with.
func WorkingDirFrom(ctx context.Context, fallback string) string {
	if dir, ok := ctx.Value(workingDirKey{}).(string); ok && dir != "" {
		return dir
	}
	return fallback
}
```

- [ ] **Step 6: Read the working directory per call in each tool**

In each closure below, add the line as the first statement of the `func(ctx context.Context, params ..., call fantasy.ToolCall)` body. It shadows the captured constructor parameter for the rest of the call, so every existing use of `workingDir` in the closure (paths, permission requests, diffs, the edit context, the shell's directory) follows it:

```go
			workingDir := WorkingDirFrom(ctx, workingDir)
```

The closures: `NewBashTool` (`bash.go`), `NewDownloadTool` (`download.go`), `NewEditTool` (`edit.go`), `NewMultiEditTool` (`multiedit.go`), `NewWriteTool` (`write.go`), `NewGlobTool` (`glob.go`), `NewGrepTool` (`grep.go`), `NewLsTool` (`ls.go`), `NewApplyPatchTool` (`apply_patch.go`).

`newViewLikeTool` (`view.go`) keeps the directory on its reader. Replace its closure body:

```go
		func(ctx context.Context, params ViewParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if len(params.FilePaths) > 0 {
				return view.readFiles(ctx, params, call)
			}
			return view.readFile(ctx, params, call)
		},
```

with:

```go
		func(ctx context.Context, params ViewParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			reader := view
			reader.workingDir = WorkingDirFrom(ctx, view.workingDir)
			if len(params.FilePaths) > 0 {
				return reader.readFiles(ctx, params, call)
			}
			return reader.readFile(ctx, params, call)
		},
```

`glob` and `grep` resolved a relative `path` argument against the process directory, which in a worktree is the Lead's tree. In `glob.go` replace `searchPath := home.Long(cmp.Or(params.Path, workingDir))` with:

```go
			searchPath := filepathext.SmartJoin(workingDir, home.Long(cmp.Or(params.Path, workingDir)))
```

and make the same replacement in `grep.go`. Add `"github.com/charmbracelet/crush/internal/filepathext"` to the imports of both files if missing.

Leave `fetch`, the MCP tools, `worktree` and every `lsp_*` tool unchanged: `fetch` and MCP tools use the directory only for a permission prompt, `worktree` manages the Lead's worktrees, and the LSP tools are refused in a worktree (Step 7).

- [ ] **Step 7: Create the wrapper**

Create `internal/agent/worktree_tool.go`:

```go
package agent

import (
	"context"
	"strings"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
)

// lspWorktreeRefusal is what an LSP tool returns to a teammate in a task
// worktree. The language servers index the Lead's tree, so their answers
// would describe files the teammate is not editing.
const lspWorktreeRefusal = "LSP tools are not available in a task worktree: the language servers serve the Lead's tree only. Use view, grep and bash in your worktree instead."

// worktreeTool runs a teammate's tool call in the git worktree of the task
// it holds. The workspace is looked up on every call, because a teammate
// can claim its task in the middle of a turn, and it reaches the tool on
// the call's context. The tool keeps the Info, provider options and MCP
// identity of the Lead's copy, so the tools block stays byte-identical and
// the cache is shared.
type worktreeTool struct {
	inner     fantasy.AgentTool
	workspace func(ctx context.Context) (agentteam.Workspace, bool)
}

// placeToolsInWorktrees wraps every tool in list.
func placeToolsInWorktrees(list []fantasy.AgentTool, workspace func(context.Context) (agentteam.Workspace, bool)) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, len(list))
	for i, tool := range list {
		out[i] = &worktreeTool{inner: tool, workspace: workspace}
	}
	return out
}

func (w *worktreeTool) Info() fantasy.ToolInfo { return w.inner.Info() }

// MCP reports the MCP server of the wrapped tool, or "" for a built-in
// tool, so MCP filtering sees through the wrapper.
func (w *worktreeTool) MCP() string {
	if mcpTool, ok := w.inner.(interface{ MCP() string }); ok {
		return mcpTool.MCP()
	}
	return ""
}

func (w *worktreeTool) ProviderOptions() fantasy.ProviderOptions {
	return w.inner.ProviderOptions()
}

func (w *worktreeTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	w.inner.SetProviderOptions(opts)
}

func (w *worktreeTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	ws, ok := w.workspace(ctx)
	if !ok {
		return w.inner.Run(ctx, call)
	}
	if strings.HasPrefix(w.inner.Info().Name, "lsp_") {
		return fantasy.NewTextErrorResponse(lspWorktreeRefusal), nil
	}
	ctx = tools.WithWorkingDir(ctx, ws.Dir)
	ctx = withWorkspace(ctx, ws)
	return w.inner.Run(ctx, call)
}

type workspaceKey struct{}

// withWorkspace puts the teammate's workspace on ctx for the scope gate,
// which needs the worktree root as well as the working directory.
func withWorkspace(ctx context.Context, ws agentteam.Workspace) context.Context {
	return context.WithValue(ctx, workspaceKey{}, ws)
}

// workspaceFrom returns the workspace withWorkspace put on ctx.
func workspaceFrom(ctx context.Context) (agentteam.Workspace, bool) {
	ws, ok := ctx.Value(workspaceKey{}).(agentteam.Workspace)
	return ws, ok && ws.Root != ""
}

// memberWorkspace returns the workspace of the teammate making the call.
// The Lead and a member with no scoped task work in the Lead's tree.
func (c *coordinator) memberWorkspace(ctx context.Context) (agentteam.Workspace, bool) {
	team := c.teamService()
	if team == nil {
		return agentteam.Workspace{}, false
	}
	member := agentteam.MemberFromContext(ctx)
	if member == agentteam.LeadName {
		return agentteam.Workspace{}, false
	}
	return team.MemberWorkspace(member)
}
```

- [ ] **Step 8: Wrap teammate tools, read the gate's directory per call, wire the service**

In `internal/agent/coordinator.go`, at the end of `buildTools`, replace:

```go
	// A teammate's write tools are held to the write scopes of the task it
	// claimed. The gate goes outside the hook wrapper, so a refused call
	// runs no hook; the role gate SetTools adds goes outside both.
	if role == roleTeammate {
		filteredTools = gateToolsForScopes(filteredTools, c.claimedScopes, c.cfg.WorkingDir())
	}
```

with:

```go
	// A teammate's write tools are held to the write scopes of the task it
	// claimed. The gate goes outside the hook wrapper, so a refused call
	// runs no hook. The worktree wrapper goes outside the gate, so the gate
	// sees the call's worktree; the role gate SetTools adds goes outside
	// all of them.
	if role == roleTeammate {
		filteredTools = gateToolsForScopes(filteredTools, c.claimedScopes, c.cfg.WorkingDir())
		filteredTools = placeToolsInWorktrees(filteredTools, c.memberWorkspace)
	}
```

In `internal/agent/scope_gate.go`, in `scopeGatedTool.Run`, replace:

```go
	for _, path := range paths {
		rel, inside := workspaceRelativePath(g.workingDir, path)
```

with:

```go
	// A teammate in a task worktree resolves its paths there, so its
	// scopes are checked there too.
	workingDir := tools.WorkingDirFrom(ctx, g.workingDir)
	for _, path := range paths {
		rel, inside := workspaceRelativePath(workingDir, path)
```

In `internal/agent/team.go`, in `syncAgentTeams`, replace:

```go
		teamService, err := agentteam.New(teamPath, agentteam.Config{
			Autostart:  c.teamAutostart,
			RoleExists: c.teamRoleExists,
		}, nil)
```

with:

```go
		teamService, err := agentteam.New(teamPath, agentteam.Config{
			Autostart:  c.teamAutostart,
			RoleExists: c.teamRoleExists,
			WorkingDir: c.cfg.WorkingDir(),
		}, nil)
```

- [ ] **Step 9: Run the tests to verify they pass**

Run: `gofumpt -w internal/agent/tools/*.go internal/agent/worktree_tool.go internal/agent/worktree_tool_test.go internal/agent/coordinator.go internal/agent/scope_gate.go internal/agent/team.go internal/agent/team_cache_test.go && go test ./internal/agent/tools/ && go test ./internal/agent/`
Expected: PASS, including every existing cache test (`TestTeammateToolPaletteMatchesLead`, `TestTeammateScopesKeepLeadPrefix`, `TestTeammateWrapperOrderKeepsLeadPrefix`).

- [ ] **Step 10: Commit**

```bash
git add internal/agent/tools/ internal/agent/worktree_tool.go internal/agent/worktree_tool_test.go internal/agent/coordinator.go internal/agent/scope_gate.go internal/agent/team.go internal/agent/team_cache_test.go
git commit -m "feat: run teammate tools in their task worktree"
```

---
### Task 9: Commit on complete, merge on pass and on accept

**Files:**
- Create: `internal/agentteam/merge.go`
- Modify: `internal/agentteam/team.go` (`taskUpdate`, `UpdateTask`, `applyTaskUpdateLocked` `case "complete":` and the verdict call, `watch`, `settleTasksLocked`)
- Modify: `internal/agentteam/verify.go` (`newVerificationTaskLocked`, `applyVerdictLocked`)
- Test: `internal/agentteam/merge_test.go` (create)

**Interfaces:**
- Consumes: `gitutil.CommitAll`, `gitutil.MergeNoFF`, `*gitutil.MergeError` (Task 5); `gitContext`, `removeTaskWorktree`, `worktreeRemoval`, `Task.Worktree`, `Task.Branch`, `s.gitMu` and the test helpers `initTeamRepo`, `gitIn`, `writeTreeFile`, `branchExists`, `newWorktreeService`, `spawnMember` (Task 7); `maxFailureNotes` (Task 2).
- Produces:
  - `type mergeRequest struct { taskID, sessionID, subject, worktree, branch string; revision int }`, `func mergeRequestFor(task Task) mergeRequest`
  - `func (s *Service) mergeTask(ctx context.Context, m mergeRequest)`
  - `func (s *Service) mergeBranch(ctx context.Context, m mergeRequest) error`
  - `func (s *Service) settleMerge(m mergeRequest, mergeErr error) bool`
  - `func (s *Service) commitHeldWork(name string) map[string]error`
  - `func (s *Service) runCompletePrep(caller string, req UpdateTaskRequest) (*mergeRequest, error)`
  - `type verdictResult struct { completedSubject string; wake, leads []string; wakeTask string; merge *mergeRequest }`; `applyVerdictLocked` returns it
  - `settleTasksLocked(ownerID string, runErr error, hold bool, uncommitted map[string]error) (settled, completed []string, merges []mergeRequest)`
  - `taskUpdate.merges []mergeRequest`

- [ ] **Step 1: Write the failing tests**

Create `internal/agentteam/merge_test.go`:

```go
package agentteam

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// scopedTask creates a docs-scoped task and has member claim it, which
// gives it its worktree.
func scopedTask(t *testing.T, s *Service, member, subject string, verify bool) TaskView {
	t.Helper()
	task := createTask(t, s, CreateTaskRequest{Subject: subject, Description: "d", WriteScopes: []string{"docs"}, Verify: new(verify)})
	view := act(t, s, member, task.ID, "claim", nil)
	require.NotEmpty(t, view.Worktree)
	return view
}

// passCheck has reviewer claim the check of taskID and pass it.
func passCheck(t *testing.T, s *Service, reviewer, taskID string) {
	t.Helper()
	check := verificationTaskFor(t, s, taskID)
	act(t, s, reviewer, check.ID, "claim", nil)
	act(t, s, reviewer, check.ID, "pass", nil)
}

func readTreeFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}

func lastLeadNotice(t *testing.T, s *Service) string {
	t.Helper()
	notices := leadNotices(t, s, pipelineSession)
	require.NotEmpty(t, notices)
	return notices[len(notices)-1]
}

// failingPreCommit makes every commit in repo, worktrees included, fail.
func failingPreCommit(t *testing.T, repo string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the test hook is a shell script")
	}
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	require.NoError(t, os.MkdirAll(filepath.Dir(hook), 0o755))
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\necho refused by hook >&2\nexit 1\n"), 0o755))
}

func TestCompleteCommitsAndPassMergesTheBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "rev")
	view := scopedTask(t, s, "impl", "Write the docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	act(t, s, "impl", view.ID, "complete", nil)
	require.Equal(t, TaskInReview, taskByID(t, s, view.ID).Status)
	require.Equal(t, "Write the docs", gitIn(t, view.Worktree, "log", "-1", "--format=%s"), "complete commits with the subject")
	require.Empty(t, gitIn(t, view.Worktree, "status", "--porcelain"))
	require.Contains(t, verificationTaskFor(t, s, view.ID).Description, "committed on branch "+view.Branch)

	passCheck(t, s, "rev", view.ID)
	got := taskByID(t, s, view.ID)
	require.Equal(t, TaskCompleted, got.Status)
	require.Empty(t, got.Worktree, "a merged task forgets its removed worktree")
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
	require.Equal(t, "Merge team task "+view.ID+": Write the docs", gitIn(t, repo, "log", "-1", "--format=%s"))
	require.Len(t, strings.Fields(gitIn(t, repo, "rev-list", "--parents", "-n", "1", "HEAD")), 3, "--no-ff makes a merge commit")
	require.NoDirExists(t, view.Worktree)
	require.False(t, branchExists(repo, view.Branch))
}

func TestEmptyChangeSetStillMovesToReview(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Nothing to do", true)
	act(t, s, "impl", view.ID, "complete", nil)
	require.Equal(t, TaskInReview, taskByID(t, s, view.ID).Status)
	require.Equal(t, gitIn(t, repo, "rev-parse", "HEAD"), gitIn(t, view.Worktree, "rev-parse", "HEAD"), "no empty commit")
}

func TestCommitFailureRefusesComplete(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	failingPreCommit(t, repo)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	err := actErr(s, "impl", view.ID, "complete", nil)
	require.Equal(t, CodeTaskGit, ErrorCode(err))
	require.Contains(t, err.Error(), "refused by hook")
	got := taskByID(t, s, view.ID)
	require.Equal(t, TaskInProgress, got.Status, "the task is unchanged")
	require.Equal(t, view.Revision, got.Revision)
}

func TestSettleCommitFailureReleasesTheTask(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	failingPreCommit(t, repo)
	s, runner := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	runner.finish("impl")
	require.Eventually(t, func() bool { return taskByID(t, s, view.ID).Status == TaskPending }, 5*time.Second, 20*time.Millisecond,
		"work that is not on its branch is released, not merged")
	require.Equal(t, view.Worktree, taskByID(t, s, view.ID).Worktree)
	require.Equal(t, "docs\n", readTreeFile(t, view.Worktree, "docs/a.md"), "the work stays for the next turn")
	require.NoFileExists(t, filepath.Join(repo, "docs", "a.md"))
}

func TestFailKeepsTheWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "rev")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
	act(t, s, "impl", view.ID, "complete", nil)
	check := verificationTaskFor(t, s, view.ID)
	act(t, s, "rev", check.ID, "claim", nil)
	act(t, s, "rev", check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "Missing a section" })

	require.Equal(t, TaskInProgress, taskByID(t, s, view.ID).Status)
	require.DirExists(t, view.Worktree)
	require.True(t, branchExists(repo, view.Branch))
	ws, ok := s.MemberWorkspace("impl")
	require.True(t, ok)
	require.Equal(t, view.Worktree, ws.Root, "the implementer is back in its worktree")
	require.NoFileExists(t, filepath.Join(repo, "docs", "a.md"))
}

// TestReopenedTaskShowsPriorWorkToTheNextImplementer pins Review Focus 3.
func TestReopenedTaskShowsPriorWorkToTheNextImplementer(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, true)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})
	first := taskByID(t, s, task.ID)
	require.NotEmpty(t, first.Worktree)
	writeTreeFile(t, first.Worktree, "docs/a.md", "first try\n")
	act(t, s, "implementer-1", task.ID, "complete", nil)
	act(t, s, leadName, task.ID, "reopen", nil)

	// implementer-1's turn is still running, so a second member takes it.
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, memberID(t, s, "implementer-2"), got.OwnerID)
	require.Equal(t, first.Worktree, got.Worktree, "the reopened task keeps its worktree")
	require.Contains(t, runner.lastPrompt(), "Worktree: "+first.Worktree)
	require.Equal(t, "first try\n", readTreeFile(t, got.Worktree, "docs/a.md"))
	require.Equal(t, "Docs", gitIn(t, got.Worktree, "log", "-1", "--format=%s"), "the prior work is committed on the branch")
	ws, ok := s.MemberWorkspace("implementer-2")
	require.True(t, ok)
	require.Equal(t, first.Worktree, ws.Root)
}

func TestLeadAcceptMergesTheBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
	act(t, s, "impl", view.ID, "complete", nil)
	check := verificationTaskFor(t, s, view.ID)

	act(t, s, leadName, view.ID, "complete", nil)
	require.Equal(t, TaskCompleted, taskByID(t, s, view.ID).Status)
	require.Equal(t, TaskDeleted, taskByID(t, s, check.ID).Status, "accepted work keeps no check")
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
	require.NoDirExists(t, view.Worktree)
	require.False(t, branchExists(repo, view.Branch))
}

func TestUnverifiedCompleteMergesTheBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	done := act(t, s, "impl", view.ID, "complete", nil)
	require.Equal(t, TaskCompleted, done.Status, "the view shows the merged outcome")
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
	require.NoDirExists(t, view.Worktree)
}

func TestSettledTurnCommitsAndMerges(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	runner.finish("impl")
	require.Eventually(t, func() bool { return taskByID(t, s, view.ID).Status == TaskCompleted }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
	require.NoDirExists(t, view.Worktree)
}

func TestMergeConflictEscalatesAndKeepsTheBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "rev")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "README.md", "branch\n")
	act(t, s, "impl", view.ID, "complete", nil)
	// The Lead's tree moves on underneath the branch.
	writeTreeFile(t, repo, "README.md", "lead\n")
	gitIn(t, repo, "commit", "-am", "lead change")

	passCheck(t, s, "rev", view.ID)
	got := taskByID(t, s, view.ID)
	require.Equal(t, TaskInReview, got.Status, "the original completes only after a merge")
	require.True(t, got.Escalated)
	require.DirExists(t, view.Worktree)
	require.True(t, branchExists(repo, view.Branch))
	require.Equal(t, "lead\n", readTreeFile(t, repo, "README.md"))
	require.Empty(t, gitIn(t, repo, "status", "--porcelain"), "the aborted merge leaves the Lead's tree clean")
	notice := lastLeadNotice(t, s)
	require.Contains(t, notice, view.Branch)
	require.Contains(t, notice, "conflict")
}

// TestMergeRefusedOnADirtyOrDetachedLeadTree pins Review Focus 1.
func TestMergeRefusedOnADirtyOrDetachedLeadTree(t *testing.T) {
	t.Parallel()
	t.Run("dirty", func(t *testing.T) {
		t.Parallel()
		repo := initTeamRepo(t)
		s, _ := newWorktreeService(t, repo, false)
		spawnMember(t, s, "impl")
		spawnMember(t, s, "rev")
		view := scopedTask(t, s, "impl", "Docs", true)
		writeTreeFile(t, view.Worktree, "README.md", "branch\n")
		act(t, s, "impl", view.ID, "complete", nil)
		writeTreeFile(t, repo, "README.md", "local\n")

		passCheck(t, s, "rev", view.ID)
		got := taskByID(t, s, view.ID)
		require.Equal(t, TaskInReview, got.Status)
		require.True(t, got.Escalated)
		require.Equal(t, "local\n", readTreeFile(t, repo, "README.md"), "the Lead's uncommitted change is untouched")
		require.Contains(t, lastLeadNotice(t, s), view.Branch)
	})
	t.Run("detached", func(t *testing.T) {
		t.Parallel()
		repo := initTeamRepo(t)
		s, _ := newWorktreeService(t, repo, false)
		spawnMember(t, s, "impl")
		spawnMember(t, s, "rev")
		view := scopedTask(t, s, "impl", "Docs", true)
		writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
		act(t, s, "impl", view.ID, "complete", nil)
		gitIn(t, repo, "checkout", "--detach")

		passCheck(t, s, "rev", view.ID)
		got := taskByID(t, s, view.ID)
		require.Equal(t, TaskInReview, got.Status)
		require.True(t, got.Escalated)
		require.NoFileExists(t, filepath.Join(repo, "docs", "a.md"))
		notice := lastLeadNotice(t, s)
		require.Contains(t, notice, "detached")
		require.Contains(t, notice, view.Branch)

		// The Lead fixes its tree and accepts the work: the merge runs now.
		gitIn(t, repo, "checkout", "main")
		act(t, s, leadName, view.ID, "complete", nil)
		require.Equal(t, TaskCompleted, taskByID(t, s, view.ID).Status)
		require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
		require.NoDirExists(t, view.Worktree)
	})
}

func TestLeadAcceptRefusedMergeReturnsTheError(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
	act(t, s, "impl", view.ID, "complete", nil)
	gitIn(t, repo, "checkout", "--detach")

	err := actErr(s, leadName, view.ID, "complete", nil)
	require.Equal(t, CodeTaskGit, ErrorCode(err))
	require.Contains(t, err.Error(), view.Branch)
	got := taskByID(t, s, view.ID)
	require.Equal(t, TaskInReview, got.Status)
	require.True(t, got.Escalated)
}

func TestMergeAfterTheBoardMovedKeepsTheMerge(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
	act(t, s, "impl", view.ID, "complete", nil)

	stale := mergeRequestFor(taskByID(t, s, view.ID))
	stale.revision--
	s.mergeTask(context.Background(), stale)
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"), "the merge happened")
	require.Equal(t, TaskInReview, taskByID(t, s, view.ID).Status, "the write did not apply to a moved task")
	require.DirExists(t, view.Worktree)
	require.Contains(t, lastLeadNotice(t, s), "The merge stays in the Lead's tree")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agentteam/ -run 'Commit|Merge|Settle|Fail|Reopened|Accept|Unverified|Empty'`
Expected: FAIL to compile with `undefined: mergeRequestFor` and `s.mergeTask undefined`.

- [ ] **Step 3: Create `internal/agentteam/merge.go`**

```go
package agentteam

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/crush/internal/gitutil"
)

// mergeRequest is a task branch to merge into the Lead's tree once a write
// left its task in review for it.
type mergeRequest struct {
	taskID, sessionID, subject string
	worktree, branch           string
	// revision is the task's revision the merge is for. The write that
	// records the outcome applies only while the task still has it.
	revision int
}

func mergeRequestFor(task Task) mergeRequest {
	return mergeRequest{
		taskID:    task.ID,
		sessionID: task.SessionID,
		subject:   task.Subject,
		worktree:  task.Worktree,
		branch:    task.Branch,
		revision:  task.Revision,
	}
}

// commitWork commits everything in a task worktree, with the task subject
// as the message. An empty change set makes no commit and is no error.
func (s *Service) commitWork(ctx context.Context, worktree, subject string) error {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	_, err := gitutil.CommitAll(ctx, worktree, subject)
	return err
}

// commitHeldWork commits the worktrees of the tasks name leaves in
// progress, before its settle decides what happens to them, so work that
// goes to review or is merged is on its branch. It runs with no lock held
// and returns the tasks whose commit failed.
func (s *Service) commitHeldWork(name string) map[string]error {
	s.mu.Lock()
	member := s.stateMemberLocked(name)
	var work []Task
	for _, task := range s.state.Tasks {
		if member.ID != "" && task.OwnerID == member.ID && task.Status == TaskInProgress && task.Worktree != "" {
			work = append(work, task)
		}
	}
	s.mu.Unlock()
	if len(work) == 0 {
		return nil
	}
	ctx, cancel := gitContext()
	defer cancel()
	failed := map[string]error{}
	for _, task := range work {
		if err := s.commitWork(ctx, task.Worktree, task.Subject); err != nil {
			failed[task.ID] = fmt.Errorf("could not commit the worktree of task %s: %w", task.ID, err)
		}
	}
	return failed
}

// completePrep is the git work a complete needs before its write.
type completePrep struct {
	// commit is the worktree of in-progress work to commit.
	commit, subject string
	// merge is in-review work the Lead accepts.
	merge *mergeRequest
}

// prepareComplete reads, holding only s.mu, what git work req needs before
// its write. A request the write would refuse needs none, so a stale or
// unauthorized complete never reaches git with a merge.
func (s *Service) prepareComplete(caller string, req UpdateTaskRequest) completePrep {
	s.mu.Lock()
	defer s.mu.Unlock()
	member, ok := s.memberLocked(caller)
	if !ok {
		return completePrep{}
	}
	_, task, ok := s.taskIndexLocked(s.scopeLocked(caller, req.SessionID), req.TaskID)
	if !ok || task.Revision != req.ExpectedRevision || task.Worktree == "" || task.Verifies != "" {
		return completePrep{}
	}
	switch {
	case task.Status == TaskInProgress:
		return completePrep{commit: task.Worktree, subject: task.Subject}
	case task.Status == TaskInReview && member.Role == RoleLead:
		m := mergeRequestFor(task)
		return completePrep{merge: &m}
	}
	return completePrep{}
}

// runCompletePrep runs the git work of a complete with no lock held. A
// failed commit refuses the complete with the task unchanged. A refused
// merge leaves the task in review, escalated, and refuses the complete. It
// returns the merge that ran, so a refused write can say it stays.
func (s *Service) runCompletePrep(caller string, req UpdateTaskRequest) (*mergeRequest, error) {
	prep := s.prepareComplete(caller, req)
	if prep.commit == "" && prep.merge == nil {
		return nil, nil
	}
	ctx, cancel := gitContext()
	defer cancel()
	if prep.commit != "" {
		if err := s.commitWork(ctx, prep.commit, prep.subject); err != nil {
			return nil, newError(CodeTaskGit, "could not commit the work of task %s in %s: %v. The task is unchanged",
				req.TaskID, prep.commit, err)
		}
		return nil, nil
	}
	if err := s.mergeBranch(ctx, *prep.merge); err != nil {
		if !errors.Is(err, errShutdown) {
			s.settleMerge(*prep.merge, err)
		}
		return nil, newError(CodeTaskGit, "%s", mergeRefusedText(*prep.merge, err))
	}
	return prep.merge, nil
}

// mergeBranch merges m's branch into the Lead's tree with --no-ff. It
// refuses once the Team is shut down, so a merge never lands that no
// write could record.
func (s *Service) mergeBranch(ctx context.Context, m mergeRequest) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errShutdown
	}
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	return gitutil.MergeNoFF(ctx, s.cfg.WorkingDir, m.branch, fmt.Sprintf("Merge team task %s: %s", m.taskID, m.subject))
}

// mergeTask merges m's branch, records the outcome and, after a merge, removes
// the worktree and starts what the completed task unblocked. Callers must
// hold no lock.
func (s *Service) mergeTask(ctx context.Context, m mergeRequest) {
	err := s.mergeBranch(ctx, m)
	if errors.Is(err, errShutdown) {
		return
	}
	if s.settleMerge(m, err) {
		s.removeTaskWorktree(ctx, worktreeRemoval{taskID: m.taskID, sessionID: m.sessionID, path: m.worktree, branch: m.branch})
	}
	s.dispatch(context.Background(), m.sessionID)
}

// settleMerge records the outcome of merging m's branch: the task completes,
// or stays in review escalated with a Lead notice. It applies only while the
// task is still the one the merge was for; a merge that happened for a task
// the board moved on stays in place and the Lead is told. It reports whether
// the task completed.
func (s *Service) settleMerge(m mergeRequest, mergeErr error) bool {
	var (
		completed bool
		noted     bool
		hook      func(sessionID, subject string)
		leadHook  func(sessionID string)
	)
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		hook, leadHook = s.taskCompletedHook, s.leadNotifyHook
		matched := false
		for i := range s.state.Tasks {
			task := &s.state.Tasks[i]
			if task.ID != m.taskID || task.SessionID != m.sessionID {
				continue
			}
			if task.Status != TaskInReview || task.Revision != m.revision {
				break
			}
			matched = true
			task.Revision++
			if mergeErr != nil {
				task.Escalated = true
				s.noteLeadLocked(m.sessionID, mergeRefusedText(m, mergeErr))
				noted = true
				break
			}
			task.Status = TaskCompleted
			task.Escalated = false
			task.LastFailure = ""
			task.Worktree, task.Branch = "", ""
			s.closeChecksLocked(task.ID, true)
			completed = true
			break
		}
		if !matched {
			if mergeErr != nil {
				return false, nil
			}
			s.noteLeadLocked(m.sessionID, mergeStaysText(m, "the board moved the task on before the merge was recorded"))
			noted = true
		}
		s.notifyLocked()
		return true, nil
	})
	if completed && hook != nil {
		hook(m.sessionID, m.subject)
	}
	if noted {
		wakeLeads(leadHook, []string{m.sessionID})
	}
	return completed
}

// noteMergeStays tells the Lead that a merge it asked for landed but the
// write recording it was refused.
func (s *Service) noteMergeStays(m mergeRequest, cause error) {
	var (
		leadHook func(sessionID string)
		noted    bool
	)
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		leadHook = s.leadNotifyHook
		s.noteLeadLocked(m.sessionID, mergeStaysText(m, "the task update was refused: "+cause.Error()))
		s.notifyLocked()
		noted = true
		return true, nil
	})
	if noted {
		wakeLeads(leadHook, []string{m.sessionID})
	}
}

// mergeStaysText is the Lead notice for a merge no write recorded.
func mergeStaysText(m mergeRequest, reason string) string {
	return fmt.Sprintf("Task %s (%s) was merged from branch %s, but %s. The merge stays in the Lead's tree, and the worktree at %s is kept.",
		m.taskID, m.subject, m.branch, reason, m.worktree)
}

// mergeRefusedText is the Lead notice, and the Lead's error, for a merge git
// refused or that conflicted.
func mergeRefusedText(m mergeRequest, err error) string {
	detail := err.Error()
	var mergeErr *gitutil.MergeError
	if errors.As(err, &mergeErr) {
		detail = mergeErr.Reason
		if mergeErr.Output != "" {
			detail += ":\n\n" + mergeErr.Output
		}
	}
	return fmt.Sprintf("Task %s (%s) could not be merged from branch %s into the Lead's tree: %s\n\nNothing was changed. The task stays in review, escalated, with its worktree at %s. Merge the branch by hand and complete the task to accept it, or reopen it.",
		m.taskID, m.subject, m.branch, detail, m.worktree)
}

// refreshView returns the current view of the task view shows, or view
// itself when the task is gone.
func (s *Service) refreshView(view TaskView) TaskView {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.state.Tasks {
		if task.ID == view.ID && task.SessionID == view.SessionID {
			return projectTaskView(s.state, task)
		}
	}
	return view
}
```

- [ ] **Step 4: The verdict leaves the merge to the caller**

In `internal/agentteam/verify.go`, replace `applyVerdictLocked` (Task 2's version) with:

```go
// verdictResult is what a verdict leaves for the caller once its write
// lands.
type verdictResult struct {
	completedSubject string
	wake, leads      []string
	wakeTask         string
	// merge is set when a pass leaves the original's branch to merge: the
	// original stays in review until the merge succeeds.
	merge *mergeRequest
}

// applyVerdictLocked applies a verifier's verdict to the task check verifies.
// An original that already left review (reopened, deleted) is left alone, so
// a late verdict never rewrites work the board has moved on from. A pass of
// work with a worktree leaves the original in review for its merge. Callers
// must hold s.mu.
func (s *Service) applyVerdictLocked(scope map[string]bool, check Task, verifier Member, verdict, notes string) verdictResult {
	index, original, ok := s.taskIndexLocked(scope, check.Verifies)
	if !ok || original.Status != TaskInReview {
		return verdictResult{}
	}
	original.Revision++
	if verdict == "pass" {
		original.LastFailure = ""
		if original.Worktree != "" {
			s.state.Tasks[index] = original
			m := mergeRequestFor(original)
			return verdictResult{merge: &m}
		}
		original.Status = TaskCompleted
		s.state.Tasks[index] = original
		return verdictResult{completedSubject: original.Subject}
	}
	original.LastFailure = truncateText(notes, maxFailureNotes)
	original.Attempts++
	if original.Attempts >= maxTaskAttempts {
		original.Escalated = true
		s.state.Tasks[index] = original
		s.noteLeadLocked(original.SessionID, fmt.Sprintf(
			"Task %s (%s) failed verification %d times and stays in review, escalated. To recover, %s. Last notes:\n\n%s",
			original.ID, original.Subject, original.Attempts, taskRecoveryActions(original.Status), notes))
		return verdictResult{leads: []string{original.SessionID}}
	}
	original.Status = TaskInProgress
	s.state.Tasks[index] = original
	text := fmt.Sprintf("Verification of task %s failed:\n\n%s\n\nFix it and complete the task again.", original.ID, notes)
	if original.OwnerID == leadName {
		s.noteLeadLocked(original.SessionID, text)
		return verdictResult{leads: []string{original.SessionID}}
	}
	for _, member := range s.state.Members {
		if member.ID == original.OwnerID {
			s.appendMessageLocked(verifier.Name, member.Name, text)
			return verdictResult{wake: []string{member.Name}, wakeTask: original.ID}
		}
	}
	return verdictResult{}
}
```

In `newVerificationTaskLocked`, replace the `Description: fmt.Sprintf(...)` field with:

```go
		Description: fmt.Sprintf("Verify task %s: %s\n\nAcceptance criteria and description of the original task:\n%s\n\n"+
			"Implemented by: %s\nWrite scopes: %s\n%s",
			original.ID, original.Subject, original.Description, implementer, scopesText(original), changeLocation(original)),
```

and add after `newVerificationTaskLocked`:

```go
// changeLocation tells a verifier where the work it checks is.
func changeLocation(original Task) string {
	if original.Worktree == "" {
		return "The change is in the shared working directory; inspect it with git status and git diff."
	}
	return fmt.Sprintf("The change is committed on branch %s in the task worktree %s, where your tools run; inspect it with git log and git diff.",
		original.Branch, original.Worktree)
}
```

The text for a task without a worktree is the old last line byte for byte, so `TestCompleteMovesToReviewAndCreatesTheCheck` keeps passing.

- [ ] **Step 5: Complete and verdicts produce merges in `applyTaskUpdateLocked`**

In `internal/agentteam/team.go`, add to `taskUpdate`, after `removal`:

```go
	// merges are task branches to merge once the write lands.
	merges []mergeRequest
```

In `applyTaskUpdateLocked`, add `merge bool` to the `var` block Task 7 extended.

In `case "complete":`, replace the in-review branch's tail:

```go
			next.Status = TaskCompleted
			completedSubject = next.Subject
			break
		}
```

with:

```go
			next.Status = TaskCompleted
			completedSubject = next.Subject
			if current.Worktree != "" {
				// The branch was merged before this write, so the worktree
				// and branch go, and a later reopen starts a fresh one.
				removal = &worktreeRemoval{taskID: current.ID, sessionID: current.SessionID, path: current.Worktree, branch: current.Branch}
				next.Worktree, next.Branch = "", ""
			}
			break
		}
```

and replace the end of the case:

```go
		if current.needsVerification() {
			next.Status = TaskInReview
			review = true
			break
		}
		next.Status = TaskCompleted
		completedSubject = next.Subject
	case "pass", "fail":
```

with:

```go
		if current.needsVerification() {
			next.Status = TaskInReview
			review = true
			break
		}
		if current.Worktree != "" {
			// Unverified work completes once its branch is merged; until
			// then it waits in review, where a refused merge leaves it for
			// the Lead.
			next.Status = TaskInReview
			merge = true
			break
		}
		next.Status = TaskCompleted
		completedSubject = next.Subject
	case "pass", "fail":
```

After the `update := taskUpdate{...}` literal, add:

```go
	if merge {
		update.merges = append(update.merges, mergeRequestFor(next))
	}
```

Replace the verdict block:

```go
	if verdict != "" {
		subject, wake, leads, wakeTask := s.applyVerdictLocked(scope, next, member, verdict, notes)
		if subject != "" {
			update.completedSubject = subject
		}
		update.wake = wake
		update.leadSessions = leads
		if wakeTask != "" {
			update.taskID = wakeTask
		}
	}
```

with:

```go
	if verdict != "" {
		result := s.applyVerdictLocked(scope, next, member, verdict, notes)
		if result.completedSubject != "" {
			update.completedSubject = result.completedSubject
		}
		update.wake = result.wake
		update.leadSessions = result.leads
		if result.wakeTask != "" {
			update.taskID = result.wakeTask
		}
		if result.merge != nil {
			update.merges = append(update.merges, *result.merge)
		}
	}
```

- [ ] **Step 6: `UpdateTask` commits before and merges after**

Replace `UpdateTask` (Task 7's version) with:

```go
// UpdateTask applies one compare-and-set task transition. Git work runs
// with no lock held: before the write, in-progress work is committed to its
// branch and in-review work the Lead accepts is merged; after it, a claim
// gets its worktree, branches left for merging are merged, and a deleted or
// accepted task loses its worktree.
func (s *Service) UpdateTask(caller string, req UpdateTaskRequest) (TaskView, error) {
	var accepted *mergeRequest
	if req.Action == "complete" {
		merged, err := s.runCompletePrep(caller, req)
		if err != nil {
			return TaskView{}, err
		}
		accepted = merged
	}
	var (
		applied  taskUpdate
		hook     func(sessionID, subject string)
		leadHook func(sessionID string)
	)
	err := s.write(refusable, func() (bool, error) {
		next, err := s.applyTaskUpdateLocked(caller, req)
		if err != nil {
			return false, err
		}
		applied = next
		hook = s.taskCompletedHook
		leadHook = s.leadNotifyHook
		s.notifyLocked()
		return true, nil
	})
	if err != nil {
		if accepted != nil {
			s.noteMergeStays(*accepted, err)
		}
		return TaskView{}, err
	}
	// A claim that needs a worktree gets it before anything else reacts to
	// the claim, so the teammate's next tool call already runs there.
	if applied.place {
		view, err := s.placeClaim(applied.view)
		if err != nil {
			return TaskView{}, err
		}
		applied.view = view
	}
	// The hooks run outside every lock, and only once the record is durable.
	if applied.completedSubject != "" && hook != nil {
		hook(applied.parentSessionID, applied.completedSubject)
	}
	wakeLeads(leadHook, applied.leadSessions)
	// Only the verdict path fills wake, so this is the failed-verification
	// wake; the log stays generic in case another path ever does.
	for _, name := range applied.wake {
		if err := s.wakeIfIdle(context.Background(), name); err != nil {
			slog.Warn("Could not wake a teammate", "member", name, "error", err)
			// The verdict handed the task back, but nobody runs to fix
			// it. A Warn log is invisible to the Lead, so say it in its
			// inbox, outside every lock, unless the Team is shut down.
			_ = s.write(mustApply, func() (bool, error) {
				if s.closed {
					return false, nil
				}
				s.noteLeadLocked(applied.parentSessionID, fmt.Sprintf(
					"Could not start %s to fix task %s after a failed verification: %s. The task stays in progress with nobody running it; to continue, %s.",
					name, applied.taskID, err, taskRecoveryActions(TaskInProgress)))
				s.notifyLocked()
				return true, nil
			})
			wakeLeads(leadHook, []string{applied.parentSessionID})
		}
	}
	ctx, cancel := gitContext()
	defer cancel()
	for _, m := range applied.merges {
		s.mergeTask(ctx, m)
	}
	if applied.removal != nil {
		s.removeTaskWorktree(ctx, *applied.removal)
	}
	if len(applied.merges) > 0 {
		applied.view = s.refreshView(applied.view)
	}
	s.dispatch(context.Background(), applied.parentSessionID)
	return applied.view, nil
}
```

- [ ] **Step 7: The settle commits first and merges after**

Replace `settleTasksLocked` with:

```go
// settleTasksLocked closes out the tasks a finished teammate left in flight.
// A successful turn completes them, sends them to review when they are
// verified, or leaves them in review for a merge when they have a worktree;
// a verification task is never completed this way, because a verifier that
// never decides must not pass work by default. A failed or interrupted turn
// releases them back to the board so they never stay half-done forever, and
// so does a task whose worktree could not be committed (uncommitted). With
// hold set, the member is about to be resumed with pending mail, so its
// in-progress tasks are left as they are for that turn to finish. It returns
// the report labels, the subjects of the completed tasks and the merges to
// run once the write lands.
func (s *Service) settleTasksLocked(ownerID string, runErr error, hold bool, uncommitted map[string]error) (settled, completed []string, merges []mergeRequest) {
	if ownerID == "" || ownerID == leadName {
		return nil, nil, nil
	}
	if hold {
		return nil, nil, nil
	}
	var review []int
	for i := range s.state.Tasks {
		task := &s.state.Tasks[i]
		if task.OwnerID != ownerID || task.Status != TaskInProgress {
			continue
		}
		task.Revision++
		// Work that is not on its branch must not go to review or merge.
		taskErr := runErr
		if taskErr == nil {
			taskErr = uncommitted[task.ID]
		}
		switch {
		case task.Verifies != "" && !s.checkWantedLocked(*task):
			// Its task left review while the check was being reviewed, so
			// there is nothing left to decide. Released, it would bring a
			// verifier back for work the board moved on from, and escalate
			// against a verdict nobody needs.
			task.Status = TaskDeleted
			task.OwnerID = ""
			settled = append(settled, task.ID+" deleted, its task left review")
		case taskErr != nil:
			task.Status = TaskPending
			task.OwnerID = ""
			settled = append(settled, task.ID+" released")
			s.countFailedTurnLocked(task, taskErr)
		case task.Verifies != "":
			task.Status = TaskPending
			task.OwnerID = ""
			task.Attempts++
			settled = append(settled, task.ID+" released without a verdict")
			if task.Attempts >= maxTaskAttempts {
				task.Escalated = true
				s.noteLeadLocked(task.SessionID, fmt.Sprintf(
					"Task %s (%s) got no verdict from its verifier %d times. It is escalated; to continue, %s.",
					task.ID, task.Subject, task.Attempts, taskRecoveryActions(task.Status)))
			}
		case task.needsVerification():
			task.Status = TaskInReview
			settled = append(settled, task.ID+" in review")
			review = append(review, i)
		case task.Worktree != "":
			// Unverified work completes once its branch is merged.
			task.Status = TaskInReview
			settled = append(settled, task.ID+" merging")
			merges = append(merges, mergeRequestFor(*task))
		default:
			task.Status = TaskCompleted
			settled = append(settled, task.ID+" completed")
			completed = append(completed, task.Subject)
		}
	}
	// Appended after the loop: an append can move the slice, and the loop
	// holds a pointer into it.
	for _, i := range review {
		s.startReviewLocked(s.state.Tasks[i])
	}
	return settled, completed, merges
}
```

In `watch`, right after `runErr := run.Err()`, add:

```go
	// Commit what the turn left in its worktrees before the settle decides
	// where that work goes; git runs with no lock held.
	uncommitted := s.commitHeldWork(name)
```

Add `merges []mergeRequest` to the `var (...)` block below it, and replace:

```go
		settled, completed := s.settleTasksLocked(member.ID, runErr, hold)
		complete = completed
```

with:

```go
		settled, completed, toMerge := s.settleTasksLocked(member.ID, runErr, hold, uncommitted)
		complete = completed
		merges = toMerge
```

Replace:

```go
	// The settle may have freed a member or finished a blocker.
	if !wasClosed {
		s.dispatch(context.Background(), member.ParentID)
	}
```

with:

```go
	// Work the settle left for merging is merged now, outside every lock;
	// each merge dispatches what its task unblocked.
	for _, m := range merges {
		ctx, cancel := gitContext()
		s.mergeTask(ctx, m)
		cancel()
	}
	// The settle may have freed a member or finished a blocker.
	if !wasClosed {
		s.dispatch(context.Background(), member.ParentID)
	}
```

In the resume-failure write, replace `s.settleTasksLocked(member.ID, err, false)` with `s.settleTasksLocked(member.ID, err, false, nil)`.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `gofumpt -w internal/agentteam/*.go && go test ./internal/agentteam/ && go test -race ./internal/agentteam/`
Expected: PASS. The verification, dispatch and shutdown tests from the pipeline branch pass unchanged: without a worktree no new path runs.

- [ ] **Step 9: Commit**

```bash
git add internal/agentteam/merge.go internal/agentteam/merge_test.go internal/agentteam/team.go internal/agentteam/verify.go
git commit -m "feat: commit team task work on complete and merge it on pass"
```

---

### Task 10: Bash is held to the write scopes

**Files:**
- Modify: `internal/shell/background.go` (`BackgroundShell` fields, the job goroutine in `start`, `GetOutput`)
- Test: `internal/shell/background_finisher_test.go` (create)
- Modify: `internal/agent/scope_gate.go` (`scopedWriteTools` comment, `gateToolsForScopes`, `scopeGatedTool.Run`; add the bash check)
- Test: `internal/agent/bash_scope_test.go` (create)

**Interfaces:**
- Consumes: `gitutil.SnapshotFiles`, `gitutil.ChangedPaths`, `gitutil.RestorePaths`, `gitutil.SnapshotCaps`, `gitutil.RestoreResult` (Task 6); `workspaceFrom`, `withWorkspace`, `initTeamGitRepo`, `gitInRepo` (Task 8); `tools.BashResponseMetadata`, `shell.GetBackgroundShellManager()`.
- Produces:
  - `func (bs *BackgroundShell) SetFinisher(fn func() string) (note string, deferred bool)`
  - `func (bs *BackgroundShell) Note() string`
  - `var bashSnapshotCaps = gitutil.SnapshotCaps{PerFile: 1 << 20, Total: 32 << 20}`
  - `func (g *scopeGatedTool) runBash(ctx context.Context, call fantasy.ToolCall, scopes []string) (fantasy.ToolResponse, error)`
  - `func checkBashScopes(ctx context.Context, ws agentteam.Workspace, scopes []string, before gitutil.FileSnapshot) string`

- [ ] **Step 1: Write the failing shell tests**

Create `internal/shell/background_finisher_test.go`:

```go
package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBackgroundFinisherNoteLeadsTheOutput(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := SubscribeBackgroundJobs(ctx)

	manager := newBackgroundShellManager()
	job, err := manager.StartWithSession(ctx, nil, t.TempDir(), nil, "sleep 0.3 && echo done", "", "session-finisher", testMaxJobs)
	require.NoError(t, err)
	note, deferred := job.SetFinisher(func() string { return "NOTE" })
	require.True(t, deferred, "a running job runs the finisher when it ends")
	require.Empty(t, note)

	job.Wait()
	stdout, _, done, _ := job.GetOutput()
	require.True(t, done)
	require.Equal(t, "NOTE\n\ndone\n", stdout)
	require.Equal(t, "NOTE", job.Note())

	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Payload.ShellID != job.ID {
				continue
			}
			require.True(t, strings.HasPrefix(event.Payload.Output, "NOTE\n\n"), event.Payload.Output)
			return
		case <-deadline:
			t.Fatal("timed out waiting for the completion notice")
		}
	}
}

func TestBackgroundFinisherRunsAtOnceForAFinishedJob(t *testing.T) {
	t.Parallel()
	manager := newBackgroundShellManager()
	job, err := manager.Start(t.Context(), nil, t.TempDir(), nil, "echo quick", "", testMaxJobs)
	require.NoError(t, err)
	job.Wait()

	note, deferred := job.SetFinisher(func() string { return "LATE" })
	require.False(t, deferred)
	require.Equal(t, "LATE", note)
	stdout, _, _, _ := job.GetOutput()
	require.Equal(t, "LATE\n\nquick\n", stdout)
}
```

- [ ] **Step 2: Write the failing agent tests**

Create `internal/agent/bash_scope_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/shell"
)

// shellTool stands in for bash: running it runs fn, the "command", and
// returns resp.
type shellTool struct {
	fn   func()
	resp fantasy.ToolResponse
}

func (s *shellTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: tools.BashToolName} }

func (s *shellTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if s.fn != nil {
		s.fn()
	}
	return s.resp, nil
}

func (s *shellTool) ProviderOptions() fantasy.ProviderOptions     { return nil }
func (s *shellTool) SetProviderOptions(_ fantasy.ProviderOptions) {}

func gatedShell(inner *shellTool, scopes ...string) fantasy.AgentTool {
	return gateToolsForScopes([]fantasy.AgentTool{inner}, fixedScopes(scopes...), "")[0]
}

func runShell(t *testing.T, tool fantasy.AgentTool, ctx context.Context) fantasy.ToolResponse {
	t.Helper()
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "b1", Name: tools.BashToolName, Input: `{"command":"x"}`})
	require.NoError(t, err)
	return resp
}

func writeIn(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func readIn(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}

func inWorktree(t *testing.T, repo string) context.Context {
	return withWorkspace(t.Context(), agentteam.Workspace{Root: repo, Dir: repo})
}

func TestBashScopeCheckRestoresAnOutOfScopeEdit(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "README.md", "sed -i was here\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")

	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "outside the write scopes of your claimed task (docs)")
	require.Contains(t, resp.Content, "Restored: README.md.")
	require.True(t, strings.HasSuffix(resp.Content, "ran"), "the command's own output follows")
	require.Equal(t, "hello\n", readIn(t, repo, "README.md"))
	require.Empty(t, gitInRepo(t, repo, "status", "--porcelain"))
}

func TestBashScopeCheckRemovesANewOutOfScopeFile(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	writeIn(t, repo, ".gitignore", "build/\n")
	gitInRepo(t, repo, "add", ".")
	gitInRepo(t, repo, "commit", "-m", "ignore build")
	tool := gatedShell(&shellTool{fn: func() {
		writeIn(t, repo, "notes.txt", "stray\n")
		writeIn(t, repo, "build/out.bin", "ignored\n")
	}, resp: fantasy.NewTextResponse("ran")}, "docs")

	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Restored: notes.txt.")
	require.NotContains(t, resp.Content, "build/out.bin", "ignored paths are not checked")
	require.NoFileExists(t, filepath.Join(repo, "notes.txt"))
	require.FileExists(t, filepath.Join(repo, "build", "out.bin"))
}

func TestBashScopeCheckKeepsInScopeEdits(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "docs/a.md", "docs\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, inWorktree(t, repo))
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "ran", resp.Content)
	require.Equal(t, "docs\n", readIn(t, repo, "docs/a.md"))
}

// TestBashScopeCheckKeepsInScopeEditsOfAMixedCommand pins Review Focus 2.
func TestBashScopeCheckKeepsInScopeEditsOfAMixedCommand(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() {
		writeIn(t, repo, "docs/a.md", "docs\n")
		writeIn(t, repo, "README.md", "changed\n")
	}, resp: fantasy.NewTextResponse("ran")}, "docs")

	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Restored: README.md.")
	require.NotContains(t, resp.Content, "docs/a.md")
	require.Equal(t, "docs\n", readIn(t, repo, "docs/a.md"), "the in-scope edit stays")
	require.Equal(t, "hello\n", readIn(t, repo, "README.md"))
}

func TestBashScopeCheckRestoresADirtyFileToItsSavedContent(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	writeIn(t, repo, "README.md", "mine\n")
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "README.md", "theirs\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Equal(t, "mine\n", readIn(t, repo, "README.md"), "a file dirty before the call gets that content back, not HEAD's")
}

func TestBashScopeCheckReportsAFileOverTheCap(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	writeIn(t, repo, "big.bin", strings.Repeat("x", 2<<20))
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "big.bin", "small\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Not restored")
	require.Contains(t, resp.Content, "big.bin")
	require.Equal(t, "small\n", readIn(t, repo, "big.bin"), "a path over the 1 MiB cap is reported, not restored")
}

func TestBashScopeCheckFollowsACommit(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() {
		writeIn(t, repo, "README.md", "committed\n")
		gitInRepo(t, repo, "commit", "-am", "sneaky")
	}, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Restored: README.md.")
	require.Equal(t, "hello\n", readIn(t, repo, "README.md"))
}

func TestBashScopeCheckMapsTheLeadSubdirectory(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	ctx := withWorkspace(t.Context(), agentteam.Workspace{Root: repo, Dir: filepath.Join(repo, "sub")})
	tool := gatedShell(&shellTool{fn: func() {
		writeIn(t, repo, "sub/docs/a.md", "in scope\n")
		writeIn(t, repo, "docs/a.md", "outside the Lead's directory\n")
	}, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, ctx)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Restored: docs/a.md.")
	require.Equal(t, "in scope\n", readIn(t, repo, "sub/docs/a.md"))
}

func TestBashScopeCheckNeedsAWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "README.md", "changed\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, t.Context())
	require.False(t, resp.IsError, "a teammate without a worktree keeps today's ungated bash")
	require.Equal(t, "changed\n", readIn(t, repo, "README.md"))
}

func TestBashScopeCheckHoldsABackgroundJob(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	ctx := inWorktree(t, repo)
	var job *shell.BackgroundShell
	inner := &shellTool{}
	inner.fn = func() {
		var err error
		job, err = shell.GetBackgroundShellManager().Start(context.Background(), nil, repo, nil,
			"sleep 1 && echo leak > leak.txt && echo finished", "bg", 10)
		require.NoError(t, err)
		meta, err := json.Marshal(tools.BashResponseMetadata{Background: true, ShellID: job.ID})
		require.NoError(t, err)
		inner.resp = fantasy.ToolResponse{Type: "text", Content: "Background shell started with ID: " + job.ID, Metadata: string(meta)}
	}
	resp := runShell(t, gatedShell(inner, "docs"), ctx)
	require.False(t, resp.IsError, "the job is checked when it finishes, not now")

	select {
	case <-job.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the background job never finished")
	}
	require.NoFileExists(t, filepath.Join(repo, "leak.txt"))
	stdout, _, done, _ := job.GetOutput()
	require.True(t, done)
	require.True(t, strings.HasPrefix(stdout, "This command changed files outside the write scopes"), stdout)
	require.Contains(t, stdout, "Restored: leak.txt.")
	require.Contains(t, stdout, "finished")
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/shell/ -run TestBackgroundFinisher ; go test ./internal/agent/ -run TestBashScopeCheck`
Expected: FAIL: the shell tests to compile with `job.SetFinisher undefined`; the agent tests with `resp.IsError` false where the check should have fired (bash is not gated yet).

- [ ] **Step 4: Background jobs run a finisher before they report**

In `internal/shell/background.go`, in the `BackgroundShell` struct, after `killed      atomic.Bool`, add:

```go
	// finishMu guards finished, finisher and note. The finisher runs once,
	// after the command returns and before its completion is announced.
	finishMu sync.Mutex
	finished bool
	finisher func() string
	note     string
```

After `MarkKilledByModel`, add:

```go
// SetFinisher registers fn to run once the job's command returns, before
// its completion is announced. The text fn returns leads the job's output,
// in the completion notice and in job_output. A job that already finished
// runs fn at once: SetFinisher then reports deferred false and returns the
// text.
func (bs *BackgroundShell) SetFinisher(fn func() string) (note string, deferred bool) {
	bs.finishMu.Lock()
	if !bs.finished {
		bs.finisher = fn
		bs.finishMu.Unlock()
		return "", true
	}
	bs.finishMu.Unlock()
	note = fn()
	bs.setNote(note)
	return note, false
}

// Note is the text the finisher returned, or "".
func (bs *BackgroundShell) Note() string {
	bs.finishMu.Lock()
	defer bs.finishMu.Unlock()
	return bs.note
}

func (bs *BackgroundShell) setNote(note string) {
	bs.finishMu.Lock()
	bs.note = note
	bs.finishMu.Unlock()
}

// runFinisher marks the command finished and runs the finisher, if one was
// set, in the job's own goroutine.
func (bs *BackgroundShell) runFinisher() {
	bs.finishMu.Lock()
	bs.finished = true
	fn := bs.finisher
	bs.finishMu.Unlock()
	if fn != nil {
		bs.setNote(fn())
	}
}

// withNote puts the finisher's text before stdout.
func (bs *BackgroundShell) withNote(stdout string) string {
	if note := bs.Note(); note != "" {
		return note + "\n\n" + stdout
	}
	return stdout
}
```

In `start`, in the job goroutine, replace:

```go
		bgShell.exitErr = err
		bgShell.completedAt.Store(time.Now().Unix())
```

with:

```go
		bgShell.exitErr = err
		bgShell.completedAt.Store(time.Now().Unix())
		bgShell.runFinisher()
```

and replace `output := bgShell.stdout.String()` with `output := bgShell.withNote(bgShell.stdout.String())`.

In `GetOutput`, replace `return bs.stdout.String(), bs.stderr.String(), true, bs.exitErr` with `return bs.withNote(bs.stdout.String()), bs.stderr.String(), true, bs.exitErr`.

- [ ] **Step 5: The scope gate checks bash**

In `internal/agent/scope_gate.go`, add to the imports `"log/slog"`, `"github.com/charmbracelet/crush/internal/gitutil"` and `"github.com/charmbracelet/crush/internal/shell"`. Replace the `scopedWriteTools` doc comment:

```go
// scopedWriteTools are the file-writing tools a teammate's claimed write
// scopes apply to. bash is not among them: its writes cannot be known
// before it runs.
```

with:

```go
// scopedWriteTools are the file-writing tools a teammate's claimed write
// scopes apply to before they run. bash is gated separately: its writes
// cannot be known in advance, so in a task worktree they are checked after
// it runs.
```

In `gateToolsForScopes`, replace `if slices.Contains(scopedWriteTools, tool.Info().Name) {` with:

```go
		if name := tool.Info().Name; slices.Contains(scopedWriteTools, name) || name == tools.BashToolName {
```

In `scopeGatedTool.Run`, replace:

```go
	name := g.inner.Info().Name
	listed := strings.Join(scopes, ", ")
```

with:

```go
	name := g.inner.Info().Name
	if name == tools.BashToolName {
		return g.runBash(ctx, call, scopes)
	}
	listed := strings.Join(scopes, ", ")
```

Append to `scope_gate.go`:

```go
// bashSnapshotCaps bound what one bash call saves to undo its changes: a
// changed file over a cap is reported, not restored.
var bashSnapshotCaps = gitutil.SnapshotCaps{PerFile: 1 << 20, Total: 32 << 20}

// runBash runs a teammate's bash call with its write scopes held by git.
// The worktree's changed files are recorded before the call and compared
// after it, and a change outside the scopes is undone and reported ahead of
// the command's output. A background job is checked when it finishes,
// against the record taken when it started. A teammate with no worktree
// keeps ungated bash.
func (g *scopeGatedTool) runBash(ctx context.Context, call fantasy.ToolCall, scopes []string) (fantasy.ToolResponse, error) {
	ws, ok := workspaceFrom(ctx)
	if !ok {
		return g.inner.Run(ctx, call)
	}
	before, err := gitutil.SnapshotFiles(ctx, ws.Root, bashSnapshotCaps)
	if err != nil {
		slog.Warn("Could not record a task worktree before a bash call", "dir", ws.Root, "error", err)
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"bash is refused: could not record your task worktree to hold this command to your write scopes (%s): %s",
			strings.Join(scopes, ", "), err)), nil
	}
	resp, runErr := g.inner.Run(ctx, call)
	if job, ok := runningJob(resp); ok {
		note, deferred := job.SetFinisher(func() string {
			return checkBashScopes(context.Background(), ws, scopes, before)
		})
		if deferred {
			return resp, runErr
		}
		return withScopeNote(resp, note), runErr
	}
	return withScopeNote(resp, checkBashScopes(context.WithoutCancel(ctx), ws, scopes, before)), runErr
}

// runningJob returns the background job a bash response started, while it
// still runs.
func runningJob(resp fantasy.ToolResponse) (*shell.BackgroundShell, bool) {
	if resp.Metadata == "" {
		return nil, false
	}
	var meta tools.BashResponseMetadata
	if err := json.Unmarshal([]byte(resp.Metadata), &meta); err != nil || meta.ShellID == "" {
		return nil, false
	}
	job, ok := shell.GetBackgroundShellManager().Get(meta.ShellID)
	if !ok || job.IsDone() {
		return nil, false
	}
	return job, true
}

// checkBashScopes compares the worktree with before, restores the changed
// paths outside scopes, and returns the note for the model, or "" when the
// command stayed inside them.
func checkBashScopes(ctx context.Context, ws agentteam.Workspace, scopes []string, before gitutil.FileSnapshot) string {
	listed := strings.Join(scopes, ", ")
	after, err := gitutil.SnapshotFiles(ctx, ws.Root, gitutil.SnapshotCaps{})
	if err == nil {
		var changed []string
		changed, err = gitutil.ChangedPaths(ctx, ws.Root, before, after)
		if err == nil {
			return restoreOutOfScope(ctx, ws, scopes, before, changed)
		}
	}
	slog.Warn("Could not check a bash call against its write scopes", "dir", ws.Root, "error", err)
	return fmt.Sprintf("Could not check this command's changes against the write scopes of your claimed task (%s): %s. Check git status in your worktree and undo any change outside them.",
		listed, err)
}

// restoreOutOfScope restores the changed paths outside scopes and describes
// what it did. Status paths are relative to the worktree root, scopes to
// the working directory inside it.
func restoreOutOfScope(ctx context.Context, ws agentteam.Workspace, scopes []string, before gitutil.FileSnapshot, changed []string) string {
	prefix := ""
	if rel, err := filepath.Rel(ws.Root, ws.Dir); err == nil && rel != "." {
		prefix = filepath.ToSlash(rel) + "/"
	}
	var outside []string
	for _, path := range changed {
		if rel, inDir := strings.CutPrefix(path, prefix); inDir && pathInScopes(rel, scopes) {
			continue
		}
		outside = append(outside, path)
	}
	if len(outside) == 0 {
		return ""
	}
	result := gitutil.RestorePaths(ctx, ws.Root, before, outside)
	var sb strings.Builder
	fmt.Fprintf(&sb, "This command changed files outside the write scopes of your claimed task (%s).", strings.Join(scopes, ", "))
	if len(result.Restored) > 0 {
		fmt.Fprintf(&sb, " Restored: %s.", strings.Join(result.Restored, ", "))
	}
	if len(result.Unrestored) > 0 {
		fmt.Fprintf(&sb, " Not restored (over the 1 MiB per-file or 32 MiB per-call save cap, or the restore failed): %s.",
			strings.Join(result.Unrestored, ", "))
	}
	sb.WriteString(" Change only files inside your scopes. The command's output follows.")
	return sb.String()
}

// withScopeNote marks resp as an error led by note, keeping its metadata
// so the UI still renders the command.
func withScopeNote(resp fantasy.ToolResponse, note string) fantasy.ToolResponse {
	if note == "" {
		return resp
	}
	resp.Content = note + "\n\n" + resp.Content
	resp.IsError = true
	return resp
}
```

`strings.CutPrefix(path, "")` returns `path, true`, so with the Lead at the repository root every changed path is checked against the scopes as is.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `gofumpt -w internal/shell/background.go internal/shell/background_finisher_test.go internal/agent/scope_gate.go internal/agent/bash_scope_test.go && go test ./internal/shell/ ./internal/agent/ && go test -race ./internal/shell/ -run Background && go test -race ./internal/agent/ -run 'BashScope|ScopeGate'`
Expected: PASS. `TestScopeGateWrapsOnlyWriteTools` still passes: its list has no bash.

- [ ] **Step 7: Commit**

```bash
git add internal/shell/background.go internal/shell/background_finisher_test.go internal/agent/scope_gate.go internal/agent/bash_scope_test.go
git commit -m "feat: hold teammate bash to its write scopes in task worktrees"
```

---
### Task 11: Documentation

**Files:**
- Modify: `docs/agent-teams.md` (`### Auto-start`, `### Verification`, a new `### Worktrees`, `### Write scopes`, `### Hooks`, `## Durable state`)
- Modify: `docs/hooks/README.md` (the **Scope** paragraph)

**Interfaces:**
- Consumes: the behavior of Tasks 2 to 10, as built.
- Produces: user-facing docs only.

- [ ] **Step 1: Auto-start brief**

In `docs/agent-teams.md`, under `### Auto-start`, replace:

```markdown
live-member limit. At the limit the task waits for a later pass. The teammate
gets a short brief with the task, its write scopes and how to complete it.
```

with:

```markdown
live-member limit. At the limit the task waits for a later pass. The teammate
gets a short brief with the task, its write scopes and how to complete it,
the task's worktree and branch when it has one (see [Worktrees](#worktrees)),
and the notes of the task's last failed verification, if any.
```

- [ ] **Step 2: Verification sees the worktree**

Under `### Verification`, replace:

```markdown
The verifier runs every check itself, but it shares the working directory
with everyone else, so it sees whatever is on disk when it runs.
```

with:

```markdown
The verifier runs every check itself. For a task with a worktree it runs in
that worktree, where the work is committed on the task branch; otherwise it
shares the working directory with everyone else and sees whatever is on disk
when it runs. A failed verification's notes stay on the task, so the next
implementer's brief carries them even when the owner could not be resumed.
```

- [ ] **Step 3: Add the Worktrees section**

Insert before `### Write scopes`:

```markdown
### Worktrees

Inside a git repository, a task with write scopes gets its own git worktree
when it is first given to a teammate: by the dispatcher, by the teammate's
own `claim`, or by the Lead's `reassign`. The worktree starts at the Lead's
current `HEAD`, on branch `crush/team-<task id>`, in the sibling directory
`.<repo>-worktrees/team-<task id>` next to the repository. The task view
names both (`worktree`, `branch`). A task that already has a worktree keeps
it: `release`, `reopen` and a failed verification leave it for the next
implementer, who sees the earlier commits. A teammate works in one worktree
at a time, so it cannot claim a second scoped task while it holds one.

The teammate's tools run in the worktree: relative paths resolve there and
`bash` starts there. When the Lead works in a subdirectory of the
repository, the teammate starts in the same subdirectory of the worktree.
The brief names the worktree and the branch; no tool definition and no
system prompt changes, so the prompt cache stays shared with the Lead. LSP
tools refuse in a worktree ("LSP tools are not available in a task
worktree"), because the language servers index the Lead's tree. The
verifier runs in the worktree of the work it checks.

When the task completes (by `complete` or by its owner's turn ending), the
service commits everything in the worktree to the branch, with the task
subject as the message and the repository's git config for author, signing
and hooks; an empty change set makes no commit. A commit that fails refuses
`complete` with git's output, and a turn whose work cannot be committed
releases its task. On `pass`, on the Lead's `complete` of the task in
review, and for a task with `verify` false as soon as it completes, the
service merges the branch into the Lead's current branch with
`git merge --no-ff` (message `Merge team task <id>: <subject>`), then
removes the worktree and deletes the branch. Dependents become ready only
after the merge.

A merge that conflicts, that an uncommitted change in the Lead's tree
blocks, or that finds the Lead on a detached `HEAD` is aborted: the Lead's
tree is left as it was, the worktree and branch stay, and the task stays
`in_review`, escalated, with a notice naming the branch and git's output.
Merge the branch by hand and `complete` the task to accept it, or `reopen`
it. Deleting a task removes its worktree and branch; if the removal fails,
the Lead is told and the task is still deleted. If a worktree cannot be
created, the claim is released with no attempt counted, the Lead is told,
and the dispatcher leaves the task until the Lead edits it.

Outside a git repository, or for a task without write scopes, every member
shares the Lead's working directory as before.
```

- [ ] **Step 4: Write scopes cover bash**

Replace the body of `### Write scopes`:

```markdown
A teammate's `edit`, `multiedit`, `write`, `download`, `apply_patch` and
`lsp_replace_symbol` calls are checked against the write scopes of the tasks
it owns in progress; a path outside them is refused with a tool error, before
any hook or permission prompt. `lsp_rename` is refused while scopes apply,
because the files it changes are not known in advance. A teammate with no
scoped task is not restricted, and the Lead never is. `bash` is not covered:
a command can write anywhere. Symlinks are not resolved: the checked path is
the one in the call, made relative to the working directory and cleaned.
Per-teammate worktrees are the planned fix.
```

with:

```markdown
A teammate's `edit`, `multiedit`, `write`, `download`, `apply_patch` and
`lsp_replace_symbol` calls are checked against the write scopes of the tasks
it owns in progress; a path outside them is refused with a tool error, before
any hook or permission prompt. `lsp_rename` is refused while scopes apply,
because the files it changes are not known in advance. A teammate with no
scoped task is not restricted, and the Lead never is. Symlinks are not
resolved: the checked path is the one in the call, made relative to the
working directory and cleaned.

In a task worktree, `bash` is held to the scopes too. Before each call the
service records the worktree's git status and a hash of every listed file;
after it, each changed path outside the scopes is put back (a clean tracked
file from `HEAD`, a file that was already changed from the content saved
before the call, a new untracked file removed), and the call returns an
error that lists the restored paths, followed by the command's output. A
background job is checked when it finishes, and its output starts with the
same message. Saved content is capped at 1 MiB per file and 32 MiB per call:
a changed file over the cap is reported, not restored. Files git ignores and
paths outside the worktree are not checked, and this is no sandbox: a
command can still write outside the worktree. Without a worktree (outside a
git repository) `bash` is not checked.
```

- [ ] **Step 5: Hooks and durable state**

Under `### Hooks`, after the paragraph that ends `their calls are non-interactive.`, add:

```markdown
A teammate in a task worktree fires its hooks with the Lead's working
directory as `cwd`, like every hook; relative paths in its `tool_input`
resolve against its worktree, which the task's view names.
```

Under `## Durable state`, after the paragraph that ends `normalizes any stale \`running\` status to \`inactive\`.`, add:

```markdown
Tasks also record their `worktree` and `branch`, and the notes of their last
failed verification (`last_failure`); members record the session summary
their inherited fork context was last sent on (`context_summary`), so the
context is sent again after a compaction. Every one of these is omitted when
empty, so an older `team.json` loads unchanged.
```

- [ ] **Step 6: Hooks README**

In `docs/hooks/README.md`, in the **Scope** paragraph, replace:

```markdown
hooked. A teammate call refused by its write scopes runs no hook. Teammates
fire no turn events, because their turns are non-interactive.
```

with:

```markdown
hooked. A teammate call refused by its write scopes runs no hook. Teammates
fire no turn events, because their turns are non-interactive. A teammate
working in a task worktree gets the same `cwd` (and `CRUSH_CWD`) as every
hook, the Lead's working directory; relative paths in its `tool_input`
resolve against the worktree, which `team_task_get` reports as the task's
`worktree`.
```

- [ ] **Step 7: Check the docs**

Run: `grep -n $'\xe2\x80\x94' docs/agent-teams.md docs/hooks/README.md ; grep -n 'planned fix' docs/agent-teams.md`
Expected: no output from either command.

- [ ] **Step 8: Commit**

```bash
git add docs/agent-teams.md docs/hooks/README.md
git commit -m "docs: describe team worktrees, merges and the bash scope check"
```

---

### Task 12: Whole-branch verification

**Files:** none changed unless a check fails; fix in the file that owns the code and add a `fix:` commit.

- [ ] **Step 1: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: no output, exit 0.

- [ ] **Step 2: Full test suite, and the race detector on the touched packages**

Run: `go test ./... && go test -race ./internal/agentteam/ ./internal/agent/ ./internal/gitutil/ ./internal/shell/`
Expected: PASS.

- [ ] **Step 3: Cache tests three times**

Run: `go test ./internal/agent/ -run 'Cache|Palette|Prefix|SystemPrompt|WrapperOrder|RoleTeammate|Worktree' -count=3`
Expected: PASS all three runs.

- [ ] **Step 4: Formatting and log style**

Run: `gofumpt -l . && ./scripts/check_log_capitalization.sh`
Expected: `gofumpt -l` prints nothing; the script exits 0.

- [ ] **Step 5: No em dashes added**

Run: `git diff main...HEAD -- . ':!docs/superpowers' | grep '^+' | grep -c $'\xe2\x80\x94'`
Expected: `0`.

- [ ] **Step 6: No git under a lock**

Run: `grep -n 'gitutil\.' internal/agentteam/*.go | grep -v _test.go`
Expected: every hit is in `worktree.go` or `merge.go`, in a function called with no `s.mu`, write slot or file lock held: `repo` (inside its `sync.Once`), and `createWorktree`, `discardWorktree`, `removeTaskWorktree`, `commitWork` and `mergeBranch`, which take `s.gitMu`. `mergeRefusedText` only names the `gitutil.MergeError` type. No hit is inside a `s.write` closure or a `...Locked` function.

- [ ] **Step 7: Lint, if available**

Run: `task lint` (or `golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m` when `task` is absent)
Expected: no findings in files this branch touched.

- [ ] **Step 8: Commit any fix**

If a step failed and you fixed it:

```bash
git add <the files you fixed>
git commit -m "fix: <what the check caught>"
```

---

## Spec coverage

| Spec section | Task |
| --- | --- |
| Constraints: cache parity, path in messages only | 8 (wrapper, cache test), Global Constraints |
| Constraints: `Runner.Start` and git outside every lock | 7, 9 (`gitMu`, write-git-write), 12 Step 6 |
| Constraints: no repo or no scopes behaves as before | 7 (`TestTasksWithoutScopesOrRepoGetNoWorktree`), 10 (`TestBashScopeCheckNeedsAWorktree`) |
| 1. Creation: base `HEAD`, branch, path, fields, CAS record, failure releases uncharged, reuse | 5 (prefix), 7 |
| 1. Where the teammate works: tools, prompt unchanged, brief, LSP refusal | 7 (brief), 8 |
| 1. One active worktree per member | 7 (`TestOneScopedWorktreePerMember`) |
| 2. Commit on complete, empty change set, commit failure | 9 |
| 2. Verification inherits the worktree | 7 (`MemberWorkspace`), 9 (description) |
| 2. Pass merges `--no-ff`, removes worktree and branch, dependents after merge | 9 |
| 2. Merge refused: conflict, dirty tree, detached HEAD, Lead resolves | 5, 9 |
| 2. Fail keeps, Lead accept merges, delete removes, reopen keeps, CAS miss notice | 7, 9 |
| 3. Bash check: snapshot, compare, restore, error, ignored, background, caps | 6, 10 |
| 4. Path resolution | 1 |
| 4. Failure notes survive | 2 |
| 4. Shutdown window | 3 |
| 4. Fork context after compaction | 4 |
| 5. Errors table | 7 (creation, removal), 9 (commit, merge), 10 (bash, cap), 7 and 10 (no repo) |
| 6. Testing, including old `team.json` loads | 1 to 10 (omitempty tests in 2, 4, 7) |
| 7. Documentation, hooks cwd | 11 |
