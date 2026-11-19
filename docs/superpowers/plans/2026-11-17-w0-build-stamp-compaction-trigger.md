# Build Stamp and Compaction Trigger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record in the session log which Crush build wrote each assistant message and what started each compaction (delivery item 1 of the measurement design).

**Architecture:** A pure `version.BuildStamp()` names the running build. The message package gains two optional fields (`Finish.Build`, `CompactionStats.Trigger`) and a `SetBuild` setter that mirrors `SetRequestUsage`. The agent calls `SetBuild` at every point where it writes a finish part, except the crash repair in `settle.go`. A typed `compactionTrigger` replaces the `trigger string` parameter of `summarize`; hooks keep receiving `auto` or `manual` through one mapping method.

**Tech Stack:** Go 1.27, `charm.land/fantasy` v0.45.5, testify `require`, SQLite through the existing message service.

**Spec:** `docs/superpowers/specs/2026-11-17-measurement-design.md`, sections 1.1 (Build), 1.3 (Compaction trigger), Validation, and Delivery item 1. The roadmap is `docs/superpowers/plans/2026-11-17-model-intelligence-roadmap.md` (W0.1).

## Global Constraints

- Work in a fresh worktree created from `origin/main` after `git fetch origin`, for example `worktree create` with name `w0-build-stamp` and base `origin/main`. Every path below is relative to that worktree's root.
- Neither new field is ever sent to a provider. The finish part and the compaction statistics are display and log metadata only.
- Both new JSON fields are `omitempty`: `build` on the finish part, `trigger` on the compaction statistics. Rows written before this change decode with empty strings.
- Build stamp values: `version.Version`, with `"." + version.BuildID` appended when the version is `devel` or ends in `+dirty`. Examples from the spec: `0.100.0-fork.100` for a release, `v0.100.0-fork.100+dirty.<fingerprint>` for a local build.
- The stamp is added by the agent, not by `Message.AddFinish`, and it is not added to a message repaired after a crash (`internal/agent/settle.go:160-166`).
- Trigger values, exactly: `threshold`, `ceiling`, `guard`, `overflow`, `model_switch`, `fusion_preflight`, `fusion_route`, `manual`.
- PreCompact and PostCompact hooks keep receiving `auto` or `manual` in their `trigger` field (`docs/hooks/README.md:856`). Only `manual` maps to `manual`.
- `internal/proto` and `internal/server/events.go` are not changed: they do not mirror `Finish.Usage` either, and the report reads the database.
- Style: gofumpt; comments on their own line start with a capital letter, end with a period and wrap at 78 columns; no em dashes; testify `require`.
- Size: the whole pull request stays near the roadmap target of 400 changed lines. The estimate for this plan is about 300.
- Commits use semantic subjects (`feat:`, `test:`). Run the commit steps only when the user has authorized commits for this work; otherwise stop at the staged diff and report.

## Review Focus

1. A finish part that is written twice on the same message (a step finish, then the error branch, or a summary that fails after streaming): `AddFinish` deletes the old part, and with it the stamp, so the stamp must be set again after the last `AddFinish`. Pinned by the error-turn and failed-summary tests in Task 3.
2. Rows written before this change: a stored finish part without `build` and statistics without `trigger` must decode, with empty fields. Pinned in Task 2.
3. Hook payloads: every automatic trigger must still reach hooks as `auto`. Pinned by the threshold hook test in Task 4.
4. Overflow recovery sets both the overflow flag and the guard flag, so the selection must read `overflow`, not `guard`. Pinned by the table test in Task 4.
5. Provider requests: a stamped session must build the same provider request as an unstamped one. Pinned by the request-identity test in Task 3.

---

### Task 1: Build stamp in the version package

**Files:**
- Modify: `internal/version/version.go`
- Create: `internal/version/version_test.go`

**Interfaces:**
- Consumes: `version.Version`, `version.BuildID` (existing package variables).
- Produces: `func BuildStamp() string` in package `version`.

- [ ] **Step 1: Write the failing test**

Create `internal/version/version_test.go`:

```go
package version

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildStamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		buildID string
		want    string
	}{
		{name: "release", version: "0.100.0-fork.100", buildID: "c71dec5e", want: "0.100.0-fork.100"},
		{name: "modified checkout", version: "v0.100.0-fork.100+dirty", buildID: "mx7k2", want: "v0.100.0-fork.100+dirty.mx7k2"},
		{name: "unversioned", version: "devel", buildID: "mx7k2", want: "devel.mx7k2"},
		{name: "clean pseudo-version", version: "v0.100.1-0.20261117120000-c71dec5e1234", buildID: "mx7k2", want: "v0.100.1-0.20261117120000-c71dec5e1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, buildStamp(tt.version, tt.buildID))
		})
	}
}

func TestBuildStampUsesTheRunningBuild(t *testing.T) {
	t.Parallel()

	require.Equal(t, buildStamp(Version, BuildID), BuildStamp())
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/version/ -run TestBuildStamp -v`
Expected: FAIL to compile with `undefined: buildStamp` and `undefined: BuildStamp`.

- [ ] **Step 3: Write the implementation**

In `internal/version/version.go`, add `"strings"` to the import block (keep it sorted: `os`, `runtime/debug`, `strconv`, `strings`) and append:

```go
// BuildStamp names the running build for the session log. A release
// version is also a git tag, which names its commit. A modified checkout
// or an unversioned build is not, so the build fingerprint is appended to
// tell two such builds apart.
func BuildStamp() string {
	return buildStamp(Version, BuildID)
}

func buildStamp(version, buildID string) string {
	if version == "devel" || strings.HasSuffix(version, "+dirty") {
		return version + "." + buildID
	}
	return version
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/version/ -v`
Expected: PASS for `TestBuildStamp` (four subtests) and `TestBuildStampUsesTheRunningBuild`.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/version
git add internal/version/version.go internal/version/version_test.go
git commit -m "feat: name the running build for the session log"
```

---

### Task 2: Message fields for the build and the trigger

**Files:**
- Modify: `internal/message/content.go:174-189` (`Finish`), `:203-214` (`CompactionStats`), and after `SetRequestUsage` (`:723-735`)
- Test: `internal/message/parts_json_test.go`, `internal/message/content_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `Finish.Build string` (JSON `build,omitempty`), `CompactionStats.Trigger string` (JSON `trigger,omitempty`), `func (m *Message) SetBuild(build string)`.

- [ ] **Step 1: Write the failing tests**

In `internal/message/parts_json_test.go`, inside `TestPartsRoundTrip`, replace the two entries

```go
		Finish{Reason: FinishReasonEndTurn, Time: 42, Message: "done"},
```

```go
		CompactionStats{TokensBefore: 397088, SummaryTokens: 8192, TailTokens: 22179, SavedTokens: 366717, KeptMessages: 60},
```

with

```go
		Finish{Reason: FinishReasonEndTurn, Time: 42, Message: "done", Build: "v0.100.0-fork.100+dirty.mx7k2"},
```

```go
		CompactionStats{TokensBefore: 397088, SummaryTokens: 8192, TailTokens: 22179, SavedTokens: 366717, KeptMessages: 60, Trigger: "guard"},
```

In the same file, add a subtest at the end of `TestUnmarshalPartsEdgeCases`, before its closing brace:

```go
	t.Run("parts stored before builds and triggers were recorded", func(t *testing.T) {
		t.Parallel()

		got, err := unmarshalParts([]byte(`[{"type":"finish","data":{"reason":"end_turn","time":7}},{"type":"compaction_stats","data":{"tokens_before":10,"summary_tokens":2,"tail_tokens":3,"saved_tokens":5,"kept_messages":1}}]`))
		require.NoError(t, err)
		require.Equal(t, []ContentPart{
			Finish{Reason: FinishReasonEndTurn, Time: 7},
			CompactionStats{TokensBefore: 10, SummaryTokens: 2, TailTokens: 3, SavedTokens: 5, KeptMessages: 1},
		}, got)
	})
```

In `internal/message/content_test.go`, after `TestSetRequestUsage`, add:

```go
func TestSetBuild(t *testing.T) {
	t.Parallel()

	t.Run("records on the finish part and keeps the usage", func(t *testing.T) {
		t.Parallel()
		usage := requestUsage()
		msg := &Message{Role: Assistant}
		msg.AddFinish(FinishReasonEndTurn, "", "")
		msg.SetRequestUsage(usage)
		msg.SetBuild("0.100.0-fork.100")

		finish := msg.FinishPart()
		require.NotNil(t, finish)
		require.Equal(t, "0.100.0-fork.100", finish.Build)
		require.NotNil(t, finish.Usage)
		require.Equal(t, usage, *finish.Usage)
	})

	t.Run("is a no-op without a finish part", func(t *testing.T) {
		t.Parallel()
		msg := &Message{Role: Assistant, Parts: []ContentPart{TextContent{Text: "hi"}}}
		msg.SetBuild("0.100.0-fork.100")
		require.Equal(t, []ContentPart{TextContent{Text: "hi"}}, msg.Parts)
	})

	t.Run("a later finish drops the stamp", func(t *testing.T) {
		t.Parallel()
		msg := &Message{Role: Assistant}
		msg.AddFinish(FinishReasonEndTurn, "", "")
		msg.SetBuild("0.100.0-fork.100")
		msg.AddFinish(FinishReasonError, "Provider Error", "boom")
		require.Empty(t, msg.FinishPart().Build, "callers stamp after the last AddFinish")
	})
}

func TestFinishWithoutBuildHasNoBuildKey(t *testing.T) {
	t.Parallel()

	encoded, err := marshalParts([]ContentPart{Finish{Reason: FinishReasonEndTurn, Time: 7}, CompactionStats{TokensBefore: 1}})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"build"`)
	require.NotContains(t, string(encoded), `"trigger"`)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/message/ -run 'TestPartsRoundTrip|TestUnmarshalPartsEdgeCases|TestSetBuild|TestFinishWithoutBuildHasNoBuildKey' -v`
Expected: FAIL to compile with `unknown field Build in struct literal`, `unknown field Trigger in struct literal` and `msg.SetBuild undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/message/content.go`, add the field at the end of `Finish`, after `Usage`:

```go
	// Usage is what the request that produced this message consumed, with
	// the prefix comparison that explains its cache read. Nil on messages
	// that were never a request of their own.
	Usage *RequestUsage `json:"usage,omitempty"`
	// Build names the Crush build that wrote this message, as
	// version.BuildStamp reports it. Empty on messages written before
	// builds were recorded and on turns repaired after a crash, since the
	// repairing build did not write them.
	Build string `json:"build,omitempty"`
}
```

Add the field at the end of `CompactionStats`, after `KeptMessages`:

```go
	KeptMessages  int   `json:"kept_messages"`
	// Trigger names what started the compaction: threshold, ceiling,
	// guard, overflow, model_switch, fusion_preflight, fusion_route or
	// manual. Empty on compactions recorded before triggers were.
	Trigger string `json:"trigger,omitempty"`
}
```

Add the setter directly after `SetRequestUsage`:

```go
// SetBuild records the build that wrote the message on its finish part.
// It is a no-op until the message has one. AddFinish replaces the finish
// part, so call this after the last AddFinish.
func (m *Message) SetBuild(build string) {
	for i, part := range m.Parts {
		finish, ok := part.(Finish)
		if !ok {
			continue
		}
		finish.Build = build
		m.Parts[i] = finish
		return
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/message/ -v -run 'TestPartsRoundTrip|TestUnmarshalPartsEdgeCases|TestSetBuild|TestFinishWithoutBuildHasNoBuildKey'`
Expected: PASS.

Then run the package and its direct consumers: `go test ./internal/message/ ./internal/proto/ ./internal/ui/chat/ ./internal/discord/`
Expected: PASS (the new fields are additive).

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/message
git add internal/message/content.go internal/message/content_test.go internal/message/parts_json_test.go
git commit -m "feat: add build and trigger fields to the session log parts"
```

---

### Task 3: The agent stamps every finish it writes

**Files:**
- Modify: `internal/agent/agent.go`: `persistCanceledTurn` (`:779`), `OnStepFinish` (`:1657`), the error branch (before the update at `:1896`), and `summarize` (`:2339`, `:2351`, `:2404`)
- Create: `internal/agent/build_stamp_test.go`
- Test: `internal/agent/compaction_test.go`, `internal/agent/interrupted_tool_call_test.go`

**Interfaces:**
- Consumes: `version.BuildStamp()` (Task 1), `(*message.Message).SetBuild` and `message.Finish.Build` (Task 2). `internal/agent/agent.go` already imports `internal/version` (`:55`).
- Produces: every assistant message the agent finishes carries `Finish.Build == version.BuildStamp()`.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/build_stamp_test.go`:

```go
package agent

import (
	"errors"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/version"
	"github.com/stretchr/testify/require"
)

func lastAssistantFinish(t *testing.T, env fakeEnv, sessionID string) *message.Finish {
	t.Helper()
	msg, err := env.messages.GetLastAssistantMessage(t.Context(), sessionID)
	require.NoError(t, err)
	finish := msg.FinishPart()
	require.NotNil(t, finish, "the assistant message must be finished")
	return finish
}

func TestBuildStamp_CompletedTurn(t *testing.T) {
	sa, env := newStreamTestAgent(t)
	sess, err := env.sessions.Create(t.Context(), "completed")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.NoError(t, err)

	require.Equal(t, version.BuildStamp(), lastAssistantFinish(t, env, sess.ID).Build)
}

func TestBuildStamp_FailedTurn(t *testing.T) {
	env := testEnv(t)
	model := &errorStreamModel{err: errors.New("boom")}
	sa := testSessionAgent(env, model, model, "system").(*sessionAgent)
	sess, err := env.sessions.Create(t.Context(), "failed")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.Error(t, err)

	finish := lastAssistantFinish(t, env, sess.ID)
	require.Equal(t, message.FinishReasonError, finish.Reason)
	require.Equal(t, version.BuildStamp(), finish.Build)
}

func TestBuildStamp_CanceledBeforeTheModelAnswered(t *testing.T) {
	sa, env := newStreamTestAgent(t)
	sess, err := env.sessions.Create(t.Context(), "canceled")
	require.NoError(t, err)

	require.NoError(t, sa.persistCanceledTurn(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"}, false))

	finish := lastAssistantFinish(t, env, sess.ID)
	require.Equal(t, message.FinishReasonCanceled, finish.Reason)
	require.Equal(t, version.BuildStamp(), finish.Build)
}

// The stamps are log metadata: a session that carries them must produce
// the provider request it produced before they existed.
func TestBuildStamp_NeverReachesTheProvider(t *testing.T) {
	sa, _ := newStreamTestAgent(t)
	transcript := func(build, trigger string) []message.Message {
		return []message.Message{
			{ID: "u1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
			{ID: "a1", Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "hello"},
				message.Finish{Reason: message.FinishReasonEndTurn, Time: 1, Build: build},
			}},
			{ID: "s1", Role: message.Assistant, IsSummaryMessage: true, Parts: []message.ContentPart{
				message.TextContent{Text: "# Summary"},
				message.CompactionStats{TokensBefore: 10, Trigger: trigger},
				message.Finish{Reason: message.FinishReasonEndTurn, Time: 2, Build: build},
			}},
		}
	}

	stamped, _ := sa.preparePrompt(transcript("v0.100.0-fork.100+dirty.mx7k2", "guard"), false, false)
	plain, _ := sa.preparePrompt(transcript("", ""), false, false)
	require.Equal(t, plain, stamped)
}
```

In `internal/agent/compaction_test.go`, add the import `"github.com/charmbracelet/crush/internal/version"` (keep the internal import group sorted). In `TestSummarize_AttachesCompactionStats`, append after the last `require.Equal`:

```go
	require.Equal(t, version.BuildStamp(), summary.FinishPart().Build)
```

In `TestSummarize_EmptySummaryLeavesSessionIntact`, append after the last `require.Empty`:

```go
	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)
	var failed *message.Finish
	for _, msg := range msgs {
		if finish := msg.FinishPart(); finish != nil && finish.Message == "Compaction Error" {
			failed = finish
		}
	}
	require.NotNil(t, failed, "the failed summary keeps its error finish")
	require.Equal(t, version.BuildStamp(), failed.Build)
```

In `internal/agent/interrupted_tool_call_test.go`, in `TestRepairInterruptedToolCalls_BackdatesTheFinish`, append after the last `require.Equal`:

```go
	require.Empty(t, finish.Build, "the repairing build did not write the turn")
```

This last assertion passes before the change too; it pins the rule from spec section 1.1 so a later refactor that stamps inside `AddFinish` fails it.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'TestBuildStamp_|TestSummarize_AttachesCompactionStats|TestSummarize_EmptySummaryLeavesSessionIntact|TestRepairInterruptedToolCalls_BackdatesTheFinish' -v`
Expected: `TestBuildStamp_CompletedTurn`, `TestBuildStamp_FailedTurn`, `TestBuildStamp_CanceledBeforeTheModelAnswered`, `TestSummarize_AttachesCompactionStats` and `TestSummarize_EmptySummaryLeavesSessionIntact` FAIL: expected the stamp of the test binary, actual `""`. `TestBuildStamp_NeverReachesTheProvider` and `TestRepairInterruptedToolCalls_BackdatesTheFinish` PASS.

If `TestBuildStamp_FailedTurn` fails earlier on `GetLastAssistantMessage` (no assistant row), the error model fails before the step creates its message: replace `errorStreamModel` there with a model whose stream yields one text delta and then the error part, and keep the assertions.

- [ ] **Step 3: Write the implementation**

In `internal/agent/agent.go`, `persistCanceledTurn`, replace

```go
	assistant.AddFinish(message.FinishReasonCanceled, "User canceled request", "")
	return a.messages.Update(writeCtx, assistant)
```

with

```go
	assistant.AddFinish(message.FinishReasonCanceled, "User canceled request", "")
	assistant.SetBuild(version.BuildStamp())
	return a.messages.Update(writeCtx, assistant)
```

In `OnStepFinish`, replace

```go
			currentAssistant.AddFinish(finishReason, finishMessage, finishDetails)
			currentAssistant.PrismModelID, currentAssistant.PrismModelName = extractPrismModel(stepResult.ProviderMetadata)
```

with

```go
			currentAssistant.AddFinish(finishReason, finishMessage, finishDetails)
			currentAssistant.SetBuild(version.BuildStamp())
			currentAssistant.PrismModelID, currentAssistant.PrismModelName = extractPrismModel(stepResult.ProviderMetadata)
```

In the error branch, replace

```go
			// Note: we use the cleanup context here because the genCtx has been
			// cancelled.
			updateErr := a.messages.Update(cleanupCtx, *currentAssistant)
```

with

```go
			// Every branch above replaced the finish part, and with it any
			// stamp the step had already written.
			currentAssistant.SetBuild(version.BuildStamp())
			// Note: we use the cleanup context here because the genCtx has been
			// cancelled.
			updateErr := a.messages.Update(cleanupCtx, *currentAssistant)
```

In `summarize`, replace

```go
		summaryMessage.AddFinish(message.FinishReasonError, "Summarization Error", err.Error())
		if updateErr := a.messages.Update(ctx, summaryMessage); updateErr != nil {
```

with

```go
		summaryMessage.AddFinish(message.FinishReasonError, "Summarization Error", err.Error())
		summaryMessage.SetBuild(version.BuildStamp())
		if updateErr := a.messages.Update(ctx, summaryMessage); updateErr != nil {
```

replace

```go
		summaryMessage.AddFinish(message.FinishReasonError, "Compaction Error", emptyErr.Error())
		if updateErr := a.messages.Update(genCtx, summaryMessage); updateErr != nil {
```

with

```go
		summaryMessage.AddFinish(message.FinishReasonError, "Compaction Error", emptyErr.Error())
		summaryMessage.SetBuild(version.BuildStamp())
		if updateErr := a.messages.Update(genCtx, summaryMessage); updateErr != nil {
```

and replace

```go
	summaryMessage.AddFinish(finishReason, finishMessage, finishDetails)
	// A compaction is a model request like any other, and the most
```

with

```go
	summaryMessage.AddFinish(finishReason, finishMessage, finishDetails)
	summaryMessage.SetBuild(version.BuildStamp())
	// A compaction is a model request like any other, and the most
```

Then confirm no agent-side finish is left unstamped:

Run: `grep -n "AddFinish(" internal/agent/*.go | grep -v _test`
Expected: calls only in `agent.go`, at `persistCanceledTurn`, `OnStepFinish`, the error branch's `if` chain and the three in `summarize`. Each is followed (directly, or after the `if` chain for the error branch) by a `SetBuild` call before the next `a.messages.Update`. `settle.go` uses only `AddFinishAt` and gets no `SetBuild`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TestBuildStamp_|TestSummarize_|TestRepairInterruptedToolCalls_' -v`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent
git add internal/agent/agent.go internal/agent/build_stamp_test.go internal/agent/compaction_test.go internal/agent/interrupted_tool_call_test.go
git commit -m "feat: stamp the build on every message the agent finishes"
```

---

### Task 4: Record what triggered each compaction

**Files:**
- Modify: `internal/agent/turn_hooks.go:15-19` (trigger constants)
- Modify: `internal/agent/agent.go`: `summarize` signature (`:2128`), its hook calls (`:2223`, `:2434`), the statistics (`:2366-2372`), and the five call sites (`:1262`, `:1941`, `:2120`, `:2526`, `:2649`)
- Modify: `internal/agent/compaction_fit_test.go:131`
- Create: `internal/agent/compaction_trigger_test.go`
- Test: `internal/agent/compaction_test.go`, `internal/agent/fusion_preflight_test.go`, `internal/agent/fusion_budget_test.go`

**Interfaces:**
- Consumes: `message.CompactionStats.Trigger` (Task 2); existing `a.compactionConfig() (threshold float64, tailTokens int64)`, `a.autoCompactMaxTokens() int64`, `effectiveContextWindow(Model) int64`.
- Produces: `type compactionTrigger string` with the eight constants below, `func (t compactionTrigger) hookTrigger() string`, `func turnCompactionTrigger(overflowRecovery, guardTripped bool, window int64, threshold float64, ceiling int64) compactionTrigger`, and `summarize(..., active bool, trigger compactionTrigger, pinned, atLimit bool) error`.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/compaction_trigger_test.go`:

```go
package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/stretchr/testify/require"
)

func TestTurnCompactionTrigger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		overflowRecovery bool
		guardTripped     bool
		window           int64
		threshold        float64
		ceiling          int64
		want             compactionTrigger
	}{
		{name: "provider rejected the request", overflowRecovery: true, guardTripped: true, window: 200_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerOverflow},
		{name: "guard tripped mid-turn", guardTripped: true, window: 200_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerGuard},
		{name: "window fraction is the lower limit", window: 200_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerThreshold},
		{name: "ceiling is the lower limit", window: 1_000_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerCeiling},
		{name: "ceiling disabled", window: 1_000_000, threshold: 0.5, ceiling: 0, want: compactionTriggerThreshold},
		{name: "ceiling equal to the fraction", window: 800_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerThreshold},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, turnCompactionTrigger(tt.overflowRecovery, tt.guardTripped, tt.window, tt.threshold, tt.ceiling))
		})
	}
}

func TestCompactionTriggerHookForm(t *testing.T) {
	t.Parallel()

	for _, trigger := range []compactionTrigger{
		compactionTriggerThreshold,
		compactionTriggerCeiling,
		compactionTriggerGuard,
		compactionTriggerOverflow,
		compactionTriggerModelSwitch,
		compactionTriggerFusionPreflight,
		compactionTriggerFusionRoute,
	} {
		require.Equal(t, "auto", trigger.hookTrigger(), string(trigger))
	}
	require.Equal(t, "manual", compactionTriggerManual.hookTrigger())
}

// summaryTrigger returns the trigger recorded on the session's current
// compaction summary.
func summaryTrigger(t *testing.T, env fakeEnv, sessionID string) string {
	t.Helper()
	sess, err := env.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, sess.SummaryMessageID, "the session must have been compacted")
	summary, err := env.messages.Get(t.Context(), sess.SummaryMessageID)
	require.NoError(t, err)
	stats, ok := summary.CompactionStatsPart()
	require.True(t, ok, "the summary must carry compaction stats")
	return stats.Trigger
}

func TestCompactionTrigger_Ceiling(t *testing.T) {
	env := testEnv(t)
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	// Half of the 200,000-token test window is 100,000; a lower ceiling
	// makes the ceiling the limit the turn crossed.
	ceiling := 50_000
	cfg.Config().Options.AutoCompactMaxTokens = &ceiling
	stub := &stubSummaryModel{
		summaryText: "# Conversation Summary\n\nAll good.",
		usage:       fantasy.Usage{InputTokens: 60_000, OutputTokens: 100, TotalTokens: 60_100},
	}
	agent := newCompactionAgent(env, stub, cfg, false)
	sess, err := env.sessions.Create(t.Context(), "ceiling")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)

	require.Equal(t, "ceiling", summaryTrigger(t, env, sess.ID))
}

// Hooks receive the documented auto or manual value, never the finer one.
func TestCompactionTrigger_HooksStillReceiveAuto(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{
		summaryText: "# Conversation Summary\n\nAll good.",
		usage:       fantasy.Usage{InputTokens: 150_000, OutputTokens: 100, TotalTokens: 150_100},
	}
	agent := newCompactionAgent(env, stub, nil, false)
	read := recordingHooks(t, agent, hooks.EventPreCompact, hooks.EventPostCompact)
	sess, err := env.sessions.Create(t.Context(), "threshold hooks")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)

	require.Equal(t, "threshold", summaryTrigger(t, env, sess.ID))
	require.Equal(t, []string{"PreCompact auto", "PostCompact auto"}, read())
}
```

Add one assertion to each existing test that drives a call site:

- `internal/agent/compaction_test.go`, `TestSummarize_AttachesCompactionStats`, after `require.Equal(t, 2, stats.KeptMessages)`:

```go
	require.Equal(t, "manual", stats.Trigger)
```

- `internal/agent/compaction_test.go`, `TestAutoCompact_DefaultThresholdAfterTurn`, after its `require.NotEmpty`:

```go
	require.Equal(t, "threshold", summaryTrigger(t, env, sess.ID))
```

- `internal/agent/fusion_preflight_test.go`, `TestFusionRouteCompactsAssembledPromptWithPreviousModel`, after `require.NotEmpty(t, updated.SummaryMessageID)`:

```go
			require.Equal(t, "fusion_route", summaryTrigger(t, env, sess.ID))
```

- `internal/agent/fusion_preflight_test.go`, `TestNonFusionModelSwitchCompactsWhenAutoSummarizeDisabled`, after `require.NotEmpty(t, updated.SummaryMessageID)`:

```go
	require.Equal(t, "model_switch", summaryTrigger(t, env, sess.ID))
```

- `internal/agent/fusion_budget_test.go`, `TestFusionPreflightCompactsWithPreparedPrimary`, after `require.NotEmpty(t, updated.SummaryMessageID)`:

```go
	require.Equal(t, "fusion_preflight", summaryTrigger(t, env, sess.ID))
```

The guard and overflow causes share the end-of-turn call site with threshold and ceiling, which the two end-to-end tests above exercise; which of the four applies is decided by `turnCompactionTrigger` and covered by its table. No test model today can both call tools mid-turn and write a summary, which an end-to-end guard test would need.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'TestTurnCompactionTrigger|TestCompactionTrigger|TestSummarize_AttachesCompactionStats|TestAutoCompact_DefaultThresholdAfterTurn|TestFusionRouteCompactsAssembledPromptWithPreviousModel|TestNonFusionModelSwitchCompactsWhenAutoSummarizeDisabled|TestFusionPreflightCompactsWithPreparedPrimary' -v`
Expected: FAIL to compile with `undefined: compactionTrigger`, `undefined: turnCompactionTrigger` and `undefined: compactionTriggerOverflow`.

- [ ] **Step 3: Write the implementation**

In `internal/agent/turn_hooks.go`, replace

```go
// Compaction triggers reported to PreCompact and PostCompact hooks.
const (
	compactTriggerAuto   = "auto"
	compactTriggerManual = "manual"
)
```

with

```go
// Compaction triggers reported to PreCompact and PostCompact hooks.
const (
	compactTriggerAuto   = "auto"
	compactTriggerManual = "manual"
)

// compactionTrigger names what started a compaction. It is recorded on the
// summary's statistics; hooks receive only its auto or manual form, which
// is the payload documented for users.
type compactionTrigger string

const (
	// A finished turn left the session over the window fraction.
	compactionTriggerThreshold compactionTrigger = "threshold"
	// A finished turn left the session over the absolute ceiling, which
	// was the lower of the two limits.
	compactionTriggerCeiling compactionTrigger = "ceiling"
	// The mid-turn guard found too little room for the next step.
	compactionTriggerGuard compactionTrigger = "guard"
	// The provider rejected the request as too long.
	compactionTriggerOverflow compactionTrigger = "overflow"
	// The user switched to a model with a smaller window.
	compactionTriggerModelSwitch compactionTrigger = "model_switch"
	// The prompt would not fit the Fusion budget.
	compactionTriggerFusionPreflight compactionTrigger = "fusion_preflight"
	// Fusion routed the turn to a model with a smaller budget.
	compactionTriggerFusionRoute compactionTrigger = "fusion_route"
	// The user asked for the compaction.
	compactionTriggerManual compactionTrigger = "manual"
)

// hookTrigger is the trigger as PreCompact and PostCompact hooks receive it.
func (t compactionTrigger) hookTrigger() string {
	if t == compactionTriggerManual {
		return compactTriggerManual
	}
	return compactTriggerAuto
}

// turnCompactionTrigger names which of the four causes that share the
// end-of-turn compaction applied. Overflow recovery also trips the guard
// flag, so it is tested first. The ceiling test is the rule
// autoCompactLimit applies.
func turnCompactionTrigger(overflowRecovery, guardTripped bool, window int64, threshold float64, ceiling int64) compactionTrigger {
	switch {
	case overflowRecovery:
		return compactionTriggerOverflow
	case guardTripped:
		return compactionTriggerGuard
	case ceiling > 0 && ceiling < int64(threshold*float64(window)):
		return compactionTriggerCeiling
	default:
		return compactionTriggerThreshold
	}
}
```

In `internal/agent/agent.go`, change the `summarize` signature from `..., active bool, trigger string, pinned, atLimit bool) error {` to:

```go
func (a *sessionAgent) summarize(ctx context.Context, sessionID string, largeModel Model, opts fantasy.ProviderOptions, onAuthRefresh func(context.Context, *fantasy.ProviderError) error, active bool, trigger compactionTrigger, pinned, atLimit bool) error {
```

In its body, replace both hook events' `Trigger: trigger` with `Trigger: trigger.hookTrigger()`:

```go
	if pre := a.hooks.Run(ctx, hooks.Event{Name: hooks.EventPreCompact, SessionID: sessionID, Trigger: trigger.hookTrigger()}); pre.Blocked() {
```

```go
	a.hooks.Run(ctx, hooks.Event{Name: hooks.EventPostCompact, SessionID: sessionID, Trigger: trigger.hookTrigger()})
```

and record the trigger on the statistics:

```go
	summaryMessage.Parts = append(summaryMessage.Parts, message.CompactionStats{
		TokensBefore:  tokensBefore,
		SummaryTokens: summaryTokens,
		TailTokens:    tailTokens,
		SavedTokens:   savedTokens,
		KeptMessages:  keptMessages,
		Trigger:       string(trigger),
	})
```

Change the five call sites:

1. Fusion preflight (`:1262`):

```go
		if err := a.summarize(genCtx, call.SessionID, largeModel, call.ProviderOptions, call.OnAuthRefresh, true, compactionTriggerFusionPreflight, false, false); err != nil {
```

2. End of turn (`:1941`), replacing the single `summarizeErr := ...` line:

```go
		threshold, _ := a.compactionConfig()
		trigger := turnCompactionTrigger(overflowRecovery, shouldSummarize, effectiveContextWindow(largeModel), threshold, a.autoCompactMaxTokens())
		summarizeErr := a.summarize(summarizeCtx, call.SessionID, largeModel, call.ProviderOptions, call.OnAuthRefresh, true, trigger, false, shouldSummarize)
```

3. Manual (`:2120`):

```go
	return a.summarize(ctx, sessionID, a.largeModel.Get(), opts, onAuthRefresh, false, compactionTriggerManual, false, false)
```

4. Fusion route (`:2526`):

```go
	if err := a.summarize(ctx, sessionID, previous.model, opts, onAuthRefresh, true, compactionTriggerFusionRoute, true, false); err != nil {
```

5. Model switch (`:2649`):

```go
	if err := a.summarize(ctx, sessionID, previousLarge, opts, onAuthRefresh, true, compactionTriggerModelSwitch, false, false); err != nil {
```

In `internal/agent/compaction_fit_test.go:131`, replace `compactTriggerManual` with `compactionTriggerManual`:

```go
			ctx, sess.ID, agent.largeModel.Get(), nil, nil, false, compactionTriggerManual, false, atLimit,
```

Then confirm no string trigger is passed any more:

Run: `grep -n "compactTriggerAuto\|compactTriggerManual" internal/agent/*.go`
Expected: only the constant block and `hookTrigger` in `turn_hooks.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TestTurnCompactionTrigger|TestCompactionTrigger|TestSummarize_|TestAutoCompact_|TestFusionRoute|TestNonFusionModelSwitch|TestFusionPreflight|TestCompactHooks_|TestCompactionFit' -v`
Expected: PASS, including the existing `TestCompactHooks_PostCompactFires` (`PreCompact manual`, `PostCompact manual`) and `TestCompactHooks_PreCompactBlocks`.

If `TestFusionPreflightCompactsWithPreparedPrimary` reports `fusion_route`, the test's session carried a retained Fusion model and went through the route site; read `maybeCompactForFusionRoute` (`agent.go:2512-2529`) and keep the assertion that matches the call site the test actually reaches, naming it in the commit body.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent
git add internal/agent/turn_hooks.go internal/agent/agent.go internal/agent/compaction_fit_test.go internal/agent/compaction_trigger_test.go internal/agent/compaction_test.go internal/agent/fusion_preflight_test.go internal/agent/fusion_budget_test.go
git commit -m "feat: record what triggered each compaction"
```

---

### Task 5: Whole-branch verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything above.
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/version/ ./internal/message/ ./internal/agent/ && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint**

Run: `task lint` if `golangci-lint` is on the `PATH` (it was not when this plan was written; `task lint:install` installs it). At minimum run `task lint:log` and `gofumpt -l internal/`.
Expected: no findings, and `gofumpt -l` prints nothing.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD` and `git diff origin/main...HEAD`
Expected: about 300 changed lines in the files named in Tasks 1 to 4 and no others; no debug output, no em dashes, no unrelated edits.

- [ ] **Step 4: Live check of the build stamp**

Build a binary, run one short prompt in a scratch project, and read the stamp back from that project's own database (`<project>/.crush/crush.db`). This uses the user's configured default model and costs one small request.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" . && mkdir "$scratch/proj"
"$scratch/crush" run -q -c "$scratch/proj" "Reply with the single word ok."
"$scratch/crush" --version
python3 -I -c "import sqlite3,sys,json; c=sqlite3.connect('file:'+sys.argv[1]+'?mode=ro', uri=True); rows=[json.loads(p) for (p,) in c.execute(\"select parts from messages where role='assistant'\")]; print([part['data'].get('build') for parts in rows for part in parts if part['type']=='finish'])" "$scratch/proj/.crush/crush.db"
```

Expected: the Python line prints one value per assistant message, each equal to the version that `crush --version` printed, with `.<fingerprint>` appended when that version is `devel` or ends in `+dirty`. Record both outputs in the pull request.

The compaction trigger has no cheap live check (it needs a session near its limit); its evidence is the call-site tests of Task 4.

- [ ] **Step 5: Clean up**

Run: `rm -rf "$scratch"` (or the directory `mktemp` printed).

The pull request itself is opened with the `pr-discipline` skill (Why, How, Testing; no AI attribution), only when the user asks for it. The repository belongs to the personal `pbtrudel` account, so no Copilot review is requested.
