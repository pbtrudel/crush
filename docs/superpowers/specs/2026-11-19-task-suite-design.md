# Task Suite Design

Design for item W0.4 of the roadmap (`docs/superpowers/plans/2026-11-17-model-intelligence-roadmap.md`). It builds on W0.1 to W0.3 (`docs/superpowers/specs/2026-11-17-measurement-design.md`). No code has been changed. Code references are to commit `3f18401c` (`origin/main`).

## Goal

Give every behavior change and experiment of the roadmap one number that says whether the same model finishes more tasks correctly inside Crush. The suite is 25 to 30 tasks cut from this repository's merged pull requests. A runner runs each task in a throwaway checkout through `crush run --json`, grades it with the pull request's own tests, and records the result. A comparison step pairs two variants run for run and applies the margin rule of the roadmap.

Nothing in this design changes what a model is sent. The runner only calls the `crush` binary that is under test.

## Terms

- **Task**: one problem with a base commit, a prompt and a grade.
- **Variant**: what is being measured. A Crush build (a git ref) plus an optional config overlay, for example `agent_teams: false` for W8.
- **Route**: how one model is reached: provider, model id, reasoning effort, small model, credentials, usage limits.
- **Run**: one task, one variant, one route, one repetition. It ends with one result line.
- **Batch**: the runs of one comparison or one baseline.
- **Hidden tests**: the test files the pull request added or changed. They are written into the checkout only after the model is done.
- **Plain task**: the hidden tests compile against the parent commit.
- **Interface task**: the hidden tests call a few names the pull request introduced, so the prompt states those names and their signatures.

## Evidence

Gathered on 2026-11-18 and 2026-11-19 with read-only `gh` queries and with Go builds in a scratch directory. No model was run.

1. **Pull requests.** `pbtrudel/crush` has 116 merged pull requests. 91 were fetched in full, and 90 of those have a body with `## Why`, `## How` and `## Testing` sections, so the prompt source is uniform. The other 25 were not fetched: upstream syncs, dependency bumps, CI and documentation changes, and changes of 1,700 added lines or more.
2. **Upstream history.** 996 squash commits ending in `(#NNN)` from `charmbracelet/crush` are reachable from `origin/main` (2025-04-30 to 2026-11-05, upstream v0.100.0). They are a second pool; see Decision needed 1.
3. **Classification.** For each of the 87 fork pull requests that change a `_test.go` file, a script took the parent commit (`merge^1`) with `git archive`, wrote over it the test files of the merge commit, and ran the touched tests (`go test -count=1 -run '^(names)$' <packages>`), then ran the same tests on the merge commit. Results:

   | Class | Count | Meaning |
   |---|---|---|
   | Plain | 13 | Tests compile on the parent, fail there, pass on the merge |
   | Introduced names | 73 | Tests do not compile on the parent: they call a function, method, field or signature the pull request added |
   | of those, 1 to 4 missing names | about 20 | Candidates for interface tasks |
   | Merge fails | 1 | #16: `TestCoderAgent` fails on the merge commit as well (recorded provider streams) |
   | Pass on parent | 0 | |

   The compile check is mechanical and is the rule this design adopts for "tests depend on names the pull request introduced".
4. **Hidden choices.** A test can compile on the parent and still encode a choice the problem does not state. #42 is the example: its Why says `ctrl+shift+s` collapses to `ctrl+s` in most terminals, and its test presses `alt+s` (`internal/ui/model/stash_test.go:124-136` at the merge commit). The compile check cannot see this; the person who writes the task has to.
5. **Grading cost.** Building and running the touched tests took 1.4 to 72 seconds per pull request on this machine (12 cores), warm module and build caches, with `GOPROXY=off`. No candidate needed the network.
6. **Leaks a worktree would allow.** A `git worktree` shares the object store and refs of the repository, so `git log --all` or `git show <merge>` in the checkout shows the answer. The suite therefore does not use a worktree of this repository; see 3.2.
7. **What `crush run` already gives.** `--json` prints one object (`internal/app/run_json.go:60-74`): `reason`, `text`, `duration_ms`, `model`, `provider`, `build`, `session` and `tree` counts with tokens, `tree.turn_ends`, `tree.undelivered_reports`, `estimated`, `cost`, `error`. Exit codes are 0 (completed), 2 (gradable, stopped short) and 3 (says nothing about the model) (`internal/app/run_exit.go:41-54`). `--max-steps` and `--timeout` bound the whole session tree (`internal/cmd/run.go:199-200`). The three flags are refused with `CRUSH_CLIENT_SERVER=1` (`internal/cmd/run.go:206-223`). Permissions are auto-approved for the run's session (`internal/app/app.go:471-473`). `curl` and `wget` are on the shell's block list (`internal/agent/tools/bash.go:90`, `:103`); `gh` is not.
8. **Where Crush reads configuration.** The global config is `$CRUSH_GLOBAL_CONFIG/crush.json`, else `~/.config/crush/crush.json`; machine state, including OAuth tokens (`internal/config/config.go:111`), is `$CRUSH_GLOBAL_DATA/crush.json`; global context files default to `CRUSH.md` beside the global config and `AGENTS.md` one directory up; global skills come from `$CRUSH_SKILLS_DIR` when it is set (`internal/config/load.go:836-845`, `:1416-1480`, `:1566-1585`). Project configs are found by walking up from the working directory to the git root (`load.go:1146-1179`). The repository tracks a `crush.json` at its root, so every task checkout carries the project config of its base commit.

## Design

### 1. Task selection

#### 1.1 Pool

The primary pool is the merged pull requests of `pbtrudel/crush`. Upstream commits reachable from `origin/main` are a reserve pool, used only if the owner accepts them (Decision needed 1) and only if fewer than 20 fork tasks survive vetting.

#### 1.2 Rules

A pull request becomes a task when all of these hold.

1. Its diff changes at least one non-test `.go` file and at least one `_test.go` file.
2. It is not an upstream sync, a dependency bump, a CI or documentation change, and it changes at most 12 source files and about 600 added source lines. A larger change is not one prompt.
3. Its Why section states a problem a user or a developer could have reported. A Why that only says "refactor" or "add feature X" is not a problem statement.
4. The touched tests, written over the parent commit, either compile (plain task) or fail to compile only on at most four introduced names or changed signatures (interface task). More than four is dropped, because the prompt would describe the solution.
5. The touched tests fail on the parent with the hidden tests applied, every time in five repetitions.
6. The touched tests and every other test of their packages pass on the merge commit, every time in five repetitions. A package with a test that fails or flakes at the merge commit is not graded as a whole; the task is dropped or the flaky test is excluded by name and the exclusion recorded.
7. The grade needs no network and finishes in under five minutes with warm caches.
8. The assertions encode no choice the prompt leaves open. When they do (a key binding, an exact string, a field name in output), the prompt states the choice.
9. At most six tasks grade the same package, so one area does not dominate the pass rate.

#### 1.3 The prompt

The prompt is the pull request's Why section, edited by a person under three rules, plus a footer that is the same for every task.

1. Sentences that describe the change are removed. A Why often states the cause, which is what a good bug report does, and that stays. A sentence that says what the fix does ("so the tool now returns ...") goes.
2. References to later commits or pull requests are removed. References to earlier ones stay: they are in the checkout's history only as code, not as commits (3.2), but the text is what a reporter would have written.
3. For an interface task, a paragraph titled "Interface" lists each introduced name with its package and signature, copied from the merge commit. For a hidden choice, the paragraph states the choice in one sentence.

Footer, identical for all tasks and variants: "Make this change in the repository in the current directory. When you are done, the work should be in the working tree of this checkout." The footer does not mention hidden tests, so models are not told that something else will check.

The original Why text and the edited prompt are both kept in the task file, so a reviewer can see every edit.

#### 1.4 Method

The method is a command of the runner (`crush-eval candidates`, section 7), so the list can be rebuilt after new pull requests merge.

1. List merged pull requests with `gh pr list -R pbtrudel/crush --state merged --limit 500 --json number,title,mergeCommit,additions,changedFiles`, then `gh pr view N --json files,body,mergeCommit` for each that passes rule 2.
2. For each, extract the parent tree with `git archive <merge>^1`, write the merge commit's versions of the touched test files over it (deleting files the pull request deleted), and run `go test -count=1 -run '^(touched tests)$'` on the packages of those files. Touched tests are the `Test` functions the diff adds or whose body it changes. Classify as plain, interface (with the list of missing names parsed from the compiler output), or dropped. Then run the same on the merge commit.
3. A person writes the task file for each plain or interface candidate: prompt, interface paragraph, hidden choices.
4. `crush-eval vet` runs rules 5 to 7 with five repetitions each and writes the timings into the task file.
5. Rule 9 is applied last, keeping the tasks with the smallest diffs.

#### 1.5 First draft

27 tasks and 3 reserves from the survey of 2026-11-19. "Parent" is `merge^1`. "Tests" are the touched tests the grade runs by name; the grade also runs the rest of their packages (3.5). Grade time is the merge-commit run of step 2 above, compile included.

Plain tasks:

| PR | Problem | Parent | Grade packages and tests | Time | Notes |
|---|---|---|---|---|---|
| 10 | Fork builds always report an update to the upstream tag they were cut from | `6dd947b1` | `./internal/update`: `TestCheckForUpdate_Fork`, `TestCheckForUpdate_Beta` | 2 s | Plain, small |
| 35 | The Discord status footer's "Done in" keeps counting after a turn ends | `977cd9c3` | `./internal/discord`: `TestFooterTerminalElapsedFreezesAtCompletion`, `TestBridgeFooterMetrics` | 36 s | Plain |
| 42 | The prompt stash shortcut opens the sessions dialog in most terminals | `d052c46d` | `./internal/ui/model`: `TestTextareaStashAltS`, `TestTextareaMouseClickDoesNotCopy` | 48 s | Hidden choice: prompt must name `alt+s` |
| 45 | Native slash commands in the Discord thread fail with "did not respond" | `f6d2ba93` | `./internal/discord`: 6 tests (`TestBridgeInteraction*`, `TestQuestionComponent*`, `TestQuestionSubmitFallsBackToEditWhenCallbackFails`) | 39 s | Plain; Why names the regressing commit, which is in history |
| 55 | The sidebar shows the previous model after a model switch | `5444c830` | `./internal/agent`: `TestUpdateModelsRefreshesAgentModel` | 26 s | Plain |
| 61 | Side questions return "no answer" because the prompt asks for tools that are absent | `b55e91d5` | `./internal/agent`: `TestEphemeralAnswersWithSideQuestionPrompt`, `TestEphemeralKeepsConversationHistory` | 23 s | Same area as 75 and 80 |
| 75 | An empty side-question answer gives no reason | `19f1cf88` | `./internal/agent`: `TestEphemeralReportsWhyAnAnswerIsEmpty`, `TestEphemeralKeepsConversationHistory` | 31 s | Check the asserted log or message text for a hidden choice |
| 79 | Teammates start their turn with an empty tool palette | `a4157de3` | `./internal/agent`: `TestTeammateAgentIsReadyWhenBuilt`, `TestTeammateIdentityOverridesLeadBrief` | 31 s | Why names the cause; trim the fix sentence |
| 80 | Side questions on long sessions come back empty with `finish_reason=stop` | `592fe726` | `./internal/agent`: `TestEphemeralRetriesWhenTheProviderAnswersWithNothing` and two more | 25 s | Same area as 61 and 75 |
| 85 | Claude Agent models show a 200K window and a bare family name | `b11c8ddf` | `./internal/claudeagent`: `TestProviderEntryContextWindow`, `TestProviderEntryName`, `TestProviderEntryPricing` | 4 s | Hidden choice likely: expected names and windows; state them |
| 92 | The model picker offers reasoning levels the configured model does not have | `02e688f3` | `./internal/ui/dialog`: `TestModelsUsesConfiguredReasoningLevels`, `TestModelsToggleIgnoredDuringOnboarding` | 40 s | Plain |
| 93 | The `/goal` dialog draws its cursor on the label row and does not wrap long text | `66fd188a` | `./internal/ui/dialog`: 5 `TestArguments*` tests | 58 s | Risk: asserts rendered rows, close to a golden file |
| 94 | `/goal` and `/loop` are missing from the palette before a session exists | `f40751a7` | `./internal/ui/dialog`, `./internal/ui/model`: `TestGoalAndLoopCommandsAlwaysVisible`, `TestGoalAndLoopCommandsVisibleWithSession` | 49 s | Plain |

Interface tasks (the names the prompt must state come from the compiler output on the parent):

| PR | Problem | Parent | Interface to state | Grade packages and tests | Time | Notes |
|---|---|---|---|---|---|---|
| 11 | `/rc` never appears in the palette on a fresh launch | `334c5d79` | `func (m *UI) toggleRemoteControl() []tea.Cmd` | `./internal/ui/dialog`, `./internal/ui/model`: `TestRemoteControlCommandVisibleWithoutSession`, `TestRemoteControlCreatesSessionWhenNoneActive` | 30 s | |
| 18 | Shell commands with file redirection skip the permission prompt | `5ff2a4bd` | `func isSafeReadOnlyCommand(cmd string) bool` | `./internal/agent/tools`: `TestIsSafeReadOnlyCommand`, `TestContainsCommandChaining`, two `TestBashTool_*` | 10 s | Hidden choice: which commands leave the allow list; state the list from the Why |
| 19 | A request over the context window fails the turn instead of compacting | `46cdb8cb` | `func isContextOverflowError(err error) bool`, `func shouldRecoverFromOverflow(err error, retried bool) bool` | `./internal/agent`: `TestIsContextOverflowError`, `TestShouldRecoverFromOverflow` | 15 s | Interface is close to the design; borderline |
| 20 | UI workspace operations wait forever on a stalled backend | `51fe11f9` | `func WorkspaceOpContext() (context.Context, context.CancelFunc)`, `const WorkspaceOpTimeout` (30 s) in `internal/ui/common` | `./internal/ui/common`: `TestWorkspaceOpContext` | 19 s | Grade covers the helper only, not its callers; weak grade |
| 21 | Image tokens are estimated from metadata, so a 1 MB screenshot counts as ten tokens | `c2d3b481` | `func estimateImageDataTokens(data []byte) int64` | `./internal/agent`: 7 `TestEstimate*` tests and `TestUpdateSessionUsageAddsProviderCost` | 14 s | Hidden choice: the formula; state the expected rule |
| 22 | Early log records are discarded before the log file exists | `50ccadad` | `func newBufferHandler() *bufferHandler` and its replay method | `./internal/log`: 4 `TestBufferHandler*` tests | 1 s | Interface names the method set; borderline |
| 27 | Cache hits from DeepSeek and Kimi are counted as input tokens | `720d1590` | `cacheAwareUsage` and `cacheAwareStreamUsage` with their fantasy signatures | `./internal/agent`: `TestCacheAwareUsageDialects` | 12 s | |
| 44 | Finished background jobs fill the 50-job cap; YOLO mode still blocks `sudo` | `9fa5f7cb` | `blockFuncs` gains a YOLO argument | `./internal/agent/tools`, `./internal/shell`: 5 tests | 15 s | Two problems in one prompt |
| 51 | `job_kill` on a reaped shell reports an error | `9530816e` | `var ErrBackgroundShellNotFound` in `internal/shell` | `./internal/agent/tools`, `./internal/shell`: `TestJobKillUnknownShellIsNoop`, `TestBackgroundShellManager_KillNonExistent` | 16 s | |
| 57 | The sidebar's average TPS mixes models after a switch | `b434bbce` | `func (m *UI) resetTPS()` | `./internal/ui/model`: 3 `TestReset*TPS*` tests | 44 s | |
| 72 | The team sidebar goes blank in sessions that did not spawn the members | `827396de` | `(*coordinator).finishAgentRun(sessionID string, err error)`, `(*coordinator).startTeammateRun(sess session.Session, member agentteam.Member)` | `./internal/agent`, `./internal/agentteam`, `./internal/ui/model`: 10 tests | 48 s | Large; three problems in one Why |
| 76 | The model is told job ids that no longer exist | `0d4af8b4` | `func (s *BackgroundShell) SetSessionID(id string)` (confirm the receiver when writing the task) | `./internal/agent/tools`, `./internal/shell`: 6 tests | 20 s | |
| 81 | Subagents cannot use MCP servers | `83250820` | `func resolveSubAgentTools(tools []string) []string` | `./internal/agent`, `./internal/config`: 7 tests | 31 s | |
| 105 | A replaced coordinator keeps waking sessions | `326d3b40` | `func (app *App) coordinatorContext() context.Context` | `./internal/app`: `TestCoordinatorContextEndsWithTheApp`, `TestCoordinatorContextLivesWithTheApp` | 11 s | |

Reserves: #50 (repetition guard misses spaced repeats; 5 packages and 11 tests, too wide), #65 (`/goal` missing from the menu; needs `dialog.ActionGoal` and `dialog.ActionLoop`), #78 (title generation budget; four introduced names).

Dropped and why: #16 (its package fails at the merge commit), #23 (the interface is the fix: turning generic methods into functions), and every pull request with more than four introduced names, which includes all of W0.1 to W0.3 (#96 to #116).

Package spread of the 27 (a task that grades two packages counts in both): `internal/agent` 10, `internal/ui/model` 5, `internal/ui/dialog` 4, `internal/agent/tools` and `internal/shell` 4, `internal/discord` 2, one each for `update`, `claudeagent`, `ui/common`, `log`, `app`, `config` and `agentteam`. `internal/agent` exceeds rule 9; vetting keeps six of its ten, preferring the plain ones.

Risks found in the draft:

- **Rendered output.** #93, #42 and #94 assert UI state and rendered rows. They are not golden files, but layout tests break on harmless choices. Vetting reads each assertion.
- **Weak grades.** #20 grades a helper, not the callers the problem is about. A model can pass by writing the helper alone. Keep it only if the suite is short.
- **Correlated tasks.** #61, #75 and #80 change the same function at three points in time. They count as three tasks but are not independent.
- **Interface tasks that give the design away.** #19 and #22. Prefer plain tasks when trimming.
- **Network.** None of the 27 needed it with `GOPROXY=off`. Discord tests use fakes.
- **Time.** No grade above 72 s. The full-package run (3.5) adds time: `internal/agent` is the slowest package and is measured during vetting.

### 2. Task format on disk

The suite lives in `evals/` at the repository root, a new directory, so it does not touch files that upstream changes.

```
evals/
  README.md
  footer.md                 the prompt footer (1.3)
  matrix.yaml               families and route names (5)
  tasks/
    pr-0051.yaml            one file per task
  experiments/
    2026-12-01-w8-delegation.yaml   stated before the run (4.3)
  baselines/
    2026-12-01-deepseek.json        per-task pass counts, no text
```

A task file:

```yaml
schema: 1
id: pr-0051
source:
  repo: pbtrudel/crush
  pr: 51
  merge: <full sha of the merge commit>
  base: 9530816e<full sha>          # merge^1
kind: interface                      # plain | interface
why: |
  The pull request's Why section, verbatim.
prompt: |
  The edited Why (1.3).
interface: |
  The tests call `shell.ErrBackgroundShellNotFound`, an error value
  declared with `errors.New` in `internal/shell`.
grade:
  packages: [./internal/agent/tools, ./internal/shell]
  tests: [TestJobKillUnknownShellIsNoop, TestBackgroundShellManager_KillNonExistent]
  exclude: []                        # flaky tests excluded by name, with a reason
  hidden:                            # files written at grading time
    - {path: internal/agent/tools/job_test.go, blob: <blob sha at merge>}
    - {path: internal/shell/background_test.go, blob: <blob sha at merge>}
  deleted: []                        # test files the pull request removed
  timeout: 10m
limits:
  max_steps: 120
  timeout: 25m
vetted:
  date: 2026-11-25
  go: go1.27.2
  base_fails: 5/5
  merge_passes: 5/5
  grade_seconds: {touched: 16, packages: 41}
risks: []
```

Choices:

- **Hidden tests are referenced by blob, not copied.** The runner reads them with `git cat-file blob <sha>` from the repository that holds the suite and checks the hash. The suite directory then holds no test code, so a model that searches the disk for the problem does not find the answer in `evals/`. This hides nothing from a model that runs `git cat-file` on this repository; 3.7 detects that.
- **The prompt sent is `prompt`, then `interface` when present, then `footer.md`.** The runner never sends `why`.
- **Limits live in the task** with suite defaults (120 steps, 25 minutes). After the baseline they are set to about twice the 90th percentile of steps and time of passing DeepSeek runs, then frozen for the suite version.
- **Suite version** is the git tree hash of `evals/tasks` and `evals/footer.md`. Every result line carries it, and two batches are compared only when it matches.

### 3. The runner

#### 3.1 Form

A Go program in this repository: `cmd/crush-eval/main.go` with its code in `internal/eval/`. Not a `crush eval` subcommand and not a script.

- **Not a subcommand.** It is a developer tool that would ship in every user's binary, and registering it edits `internal/cmd/root.go`, which upstream changes often. A separate `main` package adds files only.
- **Not a shell script.** The runner has to parse JSON, hold a resumable JSONL log, schedule work per route under rate limits, retry by exit code and compute paired statistics. Each is a few lines of Go with tests and a page of fragile shell.
- **Go, in this module.** It builds with the repository's toolchain and is tested by `go test ./...`. It imports only `internal/eval/...` and the standard library plus `gopkg.in/yaml.v3`, which `go.mod` already requires. It does not import Crush's agent or config packages: it treats `crush` as a black box, so the build under test and the build that runs the suite can differ.

Subcommands: `candidates`, `vet`, `prepare`, `grade`, `run`, `batch`, `compare`, `plan`, `login`. Sections 1.4, 3 and 4 describe them.

#### 3.2 Checkout

Each run gets a fresh directory under the eval state directory (3.3), not under the repository and not under `/tmp`.

1. `git archive <base> | tar -x` into `<run>/src`.
2. `git init`, `git add -A`, one commit named "task base" with a fixed author and date, so the model has a clean `git status` and `git diff`, and the history holds nothing after the base. Earlier history is also absent. That costs the model `git log` and `git blame`, and is the price of a checkout that cannot show the answer (Evidence 6).
3. The repository's tracked `crush.json` stays. It is part of the code at that commit, and it is the same for both variants of a pair. Its blob hash goes into the result.

After the run the directory is kept for failed runs until the batch is compared, and removed for passing runs.

#### 3.3 Isolation and routes

The run must not read the user's configuration, skills, MCP servers, hooks or context files, and the user's secrets must not enter the repository or the results.

The eval home is `$XDG_STATE_HOME/crush-eval` (default `~/.local/state/crush-eval`), owned by the user, mode 0700:

```
crush-eval/
  routes/<name>.yaml        written by the user, never in the repository
  data/                     CRUSH_GLOBAL_DATA: tokens from `crush-eval login`
  config/crush/crush.json   generated per batch (CRUSH_GLOBAL_CONFIG)
  skills/                   empty (CRUSH_SKILLS_DIR)
  gh/                       empty (GH_CONFIG_DIR)
  bin/<sha>/crush           built variants
  batches/<id>/results.jsonl
  batches/<id>/runs/<run>/{src,data,stdout.json,stderr.log,grade.json}
```

A route file:

```yaml
name: deepseek-flash
family: deepseek
provider: <provider id as Crush names it>
model: deepseek-v4.1-flash
small_model: deepseek-v4.1-flash
reasoning_effort: ""
provider_config:                 # merged into the generated config
  api_key: "$DEEPSEEK_API_KEY"   # Crush resolves it from the environment
env: [DEEPSEEK_API_KEY]          # passed through to the child, nothing else
concurrency: 3
limits: {runs_per_window: 0, window: 0}   # 0 means none
```

How credentials reach the child without being copied:

- **API keys** stay in the user's environment. The route names the variables; the generated config refers to them as `$NAME`, which Crush resolves (`internal/config/resolve.go:88`). The generated file and the results hold only the name.
- **OAuth routes** (Copilot) sign in once with `crush-eval login <route>`, which runs `crush login` with `CRUSH_GLOBAL_DATA` set to the eval home. The token then lives in the eval home's data file and is never copied from the user's own. Refreshes write there too. Two runs that refresh at once could race on that file; the route's concurrency bounds it, and a failed refresh ends as `provider_error`, which is retried (3.6).
- **Claude Agent** uses the `claude` CLI's own sign-in. The CLI also reads the user's `~/.claude` settings and instructions, which would leak into the run. The route sets `CLAUDE_CONFIG_DIR` to a directory in the eval home and signs in there once. Whether the CLI honors that variable for every file it reads is checked before Claude enters the matrix (5.2).

The generated global config holds the route's provider block and these options, identical for every variant unless the variant's overlay changes one: `disable_metrics: true`, `disable_provider_auto_update: true`, `disabled_tools: [fetch, agentic_fetch, download, sourcegraph, web_search, web_fetch]`, no MCP servers, no hooks. The child environment is built from scratch: `PATH`, `HOME`, `LANG`, `GOCACHE`, `GOMODCACHE`, the route's variables, `CRUSH_GLOBAL_CONFIG`, `CRUSH_GLOBAL_DATA`, `CRUSH_SKILLS_DIR`, `CRUSH_DISABLE_METRICS=1`, `GH_CONFIG_DIR`, `GOPROXY=off`, `GOFLAGS=-buildvcs=false`, `GOTOOLCHAIN=local`. `CRUSH_CLIENT_SERVER` is never set, which settles the point the measurement design left open; `GH_TOKEN` and `GITHUB_TOKEN` are never passed.

Global context files resolve to `crush-eval/config/CRUSH.md` and `crush-eval/AGENTS.md`, which do not exist, so only the checkout's own `AGENTS.md` reaches the model, as it would for a user of this repository. `/etc` system config is read by Crush as always; the runner records whether one exists.

#### 3.4 One run

```
crush --cwd <run>/src --data-dir <run>/data run --json --quiet \
  -m <provider>/<model> --small-model <provider>/<small> [--reasoning-effort E] \
  --max-steps <task.limits.max_steps> --timeout <task.limits.timeout> \
  < prompt
```

The prompt goes on standard input. `--data-dir` keeps the session database out of the checkout. A watchdog kills the process five minutes after `--timeout`; a killed run is recorded as `watchdog` and treated like exit 3. Standard output must be one JSON object of `schema: 1`; anything else is exit 3 with `bad_output`.

#### 3.5 Grading

1. Remove `_test.go` files the model added in the graded packages. They are the model's own, and a duplicate test name would break the build of the hidden ones. Their count is recorded. Test files the model changed elsewhere stay.
2. Write the hidden files and delete the `deleted` ones.
3. `go test -count=1 -json -timeout <grade.timeout> -run '^(<tests>)$' <packages>`: the touched tests. All must pass.
4. `go test -count=1 -json -timeout <grade.timeout> -skip '^(<exclude>)$' <packages>`: every test of the graded packages, which catches a fix that breaks a neighbor. All must pass.
5. The grade passes when both pass. A build failure is a failed grade with `compile_error`.

Grading runs in the checkout after the model has exited, so nothing the model can do reaches the hidden files in time. The working tree is graded as it is, whatever HEAD points to. Work left in another worktree or branch counts as not done; the runner records whether the model created any (`git worktree list`, `git branch`), because the repository's own instructions and Agent Teams can move work into a worktree.

`crush-eval grade --reference <task>` grades the merge commit's tree the same way. `vet` uses it, and each batch grades every task's reference once before its runs, so a change in the machine (toolchain, cache) shows as a failing reference instead of a drop in pass rate.

#### 3.6 Results, resume and retries

One JSON line per attempt in `results.jsonl`:

```json
{"schema":1,"batch":"…","suite":"<tree hash>","task":"pr-0051","variant":"main@3f18401c",
 "route":"deepseek-flash","family":"deepseek","rep":2,"attempt":1,
 "started":"…","exit":0,"reason":"completed","build":"…","model":"…","provider":"…",
 "steps":38,"tool_calls":71,"tokens":{"input":…,"cache_read":…,"cache_write":…,"output":…},
 "estimated":false,"cost":0.0,"duration_ms":412000,"undelivered_reports":0,
 "turn_ends":{"completed":2},"grade":{"touched":true,"packages":true,"compile_error":false,
 "model_tests_removed":0},"pass":true,"false_completion":false,
 "diff":{"files":3,"added":41,"deleted":6},"worktrees":0,"contaminated":false,"project_config":"<blob>"}
```

No prompt, answer, file content or path from the session goes into the line. The run's `text` is dropped; its length is kept.

The run key is (batch, task, variant, route, rep). On start, `batch` reads the file and skips every key that has a final line. Exit 0 and 2 are final. Exit 1, 3, `watchdog` and `bad_output` are retried up to two more times, after a pause; a key that is still not final after three attempts is final as `invalid`.

Rate caps: a route's `concurrency` bounds its live runs, and `runs_per_window` bounds its starts per window. When an exit-3 run's `error` carries a usage-limit message (the log already holds "You've hit your 5-hour limit"), the route pauses for its window and the attempt does not count toward the three. The machine bounds graders separately: at most three `go test` grades at once on 12 cores, and at most six live runs in total, because each model run also builds and tests.

Order: runs are interleaved by task and repetition, alternating variants (task 1 rep 1 A, task 1 rep 1 B, task 2 rep 1 A, …), so a provider that slows or degrades during a batch affects both variants alike. A batch is started with a fixed seed that shuffles task order, recorded in the batch.

#### 3.7 Contamination check

After each run the runner opens the run's own session database (in `<run>/data`, never the user's) read-only and scans tool-call inputs for: the task's merge sha, `pbtrudel/crush/pull`, `github.com/pbtrudel/crush`, `gh pr`, `gh api`, the suite path, the repository's absolute path, and `git cat-file`. A match sets `contaminated: true` with a count; no text is kept. Contaminated runs are reported and excluded from comparisons. A rate above 2% of a batch stops the comparison until the cause is found.

### 4. Scoring and comparison

#### 4.1 Per run

- **Pass**: exit 0 or 2, and the grade passes.
- **False completion**: exit 0 (`reason: completed`, the model ended on its own), and the grade fails. A completed run whose session left teammate reports unread (`undelivered_reports > 0`) still ended on its own and counts; it is flagged, which settles the second point the measurement design left open.
- **Stopped short**: exit 2. Graded like any run, never a false completion.
- **Invalid**: final after three attempts without exit 0 or 2. Excluded from rates and counted; above 5% of a batch the comparison is void.

#### 4.2 Per variant and route

Pass rate (passes over valid runs), false-completion rate (over valid runs, and over completed runs), stopped-short rate, and the medians and totals of `tree.steps`, `tree.tool_calls`, tokens by kind and duration. Cost is shown only when every run of the route has a non-zero, non-estimated cost; tokens are the figure compared across models (measurement design, 3.2).

#### 4.3 Paired comparison and the margin rule

Every comparison is stated before it runs, in `evals/experiments/<date>-<name>.yaml`, committed before the batch starts: variants, routes, repetitions, primary metric, the margin for pass rate, and the smallest effect it can detect, with the baseline used to compute them. `crush-eval plan` fills in the last two.

The unit is the task. For task t, each variant's score is its mean over repetitions, and d_t is the difference (variant minus control). The estimate is the mean of d_t, with a 95% interval from a paired bootstrap that resamples tasks (10,000 draws, fixed seed). When families are pooled, the unit is (task, route).

Rules, from the roadmap:

1. The primary result is the metric the change targets (pass rate, false completions, steps or tokens).
2. Pass rate is a guard with a margin M. The variant passes the guard when the lower bound of the pass-rate interval is above minus M.
3. M is stated in the experiment file, and must be at least the smallest detectable effect from the baseline. A change whose expected effect is below that needs more tasks, more repetitions or pooled routes, stated in the file.
4. Control runs are re-run in every batch, interleaved with the variant. The baseline is used to plan margins, not as a control.

How `plan` computes the detectable effect. From the baseline, each task has a pass probability p_t. With R repetitions per variant and T tasks, the standard error of the mean paired difference is about sqrt(Σ 2·p_t(1−p_t)/R)/T. Tasks that always pass or always fail contribute nothing, which is how pairing narrows the roadmap's unpaired 8 points. The detectable effect at 80% power and a two-sided 5% test is about 2.8 times that error. `plan` prints both for the routes and repetitions in the file. The formula assumes the repetitions of one task are independent; the A/A batch (4.4) checks it.

#### 4.4 Baseline

Collected once the corrections of roadmap step 2 have landed, before any behavior change.

1. **A/A on DeepSeek V4.1 Flash**: the same build as both variants, 3 repetitions each, all tasks: 162 runs at 27 tasks. Its interval must contain zero; if it does not, the runner or the tasks are not stable enough and the suite is fixed first. It also gives p_t per task.
2. **Limits**: task limits are set from these runs (2) and frozen.
3. **One full matrix run** of the same build on every route that has entered the matrix, 3 repetitions: per-family p_t, so family-specific changes can be planned.

The per-task pass counts are committed in `evals/baselines/` (counts only).

### 5. Model matrix

#### 5.1 Families and what runs when

Decision 1 names seven families: DeepSeek, GLM, Kimi, GPT, Claude, MiMo and Qwen. `evals/matrix.yaml` maps each to one route name, with no secrets:

```yaml
families:
  deepseek: {route: deepseek-flash, entered: 2026-12-01}
  glm:      {route: <name>, entered: null}
  ...
```

| When | Routes | Runs at 27 tasks, 3 repetitions |
|---|---|---|
| A behavior change or experiment, judged on the suite | DeepSeek V4.1 Flash, control and variant | 162 |
| A change aimed at one family (W1.2, W1.3, W7.1) | that family first, then DeepSeek as the guard | 162 + 162 |
| Acceptance of a change for its families | all seven routes | 1,134 |

Corrections verified by tests (roadmap step 2) do not run the suite. Families that receive the same variant are pooled for the pass-rate guard; a decision for one family alone states the extra tasks or repetitions it needs in its experiment file.

#### 5.2 How a family enters

1. The owner names a route: provider, model id, reasoning effort, and its usage limits from the provider's terms.
2. `crush-eval login` if the route uses OAuth.
3. Entry checks on two tasks: one run ends `completed`, one with `--max-steps 3` ends `step_limit`, one with `--timeout 1m` ends `time_limit`, and the result line has non-zero tokens. These are the live checks of the measurement design, repeated per route.
4. Route-specific checks:
   - **GPT** runs on Copilot, the only provider configured here whose catalog types GPT models as `responses`, once a captured request confirms that API is in use (roadmap W1.2). The route must list no model entry that drops the catalog type (roadmap W1.2, `internal/config/load.go:516-543`).
   - **Claude** runs through the Claude Agent provider. The CLI owns the turn loop, so the entry check also confirms that the step and time limits stop it, and that `CLAUDE_CONFIG_DIR` keeps the user's `~/.claude` out. W3 and W9 results are reported separately for this route.
   - **Subscription routes** record their window and cap. A full acceptance batch (162 runs per route) must fit the cap within a stated number of days, or the route runs fewer repetitions and says so.
5. The family is recorded in `matrix.yaml` with its entry date, and gets a baseline (4.4, step 3) before its first comparison.

### 6. Scope and risks

#### 6.1 Out of scope

- Public benchmarks (Decision 3).
- Tasks graded by anything but Go tests: UI golden files, a model as judge, manual review.
- A network sandbox. The design disables Crush's web tools, blocks `gh` credentials and sets `GOPROXY=off`, and detects contact with the answer (3.7). It does not stop a shell command from reaching the network.
- Running the suite in CI. It costs money and needs subscriptions.
- The client/server mode, Windows and macOS.
- Interactive features (TUI, Discord) beyond what their unit tests grade.

#### 6.2 Risks

- **Contamination.** A model may have seen these pull requests: the fork is public. Fork pull requests are from October and November 2026, newer than many training cutoffs; upstream commits are older and public longer, which is one reason they are the reserve pool. Paired comparisons cancel a constant advantage, so the risk is to absolute pass rates, which are not comparable to other benchmarks or across families. The run itself is checked for contact with the answer (3.7).
- **Bias toward solved tasks.** Every task was solved with this harness and a person steering, most often by the models this harness is tuned for. The suite measures changes, not how capable the harness is in general. Package spread is skewed toward `internal/agent` and the UI, which rule 9 limits.
- **Interface tasks are easier.** Naming a function tells the model where the change goes. Results are reported for plain and interface tasks separately as well as together.
- **Flaky tests.** Vetting needs 5 of 5 at the parent (failing) and the merge (passing). Every batch grades the references first (3.5). A task whose reference fails twice in different batches is quarantined until fixed.
- **Grades that test a helper.** A model can pass by writing the named helper without using it (#20). Vetting reads the assertions; weak tasks go first when trimming.
- **Model-written tests.** Removing the model's new test files in graded packages before grading is necessary for the build, and it hides nothing the grade relies on.
- **Work outside the checkout.** Agent Teams and the repository's instructions can move work into another worktree or ask for a commit and pull request. The run's working tree is what is graded; the worktree count is recorded, and a high count is a finding about the harness, which is what W8 measures.
- **Provider drift.** Providers change models and quantization without notice. Interleaving (3.6) and re-running the control in every batch keep a pair on the same provider state.
- **Small suite.** With 27 tasks one model's pass rate moves 3.7 points per task. Single-model decisions under the detectable effect need more repetitions or tasks; `plan` says which.
- **Project config.** Each checkout loads the tracked `crush.json` of its base commit. It is the same for both variants, and its hash is recorded, so a change in it between tasks is visible.

#### 6.3 Decisions needed

1. **Upstream commits.** Decision 3 says "this repository's pull requests only". Upstream pull requests are in this repository's history but were not opened here. The design uses them only if the fork pool falls below 20 tasks after vetting and the owner agrees.
2. **Route per family.** Six of the seven routes are not named yet (5.2).
3. **Interface tasks in the pass rate.** The design counts them, and reports the split. The alternative is to keep only plain tasks, which today gives 13.

### 7. Implementation

Each pull request is at most about 400 changed lines, tests included, except the task-data pull requests, which are YAML.

1. **Task format.** `internal/eval/task`: schema, loader, validation (shas resolve, blobs exist, tests named, limits set), suite version hash. `evals/README.md`, `evals/footer.md`, two tasks (#10, #51).
2. **Checkout and grade.** `internal/eval/checkout` (archive, single commit) and `internal/eval/grade` (remove model tests, write hidden blobs, the two `go test -json` passes, parse). `crush-eval prepare`, `crush-eval grade --reference`.
3. **Candidates and vetting.** `crush-eval candidates` (1.4, steps 1 and 2) and `crush-eval vet` (rules 5 to 7, five repetitions, timings written back).
4. **Isolation and routes.** Eval home, route files, the generated config, the scrubbed environment, `crush-eval login`, building a variant's binary from a ref into `bin/<sha>`.
5. **One run.** `crush-eval run <task> --variant --route`: the command line of 3.4, the watchdog, the result line, the contamination scan.
6. **Batches.** `crush-eval batch <file>`: the matrix, interleaving with a seed, resume, retries, per-route concurrency and caps, the reference grades first.
7. **Scores.** `crush-eval compare` (4.1 to 4.3, bootstrap, split by plain and interface) and `crush-eval plan` (detectable effect from a baseline).
8. **Tasks, three pull requests.** About nine tasks each from the draft in 1.5, each with its `vet` output in the pull request description and a second person's read of every prompt.
9. **Baseline.** No code: the A/A batch, the limits, the committed baseline counts (4.4).

Pull requests 1 to 3 need no model and no money. Pull request 5 is the first that runs a model; its live check is one DeepSeek run per exit code.

## Alternatives considered

1. **A `git worktree` per run.** What the roadmap says. Rejected: the worktree shares refs and objects, so the merge commit, its tests and its fix are one `git log --all` away (Evidence 6). A single-commit repository built from `git archive` keeps the throwaway checkout and drops the leak.
2. **A shallow clone of the base.** Keeps some history. Rejected for now: fetching a commit by sha from a local mirror depends on server settings, and the depth that is useful to a model is unknown. It can be a variant later.
3. **Hidden tests copied into `evals/`.** Simpler to read. Rejected: they would sit in plain text in every checkout of the repository that a model might search. Blob references keep the suite free of answer code.
4. **Grade only the touched tests.** Faster. Rejected: a fix that breaks a neighbor in the same package would pass.
5. **Keep every task that compiles, drop interface tasks.** Cleaner. Rejected as the default: it leaves 13 tasks, below the roadmap's 20; offered as Decision needed 3.
6. **A `crush eval` subcommand.** Rejected in 3.1.
7. **Compare against the stored baseline instead of a fresh control.** Half the runs. Rejected: providers drift between batches, and pairing only holds within one window.

## Noticed, not changed here

- `gh` is not on the shell's block list, while `curl` and `wget` are (`internal/agent/tools/bash.go:90`, `:103`). For the suite that is handled by withholding credentials.
- The touched-tests list of #105 includes `TestStaleWatcherReportsMissingFiles`, which the diff changed for an unrelated reason. Task authors check that each touched test belongs to the problem.
