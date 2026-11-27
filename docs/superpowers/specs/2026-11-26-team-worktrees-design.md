# Agent Team Worktrees, Bash Scopes and Pipeline Follow-ups

Status: design approved in chat on 2026-11-26; written spec awaiting review.

Builds on the agent team pipeline (PR #193). That PR left these gaps: teammates share one working tree, `bash` is not held to write scopes, `lsp_replace_symbol` resolves relative paths against the process directory, a verifier's failure notes are lost after a failed resume, a narrow Shutdown window can leave a held task in progress, and a fork teammate loses the Lead's context verbatim after compaction.

## Goals

- A teammate working on a task with write scopes edits in its own git worktree, so parallel teammates never touch each other's files or the Lead's tree.
- Verified work reaches the Lead's tree on its own: a `pass` merges the task branch.
- `bash` is held to the claimed write scopes, like the file tools.
- The four smaller follow-ups are fixed.

Non-goals: kernel sandboxing (Landlock, namespaces); per-member worktrees; LSP inside worktrees; checking files outside the worktree (for example `/tmp`) or files git ignores.

## Constraints

- Prompt caching stays first. The system prompt and the tools block (every tool's `Info()`, the tool list and its order) remain byte-identical between the Lead and every teammate, whatever its worktree. The worktree path travels in messages only.
- `Runner.Start` never runs under the service lock, the write slot or the file lock (unchanged rule). Git commands never run under `s.mu` or the file lock either: they can be slow and they touch the filesystem.
- Outside a git repository, or for a task without write scopes, behavior is exactly as after PR #193.
- No new module dependencies; git is driven through the existing `internal/gitutil` helpers (extended where needed).

## 1. One worktree per scoped task

**Creation.** When the dispatcher (or a manual `claim`) gives a task with write scopes to a teammate, and the Lead's working directory is inside a git repository, the service creates a worktree for the task before the teammate starts:

- Base: the Lead tree's current `HEAD` commit at claim time.
- Branch `crush/team-<task-id>`, path from `gitutil.CreateWorktreeFrom` (sibling `.<repo>-worktrees/team-<task-id>`).
- The task records it: new durable fields `Task.Worktree` (absolute path) and `Task.Branch`, both `omitempty`.
- If creation fails, the claim is released without an attempt charged and the Lead gets a notice naming the error; the task stays pending.
- A task that already has a worktree (reopened, failed verification, re-dispatched after a failed resume) reuses it; nothing is recreated.

Creation runs outside every lock: the dispatcher plans the claim in its write as today, creates the worktree after the write returns and before `launch`, then records `Worktree`/`Branch` in a CAS write (task id, revision, owner). A claim the Lead changed meanwhile removes the just-created worktree.

**Where the teammate works.** A teammate holding a task with a worktree runs its tools in that worktree:

- `teamRunner.Start` builds the teammate's tools with the worktree as their working directory (file tools, `bash`, `view`, `glob`, `grep`, `ls`, `apply_patch`, `download`, scope gate, file tracker paths). Teammate agents are already built per start, so this is a parameter of that build, not a new code path.
- The system prompt is the Lead's prebuilt prompt, unchanged. The tools block is unchanged: no tool `Info()` mentions a working directory (asserted by a test).
- The teammate's first message (the task brief) states the worktree path and the branch, and that relative paths resolve there.
- LSP clients serve the Lead's tree only. `lsp_*` tools called by a teammate in a worktree return a refusal ("LSP tools are not available in a task worktree"), as they do for paths outside the workspace today.

**One active worktree per member.** A teammate owns at most one in-progress task with a worktree at a time; the dispatcher does not give a member a second scoped task while it holds one. A member's tools follow the worktree of its current scoped task; a member with no scoped task works in the Lead tree.

## 2. Completion, verification and merge

- **complete.** Before the task enters `in_review`, the service commits all changes in the worktree to the task branch (`git add -A`, commit message = task subject, author from the repo's git config). An empty change set still moves to review. A commit failure refuses `complete` with the git error, task unchanged.
- **Verification.** The verification task inherits the original's worktree for running: the verifier's tools run in the same worktree, read-only by role. The verifier has no write scopes (unchanged), so the scope gate and bash check do not apply to it.
- **pass.** The service merges the task branch into the Lead tree's current branch with `git merge --no-ff` (message "Merge team task <id>: <subject>"), then removes the worktree and deletes the branch. The original completes and dependents become ready only after the merge succeeds.
- **Merge refused.** A conflict, a dirty Lead tree that blocks the merge, or a detached Lead `HEAD` aborts the merge (`git merge --abort` when started), keeps the worktree and branch, leaves the original `in_review` and escalated, and sends the Lead a notice naming the branch and the git output. The Lead resolves it by merging by hand then `complete` (Lead accept path from PR #193), or by `reopen`.
- **fail.** The worktree stays for the next implementer turn (same or new member).
- **Lead complete of an in_review task** (accept as is) runs the same merge as `pass`.
- **delete.** Deleting a task removes its worktree and branch (best effort; a failure is logged and noticed to the Lead). **reopen** keeps them.

All git work for these transitions runs outside the service locks. The service first validates the transition read-only under `s.mu`, then runs git with no lock held, then applies the state change in a CAS write (task id and revision). No new "merging" status is introduced. A CAS miss after a successful merge leaves the merge in place and sends the Lead a notice.

## 3. Bash held to write scopes

For a teammate with claimed write scopes and a task worktree:

- Before each `bash` call, the scope gate records `git status --porcelain=v1 -z --untracked-files=all` for the worktree, plus a content hash of each listed path.
- After the call returns, it records the status again. A path is "changed by the command" if it is new in the list, left the list, or its hash changed.
- Changed paths outside the claimed scopes are restored: tracked paths with `git restore --source=HEAD --staged --worktree -- <path>` when they were clean before the call, otherwise from the content saved before the call; untracked new paths are removed; an untracked path that existed before is rewritten from the saved content.
- The call's result is replaced by an error that lists the restored paths and the scopes, followed by the command's original output.
- Ignored paths (`.gitignore`) are not checked. Paths outside the worktree are not checked.
- A background `bash` job is checked when it finishes, using the snapshot taken when it started; its completion output carries the same error.
- Saved content is capped (1 MiB per file, 32 MiB per call); a path over the cap that changed out of scope is reported and not restored.

A teammate without a worktree (no git repository) keeps today's behavior: `bash` is not gated, as documented.

## 4. Smaller fixes

- **Path resolution.** `lsp_replace_symbol`, `lsp_symbols` and `diagnostics` resolve `file_path` with `filepathext.SmartJoin(workingDir, home.Long(path))` like `edit`.
- **Failure notes survive.** A `fail` verdict stores its notes on the original task in a new durable field `Task.LastFailure` (`omitempty`, capped at 8 KiB), cleared on `pass` and on Lead `edit`. `taskBrief` appends "The last verification failed:" and the notes when the field is set. The resume path is unchanged (the owner still gets the notes as mail).
- **Shutdown window.** In `watch`, when the resume after a held settle fails with `errShutdown`, or the resume-failure write sees `closed`, the held tasks are released to pending without charging an attempt, in the same write that hands the mail back. Shutdown still waits for this write (the watcher is counted in `s.watchers`).
- **Fork context after compaction.** The member record gets `Member.ContextSummary` (`omitempty`): the teammate session's `SummaryMessageID` when the fork prefix was last sent. `teammatePrompt` prepends the fork prefix on the first turn and on the first turn whose session `SummaryMessageID` differs from the recorded value; the role block stays first-turn only. The value is recorded in the write that starts the run.

## 5. Errors

| Case | Behavior |
| --- | --- |
| Worktree creation fails | Claim released, no attempt, Lead notice |
| Commit on complete fails | `complete` refused with git output |
| Merge conflict / dirty Lead tree / detached HEAD | Merge aborted, task stays `in_review` escalated, Lead notice with branch |
| Worktree removal fails | Logged, Lead notice, task state change still applies |
| Bash changes out-of-scope paths | Restored, call returns an error listing them |
| Out-of-scope change over the size cap | Reported, not restored |
| Not a git repo | Shared tree, bash ungated (today's behavior) |

## 6. Testing

- Worktree lifecycle against a real temporary git repository (`t.TempDir()`, `git init`): claim creates the worktree on `crush/team-<id>`; complete commits; pass merges with `--no-ff` and removes worktree and branch; fail keeps it; delete removes it; reopen keeps it; a reused task does not recreate it.
- Merge conflict escalates and keeps the branch; dirty Lead tree escalates.
- Creation failure releases the claim without an attempt.
- A teammate in a worktree: its file tools and bash run in the worktree; its system prompt and tools block are byte-identical to the Lead's (extend the existing cache tests); `lsp_*` refuse.
- Bash check: an out-of-scope `sed -i` on a tracked file is restored; a new out-of-scope file is removed; an in-scope change stays; a background job is checked at completion; the size cap reports without restoring.
- Each smaller fix: a test that fails before the change (relative path for the three LSP tools; next implementer brief carries failure notes; Shutdown in the held-settle window releases without an attempt; fork prefix re-sent once after a compaction and not on the turn after).
- Old `team.json` files load unchanged (new fields `omitempty`).

## 7. Documentation

`docs/agent-teams.md`: worktrees (when they are created, where they live, merge on pass, conflicts, cleanup), the bash check and its limits, the LSP refusal in worktrees. `docs/hooks/README.md`: state which working directory a teammate's tool hooks receive when the teammate works in a task worktree (the plan checks how hooks get their cwd today and keeps the payload accurate).
