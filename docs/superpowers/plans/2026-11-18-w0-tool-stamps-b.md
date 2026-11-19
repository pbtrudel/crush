# Tool Stamps, Part B (Denials, Library Errors, Interrupted Calls) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record in the session log the error kind of every refused permission, every call a hook blocked, every call the provider library could not run, and every call Crush settled because its run never answered it (the second half of delivery item 3 of the measurement design).

**Architecture:** Part A added `ErrorKind` and a helper that writes `error_kind` into a result's metadata without touching other keys (`internal/agent/tools/error_kind.go`). This pull request adds the last five kinds. `permission_denied` is set in `NewPermissionDeniedResponse` and set again at the six denials that replace the metadata afterwards. `hook_denied` and `interrupted` are set in package `agent` where the hook block and the settled results are produced. `invalid_arguments` and `unknown_tool` are set in `OnToolResult`, the one place every live result passes before it is stored, from the provider library's message prefixes; a test drives the pinned library to produce each one. One UI renderer that decoded stamp-only metadata as todo data is guarded.

**Tech Stack:** Go 1.27, `charm.land/fantasy` v0.45.5 (pinned in `go.mod`), `github.com/tidwall/gjson` and `github.com/tidwall/sjson`, testify `require`.

**Spec:** `docs/superpowers/specs/2026-11-17-measurement-design.md`, sections 1.5 (Error kind), Failure behavior, Validation, Scope, Delivery item 3. Part A (`d6659751`, plan `docs/superpowers/plans/2026-11-18-w0-tool-stamps-a.md`) is merged. Code references below are to `origin/main` at `d6659751`.

## Global Constraints

- Work in a fresh worktree created from `origin/main` after `git fetch origin` (for example `worktree create` with name `w0-tool-stamps-b`, base `origin/main`). Paths below are relative to its root.
- This pull request adds exactly five kinds: `permission_denied`, `hook_denied`, `invalid_arguments`, `unknown_tool`, `interrupted`. With part A's four the list in spec 1.5 is complete and closed: any other failure carries no kind.
- `error_kind` is a string key in the tool result's metadata JSON, next to the `hook` key. Writing it keeps every other key. Metadata that is not a JSON object is left unchanged (part A's `setErrorKind`, exported here as `SetErrorKind`).
- Where each kind is set:
  - `permission_denied`: every `NewPermissionDeniedResponse()` result (`internal/agent/tools/tools.go:92-98`), including the six that attach their own metadata afterwards (`edit.go` three, `multiedit.go` two, `write.go` one), which now go through `permissionDeniedWithMetadata`. A multi-file `view` that is refused one file still succeeds as a whole and carries no kind.
  - `hook_denied`: the PreToolUse block or halt in `hookedTool.Run` (`internal/agent/hooked_tool.go:61-73`), set after `mergeHookMetadata`. A PostToolUse hook that blocks or halts runs after the call already ran and adds no kind.
  - `invalid_arguments`: a stored result that is an error, carries no kind yet, and starts with `invalid JSON input: `, `missing required parameter: ` or `invalid parameters: `; also every result Crush rewrites to "Tool call failed: arguments were not valid JSON..." (`sanitizedToolCalls`, `internal/agent/agent.go:1622-1625`), set explicitly there.
  - `unknown_tool`: a stored result that is an error, carries no kind yet, and starts with `tool not found: `.
  - `interrupted`: every result `settleToolCalls` writes (`internal/agent/settle.go:99-135`), whatever its reason text: cancelled, failed, truncated, refused, abandoned, or repaired after a dead process.
- The library prefixes are the wording of `charm.land/fantasy` v0.45.5: `validateToolCall` and `executeSingleTool` in `agent.go` and `funcToolWrapper.Run` in `tool.go`. They were checked against that version: `tool not found: nope. Available tools: typed`, `invalid JSON input: json: cannot unmarshal array into Go value of type map[string]interface {}`, `missing required parameter: path`, `invalid parameters: json: cannot unmarshal number into Go struct field ...`, and an empty input reaches `OnToolResult` as a sanitized call. Do not bump the library in this pull request.
- The text the model reads is unchanged, byte for byte. The sanitized-call message moves into a constant with the same text.
- Writing a stamp never fails a tool call: `SetErrorKind` returns the metadata unchanged when it cannot set the key.
- Known visible effects: a denied, interrupted or invalid-argument result that had no metadata now has `{"error_kind":...}`. The bash, job, view, write, definition, replace-symbol and agent renderers read only their own keys and render as before. `edit` and `multiedit` render it through their existing error-line branch, as part A already does for its kinds; copying such an `edit` or `multiedit` error to the clipboard yields an empty string instead of the error text, which is already true today for any such error a hook touched (`formatEditResultForCopy`, `internal/ui/chat/tools.go:1657-1689`). Left as is. The todos renderer took any decodable metadata as todo data, so an interrupted todos call would show `0/0` instead of its own counts; Task 2 guards it.
- Style: gofumpt (`/home/pbtrudel/.local/bin/gofumpt`); comments on their own line start with a capital, end with a period, wrap at 78 columns; no em dashes anywhere; testify `require`; log messages start with a capital letter. Never open or print any `crush.json` or `crushrc` file.
- Size: about 500 changed lines, about 85 of them production code and the rest tests. This is above the 400-line target because the brief requires a pin for each of the six metadata-replacing denials and a run of the pinned library for each library error; splitting further would separate a kind from the test that proves it.
- Commits: one per task, semantic subjects, no attribution lines. Commits are authorized for this work.

## Review Focus

1. A tool that sets its own metadata after the denial: `fantasy.WithResponseMetadata` replaces the whole string, so the kind must be set after it. Pinned in Task 1 by one subtest per site, each asserting both the kind and one of the tool's own keys.
2. A kind already set must win: a tool's denial must keep `permission_denied` through a hook, and the central classifier must not overwrite a kind a tool set, even when the text matches a library prefix. Pinned in Task 2 (`TestHookedToolPassThroughKind`) and Task 3 (`a kind already set wins`).
3. A tool's own error text that merely contains a library phrase (an MCP server saying "MCP said: tool not found: x") or a successful result that starts with one must not be classified. Pinned in Task 3 by the `not an error` and `the prefix must start the text` cases.
4. A future library version that rewords a message would silently stop the kinds. Pinned in Task 3 by `TestLibraryErrorKindsFromThePinnedLibrary`, which drives the real library through a session run and asserts the prefix of each stored text.
5. A renderer that decodes any JSON metadata as its own data would show stamp-only metadata as empty data. Pinned in Task 2 by `TestTodosErrorRendersWithoutItsMetadata`.

---

### Task 1: `permission_denied`

**Files:**
- Modify: `internal/agent/tools/error_kind.go:15-28` (one constant)
- Modify: `internal/agent/tools/tools.go:92-98` (`NewPermissionDeniedResponse`, one new function)
- Modify: `internal/agent/tools/edit.go:147-155`, `:375-383`, `:448-456`
- Modify: `internal/agent/tools/multiedit.go:198-208`, `:308-318`
- Modify: `internal/agent/tools/write.go:130-138`
- Test: `internal/agent/tools/permission_denied_kind_test.go` (new)

**Interfaces:**
- Consumes: `withErrorKind(resp fantasy.ToolResponse, kind ErrorKind) fantasy.ToolResponse` (`error_kind.go`); test helpers `editContextForKind(t *testing.T, dir string, lastRead time.Time) editContext` (`error_kind_test.go`), `mockPermissionService{deny bool}` and `mockHistoryService` (`multiedit_test.go`), `newBashToolWithRecordingPerms(workingDir string, allow bool) (fantasy.AgentTool, *recordingPermissionService)` and `runBashTool(t, tool, ctx, params) fantasy.ToolResponse` (`bash_test.go`).
- Produces: `ErrorKindPermissionDenied ErrorKind = "permission_denied"`; `func permissionDeniedWithMetadata(metadata any) fantasy.ToolResponse` (unexported, package `tools`). `NewPermissionDeniedResponse()` now returns metadata `{"error_kind":"permission_denied"}`.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/tools/permission_denied_kind_test.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNewPermissionDeniedResponseKind(t *testing.T) {
	t.Parallel()

	resp := NewPermissionDeniedResponse()
	require.True(t, resp.IsError)
	require.True(t, resp.StopTurn)
	require.Equal(t, "User denied permission", resp.Content, "the text the model reads is unchanged")
	require.Equal(t, `{"error_kind":"permission_denied"}`, resp.Metadata)
}

func TestBashPermissionDeniedKind(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, false)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{Description: "denied", Command: "touch newfile"})
	require.Equal(t, 1, perms.requestCount)
	require.Equal(t, "User denied permission", resp.Content)
	require.Equal(t, string(ErrorKindPermissionDenied), gjson.Get(resp.Metadata, "error_kind").String())
}

// These six denials attach the tool's own metadata, which replaces the
// metadata whole, so each one is pinned here.
func TestPermissionDeniedKindSurvivesToolMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ownKey  string
		respond func(t *testing.T, edit editContext, existing, missing string) (fantasy.ToolResponse, error)
	}{
		{
			name:   "edit, create",
			ownKey: "new_content",
			respond: func(t *testing.T, edit editContext, _, missing string) (fantasy.ToolResponse, error) {
				return createNewFile(edit, missing, "gamma\n", fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "edit, delete",
			ownKey: "old_content",
			respond: func(t *testing.T, edit editContext, existing, _ string) (fantasy.ToolResponse, error) {
				return deleteContent(edit, existing, "beta\n", false, fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "edit, replace",
			ownKey: "new_content",
			respond: func(t *testing.T, edit editContext, existing, _ string) (fantasy.ToolResponse, error) {
				return replaceContent(edit, existing, "beta", "BETA", false, fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "multiedit, create",
			ownKey: "edits_applied",
			respond: func(t *testing.T, edit editContext, _, missing string) (fantasy.ToolResponse, error) {
				return processMultiEditWithCreation(edit, MultiEditParams{
					FilePath: missing,
					Edits:    []MultiEditOperation{{OldString: "", NewString: "gamma\n"}},
				}, fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "multiedit, existing file",
			ownKey: "edits_applied",
			respond: func(t *testing.T, edit editContext, existing, _ string) (fantasy.ToolResponse, error) {
				return processMultiEditExistingFile(edit, MultiEditParams{
					FilePath: existing,
					Edits:    []MultiEditOperation{{OldString: "beta", NewString: "BETA"}},
				}, fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "write",
			ownKey: "diff",
			respond: func(t *testing.T, edit editContext, existing, _ string) (fantasy.ToolResponse, error) {
				tool := NewWriteTool(nil, edit.permissions, edit.files, edit.filetracker, edit.workingDir)
				input, err := json.Marshal(WriteParams{FilePath: existing, Content: "gamma\n"})
				require.NoError(t, err)
				return tool.Run(edit.ctx, fantasy.ToolCall{ID: "call", Name: WriteToolName, Input: string(input)})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			existing := filepath.Join(dir, "a.txt")
			require.NoError(t, os.WriteFile(existing, []byte("alpha\nbeta\n"), 0o644))
			missing := filepath.Join(dir, "new.txt")
			edit := editContextForKind(t, dir, time.Now().Add(time.Second))
			edit.permissions = &mockPermissionService{deny: true}

			resp, err := tt.respond(t, edit, existing, missing)
			require.NoError(t, err)
			require.True(t, resp.IsError)
			require.True(t, resp.StopTurn)
			require.Equal(t, "User denied permission", resp.Content, "the text the model reads is unchanged")
			require.Equal(t, string(ErrorKindPermissionDenied), gjson.Get(resp.Metadata, "error_kind").String())
			require.True(t, gjson.Get(resp.Metadata, tt.ownKey).Exists(), "the tool's own metadata is kept: %s", resp.Metadata)

			content, err := os.ReadFile(existing)
			require.NoError(t, err)
			require.Equal(t, "alpha\nbeta\n", string(content), "a denied call changes nothing")
			require.NoFileExists(t, missing)
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/tools/ -run 'TestNewPermissionDeniedResponseKind|TestBashPermissionDeniedKind|TestPermissionDeniedKindSurvivesToolMetadata' -count=1`
Expected: build failure `undefined: ErrorKindPermissionDenied`. (If you add only the constant first, all three tests fail on `error_kind` being `""`; with only the `NewPermissionDeniedResponse` change, the six subtests of `TestPermissionDeniedKindSurvivesToolMetadata` still fail on `""`, which is the bug they pin.)

- [ ] **Step 3: Implement**

In `internal/agent/tools/error_kind.go`, replace

```go
	ErrorKindMultipleMatches ErrorKind = "multiple_matches"
)
```

with

```go
	ErrorKindMultipleMatches ErrorKind = "multiple_matches"
	// ErrorKindPermissionDenied is a call the user refused permission for.
	ErrorKindPermissionDenied ErrorKind = "permission_denied"
)
```

In `internal/agent/tools/tools.go`, replace

```go
	resp := fantasy.NewTextErrorResponse("User denied permission")
	resp.StopTurn = true
	return resp
}
```

with

```go
	resp := fantasy.NewTextErrorResponse("User denied permission")
	resp.StopTurn = true
	return withErrorKind(resp, ErrorKindPermissionDenied)
}

// permissionDeniedWithMetadata is NewPermissionDeniedResponse carrying a
// tool's own metadata. The kind is set again after the metadata, which
// fantasy.WithResponseMetadata replaces whole.
func permissionDeniedWithMetadata(metadata any) fantasy.ToolResponse {
	resp := fantasy.WithResponseMetadata(NewPermissionDeniedResponse(), metadata)
	return withErrorKind(resp, ErrorKindPermissionDenied)
}
```

In `internal/agent/tools/edit.go`, `createNewFile`, replace

```go
	if !p {
		resp := NewPermissionDeniedResponse()
		resp = fantasy.WithResponseMetadata(resp, EditResponseMetadata{
			OldContent: "",
			NewContent: content,
			Additions:  additions,
			Removals:   removals,
		})
		return resp, nil
	}
```

with

```go
	if !p {
		return permissionDeniedWithMetadata(EditResponseMetadata{
			OldContent: "",
			NewContent: content,
			Additions:  additions,
			Removals:   removals,
		}), nil
	}
```

In `deleteContent`, replace

```go
	if !p {
		resp := NewPermissionDeniedResponse()
		resp = fantasy.WithResponseMetadata(resp, EditResponseMetadata{
			OldContent: oldContent,
			NewContent: newContent,
			Additions:  additions,
			Removals:   removals,
		})
		return resp, nil
	}
```

with

```go
	if !p {
		return permissionDeniedWithMetadata(EditResponseMetadata{
			OldContent: oldContent,
			NewContent: newContent,
			Additions:  additions,
			Removals:   removals,
		}), nil
	}
```

In `replaceContent`, replace

```go
	if !p {
		resp := NewPermissionDeniedResponse()
		resp = fantasy.WithResponseMetadata(resp, EditResponseMetadata{
			OldContent: oldContent,
			NewContent: result,
			Additions:  additions,
			Removals:   removals,
		})
		return resp, nil
	}
```

with

```go
	if !p {
		return permissionDeniedWithMetadata(EditResponseMetadata{
			OldContent: oldContent,
			NewContent: result,
			Additions:  additions,
			Removals:   removals,
		}), nil
	}
```

In `internal/agent/tools/multiedit.go`, `processMultiEditWithCreation`, replace

```go
	if !p {
		resp := NewPermissionDeniedResponse()
		resp = fantasy.WithResponseMetadata(resp, MultiEditResponseMetadata{
			OldContent:   "",
			NewContent:   currentContent,
			Additions:    additions,
			Removals:     removals,
			EditsApplied: editsApplied,
			EditsFailed:  failedEdits,
		})
		return resp, nil
	}
```

with

```go
	if !p {
		return permissionDeniedWithMetadata(MultiEditResponseMetadata{
			OldContent:   "",
			NewContent:   currentContent,
			Additions:    additions,
			Removals:     removals,
			EditsApplied: editsApplied,
			EditsFailed:  failedEdits,
		}), nil
	}
```

In `processMultiEditExistingFile`, replace

```go
	if !p {
		resp := NewPermissionDeniedResponse()
		resp = fantasy.WithResponseMetadata(resp, MultiEditResponseMetadata{
			OldContent:   oldContent,
			NewContent:   currentContent,
			Additions:    additions,
			Removals:     removals,
			EditsApplied: editsApplied,
			EditsFailed:  failedEdits,
		})
		return resp, nil
	}
```

with

```go
	if !p {
		return permissionDeniedWithMetadata(MultiEditResponseMetadata{
			OldContent:   oldContent,
			NewContent:   currentContent,
			Additions:    additions,
			Removals:     removals,
			EditsApplied: editsApplied,
			EditsFailed:  failedEdits,
		}), nil
	}
```

In `internal/agent/tools/write.go`, replace

```go
			if !p {
				resp := NewPermissionDeniedResponse()
				resp = fantasy.WithResponseMetadata(resp, WriteResponseMetadata{
					Diff:      diff,
					Additions: additions,
					Removals:  removals,
				})
				return resp, nil
			}
```

with

```go
			if !p {
				return permissionDeniedWithMetadata(WriteResponseMetadata{
					Diff:      diff,
					Additions: additions,
					Removals:  removals,
				}), nil
			}
```

Then confirm no metadata-replacing denial is left: `grep -n "resp = fantasy.WithResponseMetadata(resp" internal/agent/tools/edit.go internal/agent/tools/multiedit.go internal/agent/tools/write.go` must print nothing.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/tools/ -count=1`
Expected: PASS (the whole package, so the existing edit, multiedit, write and bash tests confirm nothing else changed).

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent/tools/error_kind.go internal/agent/tools/tools.go internal/agent/tools/edit.go internal/agent/tools/multiedit.go internal/agent/tools/write.go internal/agent/tools/permission_denied_kind_test.go
git add internal/agent/tools/error_kind.go internal/agent/tools/tools.go internal/agent/tools/edit.go internal/agent/tools/multiedit.go internal/agent/tools/write.go internal/agent/tools/permission_denied_kind_test.go
git commit -m "feat(tools): record a refused permission as an error kind"
```

---

### Task 2: `hook_denied` and `interrupted`

**Files:**
- Modify: `internal/agent/tools/error_kind.go` (two constants; rename `setErrorKind` to `SetErrorKind`; one new function)
- Modify: `internal/agent/tools/error_kind_test.go:35` (the rename)
- Modify: `internal/agent/hooked_tool.go:71` (the PreToolUse block)
- Modify: `internal/agent/settle.go:9-12` (imports), `:120-127` (`settleToolCalls`)
- Modify: `internal/ui/chat/todos.go:76-77` (guard)
- Test: `internal/agent/error_kind_test.go` (new), `internal/ui/chat/todos_error_kind_test.go` (new)

**Interfaces:**
- Consumes: `ErrorKindPermissionDenied`, `NewPermissionDeniedResponse()` (Task 1); test helpers in package `agent`: `fakeTool{name string; called bool; resp fantasy.ToolResponse}` and `newRunner(t *testing.T, cmd string) *hooks.Runner` (`hooked_tool_test.go`), `testEnv(t) fakeEnv` (`common_test.go`), `answeredOnly(ids ...string) func(string) bool` (`unfinished_tool_calls_test.go`); constants `canceledToolCallResult`, `abandonedToolCallResult`, `interruptedToolCallResult` (`settle.go`); `repairInterruptedToolCalls` (`settle.go`). In package `chat`: `styles.CharmtonePantera()`, `NewTodosToolMessageItem`.
- Produces: `ErrorKindHookDenied ErrorKind = "hook_denied"`, `ErrorKindInterrupted ErrorKind = "interrupted"`; `func SetErrorKind(metadata string, kind ErrorKind) string` (exported rename of `setErrorKind`, same behavior); `func MetadataErrorKind(metadata string) ErrorKind` (returns `""` when there is none).

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/error_kind_test.go`:

```go
package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestHookedToolBlockKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cmd      string
		content  string
		stopTurn bool
	}{
		{name: "deny", cmd: `echo "blocked" >&2; exit 2`, content: "Tool call blocked by hook. Reason: blocked"},
		{name: "halt", cmd: `echo '{"halt":true,"reason":"turn over"}'`, content: "Turn halted by hook. Reason: turn over", stopTurn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inner := &fakeTool{name: "bash"}
			tool := newHookedTool(inner, newRunner(t, tt.cmd))

			resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call", Name: "bash"})
			require.NoError(t, err)
			require.False(t, inner.called)
			require.True(t, resp.IsError)
			require.Equal(t, tt.stopTurn, resp.StopTurn)
			require.Equal(t, tt.content, resp.Content, "the text the model reads is unchanged")
			require.Equal(t, string(tools.ErrorKindHookDenied), gjson.Get(resp.Metadata, "error_kind").String())
			require.True(t, gjson.Get(resp.Metadata, "hook").Exists(), "the hook metadata is kept: %s", resp.Metadata)
		})
	}
}

// A hook that lets a call through leaves the tool's own kind alone, and
// adds none to a call that succeeded.
func TestHookedToolPassThroughKind(t *testing.T) {
	t.Parallel()

	denied := &fakeTool{name: "bash", resp: tools.NewPermissionDeniedResponse()}
	resp, err := newHookedTool(denied, newRunner(t, `exit 0`)).Run(t.Context(), fantasy.ToolCall{ID: "call", Name: "bash"})
	require.NoError(t, err)
	require.Equal(t, string(tools.ErrorKindPermissionDenied), gjson.Get(resp.Metadata, "error_kind").String())

	ok := &fakeTool{name: "bash", resp: fantasy.NewTextResponse("ok")}
	resp, err = newHookedTool(ok, newRunner(t, `exit 0`)).Run(t.Context(), fantasy.ToolCall{ID: "call", Name: "bash"})
	require.NoError(t, err)
	require.False(t, gjson.Get(resp.Metadata, "error_kind").Exists())
}

func TestSettledToolCallsAreInterrupted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		runErr error
		want   string
	}{
		{name: "canceled", runErr: context.Canceled, want: canceledToolCallResult},
		{name: "abandoned", want: abandonedToolCallResult},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := testEnv(t)
			agent := &sessionAgent{messages: env.messages}
			sess, err := env.sessions.Create(t.Context(), "t")
			require.NoError(t, err)
			assistant, err := env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ToolCall{ID: "done", Name: "view", Input: `{}`, Finished: true},
					message.ToolCall{ID: "open", Name: "bash", Input: `{}`, Finished: true},
				},
			})
			require.NoError(t, err)

			require.NoError(t, agent.settleTurn(t.Context(), &assistant, answeredOnly("done"), tt.runErr))

			msgs, err := env.messages.List(t.Context(), sess.ID)
			require.NoError(t, err)
			var results []message.ToolResult
			for _, m := range msgs {
				results = append(results, m.ToolResults()...)
			}
			require.Len(t, results, 1)
			require.Equal(t, "open", results[0].ToolCallID)
			require.True(t, results[0].IsError)
			require.Equal(t, tt.want, results[0].Content, "the text the model reads is unchanged")
			require.Equal(t, `{"error_kind":"interrupted"}`, results[0].Metadata)
		})
	}
}

// A call a dead process left without a reply is settled by the repair,
// which goes through the same writer.
func TestRepairedToolCallsAreInterrupted(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "t")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.ToolCall{ID: "orphan", Name: "bash", Input: `{}`, Finished: true}},
	})
	require.NoError(t, err)
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	repaired, err := repairInterruptedToolCalls(t.Context(), env.messages, msgs)
	require.NoError(t, err)
	var results []message.ToolResult
	for _, m := range repaired {
		results = append(results, m.ToolResults()...)
	}
	require.Len(t, results, 1)
	require.Equal(t, interruptedToolCallResult, results[0].Content)
	require.Equal(t, string(tools.ErrorKindInterrupted), gjson.Get(results[0].Metadata, "error_kind").String())
}
```

Create `internal/ui/chat/todos_error_kind_test.go`:

```go
package chat

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// A failed todos call carries no todo metadata, only stamps such as its
// error kind, so it renders the same with or without them.
func TestTodosErrorRendersWithoutItsMetadata(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	call := message.ToolCall{
		ID:       "1",
		Name:     "todos",
		Input:    `{"todos":[{"content":"Write tests","status":"completed","active_form":"Writing tests"},{"content":"Ship","status":"in_progress","active_form":"Shipping"}]}`,
		Finished: true,
	}
	render := func(metadata string) string {
		result := &message.ToolResult{ToolCallID: "1", Content: "Error: user cancelled assistant tool calling", IsError: true, Metadata: metadata}
		return NewTodosToolMessageItem(&sty, call, result, false).Render(100)
	}

	require.Equal(t, render(""), render(`{"error_kind":"interrupted"}`))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'TestHookedToolBlockKind|TestHookedToolPassThroughKind|TestSettledToolCallsAreInterrupted|TestRepairedToolCallsAreInterrupted' -count=1` and `go test ./internal/ui/chat/ -run TestTodosErrorRendersWithoutItsMetadata -count=1`
Expected: the agent package fails to build with `undefined: tools.ErrorKindHookDenied` and `undefined: tools.ErrorKindInterrupted`. The UI test fails: the header reads `0/0` with the metadata and `1/2 · Shipping` without it.

- [ ] **Step 3: Implement**

In `internal/agent/tools/error_kind.go`, replace

```go
	ErrorKindPermissionDenied ErrorKind = "permission_denied"
)
```

with

```go
	ErrorKindPermissionDenied ErrorKind = "permission_denied"
	// ErrorKindHookDenied is a call a PreToolUse hook blocked or halted
	// before it ran.
	ErrorKindHookDenied ErrorKind = "hook_denied"
	// ErrorKindInterrupted is the result Crush writes for a call its run
	// never answered.
	ErrorKindInterrupted ErrorKind = "interrupted"
)
```

In the same file, replace

```go
// setErrorKind returns metadata with kind stored under "error_kind",
// keeping every other key. Metadata that is not a JSON object is returned
// unchanged, because a stamp must never alter or break a result.
func setErrorKind(metadata string, kind ErrorKind) string {
```

with

```go
// SetErrorKind returns metadata with kind stored under "error_kind",
// keeping every other key. Metadata that is not a JSON object is returned
// unchanged, because a stamp must never alter or break a result.
func SetErrorKind(metadata string, kind ErrorKind) string {
```

and replace

```go
// withErrorKind records kind in a tool response's metadata. Call it after
// any fantasy.WithResponseMetadata, which replaces the metadata whole.
func withErrorKind(resp fantasy.ToolResponse, kind ErrorKind) fantasy.ToolResponse {
	resp.Metadata = setErrorKind(resp.Metadata, kind)
	return resp
}
```

with

```go
// MetadataErrorKind returns the kind stored in metadata, or "" when it
// has none.
func MetadataErrorKind(metadata string) ErrorKind {
	return ErrorKind(gjson.Get(metadata, errorKindKey).String())
}

// withErrorKind records kind in a tool response's metadata. Call it after
// any fantasy.WithResponseMetadata, which replaces the metadata whole.
func withErrorKind(resp fantasy.ToolResponse, kind ErrorKind) fantasy.ToolResponse {
	resp.Metadata = SetErrorKind(resp.Metadata, kind)
	return resp
}
```

In `internal/agent/tools/error_kind_test.go`, replace

```go
			require.Equal(t, tt.want, setErrorKind(tt.metadata, ErrorKindNotRead))
```

with

```go
			require.Equal(t, tt.want, SetErrorKind(tt.metadata, ErrorKindNotRead))
```

Then `grep -rn "setErrorKind" internal/` must print nothing.

In `internal/agent/hooked_tool.go`, replace

```go
		resp.Metadata = mergeHookMetadata("", pre)
		return resp, nil
```

with

```go
		// The call never ran, so the hook's metadata is all it carries,
		// with the kind added after it.
		resp.Metadata = tools.SetErrorKind(mergeHookMetadata("", pre), tools.ErrorKindHookDenied)
		return resp, nil
```

(`tools` is already imported there.)

In `internal/agent/settle.go`, replace the imports

```go
	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/message"
)
```

with

```go
	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
)
```

and in `settleToolCalls` replace

```go
				Content:    reason,
				IsError:    true,
			}},
```

with

```go
				Content:    reason,
				Metadata:   tools.SetErrorKind("", tools.ErrorKindInterrupted),
				IsError:    true,
			}},
```

In `internal/ui/chat/todos.go`, replace

```go
		// If we have metadata, use it for richer display.
		if opts.HasResult() && opts.Result.Metadata != "" {
```

with

```go
		// If we have metadata, use it for richer display. A failed call
		// has no todo metadata, only stamps such as its error kind.
		if opts.HasResult() && !opts.Result.IsError && opts.Result.Metadata != "" {
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/tools/ ./internal/agent/ ./internal/ui/chat/ -count=1`
Expected: PASS (the three whole packages, so the existing hook, settle, interrupted-call and renderer tests confirm nothing else changed).

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent/tools/error_kind.go internal/agent/tools/error_kind_test.go internal/agent/hooked_tool.go internal/agent/settle.go internal/agent/error_kind_test.go internal/ui/chat/todos.go internal/ui/chat/todos_error_kind_test.go
git add internal/agent/tools/error_kind.go internal/agent/tools/error_kind_test.go internal/agent/hooked_tool.go internal/agent/settle.go internal/agent/error_kind_test.go internal/ui/chat/todos.go internal/ui/chat/todos_error_kind_test.go
git commit -m "feat(agent): record hook blocks and settled tool calls as error kinds"
```

---

### Task 3: `invalid_arguments` and `unknown_tool`

**Files:**
- Modify: `internal/agent/tools/error_kind.go` (two constants)
- Create: `internal/agent/tool_result_kind.go`
- Modify: `internal/agent/agent.go:1620-1625` (`OnToolResult` in the run's stream call)
- Test: `internal/agent/tool_result_kind_test.go` (new)

**Interfaces:**
- Consumes: `SetErrorKind`, `MetadataErrorKind`, `ErrorKindHookDenied` (Task 2); test helpers in package `agent`: `scriptedModel{script []scriptedCall}`, `scriptedCall{parts []fantasy.StreamPart}`, `answer(text string, finish fantasy.FinishReason, inputTokens int64) scriptedCall` (`turn_end_run_test.go`), `testEnv(t) fakeEnv` and `testSessionAgent(env, large, small, systemPrompt, tools...) SessionAgent` (`common_test.go`).
- Produces: `ErrorKindInvalidArguments ErrorKind = "invalid_arguments"`, `ErrorKindUnknownTool ErrorKind = "unknown_tool"`; `const sanitizedToolCallResult` (package `agent`, same text as before); `func stampLibraryErrorKind(result message.ToolResult) message.ToolResult`.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/tool_result_kind_test.go`:

```go
package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStampLibraryErrorKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result message.ToolResult
		want   string
	}{
		{
			name:   "unknown tool",
			result: message.ToolResult{Content: "tool not found: nope. Available tools: view", IsError: true},
			want:   `{"error_kind":"unknown_tool"}`,
		},
		{
			name:   "keeps hook metadata",
			result: message.ToolResult{Content: "invalid parameters: bad", IsError: true, Metadata: `{"hook":{"hook_count":1}}`},
			want:   `{"hook":{"hook_count":1},"error_kind":"invalid_arguments"}`,
		},
		{
			name:   "a kind already set wins",
			result: message.ToolResult{Content: "invalid parameters: bad", IsError: true, Metadata: `{"error_kind":"permission_denied"}`},
			want:   `{"error_kind":"permission_denied"}`,
		},
		{
			name:   "not an error",
			result: message.ToolResult{Content: "tool not found: in this output"},
			want:   "",
		},
		{
			name:   "an error outside the list",
			result: message.ToolResult{Content: "Failed to write file: disk full", IsError: true},
			want:   "",
		},
		{
			name:   "the prefix must start the text",
			result: message.ToolResult{Content: "MCP said: tool not found: x", IsError: true},
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := stampLibraryErrorKind(tt.result)
			require.Equal(t, tt.want, got.Metadata)
			require.Equal(t, tt.result.Content, got.Content, "the text the model reads is unchanged")
			require.Equal(t, tt.result.IsError, got.IsError)
		})
	}
}

// kindProbeParams has one required parameter, so the library checks for it
// before the call runs.
type kindProbeParams struct {
	Path string `json:"path" description:"A path"`
}

// TestLibraryErrorKindsFromThePinnedLibrary drives one step whose calls
// make charm.land/fantasy refuse in each way it can, and reads the kinds
// back from the session log. A reworded library message fails here.
func TestLibraryErrorKindsFromThePinnedLibrary(t *testing.T) {
	env := testEnv(t)
	typed := fantasy.NewAgentTool("typed", "Takes a path.", func(_ context.Context, p kindProbeParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ran with " + p.Path), nil
	})
	call := func(id, name, input string) fantasy.StreamPart {
		return fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: id, ToolCallName: name, ToolCallInput: input}
	}
	model := &scriptedModel{script: []scriptedCall{
		{parts: []fantasy.StreamPart{
			call("unknown", "nope", `{}`),
			call("not-an-object", "typed", `[1]`),
			call("missing", "typed", `{}`),
			call("wrong-type", "typed", `{"path":5}`),
			call("not-json", "typed", ``),
			call("fine", "typed", `{"path":"a"}`),
			{
				Type:         fantasy.StreamPartTypeFinish,
				FinishReason: fantasy.FinishReasonToolCalls,
				Usage:        fantasy.Usage{InputTokens: 1000, OutputTokens: 10, TotalTokens: 1010},
			},
		}},
		answer("done", fantasy.FinishReasonStop, 1000),
	}}
	agent := testSessionAgent(env, model, model, "system", typed)
	sess, err := env.sessions.Create(t.Context(), "library kinds")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{Prompt: "go", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	results := map[string]message.ToolResult{}
	for _, m := range msgs {
		for _, tr := range m.ToolResults() {
			results[tr.ToolCallID] = tr
		}
	}

	tests := []struct {
		id     string
		prefix string
		want   tools.ErrorKind
	}{
		{id: "unknown", prefix: "tool not found: nope", want: tools.ErrorKindUnknownTool},
		{id: "not-an-object", prefix: "invalid JSON input: ", want: tools.ErrorKindInvalidArguments},
		{id: "missing", prefix: "missing required parameter: path", want: tools.ErrorKindInvalidArguments},
		{id: "wrong-type", prefix: "invalid parameters: ", want: tools.ErrorKindInvalidArguments},
		{id: "not-json", prefix: sanitizedToolCallResult, want: tools.ErrorKindInvalidArguments},
	}
	for _, tt := range tests {
		result, ok := results[tt.id]
		require.True(t, ok, "call %s has a stored result", tt.id)
		require.True(t, result.IsError, "call %s", tt.id)
		require.True(t, strings.HasPrefix(result.Content, tt.prefix), "call %s: the library wrote %q", tt.id, result.Content)
		require.Equal(t, string(tt.want), gjson.Get(result.Metadata, "error_kind").String(), "call %s", tt.id)
	}

	fine := results["fine"]
	require.False(t, fine.IsError)
	require.Equal(t, "ran with a", fine.Content)
	require.False(t, gjson.Get(fine.Metadata, "error_kind").Exists())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'TestStampLibraryErrorKind|TestLibraryErrorKindsFromThePinnedLibrary' -count=1`
Expected: build failure `undefined: stampLibraryErrorKind`, `undefined: sanitizedToolCallResult`, `undefined: tools.ErrorKindUnknownTool`, `undefined: tools.ErrorKindInvalidArguments`. (With the new file in place but `agent.go` not yet changed, `TestLibraryErrorKindsFromThePinnedLibrary` fails with `expected: "unknown_tool"`, `actual: ""`, `call unknown`.)

- [ ] **Step 3: Implement**

In `internal/agent/tools/error_kind.go`, replace

```go
	ErrorKindHookDenied ErrorKind = "hook_denied"
```

with

```go
	ErrorKindHookDenied ErrorKind = "hook_denied"
	// ErrorKindInvalidArguments is a call whose arguments are not valid
	// JSON, miss a required parameter or do not fit the tool's parameters.
	ErrorKindInvalidArguments ErrorKind = "invalid_arguments"
	// ErrorKindUnknownTool is a call to a tool that does not exist.
	ErrorKindUnknownTool ErrorKind = "unknown_tool"
```

Create `internal/agent/tool_result_kind.go`:

```go
package agent

import (
	"strings"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
)

// sanitizedToolCallResult replaces the result of a call whose arguments
// were not valid JSON.
const sanitizedToolCallResult = "Tool call failed: arguments were not valid JSON. Please check your tool call format and try again."

// libraryErrorKinds maps the start of each error charm.land/fantasy writes
// for a call it could not run to its kind. The wording is the library's,
// so tool_result_kind_test.go produces every one of them with the pinned
// version: a reworded message fails that test instead of the report.
var libraryErrorKinds = []struct {
	prefix string
	kind   tools.ErrorKind
}{
	{prefix: "tool not found: ", kind: tools.ErrorKindUnknownTool},
	{prefix: "invalid JSON input: ", kind: tools.ErrorKindInvalidArguments},
	{prefix: "missing required parameter: ", kind: tools.ErrorKindInvalidArguments},
	{prefix: "invalid parameters: ", kind: tools.ErrorKindInvalidArguments},
}

// stampLibraryErrorKind records the kind of an error the provider library
// wrote for a call it could not run. A result that is not an error, or
// that already carries a kind, is returned unchanged.
func stampLibraryErrorKind(result message.ToolResult) message.ToolResult {
	if !result.IsError || tools.MetadataErrorKind(result.Metadata) != "" {
		return result
	}
	for _, lib := range libraryErrorKinds {
		if strings.HasPrefix(result.Content, lib.prefix) {
			result.Metadata = tools.SetErrorKind(result.Metadata, lib.kind)
			break
		}
	}
	return result
}
```

In `internal/agent/agent.go`, `OnToolResult`, replace

```go
			toolResult := a.convertToToolResult(result)
			if _, ok := sanitizedToolCalls.Get(result.ToolCallID); ok {
				toolResult.Content = "Tool call failed: arguments were not valid JSON. Please check your tool call format and try again."
				toolResult.IsError = true
			}
```

with

```go
			toolResult := a.convertToToolResult(result)
			if _, ok := sanitizedToolCalls.Get(result.ToolCallID); ok {
				toolResult.Content = sanitizedToolCallResult
				toolResult.IsError = true
				toolResult.Metadata = tools.SetErrorKind(toolResult.Metadata, tools.ErrorKindInvalidArguments)
			}
			toolResult = stampLibraryErrorKind(toolResult)
```

(`tools` is already imported in `agent.go`.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/... -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent/tools/error_kind.go internal/agent/tool_result_kind.go internal/agent/tool_result_kind_test.go internal/agent/agent.go
git add internal/agent/tools/error_kind.go internal/agent/tool_result_kind.go internal/agent/tool_result_kind_test.go internal/agent/agent.go
git commit -m "feat(agent): record calls the provider library could not run"
```

---

### Task 4: Whole-branch verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything above.
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/agent/... ./internal/ui/chat/ && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m ./internal/agent/... ./internal/ui/chat/...`, `./scripts/check_log_capitalization.sh`, and `/home/pbtrudel/.local/bin/gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD` and `git diff origin/main...HEAD`
Expected: about 500 changed lines, in the files named in Tasks 1 to 3 only; `git diff origin/main...HEAD | grep -c $'\u2014'` prints 0 (no em dash); no change to any text a tool returns (the sanitized-call text only moved into `sanitizedToolCallResult`).

- [ ] **Step 4: Live check**

What a live `crush run` can show: `invalid_arguments` and `unknown_tool`, only if the model complies with a request to make a malformed call, so both are best effort. What it cannot show: `permission_denied` (`crush run` auto-approves its session, `internal/app/app.go:454`), `hook_denied` (needs a hook in a config file, which this check does not write), and `interrupted` (needs a cancel or a crash at the right moment; the in-process `crush run --continue` does not settle stored calls and returns on a signal without waiting). The unit tests cover all five.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" . && mkdir "$scratch/proj" && printf 'hello\n' > "$scratch/proj/a.txt"
"$scratch/crush" run -q -c "$scratch/proj" "This is a test of tool error handling. Do exactly two things, then reply done. First, call the glob tool with an empty arguments object, {}, leaving out the pattern on purpose. Second, call a tool named does_not_exist with the arguments {}."
python3 -I - "$scratch/proj/.crush/crush.db" <<'EOF'
import json, sqlite3, sys

conn = sqlite3.connect('file:' + sys.argv[1] + '?mode=ro', uri=True)
for (parts,) in conn.execute("select parts from messages where role='tool'"):
    for part in json.loads(parts):
        if part['type'] != 'tool_result':
            continue
        data = part['data']
        raw = data.get('metadata') or ''
        meta = json.loads(raw) if raw.startswith('{') else {}
        print(data.get('name'), data.get('is_error'), meta.get('error_kind'), (data.get('content') or '')[:60])
EOF
rm -rf "$scratch"
```

Expected: a `glob` result with `is_error` true, `error_kind` `invalid_arguments` and content starting `missing required parameter: pattern`, and a `does_not_exist` result with `error_kind` `unknown_tool` and content starting `tool not found: does_not_exist`. A model or provider that refuses the malformed call, or enforces the schema itself, leaves no such row; report what happened rather than prompting again. Record the output in the pull request.

The pull request is opened with the `pr-discipline` skill only when the user asks for it.

---

## Self-Review

1. **Spec coverage.** Spec 1.5 lists nine kinds; part A shipped four; Tasks 1 to 3 ship `permission_denied` (Task 1), `hook_denied` and `interrupted` (Task 2), `invalid_arguments` and `unknown_tool` (Task 3). "Set by the code that produces the error": Tasks 1 and 2, and the sanitized branch in Task 3. "At the one place every result passes before it is stored, from the library's message prefixes" and "a test produces each of those errors with the pinned library": Task 3. Failure behavior (a stamp never fails a call): `SetErrorKind` is part A's function, renamed. Validation (a test for each kind where it is produced): every task. Scope (no prompt or tool text change): byte-for-byte assertions in every task. No gap found.
2. **Placeholder scan.** Every code step carries its code; no "TBD", no "similar to Task N"; the six denial sites are each written out.
3. **Type consistency.** `SetErrorKind(string, ErrorKind) string` and `MetadataErrorKind(string) ErrorKind` are defined in Task 2 and used in Task 3; `ErrorKindHookDenied` is the anchor Task 3 inserts after; `permissionDeniedWithMetadata(any) fantasy.ToolResponse` is defined and used only in Task 1; `sanitizedToolCallResult` and `stampLibraryErrorKind` are defined and tested in Task 3.
4. **Review Focus.** Each of the five lines has its test in the owning task: 1 in Task 1, 2 in Tasks 2 and 3, 3 and 4 in Task 3, 5 in Task 2.
