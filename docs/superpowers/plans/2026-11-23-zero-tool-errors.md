# Zero Tool Errors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove every tool error that Crush itself causes, and turn the
guard and external errors into failures that fix themselves in one step
instead of repeating.

**Architecture:** One pull request (user ruling, 2026-11-23) in five parts,
one per area: tool inputs, the fetch subagent and web tools, the edit
guards, images, and the coordination tools. Each fix changes the tool that
produced the error. No new subsystems are
added; two new helpers (`describeCommand`, `shellReadPaths`) and one new
file (`image_fit.go`) are the only new units.

**Tech Stack:** Go, `charm.land/fantasy` v0.48.1 (tool schema and required
checks in `schema/schema.go:138-169` and `agent.go:1203-1215`),
`mvdan.cc/sh/v3` (already the shell), `github.com/disintegration/imaging`
(already a direct dependency, `go.mod:42`).

**Spec:** this document. The evidence comes from the session database
`.crush/crush.db`, window 2026-11-01 to 2026-11-23, gathered by
`/mnt/data/crush/scratch/errors-agg.py` and the research notes
`/mnt/data/crush/scratch/task-213-report.md` (edit tools),
`/tmp/crush-model-intel/task-216.md` (images, team, LSP, turn ends), and the
task-214 and task-215 reports summarized below.

## What "zero" can mean

Of 52,973 tool calls since Nov 1, about 950 failed (1.8%). They fall into
three classes, and each class gets a different target:

1. **Crush bugs and schema mismatches** (about 330 errors). The tool asks
   for something its own description does not mention, or Crush deletes a
   directory a running subagent still uses. Target: zero.
2. **Guards** (about 350 errors): "read before editing", "modified since
   read", task revision checks. A guard stops something unsafe, so it stays.
   On 2026-11-22 the `edit_without_read` bypass was measured on DeepSeek
   (`evals/experiments/2026-11-21-w21-edit-default-results.md`): pass rate
   fell by 7.4 points, interval [-12.5, -2.8]. So this plan does not remove
   or bypass any guard. Target: the guard sees the reads that really
   happened (bash `cat`, `sed -n`), and a refusal carries what the model
   needs to retry at once, so it never refuses twice.
3. **External or user actions** (about 270 errors): DuckDuckGo blocking
   this machine, sites returning 404, the Claude 5-hour quota, invalid API
   keys, the user pressing cancel. Crush cannot make these zero. Target: no
   repeats (a blocked search is tried once, not 74 times), a message that
   names the next step, and no error at all where the outcome is a normal
   answer (a missing glob root means "no files").

## Error inventory and owner task

| Error (since Nov 1) | Count | Class | Task | Expected after |
|---|---|---|---|---|
| bash "missing required parameter: description" | 148 | 1 | 1 | 0 |
| job_output "missing required parameter: wait" | 6 | 1 | 2 | 0 |
| job_output "background shell not found" | 124 | 1 | already fixed by 997003c2c (Nov 6), 0 since Nov 7 | 0 |
| multiedit `edits` sent as a JSON string | 5 | 1 | 3 | 0 |
| multiedit "only the first edit can have empty old_string" (later edit had null) | 3 | 1 | 3 | 0 |
| `todo` instead of `todos`, todos sent as a string | 2 | 1 | 4 | 0 |
| question: string with a trailing comma | 2 | 1 | 4 | 0 |
| web_fetch "Failed to create temporary file" | 13 | 1 | 5 | 0 |
| read/view "File not found" inside a deleted fetch dir | 4 | 1 | 5 | 0 |
| glob "fastwalk error: stat ... no such file" | 5 | 1 | 6 | 0 (answer "No files found") |
| read/view "File not found" for moved or deleted paths | 8 | 3 | 6 | fewer: nearest existing folder suggested |
| web_search "DuckDuckGo is rate-limiting" | 74 | 3 | 7 | at most 1 per blocked period |
| web_fetch status 404/429/503/202/403 | 44 | 3 | 8 | fewer: 202 from DDG folded into Task 7, others get a next step |
| agentic_fetch URL 404 aborts the subagent | 1 | 3 | 8 | 0 (falls back to search) |
| edit/multiedit/write "must read the file first" | 237 | 2 | 9, 10 | about 0 for bash-inspected files (163 of 237); one try for the rest |
| edit/multiedit/write "modified since last read" | 110 | 2 | 10, 11 | one try; self-caused changes announced by bash |
| edit "old_string not found" / "appears multiple times" | 19 | 2 | 10 | one try (refusal shows the current text) |
| edit "new content is the same as old content" | 6 | 2 | 12 | 3 of 6 become a non-error "already applied" |
| read/view "Image file is too large" | 31 | 1 | 13 | 0 |
| team_task_* stale/not found/blocked/owned/not ready | 40 | 2 | 14 | one try (error carries the current state) |
| team_task_create absolute write scope | 5 (+4 by the lead today) | 1 | 14 | 0 (relativized when inside the workspace) |
| agent_status/stop/send on a teammate name | 11 | 1 | 15 | 0 |
| send_message to a subagent label | 1 | 1 | 15 | 0 |
| lsp_symbols/lsp_replace_symbol "no LSP client" | 6 | 3 | 16 | same count, message says why |
| read_mcp_resource `crush://skills/...` | 2 | 1 | 16 | 0 |
| turn end "context deadline exceeded" | 9 | 3 | 17 | same count, labelled as a timeout |
| turn end Claude 5-hour quota | 22 | 3 | not in this plan, see "Out of scope" | 22 |
| turn end invalid API key, credits, capacity | 5 | 3 | none: rotate the key / top up | 5 |
| user cancel, interrupted turns, settle.go placeholders | 47 | user | none | unchanged |

## Global Constraints

- Go module `github.com/charmbracelet/crush`; build with `CGO_ENABLED=0`.
- No new dependencies. `imaging` and `golang.org/x/image` are already in
  `go.mod`; `mvdan.cc/sh/v3/syntax` is already used by `internal/shell`.
- Format with `gofumpt -w` on every changed Go file.
- Tests use testify `require`, `t.Parallel()` where the package already does,
  and `t.TempDir()`.
- Log messages start with a capital letter (`./scripts/check_log_capitalization.sh`).
- Comments on their own line start with a capital and end with a period,
  wrapped at 78 columns.
- No em dashes in any added line.
- Semantic commit subjects (`fix:`, `feat:`, `perf:`), no attribution trailers.
- No guard is removed or bypassed: the read guard, the modified-since-read
  guard and the team task CAS keep refusing what they refuse today, except
  where this plan proves the model did see the current content.
- Every pull request: `go test ./...`, `go test -race -count=1 ./internal/agent/`,
  golangci-lint with `.golangci.yml`, `GOOS=windows go build ./...`,
  `GOOS=darwin go build ./...`, `git diff --check`, 0 open code-scanning
  alerts before merging.
- One pull request for all five parts (user ruling); the 400-line target
  is waived for this change.
- No task-suite batch for Part C (user ruling); its tests are the gate.

## Review Focus

1. A bash command that both prints and rewrites a file (`sed -n p f > f`,
   `cat f | tee f`, `sed -i`) must not count as a read: Task 9 counts only
   commands that cannot write, and its tests pin `sed -i` and redirections.
2. A fetch subagent that receives an `agent_send` follow-up after its first
   run finished must still be able to save pages: Task 5 makes web_fetch
   recreate its folder, and its test runs web_fetch after the folder is gone.
3. A refusal that returns file content must respect the same size limits
   as view, or a 3 MB file floods the context: Task 10 caps it at view's
   default window and pins that with a large-file test.
4. Downscaling a screenshot must keep it readable and must never return
   something larger than the cap: Task 13 loops until it fits and errors only
   when decoding fails, pinned by a test that checks the output size.
5. Relativizing an absolute write scope must reject paths outside the
   workspace exactly as today: Task 14 pins `/mnt/data/...` staying an error.

---

## Part A: Tool inputs match their descriptions

Tasks 1 to 4. About 250 lines.

### Task 1: bash works without a description

**Why:** 148 bash calls failed because `description` is required by the
struct tag (`internal/agent/tools/bash.go:27`, checked by fantasy at
`agent.go:1211`), while `bash.md.tpl:19` says "Command required,
working_dir optional". 144 came from Sonnet in subagent sessions, where the
coder prompt's own note is absent. 118 of 144 retried with it, so the field
carries no information the model would not give.

**Files:**
- Modify: `internal/agent/tools/bash.go:27` and the start of the tool's run
  function (the block that reads `params.Command` before the permission
  request at `bash.go:326`).
- Modify: `internal/agent/tools/bash.md.tpl:19`.
- Test: `internal/agent/tools/bash_test.go`.

**Interfaces:**
- Produces: `func describeCommand(command string) string`, unexported, in
  `bash.go`.

- [ ] **Step 1: Write the failing tests**

```go
func TestBashDescriptionIsOptional(t *testing.T) {
	t.Parallel()
	tool := newBashToolForTest(t) // Reuse the constructor setup of the existing bash tests in this file.
	require.NotContains(t, tool.Info().Required, "description")
	require.Contains(t, tool.Info().Required, "command")
}

func TestDescribeCommand(t *testing.T) {
	t.Parallel()
	require.Equal(t, "go test ./...", describeCommand("go test ./..."))
	require.Equal(t, "echo one", describeCommand("echo one\necho two"))
	long := strings.Repeat("a", 100)
	require.Equal(t, strings.Repeat("a", 57)+"...", describeCommand(long))
	require.Equal(t, "", describeCommand("   "))
}
```

If no constructor helper exists in `bash_test.go`, write `newBashToolForTest`
from the setup the first existing bash tool test uses (permission mock
`mockBashPermissionService`, `t.TempDir()` as the working directory).

- [ ] **Step 2: Run and confirm both fail**

Run: `go test ./internal/agent/tools/ -run 'TestBashDescriptionIsOptional|TestDescribeCommand'`
Expected: FAIL, `Required` contains "description"; `describeCommand` undefined.

- [ ] **Step 3: Implement**

In `bash.go:27` change the tag to `json:"description,omitempty"`. Add:

```go
// describeCommand derives a short label from the command for calls that
// leave description out. The label is only shown in the UI and the job
// list; permission prompts already show the command itself.
func describeCommand(command string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(command), "\n")
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) <= 60 {
		return line
	}
	return string([]rune(line)[:57]) + "..."
}
```

At the start of the run function, before anything reads `params.Description`:

```go
if strings.TrimSpace(params.Description) == "" {
	params.Description = describeCommand(params.Command)
}
```

In `bash.md.tpl:19` replace the line with:

```
- command is required; description (a label under 30 characters) is recommended; working_dir is optional
```

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/agent/tools/ ./internal/agent/`
Expected: PASS. If a golden file of the rendered bash description changes,
regenerate it with `-update` and check the diff is only line 19.

- [ ] **Step 5: Commit** `fix: let bash calls leave out the description`

### Task 2: job_output works without wait

**Why:** 6 calls failed: `Wait bool` is required by its tag
(`tools/job_output.go:22`) but `job_output.md` never says so. A missing
boolean already means false everywhere else.

**Files:**
- Modify: `internal/agent/tools/job_output.go:22`, `internal/agent/tools/job_output.md`.
- Test: `internal/agent/tools/job_test.go`.

- [ ] **Step 1: Failing test**

```go
func TestJobOutputWaitIsOptional(t *testing.T) {
	t.Parallel()
	tool := NewJobOutputTool() // Use the constructor the existing tests in this file use.
	require.Equal(t, []string{"shell_id"}, tool.Info().Required)
}
```

- [ ] **Step 2: Run, expect FAIL** (`[shell_id wait]`).
- [ ] **Step 3: Implement.** Tag becomes `json:"wait,omitempty"`. Add to
  `job_output.md` the line `- wait defaults to false; pass true only to block until the job finishes`.
- [ ] **Step 4: Run** `go test ./internal/agent/tools/ -run JobOutput`; PASS.
- [ ] **Step 5: Commit** `fix: make job_output wait optional`

### Task 3: multiedit accepts edits sent as a string and names a null old_string

**Why:** 5 calls sent `edits` as a JSON-encoded string; fantasy's unmarshal
fails before the tool runs. 3 calls had `old_string: null` in a later edit
and got a message about file creation (`multiedit.go:127`).

**Files:**
- Modify: `internal/agent/tools/multiedit.go` (the `MultiEditParams.Edits`
  field type and `validateEdits` at `multiedit.go:122-130`).
- Test: `internal/agent/tools/multiedit_test.go`.

**Interfaces:**
- Produces: `type multiEditList []MultiEditOperation` with
  `UnmarshalJSON`. `MultiEditParams.Edits` becomes `multiEditList`; every
  existing `range params.Edits` and `len(params.Edits)` keeps working.

- [ ] **Step 1: Failing tests**

```go
func TestMultiEditListAcceptsStringEncodedArray(t *testing.T) {
	t.Parallel()
	var params MultiEditParams
	input := `{"file_path":"a.go","edits":"[{\"old_string\":\"x\",\"new_string\":\"y\"}]"}`
	require.NoError(t, json.Unmarshal([]byte(input), &params))
	require.Len(t, params.Edits, 1)
	require.Equal(t, "x", params.Edits[0].OldString)
}

func TestValidateEditsNamesEmptyOldString(t *testing.T) {
	t.Parallel()
	err := validateEdits(multiEditList{{OldString: "a", NewString: "b"}, {OldString: "", NewString: "c"}})
	require.EqualError(t, err, "edit 2: old_string is empty; every edit after the first must quote the exact text it replaces (only edit 1 may be empty, to create the file)")
}
```

Also check the schema still says array:
`require.Equal(t, "array", NewMultiEditTool(...).Info().Parameters["edits"].(map[string]any)["type"])`
(build the tool as the existing multiedit tests do).

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement**

```go
// multiEditList is the edits array. Some models send it as a JSON string
// holding the array; decoding that too saves a failed call and a retry.
type multiEditList []MultiEditOperation

func (l *multiEditList) UnmarshalJSON(data []byte) error {
	var edits []MultiEditOperation
	if err := json.Unmarshal(data, &edits); err == nil {
		*l = edits
		return nil
	}
	var encoded string
	if err := json.Unmarshal(data, &encoded); err != nil {
		return fmt.Errorf("edits must be an array of {old_string, new_string}: %w", err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(encoded)), &edits); err != nil {
		return fmt.Errorf("edits must be an array of {old_string, new_string}: %w", err)
	}
	*l = edits
	return nil
}
```

Replace the error in `validateEdits` with the message in the test. If the
schema test fails because fantasy's generator does not see a named slice as
an array, keep `Edits []MultiEditOperation` and instead give
`MultiEditParams` an `UnmarshalJSON` that decodes through an alias struct
with `Edits json.RawMessage`, the same pattern as `question.go:30-55`.

- [ ] **Step 4: Run** `go test ./internal/agent/tools/ -run MultiEdit`; PASS.
- [ ] **Step 5: Commit** `fix: accept multiedit edits sent as a string`

### Task 4: repair `todo`, string todos and trailing commas

**Why:** `todo` is not a tool, so fantasy fails the call (`agent.go:1199`).
Crush wires no `RepairToolCall` (`fantasy agent.go:190`). Registering a
second tool would add a schema to every request; a repair costs nothing
until it fires. The same calls sent `todos` as a string. Two question calls
sent a string array with a trailing comma (`question.go:44-53`).

**Files:**
- Create: `internal/agent/tool_repair.go`, `internal/agent/tool_repair_test.go`.
- Modify: the `fantasy.AgentStreamCall` (or `fantasy.NewAgent` options) built
  in `internal/agent/agent.go` `Run` near the `PrepareStep` field at
  `agent.go:1557`: add `RepairToolCall: repairToolCall`.
- Modify: `internal/agent/tools/todos.go:17-19` (params decoding),
  `internal/agent/tools/question.go:44-53`.
- Modify: `internal/agent/parallel_reads.go:38-43` comment: the gate still
  trusts the announced name; a repaired name is never a read tool, so a
  repaired step stays sequential.

**Interfaces:**
- Produces: `func repairToolCall(ctx context.Context, opts fantasy.ToolCallRepairOptions) (*fantasy.ToolCallContent, error)`
  and `var toolNameAliases = map[string]string{"todo": tools.TodosToolName}`.
- Produces: `func trimTrailingCommas(s string) string` in `tools`, used by
  todos and question.

- [ ] **Step 1: Read the repair contract.** Open
  `/home/pbtrudel/go/pkg/mod/charm.land/fantasy@v0.48.1/agent.go` around
  lines 133 and 1180-1200 and note the fields of `ToolCallRepairOptions` and
  when the function is called (unknown tool, invalid input). Write the
  answer in the test file's top comment.

- [ ] **Step 2: Failing tests**

```go
func TestRepairToolCallRenamesKnownAlias(t *testing.T) {
	t.Parallel()
	opts := fantasy.ToolCallRepairOptions{ /* Fill from Step 1: original call named "todo". */ }
	repaired, err := repairToolCall(t.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, tools.TodosToolName, repaired.ToolName)
}

func TestRepairToolCallLeavesOtherNamesAlone(t *testing.T) {
	t.Parallel()
	opts := fantasy.ToolCallRepairOptions{ /* Original call named "rm_rf". */ }
	repaired, err := repairToolCall(t.Context(), opts)
	require.NoError(t, err)
	require.Nil(t, repaired)
}
```

In `tools`:

```go
func TestTrimTrailingCommas(t *testing.T) {
	t.Parallel()
	require.Equal(t, `[{"a":1}]`, trimTrailingCommas(`[{"a":1},]`))
	require.Equal(t, `{"a":[1,2]}`, trimTrailingCommas(`{"a":[1,2,],}`))
	require.Equal(t, `["x,]"]`, trimTrailingCommas(`["x,]"]`))
}
```

plus a todos test that decodes `{"todos":"[{\"content\":\"a\",\"status\":\"pending\",\"active_form\":\"A\"}]"}`
into one todo, and a question test with a trailing comma inside the string.

- [ ] **Step 3: Run, expect FAIL.**
- [ ] **Step 4: Implement.** `repairToolCall` returns a copy of the call with
  the alias target as its name when `toolNameAliases` has the name and the
  target is in the step's tool list, otherwise `nil, nil` (no repair, the
  original error stands). `trimTrailingCommas` walks the string, tracks
  whether it is inside a JSON string (handling `\"`), and drops a comma whose
  next non-space character is `]` or `}`. Todos gets the same
  array-or-string decoding as Task 3; question calls `trimTrailingCommas`
  before its string fallback re-parse.
- [ ] **Step 5: Run** `go test ./internal/agent/ ./internal/agent/tools/`; PASS.
  Then `go test -race -count=1 ./internal/agent/`.
- [ ] **Step 6: Commit** `fix: repair the todo tool name and loose JSON arrays`

---

## Part B: Fetch subagent and web tools

Tasks 5 to 8. About 400 lines.

### Task 5: the fetch folder lives as long as the fetch subagent

**Why:** `agentic_fetch_tool.go:104-108` creates `crush-fetch-*` and removes
it with `defer` when the tool returns. After 60 s,
`runSubAgentMaybeBackground` returns a "moved to the background" notice
(`agent_background.go:104`) while the subagent keeps running, so its next
`web_fetch` (`web_fetch.go:55`), `view` and `glob` find the folder gone.
That caused 13 temp-file errors, 4 file-not-found errors and 5 glob errors,
landing 2 to 10 s after the notice.

**Files:**
- Modify: `internal/agent/agentic_fetch_tool.go:104-108` and the
  `subAgentParams` literal at about line 199.
- Modify: the `subAgentParams` struct and `runSubAgent` (find them with
  `grep -n "type subAgentParams\|func (c \*coordinator) runSubAgent(" internal/agent/*.go`).
- Modify: `internal/agent/tools/web_fetch.go:55`.
- Test: `internal/agent/agent_background_test.go`, `internal/agent/tools/fetch_test.go`.

**Interfaces:**
- Produces: field `Cleanup func()` on `subAgentParams`, run once when the
  subagent run returns, in the foreground or the background.

- [ ] **Step 1: Failing tests**

`agent_background_test.go`, using the existing setup that sets
`coord.backgroundAfter = time.Millisecond` (line 34):

```go
func TestBackgroundedSubagentCleansUpWhenItFinishes(t *testing.T) {
	// Setup as in the test at line 34: a subagent that blocks on release.
	cleaned := make(chan struct{})
	params.Cleanup = func() { close(cleaned) }
	resp, err := coord.runSubAgentMaybeBackground(ctx, "agentic_fetch", accessRead, params)
	require.NoError(t, err)
	require.Contains(t, resp.Content, "moved to the background")
	select {
	case <-cleaned:
		t.Fatal("cleanup ran while the subagent was still running")
	default:
	}
	close(release)
	select {
	case <-cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup never ran")
	}
}
```

`fetch_test.go`:

```go
func TestWebFetchRecreatesMissingFolder(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "gone")
	// Serve a page larger than LargeContentThreshold from httptest, then:
	tool := NewWebFetchTool(dir, server.Client())
	resp := runTool(t, tool, `{"url":"`+server.URL+`"}`) // Use the run helper the fetch tests already use.
	require.False(t, resp.IsError, resp.Content)
	require.DirExists(t, dir)
}
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** In `runSubAgent`, first line:
  `if params.Cleanup != nil { defer params.Cleanup() }`. Both paths call
  `runSubAgent`, so this runs when the run ends. In `agentic_fetch_tool.go`
  replace the `defer os.RemoveAll(tmpDir)` with:

```go
handedOff := false
defer func() {
	if !handedOff {
		_ = os.RemoveAll(tmpDir)
	}
}()
```

  and set `handedOff = true` right before `return c.runSubAgentMaybeBackground(...)`,
  passing `Cleanup: func() { _ = os.RemoveAll(tmpDir) }`. In `web_fetch.go`
  before `os.CreateTemp(workingDir, ...)` add
  `if err := os.MkdirAll(workingDir, 0o700); err != nil { return error response }`
  so a follow-up run after cleanup still works.
- [ ] **Step 4: Run** `go test -race -count=1 ./internal/agent/ ./internal/agent/tools/`; PASS.
- [ ] **Step 5: Commit** `fix: keep the fetch folder until the fetch subagent finishes`

### Task 6: missing paths answer instead of failing

**Why:** glob returns fastwalk's stat error for a root that does not exist
(`glob.go:71-74`); "no files" is the true answer. view's "Did you mean"
looks only in the immediate parent (`view.go:261-285`), which is gone when
a worktree was deleted (6 cases), so 0 of 12 not-found errors got a hint.

**Files:**
- Modify: `internal/agent/tools/glob.go:63-74`, `internal/agent/tools/view.go:261-285`.
- Test: `glob_test.go`, `view_test.go`.

- [ ] **Step 1: Failing tests**

```go
func TestGlobMissingRootIsNoFiles(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope")
	resp := runGlob(t, `{"pattern":"*.go","path":"`+missing+`"}`) // Build the tool as TestGlobToolExpandsHomeDirectory does.
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "No files found")
	require.Contains(t, resp.Content, "does not exist")
}

func TestViewNotFoundSuggestsNearestFolder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "kept.go"), nil, 0o644))
	resp := runView(t, filepath.Join(root, "deleted", "sub", "file.go"))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, root)
	require.Contains(t, resp.Content, "kept.go")
}
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** In glob, before `globFiles`:
  `if _, err := os.Stat(searchPath); errors.Is(err, fs.ErrNotExist) { return fantasy.NewTextResponse(fmt.Sprintf("No files found: %s does not exist", searchPath)), nil }`.
  In view's not-found branch, walk `filepath.Dir` upward until a folder
  exists (stop at the working directory's root), and list up to 10 entries
  of that folder: `File not found: X. The nearest existing folder is Y, which contains: a, b, c`.
- [ ] **Step 4: Run** `go test ./internal/agent/tools/ -run 'Glob|View'`; PASS.
- [ ] **Step 5: Commit** `fix: answer missing glob roots and point to the nearest folder`

### Task 7: a blocked web search is tried once per blocked period

**Why:** web_search scrapes DuckDuckGo Lite with no fallback
(`search.go:68`). On Nov 7 the machine was blocked and 42 fetch subagents
each hit the block (74 errors); 54 of them then guessed URLs, which made
most of Task 8's 404s.

**Files:**
- Modify: `internal/agent/tools/search.go:49-110`, `internal/agent/agentic_fetch_tool.go:164-174`,
  `internal/agent/tools/web_search.md.tpl` (or the description file the
  web_search tool embeds).
- Test: `internal/agent/tools/search_test.go`, `internal/agent/agentic_fetch_tool_test.go`
  (create if missing).

**Interfaces:**
- Produces: `func SearchBlocked() bool` (exported for the coordinator) and
  `var searchBlockedFor = 15 * time.Minute`, `var searchNow = time.Now`
  (test hooks).

- [ ] **Step 1: Failing tests**

```go
func TestSearchBlockSkipsTheNetwork(t *testing.T) {
	// Not parallel: it changes package state.
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	// Point ddgLiteEndpoint at server as serveSearchStub does; reset block state in t.Cleanup.
	_, err := searchDuckDuckGo(t.Context(), server.Client(), "q", 5)
	require.ErrorIs(t, err, errSearchRateLimited)
	_, err = searchDuckDuckGo(t.Context(), server.Client(), "q", 5)
	require.ErrorIs(t, err, errSearchRateLimited)
	require.Equal(t, int32(1), hits.Load())
	require.True(t, SearchBlocked())
}
```

Plus: with `searchNow` moved 16 minutes ahead, `SearchBlocked()` is false
and the next call reaches the server. In the coordinator test: when
`SearchBlocked()` is true, the fetch subagent's palette has no
`web_search`, and an agentic_fetch call without a URL returns a non-error
response saying web search is unavailable and the caller should use its own
search tool.

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** A mutex-guarded `blockedUntil time.Time`;
  `searchDuckDuckGo` returns `errSearchRateLimited` before the request while
  `searchNow().Before(blockedUntil)`, and sets it when it detects a block.
  In `agentic_fetch_tool.go`, skip `webSearchTool` while blocked; when the
  call has no URL and search is blocked, return
  `fantasy.NewTextResponse("Web search is blocked for this machine for a few minutes. Use a search tool you have directly, or call agentic_fetch again with a known URL.")`
  without starting a subagent. Add to the web_search description: "This
  searches DuckDuckGo and can be blocked; if a dedicated search tool is
  available, prefer it."
- [ ] **Step 4: Run** `go test -race -count=1 ./internal/agent/ ./internal/agent/tools/`; PASS.
- [ ] **Step 5: Commit** `fix: stop retrying a blocked web search`

### Task 8: fetch failures name the next step, and a bad URL falls back to search

**Why:** every non-200 becomes one string (`fetch_helpers.go:43-45`,
`fetch.go:125-128`). 30 of 44 failed URLs were never seen in any output:
the model built them from a host name. A 202 came from fetching DuckDuckGo's
own result page to get around the block. An agentic_fetch URL that 404s
aborts the whole subagent (`agentic_fetch_tool.go:114-117`).

**Files:**
- Modify: `internal/agent/tools/fetch_helpers.go:43-45`, `fetch.go:125-128`,
  `agentic_fetch_tool.go:112-117`.
- Test: `fetch_helpers_test.go`, `agentic_fetch_tool_test.go`.

**Interfaces:**
- Produces: `type FetchStatusError struct { Status int; RetryAfter string }`
  with `Error()`; `FetchURLAndConvert` returns it for non-200 answers.

- [ ] **Step 1: Failing tests** (table over httptest answers):

```go
func TestFetchStatusErrorsNameTheNextStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		header string
		want   string
	}{
		{404, "", "does not exist; the address was probably guessed. Open the site's index or search instead of guessing another path"},
		{429, "120", "rate-limited (retry after 120s); do not retry right away"},
		{503, "", "temporarily unavailable; do not retry right away"},
		{403, "", "refused access"},
	}
	for _, tc := range cases {
		// Serve tc.status with Retry-After tc.header; call FetchURLAndConvert.
		var statusErr *FetchStatusError
		require.ErrorAs(t, err, &statusErr)
		require.Contains(t, err.Error(), tc.want)
	}
}
```

For a host ending in `duckduckgo.com`, any non-200 returns
`errSearchRateLimited` and marks the search blocked (Task 7). In the
agentic_fetch test: a URL whose fetch returns 404 runs the subagent in
search mode with a prompt that starts
`The URL <u> returned 404, so search for the answer instead.`.

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** the type and messages; in agentic_fetch URL
  mode, on a `*FetchStatusError` with status 404 or 410, fall through to the
  search-mode prompt with the note above (unless `SearchBlocked()`, then
  return the fetch error as today).
- [ ] **Step 4: Run** `go test ./internal/agent/ ./internal/agent/tools/`; PASS.
- [ ] **Step 5: Commit** `fix: tell the model what to do after a failed fetch`

---

## Part C: Edit guards see real reads and refuse only once

Tasks 9 to 12. About 400 lines. This part changes what the model sees on
its most common error path; the user ruled on 2026-11-23 that it ships
without a task-suite batch.

### Task 9: reading a file through bash counts as reading it

**Why:** 151 of 225 "must read first" refusals, and all 12 write refusals,
came from files the model had read only through bash (`sed -n`, `cat`,
`head`), which the tracker never records: only view, read and the edit
tools call `RecordRead` (`view.go:358`, `edit.go:187,354`, `write.go:164`,
`multiedit.go:256`). view with an offset already counts as a read, so a
partial `sed -n 10,40p` read fits the guard's existing meaning.

**Files:**
- Create: `internal/agent/tools/bash_reads.go`, `internal/agent/tools/bash_reads_test.go`.
- Modify: `internal/agent/tools/bash.go`, the foreground success path after
  the command returns (near the response built at `bash.go:597-605`); the
  tool needs the `filetracker.Service` (add it to `NewBashTool` and its call
  sites, found with `grep -rn "NewBashTool(" internal`).

**Interfaces:**
- Produces: `func shellReadPaths(command, workingDir string) []string`:
  absolute paths of files that the command only reads.

- [ ] **Step 1: Failing tests**

```go
func TestShellReadPaths(t *testing.T) {
	t.Parallel()
	wd := "/w"
	cases := map[string][]string{
		"cat a.go":                       {"/w/a.go"},
		"sed -n '10,40p' a.go":           {"/w/a.go"},
		"head -50 a.go && tail b.go":     {"/w/a.go", "/w/b.go"},
		"nl -ba a.go | sed -n 1,20p":     {"/w/a.go"},
		"cd sub && cat a.go":             nil, // Changes directory: not counted.
		"sed -i 's/a/b/' a.go":           nil,
		"sed -n p a.go > a.go":           nil,
		"cat a.go | tee a.go":            nil,
		"cat $FILE":                      nil,
		"grep -n foo a.go":               nil, // Shows matching lines only.
		"cat a.go b.go > out.txt":        nil, // Any redirection: not counted.
	}
	for cmd, want := range cases {
		require.Equal(t, want, shellReadPaths(cmd, wd), cmd)
	}
}
```

And a bash tool test: run `cat f.go` in a temp dir with a mock tracker,
then assert `RecordRead` was called with the file; run `sed -i ...` and
assert it was not.

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** with `mvdan.cc/sh/v3/syntax`: parse the command;
  if parsing fails, or any statement has a redirection, or any command is
  not one of `cat`, `head`, `tail`, `nl`, `sed`, or `sed` is used without
  `-n` or with `-i`, or any word is not a plain literal, or a `cd` or
  `pushd` appears, return nil. Otherwise collect the arguments that do not
  start with `-` and are not the sed script (the first non-flag argument of
  sed), join them with `workingDir`, and keep those that are regular files.
  In bash.go, after a foreground command exits 0, call
  `filetracker.RecordRead(ctx, sessionID, path)` for each path.
- [ ] **Step 4: Run** `go test ./internal/agent/tools/ -run 'ShellReadPaths|Bash'`; PASS.
- [ ] **Step 5: Commit** `fix: count bash file reads for the edit guard`

### Task 10: a refusal shows the current file, so the next try succeeds

**Why:** 222 of 225 read refusals were followed by a view and a retry: two
extra calls each. "Modified since read" (110) and "old_string not found"
(15) need the same thing: the current text. Showing it and recording the
read keeps the guard's rule (the model sees the current file before the edit
applies) and saves the view call.

**Files:**
- Modify: `internal/agent/tools/edit_guard.go:34-41` (`refusal`),
  `edit.go:393-407`, `write.go:76-88`, the not-found message path in
  `edit_whitespace.go` near line 333.
- Test: `internal/agent/tools/edit_test.go`, `write_test.go`.

**Interfaces:**
- Produces: `func (edit editContext) refuseWithContent(guard readGuard, filePath, oldString string) fantasy.ToolResponse`.

- [ ] **Step 1: Failing tests**

```go
func TestUnreadEditRefusalShowsFileAndRecordsRead(t *testing.T) {
	t.Parallel()
	// File with 30 lines, never read; tracker = mockEditFileTracker.
	resp := runEdit(t, `{"file_path":"f.go","old_string":"line 12","new_string":"x"}`)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "you must read the file before editing it")
	require.Contains(t, resp.Content, "    12|line 12")
	require.True(t, tracker.recorded("f.go"))
	resp = runEdit(t, `{"file_path":"f.go","old_string":"line 12","new_string":"x"}`)
	require.False(t, resp.IsError, resp.Content)
}

func TestRefusalContentIsCapped(t *testing.T) {
	t.Parallel()
	// File with 50,000 lines; old_string on line 40,000.
	resp := runEdit(t, `{"file_path":"big.go","old_string":"line 40000","new_string":"x"}`)
	require.Contains(t, resp.Content, "40000|line 40000")
	require.Less(t, len(resp.Content), 60_000)
	require.Contains(t, resp.Content, "Showing lines")
}
```

Same pair for write (whole file up to the view limit; above it, the first
window and "use view with offset for the rest"), and for "modified since
read".

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** `refuseWithContent` reads the file, finds the
  first line of `oldString` (or line 1 when empty or absent), and returns
  the guard text plus
  `"\n\nCurrent content (lines A-B of N), now marked as read; retry the edit against it:\n"`
  and `addLineNumbers(window, A)` (`view.go:531`). The window is view's
  default line count centred on the match, with view's line truncation.
  It calls `RecordRead`. Skip the content for files view would refuse
  (binary, image, larger than view's size limit) and keep today's text.
- [ ] **Step 4: Run** `go test ./internal/agent/tools/`; PASS.
- [ ] **Step 5: Commit** `fix: show the current file when an edit is refused`

### Task 11: bash says which read files it changed

**Why:** 86 of 110 "modified since read" errors followed a bash command of
the same session that touched the file (formatters, `git checkout`, file
copies). Telling the model at once lets it view before editing, instead of
learning from a refusal.

**Files:**
- Modify: `internal/agent/tools/bash.go` (foreground path, same place as
  Task 9).
- Test: `bash_test.go`.

- [ ] **Step 1: Failing test:** read `f.go` through the mock tracker, run
  `gofmt -w f.go` (or `touch f.go` when gofmt is missing) through the tool,
  and assert the output ends with
  `Files you read earlier changed during this command: f.go. View them before editing.`
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** Before running, list the session's read files
  (`filetracker.ListReadFiles`) under the working directory, capped at 200,
  with their mod times; after the command, re-stat them and name the ones
  whose mod time changed (cap the list at 10 names). Do not record a read.
- [ ] **Step 4: Run** `go test ./internal/agent/tools/ -run Bash`; PASS.
- [ ] **Step 5: Commit** `feat: tell the model when a command changed files it read`

### Task 12: an edit that is already applied is not an error

**Why:** 3 of the 6 "new content is the same" cases were a resend after the
edit had already been applied. When `old_string` is absent and `new_string`
is present exactly once, the file already has the wanted text.

**Files:** `internal/agent/tools/edit.go` (the not-found branch), `edit_test.go`.

- [ ] **Step 1: Failing test:** edit `a` to `b`, then send the same edit
  again; the second response has `IsError == false` and says
  `No change needed: the file already contains new_string and no longer contains old_string.`
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** the check in the not-found branch, before the
  whitespace fallback, only when `new_string` is non-empty and appears
  exactly once.
- [ ] **Step 4: Run** `go test ./internal/agent/tools/`; PASS.
- [ ] **Step 5: Commit** `fix: treat a repeated edit as already applied`

---

## Part D: Large images are shrunk instead of refused

Task 13. About 200 lines.

### Task 13: fit images under the size cap

**Why:** 31 reads of png and jpg screenshots failed against the single
200 KiB cap (`view.go:79`, checked at `view.go:306-312` on raw bytes). The
smallest failure was 36 bytes over. The model then retried 2 or 3 times.

**Files:**
- Create: `internal/agent/tools/image_fit.go`, `image_fit_test.go`.
- Modify: `internal/agent/tools/view.go:306-312`.

**Interfaces:**
- Produces: `func fitImage(data []byte, maxBytes int) ([]byte, string, error)`
  returning the new bytes and MIME type.

- [ ] **Step 1: Failing tests**

```go
func TestFitImageShrinksLargePNG(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 4000, 3000))
	// Fill with noise from a fixed-seed math/rand source so PNG cannot compress it.
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	require.Greater(t, buf.Len(), MaxViewSize)
	out, mime, err := fitImage(buf.Bytes(), MaxViewSize)
	require.NoError(t, err)
	require.LessOrEqual(t, len(out), MaxViewSize)
	require.Equal(t, "image/jpeg", mime)
	decoded, _, err := image.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	require.GreaterOrEqual(t, decoded.Bounds().Dx(), 800)
}

func TestFitImageRejectsGarbage(t *testing.T) {
	t.Parallel()
	_, _, err := fitImage(bytes.Repeat([]byte{0xff}, MaxViewSize+1), MaxViewSize)
	require.Error(t, err)
}
```

and a view test: the 4000x3000 png file now returns an image response, not
an error (`TestViewToolBlocksOversizedImages` at `view_test.go:162` changes
to cover only an undecodable oversized file).

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** Decode with `image.Decode` (register png, jpeg,
  gif from the standard library and webp from `golang.org/x/image/webp`).
  Loop: width starts at `min(original, 2000)`; `imaging.Resize(img, width, 0, imaging.Lanczos)`;
  encode JPEG at quality 85, then 70; if still too large, width times 0.75,
  stop below 400 px with an error. Return the first encoding that fits.
  In view, when an image is over the cap, call `fitImage`; on error keep
  today's message plus `(it could not be shrunk: <err>)`; on success prepend
  `Image shrunk from W1xH1 to W2xH2 to fit the size limit.`
- [ ] **Step 4: Run** `go test ./internal/agent/tools/`; then
  `GOOS=windows go build ./...` (pure Go, no CGO).
- [ ] **Step 5: Commit** `feat: shrink large images instead of refusing them`

---

## Part E: Coordination tools carry what a retry needs

Tasks 14 to 17. About 400 lines.

### Task 14: team task errors carry the current state, and scopes are relativized

**Why:** stale revision (11), still blocks (6), owned by another (3), not
ready (3) and not found (19) all come back as a bare string from
`teamError` (`team_tools.go:129-131`), so the model must call
`team_task_get` or `team_task_list` before it can retry. Absolute write
scopes inside the workspace (5 worktree paths) are rejected
(`team.go:2170-2185`) although their relative form is obvious.

**Files:**
- Modify: `internal/agent/team_tools.go:129-131` and the update, get and
  create handlers; `internal/agent/team.go:2170-2185`.
- Test: `internal/agent/team_tools_test.go` (or the existing team test file).

**Interfaces:**
- Produces: `func (c *coordinator) teamErrorWithState(err error, taskID string) fantasy.ToolResponse`.

- [ ] **Step 1: Failing tests** (table):
  - update with a stale revision: content contains the error and the
    current task as JSON with its `revision`.
  - delete of a task with dependents: names the dependent task ids.
  - claim of a task owned by another member: names the owner.
  - get of an unknown id: lists up to 10 live task ids and subjects.
  - create with write scope `<workspace>/internal/agent`: succeeds and
    stores `internal/agent`; the response says the scope was rewritten.
  - create with `/mnt/data/crush/scratch/`: still the same error, plus
    "write scopes must be inside the workspace; leave scratch paths out".
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** `teamErrorWithState`: the error text, then
  `\n\nCurrent task:\n<json of the TaskView>` or the live-task list. In the
  scope validator, when a scope is absolute and `filepath.Rel(workspace, scope)`
  does not start with `..`, replace it with the relative form. Do not retry
  the CAS automatically: the model must see the new state before acting.
- [ ] **Step 4: Run** `go test -race -count=1 ./internal/agent/ -run Team`; PASS.
- [ ] **Step 5: Commit** `fix: give team task errors the state needed to retry`

### Task 15: subagent and teammate names work in either tool family

**Why:** `agent_status`, `agent_stop` and `agent_send` look only at
dispatched subagents (`agent_dispatch_status.go:250`, `agent_dispatch.go:731`),
so a teammate name gives "known: none" (11 errors). `send_message` looks
only at the team roster, so a subagent label fails (1).

**Files:**
- Modify: `internal/agent/agent_dispatch_status.go:240-255`, `agent_dispatch.go:725-735`,
  the `send_message` handler in `team_tools.go`.
- Test: `agent_dispatch_status_test.go`, `team_tools_test.go`.

- [ ] **Step 1: Failing tests:** spawn a teammate named `w-impl` in a test
  team; `agent_status` with label `w-impl` returns a non-error response
  with the teammate's status from the roster (same fields `list_agents`
  shows). `agent_send` to `w-impl` delivers through the team mailbox.
  `send_message` to a dispatched subagent label delivers through
  `agent_send`'s path.
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** the fallback lookups: on a dispatch miss, look
  the name up in the team roster and answer from it (status) or forward
  (send); `agent_stop` on a teammate calls the team's interrupt. On a miss
  in both, list both rosters in the error.
- [ ] **Step 4: Run** `go test -race -count=1 ./internal/agent/`; PASS.
- [ ] **Step 5: Commit** `fix: let subagent and team tools find each other's agents`

### Task 16: LSP and skill resource misses say why or just work

**Why:** "no LSP client handles file" (6) hides the reason: all six files
were outside the session workspace, which `lsp/manager.go:104-110` skips on
purpose. `read_mcp_resource` with `crush://skills/...` fails
(`mcp/init.go:892`) although skills are advertised with that URI and view
reads it.

**Files:**
- Modify: `internal/agent/tools/lsp_symbols.go:33-36`, `lsp_replace_symbol.go:80`,
  `internal/agent/tools/mcp/init.go:885-895` (or the read_mcp_resource tool
  that calls it).
- Test: `lsp_helpers_test.go`, the read_mcp_resource test file.

- [ ] **Step 1: Failing tests:** a path outside the working directory gives
  `no LSP client handles file X: it is outside the workspace W; LSP tools only work on workspace files, use view and edit instead`;
  a workspace `.zzz` file gives `no language server is configured for .zzz files`.
  `read_mcp_resource` with `crush://skills/<builtin>/SKILL.md` returns the
  skill text.
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement** the reason selection (workspace check with
  `filepath.Rel`, then extension lookup in the LSP config) and, in
  read_mcp_resource, route `crush://` URIs to the same reader view uses for
  skills.
- [ ] **Step 4: Run** `go test ./internal/agent/tools/... ./internal/lsp/`; PASS.
- [ ] **Step 5: Commit** `fix: explain LSP misses and read skill resources`

### Task 17: timeouts are reported as timeouts

**Why:** 9 turns ended with "Provider Error: context deadline exceeded"
(kimi on Neuralwatt 5, opus 2, commandcode 2). The request guard has its own
"Request timed out" text (`request_guard.go:65-68`), but paths that return
the raw context error (`claudeagent/session.go:733`) skip it, so the user
cannot tell a timeout from a provider fault.

**Files:**
- Modify: the turn-end error mapping in `internal/agent/agent.go:2020-2110`
  (where a provider error becomes the finish message).
- Test: the turn-end test file (`internal/agent/turn_end_test.go`).

- [ ] **Step 1: Failing test:** a model whose stream returns
  `fmt.Errorf("stream: %w", context.DeadlineExceeded)` while the parent
  context is still alive ends the turn with message `Request timed out` and
  details naming the configured timeout.
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement:** `errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil`
  maps to the guard's timeout finish.
- [ ] **Step 4: Run** `go test -race -count=1 ./internal/agent/`; PASS.
- [ ] **Step 5: Commit** `fix: report provider timeouts as timeouts`

---

## Out of scope, with reasons

- **Claude 5-hour quota (22 turn ends).** Avoiding it means falling back to
  another model mid-turn; the user ruled on 2026-11-23 not to add a
  fallback.
- **Invalid API key (3, Neuralwatt), insufficient credits (1), capacity
  (1).** Account state, not code.
- **User cancels and interrupted turns (47), settle.go placeholders.** These
  record what the user did.
- **Changes made outside Crush (23 "modified since read").** Catching these
  is the guard's purpose; Task 10 makes each one a single retry.

## Verification after the merge

Re-run `python3 -I /mnt/data/crush/scratch/errors-agg.py <merge date>` after
a week of normal use and compare each row of the inventory table with its
"Expected after" column.
