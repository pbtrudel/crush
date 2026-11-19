# Tool Stamps, Part A (Shell Exit Code and Edit Error Kinds) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record in the session log the exit code of every shell command the tool waited for, and the error kind of every refused or failed `edit`, `multiedit` and `write` (the first half of delivery item 3 of the measurement design).

**Architecture:** The shell tool's result metadata gains `exit_code` (zero included) and `interrupted`, computed by one small function from the error the shell returned. A new file `internal/agent/tools/error_kind.go` holds the closed `ErrorKind` type, the four kinds this pull request produces, and a helper that writes `error_kind` into a result's metadata without disturbing other keys. The read guards shared by `edit` and `multiedit`, the read guard in `write`, and the two find-and-replace failures set their kind where the error is produced; `multiedit` also records the kind on each failed edit of a batch.

**Tech Stack:** Go 1.27, `charm.land/fantasy` v0.45.5, `github.com/tidwall/gjson` and `github.com/tidwall/sjson` (already direct dependencies), `mvdan.cc/sh/v3`, testify `require`.

**Spec:** `docs/superpowers/specs/2026-11-17-measurement-design.md`, sections 1.4 (Shell exit code), 1.5 (Error kind), Failure behavior, Validation, Delivery item 3. PR 1 (`28b5513d`) and PR 2 (`1321284a`) are merged. Code references below are to `1321284a`.

## Global Constraints

- Work in a fresh worktree created from `origin/main` after `git fetch origin` (for example `worktree create` with name `w0-tool-stamps-a`, base `origin/main`). Paths below are relative to its root.
- Delivery item 3 is split in two pull requests to stay near the 400-line target. This one (part A) adds `exit_code`, `interrupted` and the kinds `not_read`, `changed_since_read`, `text_not_found`, `multiple_matches`. Part B adds `permission_denied`, `hook_denied`, `invalid_arguments`, `unknown_tool` and `interrupted` (the error kind). Do not add part B's kinds here.
- `exit_code` is in the shell tool's result metadata (`BashResponseMetadata`), JSON `exit_code,omitempty` on a `*int`, so a zero exit is stored. It is set for every command the tool waited for and that finished, including a command started with `run_in_background` that finished inside the one-second probe window (its result reports the exit inline, like a foreground command). It is not set for a command moved to the background, nor for a result produced before the command ran. `interrupted` (JSON `interrupted,omitempty`) is set when the command was aborted; such a command has no `exit_code`.
- `error_kind` is a string key in the tool result's metadata JSON, next to the `hook` key hooks already add. Writing it keeps every other key. Metadata that is not a JSON object is left unchanged.
- Kinds, exactly: `not_read` (the edit read guard when the file was never read; `write` when its file exists and was never read), `changed_since_read` (the edit and write read guards when the file changed after it was read), `text_not_found` and `multiple_matches` (a find-and-replace failure in `edit`, or in one edit of a `multiedit` batch). The list is closed: any other failure carries no kind.
- A `multiedit` call whose edits all failed takes the first failed edit's kind at the top level; each failed edit also carries its own kind in `edits_failed[].error_kind`. A `multiedit` call that applied at least one edit has no top-level kind.
- The text the model reads is unchanged, byte for byte, including the `write` message that says "modified since it was last read" for a file that was never read (spec Evidence 5; rewording is out of scope).
- Writing a stamp never fails a tool call: the helper returns the metadata unchanged when it cannot set the key.
- Known visible effect: an `edit` or `multiedit` error that had no metadata now has `{"error_kind":...}`, so the TUI renders it through its existing error-line branch (`internal/ui/chat/file.go:244-252`), the way it already renders the same error for users with a hook configured (`internal/ui/chat/edit_error_test.go`). No UI code changes.
- Style: gofumpt; comments on their own line start with a capital, end with a period, wrap at 78 columns; no em dashes; testify `require`; log messages start with a capital letter. Shell tool tests do not call `t.Parallel()` (the existing ones do not; they share the background shell manager).
- Size: estimate about 400 changed lines, about 110 of them production code.
- Commits: one per task, semantic subjects. Run commit steps only when the user has authorized commits for this work; otherwise stop at the staged diff.

## Review Focus

1. Metadata set after the kind: `fantasy.WithResponseMetadata` replaces the whole metadata string, so a kind must be applied after it, never before. Pinned in Task 3 by the all-failed `multiedit` test, which asserts both `edits_failed` and `error_kind`.
2. Hooks merge their metadata into the tool's (`mergeHookMetadata`, `internal/agent/hooked_tool.go`), so a kind set by a tool must survive a hook. Pinned in Task 2.
3. The model must read exactly what it read before. Pinned by exact or prefix message assertions in Tasks 2 and 3.
4. A zero exit must be stored, not omitted. Pinned in Task 1 by checking that the key exists.
5. Metadata from other tools may not be a JSON object (an MCP tool can return anything). The helper must leave it unchanged. Pinned in Task 2.

---

### Task 1: Shell exit code

**Files:**
- Modify: `internal/agent/tools/bash.go` (`BashResponseMetadata`; the two `if done {` branches of `executeBashCommand`; a new function after `formatOutput`)
- Test: `internal/agent/tools/bash_exit_code_test.go` (new)

**Interfaces:**
- Consumes: `shell.IsInterrupt(err error) bool`, `shell.ExitCode(err error) int` (`internal/shell/shell.go:520-534`).
- Produces: `BashResponseMetadata.ExitCode *int` (JSON `exit_code,omitempty`), `BashResponseMetadata.Interrupted bool` (JSON `interrupted,omitempty`), `func commandOutcome(execErr error) (exitCode *int, interrupted bool)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/tools/bash_exit_code_test.go`:

```go
package tools

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"mvdan.cc/sh/v3/interp"
)

func TestCommandOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantCode    *int
		interrupted bool
	}{
		{name: "success", err: nil, wantCode: new(0)},
		{name: "exit status", err: interp.ExitStatus(2), wantCode: new(2)},
		{name: "other error", err: errors.New("boom"), wantCode: new(1)},
		{name: "cancelled", err: context.Canceled, interrupted: true},
		{name: "deadline", err: fmt.Errorf("run: %w", context.DeadlineExceeded), interrupted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, interrupted := commandOutcome(tt.err)
			require.Equal(t, tt.wantCode, code)
			require.Equal(t, tt.interrupted, interrupted)
		})
	}
}

func TestBashTool_ExitCodeInMetadata(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tests := []struct {
		name   string
		params BashParams
		want   int64
	}{
		{name: "success", params: BashParams{Description: "succeeds", Command: "echo ok"}, want: 0},
		{name: "failure", params: BashParams{Description: "fails", Command: "exit 3"}, want: 3},
		{name: "background command finished in the probe window", params: BashParams{Description: "fails fast", Command: "exit 4", RunInBackground: true}, want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := runBashTool(t, tool, ctx, tt.params)
			if shellID := gjson.Get(resp.Metadata, "shell_id").String(); shellID != "" {
				defer shell.GetBackgroundShellManager().Kill(shellID)
			}

			code := gjson.Get(resp.Metadata, "exit_code")
			require.True(t, code.Exists(), "a finished command records its exit code, zero included: %s", resp.Metadata)
			require.Equal(t, tt.want, code.Int())
			require.False(t, gjson.Get(resp.Metadata, "interrupted").Exists())
			if tt.want != 0 {
				require.Contains(t, resp.Content, fmt.Sprintf("Exit code %d", tt.want), "the text the model reads is unchanged")
			}
		})
	}
}

func TestBashTool_MovedToBackgroundHasNoExitCode(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:         "moved to background",
		Command:             "sleep 1.5 && exit 5",
		AutoBackgroundAfter: 1,
	})

	require.Contains(t, resp.Content, "moved to background")
	require.False(t, gjson.Get(resp.Metadata, "exit_code").Exists(), "a background job reports through its notice")
	require.False(t, gjson.Get(resp.Metadata, "interrupted").Exists())
	require.NoError(t, shell.GetBackgroundShellManager().Kill(gjson.Get(resp.Metadata, "shell_id").String()))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/tools/ -run 'TestCommandOutcome|TestBashTool_ExitCodeInMetadata|TestBashTool_MovedToBackgroundHasNoExitCode' -count=1`
Expected: build failure `undefined: commandOutcome`. `TestBashTool_MovedToBackgroundHasNoExitCode` pins existing behavior and passes as soon as the package builds; the other two fail until Step 3 is complete.

- [ ] **Step 3: Implement**

In `internal/agent/tools/bash.go`, replace

```go
	Background       bool   `json:"background,omitempty"`
	ShellID          string `json:"shell_id,omitempty"`
}
```

with

```go
	Background       bool   `json:"background,omitempty"`
	ShellID          string `json:"shell_id,omitempty"`
	// ExitCode is set for every command the tool waited for and that
	// finished, zero included. Interrupted marks one that was aborted,
	// which has no exit code.
	ExitCode    *int `json:"exit_code,omitempty"`
	Interrupted bool `json:"interrupted,omitempty"`
}
```

In the `RunInBackground` branch, replace

```go
				WorkingDirectory: bgShell.WorkingDir,
				ShellID:          bgShell.ID,
			}
			if stdout == "" {
```

with

```go
				WorkingDirectory: bgShell.WorkingDir,
				ShellID:          bgShell.ID,
			}
			metadata.ExitCode, metadata.Interrupted = commandOutcome(execErr)
			if stdout == "" {
```

In the synchronous `if done {` branch, replace

```go
			Background:       params.RunInBackground,
			WorkingDirectory: bgShell.WorkingDir,
		}
		if stdout == "" {
```

with

```go
			Background:       params.RunInBackground,
			WorkingDirectory: bgShell.WorkingDir,
		}
		metadata.ExitCode, metadata.Interrupted = commandOutcome(execErr)
		if stdout == "" {
```

Directly after the closing brace of `formatOutput`, add

```go

// commandOutcome reports how a finished command ended, for the result
// metadata: its exit status, or that it was aborted, which has none.
func commandOutcome(execErr error) (exitCode *int, interrupted bool) {
	if shell.IsInterrupt(execErr) {
		return nil, true
	}
	return new(shell.ExitCode(execErr)), false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/tools/ -run 'TestCommandOutcome|TestBashTool_' -count=1`
Expected: PASS, including the existing `TestBashTool_*` tests.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent/tools/bash.go internal/agent/tools/bash_exit_code_test.go
git add internal/agent/tools/bash.go internal/agent/tools/bash_exit_code_test.go
git commit -m "feat(tools): record the shell exit code in the result metadata"
```

---

### Task 2: The error-kind value and the read guards

**Files:**
- Create: `internal/agent/tools/error_kind.go`
- Modify: `internal/agent/tools/edit.go` (`loadExistingFile`), `internal/agent/tools/write.go` (the read guard in `NewWriteTool`)
- Test: `internal/agent/tools/error_kind_test.go` (new), `internal/agent/hook_metadata_kind_test.go` (new)

**Interfaces:**
- Consumes: `mockEditFileTracker` (`internal/agent/tools/edit_test.go:16-31`), `mockPermissionService` and `mockHistoryService` (`internal/agent/tools/multiedit_test.go`), `testutil.MockPermissionService` (`internal/permission/testutil`), `mergeHookMetadata` (`internal/agent/hooked_tool.go`).
- Produces: `type ErrorKind string`; `ErrorKindNotRead` (`"not_read"`), `ErrorKindChangedSinceRead` (`"changed_since_read"`); `func setErrorKind(metadata string, kind ErrorKind) string`; `func withErrorKind(resp fantasy.ToolResponse, kind ErrorKind) fantasy.ToolResponse`; test helper `func editContextForKind(t *testing.T, dir string, lastRead time.Time) editContext` (in `error_kind_test.go`, used again by Task 3).

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/tools/error_kind_test.go`:

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
	"github.com/charmbracelet/crush/internal/permission/testutil"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSetErrorKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata string
		want     string
	}{
		{name: "no metadata", metadata: "", want: `{"error_kind":"not_read"}`},
		{name: "keeps other keys", metadata: `{"additions":2}`, want: `{"additions":2,"error_kind":"not_read"}`},
		{name: "replaces an earlier kind", metadata: `{"error_kind":"changed_since_read"}`, want: `{"error_kind":"not_read"}`},
		{name: "not an object", metadata: `["a"]`, want: `["a"]`},
		{name: "not JSON", metadata: "plain text", want: "plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, setErrorKind(tt.metadata, ErrorKindNotRead))
		})
	}
}

// editContextForKind builds an edit context whose tracker reports lastRead
// for every file; the zero time means the file was never read.
func editContextForKind(t *testing.T, dir string, lastRead time.Time) editContext {
	t.Helper()
	return editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: &mockEditFileTracker{lastRead: lastRead},
		workingDir:  dir,
	}
}

func TestEditReadGuardKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lastRead time.Time
		want     ErrorKind
		message  string
	}{
		{name: "never read", want: ErrorKindNotRead, message: "you must read the file before editing it. Use the View tool first"},
		{name: "changed since read", lastRead: time.Now().Add(-time.Hour), want: ErrorKindChangedSinceRead, message: "has been modified since it was last read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := filepath.Join(dir, "a.txt")
			require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))
			edit := editContextForKind(t, dir, tt.lastRead)

			resp, err := replaceContent(edit, filePath, "alpha", "beta", false, fantasy.ToolCall{ID: "call"})
			require.NoError(t, err)
			require.True(t, resp.IsError)
			require.Contains(t, resp.Content, tt.message)
			require.Equal(t, string(tt.want), gjson.Get(resp.Metadata, "error_kind").String())

			// multiedit goes through the same guard.
			multi, err := processMultiEditExistingFile(edit, MultiEditParams{
				FilePath: filePath,
				Edits:    []MultiEditOperation{{OldString: "alpha", NewString: "beta"}},
			}, fantasy.ToolCall{ID: "call"})
			require.NoError(t, err)
			require.True(t, multi.IsError)
			require.Equal(t, string(tt.want), gjson.Get(multi.Metadata, "error_kind").String())
		})
	}
}

func TestWriteReadGuardKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lastRead time.Time
		want     ErrorKind
	}{
		{name: "never read", want: ErrorKindNotRead},
		{name: "changed since read", lastRead: time.Now().Add(-time.Hour), want: ErrorKindChangedSinceRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := filepath.Join(dir, "a.txt")
			require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))
			tool := NewWriteTool(nil, &testutil.MockPermissionService{}, &mockHistoryService{}, &mockEditFileTracker{lastRead: tt.lastRead}, dir)
			input, err := json.Marshal(WriteParams{FilePath: "a.txt", Content: "beta\n"})
			require.NoError(t, err)

			resp, err := tool.Run(context.WithValue(t.Context(), SessionIDContextKey, "session"), fantasy.ToolCall{
				ID:    "call",
				Name:  WriteToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError)
			// The wording is kept for both cases; only the kind tells them apart.
			require.Contains(t, resp.Content, "has been modified since it was last read")
			require.Equal(t, string(tt.want), gjson.Get(resp.Metadata, "error_kind").String())

			content, err := os.ReadFile(filePath)
			require.NoError(t, err)
			require.Equal(t, "alpha\n", string(content))
		})
	}
}
```

Create `internal/agent/hook_metadata_kind_test.go`:

```go
package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Hooks merge their metadata into the tool's, so a kind the tool recorded
// must survive a hook.
func TestMergeHookMetadataKeepsErrorKind(t *testing.T) {
	t.Parallel()

	merged := mergeHookMetadata(`{"error_kind":"not_read"}`, hooks.AggregateResult{HookCount: 1, Decision: hooks.DecisionAllow})
	require.Equal(t, "not_read", gjson.Get(merged, "error_kind").String())
	require.True(t, gjson.Get(merged, "hook").Exists())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/tools/ -run 'TestSetErrorKind|TestEditReadGuardKinds|TestWriteReadGuardKinds' -count=1` and `go test ./internal/agent/ -run TestMergeHookMetadataKeepsErrorKind -count=1`
Expected: the tools package fails to build with `undefined: setErrorKind` and `undefined: ErrorKindNotRead`. The agent test passes: it pins existing behavior.

- [ ] **Step 3: Implement**

Create `internal/agent/tools/error_kind.go`:

```go
package tools

import (
	"charm.land/fantasy"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ErrorKind classifies a failed tool result for the session log. It is
// stored under "error_kind" in the result's metadata, which is never part
// of a provider request. The list is closed: a failure outside it carries
// no kind, and adding one is a code change with a test.
type ErrorKind string

const (
	// ErrorKindNotRead is an edit or write refused because the file was
	// never read in the session.
	ErrorKindNotRead ErrorKind = "not_read"
	// ErrorKindChangedSinceRead is an edit or write refused because the
	// file changed after it was last read.
	ErrorKindChangedSinceRead ErrorKind = "changed_since_read"
)

const errorKindKey = "error_kind"

// setErrorKind returns metadata with kind stored under "error_kind",
// keeping every other key. Metadata that is not a JSON object is returned
// unchanged, because a stamp must never alter or break a result.
func setErrorKind(metadata string, kind ErrorKind) string {
	if metadata == "" {
		metadata = "{}"
	}
	if !gjson.Valid(metadata) || !gjson.Parse(metadata).IsObject() {
		return metadata
	}
	updated, err := sjson.Set(metadata, errorKindKey, string(kind))
	if err != nil {
		return metadata
	}
	return updated
}

// withErrorKind records kind in a tool response's metadata. Call it after
// any fantasy.WithResponseMetadata, which replaces the metadata whole.
func withErrorKind(resp fantasy.ToolResponse, kind ErrorKind) fantasy.ToolResponse {
	resp.Metadata = setErrorKind(resp.Metadata, kind)
	return resp
}
```

In `internal/agent/tools/edit.go`, `loadExistingFile`, replace

```go
	if lastRead.IsZero() {
		return "", "", false, fantasy.NewTextErrorResponse("you must read the file before editing it. Use the View tool first"), nil
	}
```

with

```go
	if lastRead.IsZero() {
		return "", "", false, withErrorKind(fantasy.NewTextErrorResponse("you must read the file before editing it. Use the View tool first"), ErrorKindNotRead), nil
	}
```

and replace

```go
		return "", "", false, fantasy.NewTextErrorResponse(
			fmt.Sprintf(
				"file %s has been modified since it was last read (mod time: %s, last read: %s)",
				filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339),
			),
		), nil
```

with

```go
		return "", "", false, withErrorKind(fantasy.NewTextErrorResponse(
			fmt.Sprintf(
				"file %s has been modified since it was last read (mod time: %s, last read: %s)",
				filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339),
			),
		), ErrorKindChangedSinceRead), nil
```

In `internal/agent/tools/write.go`, replace

```go
				if modTime.After(lastRead) {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("File %s has been modified since it was last read.\nLast modification: %s\nLast read: %s\n\nPlease read the file again before modifying it.",
						filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339))), nil
				}
```

with

```go
				if modTime.After(lastRead) {
					// A file that was never read gets the same message, which
					// is wrong for it; the wording stays, and the kind tells
					// the two cases apart in the session log.
					kind := ErrorKindChangedSinceRead
					if lastRead.IsZero() {
						kind = ErrorKindNotRead
					}
					return withErrorKind(fantasy.NewTextErrorResponse(fmt.Sprintf("File %s has been modified since it was last read.\nLast modification: %s\nLast read: %s\n\nPlease read the file again before modifying it.",
						filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339))), kind), nil
				}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/tools/ -count=1` and `go test ./internal/agent/ -run TestMergeHookMetadataKeepsErrorKind -count=1`
Expected: PASS (the whole tools package, so the existing edit, multiedit and write tests confirm nothing else changed).

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent/tools/error_kind.go internal/agent/tools/error_kind_test.go internal/agent/tools/edit.go internal/agent/tools/write.go internal/agent/hook_metadata_kind_test.go
git add internal/agent/tools/error_kind.go internal/agent/tools/error_kind_test.go internal/agent/tools/edit.go internal/agent/tools/write.go internal/agent/hook_metadata_kind_test.go
git commit -m "feat(tools): record why the read guards refused an edit or write"
```

---

### Task 3: Match failures in edit and multiedit

**Files:**
- Modify: `internal/agent/tools/error_kind.go` (two constants), `internal/agent/tools/edit.go` (`findAndReplace`, `notFoundError`, `deleteContent`, `replaceContent`, two new functions and one type), `internal/agent/tools/multiedit.go` (`FailedEdit`, `applyEditsToContent`, the all-failed return in `processMultiEditExistingFile`)
- Test: `internal/agent/tools/edit_match_kind_test.go` (new)

**Interfaces:**
- Consumes: `ErrorKind`, `withErrorKind` (Task 2); `editContextForKind(t, dir, lastRead)` (Task 2, `error_kind_test.go`).
- Produces: `ErrorKindTextNotFound` (`"text_not_found"`), `ErrorKindMultipleMatches` (`"multiple_matches"`); `type editMatchError struct { kind ErrorKind; msg string }`; `func errorKindOf(err error) ErrorKind`; `func editErrorResponse(err error) fantasy.ToolResponse`; `FailedEdit.Kind ErrorKind` (JSON `error_kind,omitempty`).

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/tools/edit_match_kind_test.go`:

```go
package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	notFoundMessage       = "old_string not found in file. Make sure it matches exactly, including whitespace and line breaks"
	multipleMatchMessage  = "old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true"
)

func TestFindAndReplaceKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		old        string
		replaceAll bool
		want       ErrorKind
		message    string
	}{
		{name: "not found", content: "alpha\n", old: "gamma", want: ErrorKindTextNotFound, message: notFoundMessage},
		{name: "not found with replace_all", content: "alpha\n", old: "gamma", replaceAll: true, want: ErrorKindTextNotFound, message: notFoundMessage},
		{name: "multiple matches", content: "alpha\nalpha\n", old: "alpha", want: ErrorKindMultipleMatches, message: multipleMatchMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := findAndReplace(tt.content, tt.old, "x", tt.replaceAll)
			require.Error(t, err)
			require.True(t, strings.HasPrefix(err.Error(), tt.message), "the model reads the same text as before: %q", err.Error())
			require.Equal(t, tt.want, errorKindOf(err))
		})
	}
}

func TestErrorKindOf(t *testing.T) {
	t.Parallel()

	require.Equal(t, ErrorKind(""), errorKindOf(errors.New("failed to write file")))
	require.Equal(t, ErrorKind(""), errorKindOf(nil))
	wrapped := fmt.Errorf("edit 2: %w", &editMatchError{kind: ErrorKindTextNotFound, msg: "missing"})
	require.Equal(t, ErrorKindTextNotFound, errorKindOf(wrapped))
}

func TestEditMatchFailureKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		delete bool
		old    string
		want   ErrorKind
	}{
		{name: "replace, not found", old: "gamma", want: ErrorKindTextNotFound},
		{name: "replace, multiple matches", old: "alpha\n", want: ErrorKindMultipleMatches},
		{name: "delete, not found", delete: true, old: "gamma", want: ErrorKindTextNotFound},
		{name: "delete, multiple matches", delete: true, old: "alpha\n", want: ErrorKindMultipleMatches},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := filepath.Join(dir, "a.txt")
			require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\nalpha\n"), 0o644))
			edit := editContextForKind(t, dir, time.Now().Add(time.Second))

			var resp fantasy.ToolResponse
			var err error
			if tt.delete {
				resp, err = deleteContent(edit, filePath, tt.old, false, fantasy.ToolCall{ID: "call"})
			} else {
				resp, err = replaceContent(edit, filePath, tt.old, "x", false, fantasy.ToolCall{ID: "call"})
			}
			require.NoError(t, err)
			require.True(t, resp.IsError)
			require.Equal(t, string(tt.want), gjson.Get(resp.Metadata, "error_kind").String())
		})
	}
}

func TestMultiEditFailedEditKinds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\ntwo\ntwo\n"), 0o644))

	resp, err := processMultiEditExistingFile(editContextForKind(t, dir, time.Now().Add(time.Second)), MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "one", NewString: "ONE"},
			{OldString: "missing", NewString: "MISSING"},
			{OldString: "two", NewString: "TWO"},
		},
	}, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	var meta MultiEditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Len(t, meta.EditsFailed, 2)
	require.Equal(t, 2, meta.EditsFailed[0].Index)
	require.Equal(t, ErrorKindTextNotFound, meta.EditsFailed[0].Kind)
	require.Equal(t, 3, meta.EditsFailed[1].Index)
	require.Equal(t, ErrorKindMultipleMatches, meta.EditsFailed[1].Kind)
	require.False(t, gjson.Get(resp.Metadata, "error_kind").Exists(), "a call that applied an edit has no kind of its own")
}

func TestMultiEditAllFailedTakesFirstKind(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\none\n"), 0o644))

	resp, err := processMultiEditExistingFile(editContextForKind(t, dir, time.Now().Add(time.Second)), MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "one", NewString: "ONE"},
			{OldString: "missing", NewString: "MISSING"},
		},
	}, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Equal(t, "no changes made - all 2 edit(s) failed", resp.Content)
	require.Equal(t, string(ErrorKindMultipleMatches), gjson.Get(resp.Metadata, "error_kind").String())
	require.Equal(t, int64(2), gjson.Get(resp.Metadata, "edits_failed.#").Int(), "the kind must not replace the batch's metadata")
	require.Equal(t, string(ErrorKindTextNotFound), gjson.Get(resp.Metadata, "edits_failed.1.error_kind").String())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/tools/ -run 'TestFindAndReplaceKinds|TestErrorKindOf|TestEditMatchFailureKinds|TestMultiEditFailedEditKinds|TestMultiEditAllFailedTakesFirstKind' -count=1`
Expected: build failure `undefined: ErrorKindTextNotFound`, `undefined: errorKindOf`, `undefined: editMatchError`, `meta.EditsFailed[0].Kind undefined`.

- [ ] **Step 3: Implement**

In `internal/agent/tools/error_kind.go`, replace

```go
	ErrorKindChangedSinceRead ErrorKind = "changed_since_read"
)
```

with

```go
	ErrorKindChangedSinceRead ErrorKind = "changed_since_read"
	// ErrorKindTextNotFound is a find-and-replace whose text is not in the
	// file.
	ErrorKindTextNotFound ErrorKind = "text_not_found"
	// ErrorKindMultipleMatches is a find-and-replace whose text appears more
	// than once while replace_all is off.
	ErrorKindMultipleMatches ErrorKind = "multiple_matches"
)
```

In `internal/agent/tools/edit.go`, `findAndReplace`, replace

```go
			return "", false, fmt.Errorf("old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true")
```

with

```go
			return "", false, &editMatchError{kind: ErrorKindMultipleMatches, msg: "old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true"}
```

In `notFoundError`, replace

```go
	return errors.New(msg)
}
```

with

```go
	return &editMatchError{kind: ErrorKindTextNotFound, msg: msg}
}

// editMatchError is a find-and-replace that could not be applied. The
// message is what the model reads; the kind is what the session log
// records, so a report never has to match the wording.
type editMatchError struct {
	kind ErrorKind
	msg  string
}

func (e *editMatchError) Error() string { return e.msg }

// errorKindOf returns the kind of a failed edit, or "" for a failure the
// session log does not classify.
func errorKindOf(err error) ErrorKind {
	if matchErr, ok := errors.AsType[*editMatchError](err); ok {
		return matchErr.kind
	}
	return ""
}

// editErrorResponse turns a failed edit into its response, recording the
// failure's kind when it has one.
func editErrorResponse(err error) fantasy.ToolResponse {
	resp := fantasy.NewTextErrorResponse(err.Error())
	if kind := errorKindOf(err); kind != "" {
		resp = withErrorKind(resp, kind)
	}
	return resp
}
```

In `deleteContent`, replace

```go
	newContent, whitespaceCorrected, err := findAndReplace(oldContent, oldString, "", replaceAll)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
```

with

```go
	newContent, whitespaceCorrected, err := findAndReplace(oldContent, oldString, "", replaceAll)
	if err != nil {
		return editErrorResponse(err), nil
	}
```

In `replaceContent`, replace

```go
	result, whitespaceCorrected, err := findAndReplace(oldContent, oldString, newString, replaceAll)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
```

with

```go
	result, whitespaceCorrected, err := findAndReplace(oldContent, oldString, newString, replaceAll)
	if err != nil {
		return editErrorResponse(err), nil
	}
```

In `internal/agent/tools/multiedit.go`, replace

```go
type FailedEdit struct {
	Index int                `json:"index"`
	Error string             `json:"error"`
	Edit  MultiEditOperation `json:"edit"`
}
```

with

```go
type FailedEdit struct {
	Index int                `json:"index"`
	Error string             `json:"error"`
	Kind  ErrorKind          `json:"error_kind,omitempty"`
	Edit  MultiEditOperation `json:"edit"`
}
```

In `applyEditsToContent`, replace

```go
			failedEdits = append(failedEdits, FailedEdit{
				Index: startIndex + i + 1,
				Error: err.Error(),
				Edit:  edit,
			})
```

with

```go
			failedEdits = append(failedEdits, FailedEdit{
				Index: startIndex + i + 1,
				Error: err.Error(),
				Kind:  errorKindOf(err),
				Edit:  edit,
			})
```

In `processMultiEditExistingFile`, replace

```go
		if len(failedEdits) > 0 {
			return fantasy.WithResponseMetadata(
				fantasy.NewTextErrorResponse(fmt.Sprintf("no changes made - all %d edit(s) failed", len(failedEdits))),
				MultiEditResponseMetadata{
					EditsApplied: 0,
					EditsFailed:  failedEdits,
				},
			), nil
		}
```

with

```go
		if len(failedEdits) > 0 {
			resp := fantasy.WithResponseMetadata(
				fantasy.NewTextErrorResponse(fmt.Sprintf("no changes made - all %d edit(s) failed", len(failedEdits))),
				MultiEditResponseMetadata{
					EditsApplied: 0,
					EditsFailed:  failedEdits,
				},
			)
			// The call failed because its edits did, so it takes the first
			// failed edit's kind. It is set after the metadata, which would
			// otherwise replace it.
			if kind := failedEdits[0].Kind; kind != "" {
				resp = withErrorKind(resp, kind)
			}
			return resp, nil
		}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/tools/ -count=1` and `go test ./internal/ui/chat/ -count=1`
Expected: PASS (the UI package confirms the edit renderers still handle these results, including `TestEditErrorWithoutDiffHasNoTrailingBlank`).

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent/tools/error_kind.go internal/agent/tools/edit.go internal/agent/tools/multiedit.go internal/agent/tools/edit_match_kind_test.go
git add internal/agent/tools/error_kind.go internal/agent/tools/edit.go internal/agent/tools/multiedit.go internal/agent/tools/edit_match_kind_test.go
git commit -m "feat(tools): record which match failure stopped an edit"
```

---

### Task 4: Whole-branch verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything above.
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/agent/... && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m ./internal/agent/...`, `./scripts/check_log_capitalization.sh`, and `gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD` and `git diff origin/main...HEAD`
Expected: about 400 changed lines in the files named in Tasks 1 to 3 only; no debug output, no em dashes; no change to any text a tool returns.

- [ ] **Step 4: Live check of both stamps**

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" . && mkdir "$scratch/proj" && printf 'hello\n' > "$scratch/proj/a.txt"
"$scratch/crush" run -y -q -c "$scratch/proj" "Do exactly two things, then reply done. First, use the bash tool to run the command: exit 3. Second, without reading it first, use the write tool to replace the contents of a.txt with the word bye."
python3 -I - "$scratch/proj/.crush/crush.db" <<'EOF'
import json, sqlite3, sys

conn = sqlite3.connect('file:' + sys.argv[1] + '?mode=ro', uri=True)
for (parts,) in conn.execute("select parts from messages where role='tool'"):
    for part in json.loads(parts):
        if part['type'] != 'tool_result':
            continue
        raw = part['data'].get('metadata') or ''
        meta = json.loads(raw) if raw.startswith('{') else {}
        print(part['data'].get('name'), meta.get('exit_code'), meta.get('error_kind'))
EOF
rm -rf "$scratch"
```

Expected: a `bash` result with `exit_code` 3, and a `write` result with `error_kind` `not_read`. If the model read the file before writing, there is no `not_read` result; report that as it happened rather than prompting again, since the unit tests cover the kind. Record the output in the pull request.

The pull request is opened with the `pr-discipline` skill only when the user asks for it.
