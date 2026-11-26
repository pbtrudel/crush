# Agent Team Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a shared task name a role so the team service starts a teammate of that role when the task is ready, route finished implementer work through a verifier before dependents start, refuse teammate file edits outside the claimed task's write scopes, and ship four fixes (hook-wrapped MCP identity, bounded role and skill reads, fork prefix on the first turn only, hooks for teammates).

**Architecture:** All coordination is deterministic Go in `internal/agentteam`. A dispatch pass (`Service.dispatch`) plans claims and spawns inside one `s.write` call, then starts the planned members with `s.launch` after the write returns, so `Runner.Start` never runs under a lock. Verification is a new `in_review` status plus a service-created verification task owned by role `verifier`. In `internal/agent`, a `scopeGatedTool` wraps six write tools for teammates between the role gate and the hook wrapper; every wrapper forwards `Info()`, provider options and `MCP()`, so no tools block changes per agent.

**Tech Stack:** Go (module `github.com/charmbracelet/crush`), `charm.land/fantasy`, testify `require`, `golang.org/x/sys/unix` (already a dependency) for FIFOs in tests.

**Spec:** `docs/superpowers/specs/2026-11-26-team-pipeline-design.md`. The spec's line numbers are at `main` 318cc96e0; this plan was written at 1e3a7e247 and line numbers drift as tasks land. Find anchors by the quoted code, not the number.

## Global Constraints

- Prompt caching is the first constraint. No tool `Info()`, tool list, tool order or system prompt may differ between the Lead and any teammate, whatever the teammate's role, write scopes or hooks. Pipeline text travels in messages only.
- The only static, one-time prefix changes allowed are: the Policy sentence "Task readiness never starts an owner." replaced by "A ready task that names a role starts on its own; tasks without a role stay manual.", and the new optional parameters `role` and `verify` on `team_task_create` and `role`, `verify`, `notes` on `team_task_update` (required by spec section 1 and 2). Both are the same bytes for every agent.
- Every wrapper (`hookedTool`, `scopeGatedTool`, `roleGatedTool`) forwards `Info()`, `ProviderOptions()`, `SetProviderOptions()` and `MCP()` to its inner tool.
- A teammate tool is wrapped, outermost first: role gate, scope gate, hook wrapper, tool. Dispatched subagents (`roleSubAgent`) are never hook-wrapped.
- `Runner.Start` (and therefore `s.launch`) is never called while `s.mu`, the in-process write slot or the file lock is held. Never call `s.write` with `s.mu` held.
- New `Task` fields are all `omitempty`, so an old `team.json` loads unchanged and a role-less task serializes and behaves exactly as today.
- `options.agent_team_autostart` defaults to true; crushrc `option agent-team-autostart false`; JSON `"agent_team_autostart": false`. It is read on every dispatch pass.
- Attempts cap: 3 (`maxTaskAttempts`). Verification role name `verifier`; default-verified role name `implementer`.
- No new module dependencies.
- Format every Go file with `gofumpt -w <file>` (fall back to `goimports`, then `gofmt`).
- No em dashes in source, comments, tests, docs or commit messages.
- Log messages start with a capital letter (`scripts/check_log_capitalization.sh`).
- Comments on their own line start with a capital letter, end with a period, wrap at 78 columns.
- Tests use testify `require`, `t.Parallel()` where the package already does, `t.TempDir()` for directories, `t.Setenv()` for environment. File permissions in octal (`0o644`, `0o755`). JSON tags snake_case on durable types (`TaskView` keeps its existing camelCase style; the new keys are single words).
- Semantic commit subjects, one line, no attribution trailers.

## Review Focus

1. The Lead interrupts a dispatched teammate: the task must not restart on its own in a loop; it is escalated for the Lead (Task 7 test `TestDispatchInterruptedTurnEscalates`).
2. A dispatched teammate whose every turn fails (provider down): the dispatcher must stop after three attempts instead of restarting it forever (Task 7 test `TestDispatchFailedTurnsEscalate`).
3. A Lead notice (escalation, removed role) must reach that Lead's inbox, wake it, count as unread for `crush run`, and never leak to another Lead session (Task 7 test `TestLeadNoticeReachesOnlyItsLead`).
4. The original task is reopened or deleted while its verification is open: `pass` or `fail` must still complete the verification task and leave the original alone (Task 8 test `TestVerdictAfterOriginalMoved`).
5. A path in a sibling directory that shares a scope's prefix (`internal/agentteam/x.go` under scope `internal/agent`), and a `./`-prefixed or absolute path inside the scope: the first is refused, the others pass (Task 9 test `TestScopeGatePaths`).

---

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/fsext/regular_file.go` (create) | `ReadRegularFileLimit`, `ReadRegularFSFileLimit`, `ErrNotRegularFile`, `ErrFileTooLarge` |
| `internal/roles/discover.go` | Read role files through `fsext.ReadRegularFSFileLimit` |
| `internal/skills/skills.go`, `internal/skills/catalog.go` | `MaxSkillFileBytes`; bounded reads in `Parse` and `ReadContent` |
| `internal/agent/hooked_tool.go` | `hookedTool.MCP()`; hooks for teammates |
| `internal/agent/team.go` | Fork prefix on first turn; `teamAutostart`, `teamRoleExists`; Config wiring |
| `internal/agent/coordinator.go` | `buildTools`: hook wrap for Lead and teammates, scope gate for teammates |
| `internal/agent/scope_gate.go` (create) | `scopeGatedTool`, `gateToolsForScopes`, `claimedScopes` |
| `internal/agent/team_tools.go` | `role`, `verify`, `notes` parameters and role validation |
| `internal/agentteam/team.go` | Task fields, `TaskInReview`, Config fields, notices, settle rules, dispatch call sites, Policy |
| `internal/agentteam/dispatch.go` (create) | Dispatch pass: plan under lock, start after unlock, failure handling |
| `internal/agentteam/verify.go` (create) | Verification task creation, verdicts, owner wake |
| `internal/config/config.go`, `internal/shellconfig/options.go`, `schema.json` | `agent_team_autostart` option |
| `internal/ui/model/team.go`, `internal/cmd/team.go` | Show `in_review`, role, verification link and escalation |
| `docs/agent-teams.md`, `docs/hooks/README.md`, `internal/skills/builtin/crush-config/SKILL.md` | Documentation |

## Task order and parallelism

Tasks 1, 2 and 3 touch disjoint files and may run in parallel. Task 4 edits `hooked_tool.go` after Task 2. Tasks 5 to 8 all edit `internal/agentteam/team.go` and must run in order. Task 6 comes before the dispatcher because the dispatcher reads its Config fields. Task 9 needs Tasks 4 and 5 (hook wrap position, `TaskInProgress` scopes). Task 10 needs 5 to 8 (statuses and fields to display). Hand-off tests (spec 7.2) live in Task 8 because they need verification.

---

### Task 1: Bounded regular-file reads for roles and skills (bug B)

**Files:**
- Create: `internal/fsext/regular_file.go`
- Test: `internal/fsext/regular_file_test.go`, `internal/fsext/regular_file_unix_test.go`
- Modify: `internal/roles/discover.go` (`readLayer`, the `fs.ReadFile` call)
- Test: `internal/roles/discover_unix_test.go`
- Modify: `internal/skills/skills.go` (const block, `Parse`), `internal/skills/catalog.go` (`ReadContent`, the `os.ReadFile` call)
- Test: `internal/skills/skills_unix_test.go`

**Interfaces:**
- Consumes: `roles.MaxBodyBytes` (`internal/roles/parse.go`).
- Produces:
  - `var fsext.ErrNotRegularFile error`, `var fsext.ErrFileTooLarge error`
  - `func fsext.ReadRegularFileLimit(path string, limit int64) ([]byte, error)`
  - `func fsext.ReadRegularFSFileLimit(fsys fs.FS, name string, limit int64) ([]byte, error)`
  - `const roles.maxRoleFileBytes = MaxBodyBytes + 4<<10` (unexported)
  - `const skills.MaxSkillFileBytes = 256 << 10`

Note: the spec names `ReadSkill`; the function that reads a skill file in `internal/skills/catalog.go` is `ReadContent`, so that is what this task changes. The parameter is named `limit` rather than `max` so it does not shadow the builtin.

- [ ] **Step 1: Write the failing portable tests**

`internal/fsext/regular_file_test.go`:

```go
package fsext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestReadRegularFileLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))

	data, err := ReadRegularFileLimit(path, 5)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))

	_, err = ReadRegularFileLimit(path, 4)
	require.ErrorIs(t, err, ErrFileTooLarge)

	_, err = ReadRegularFileLimit(dir, 1<<20)
	require.ErrorIs(t, err, ErrNotRegularFile)

	_, err = ReadRegularFileLimit(filepath.Join(dir, "missing.md"), 1<<20)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestReadRegularFSFileLimit(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a.md":     {Data: []byte("hello")},
		"big.md":   {Data: []byte(strings.Repeat("x", 10))},
		"dir/b.md": {Data: []byte("b")},
	}
	data, err := ReadRegularFSFileLimit(fsys, "a.md", 5)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))

	_, err = ReadRegularFSFileLimit(fsys, "big.md", 9)
	require.ErrorIs(t, err, ErrFileTooLarge)

	_, err = ReadRegularFSFileLimit(fsys, "dir", 1<<20)
	require.ErrorIs(t, err, ErrNotRegularFile)
}
```

- [ ] **Step 2: Write the failing unix tests (FIFO, `/dev/zero`)**

`internal/fsext/regular_file_unix_test.go`:

```go
//go:build unix

package fsext

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// readWithin fails the test when read does not return within a few seconds,
// which is what a FIFO without a writer or /dev/zero would do unchecked.
func readWithin(t *testing.T, read func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- read() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("read did not return; a special file was opened")
		return nil
	}
}

func TestReadRegularFileLimitRefusesSpecialFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.md")
	require.NoError(t, unix.Mkfifo(fifo, 0o644))
	zero := filepath.Join(dir, "zero.md")
	require.NoError(t, os.Symlink("/dev/zero", zero))

	for _, path := range []string{fifo, zero} {
		err := readWithin(t, func() error {
			_, err := ReadRegularFileLimit(path, 1<<20)
			return err
		})
		require.ErrorIs(t, err, ErrNotRegularFile, path)

		err = readWithin(t, func() error {
			_, err := ReadRegularFSFileLimit(os.DirFS(dir), filepath.Base(path), 1<<20)
			return err
		})
		require.ErrorIs(t, err, ErrNotRegularFile, path)
	}
}
```

- [ ] **Step 3: Run the fsext tests to verify they fail**

Run: `go test ./internal/fsext/ -run 'TestReadRegular'`
Expected: FAIL to compile with `undefined: ReadRegularFileLimit`.

- [ ] **Step 4: Implement the helpers**

`internal/fsext/regular_file.go`:

```go
package fsext

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// ErrNotRegularFile is returned for a path that is not a regular file once
// symlinks are followed: a directory, a FIFO, a device or a socket.
var ErrNotRegularFile = errors.New("not a regular file")

// ErrFileTooLarge is returned for a file larger than the caller's limit.
var ErrFileTooLarge = errors.New("file too large")

// ReadRegularFileLimit reads path when it is a regular file of at most limit
// bytes. It stats first, following symlinks, and refuses anything else
// before opening it, so a FIFO cannot block the caller and a link to
// /dev/zero cannot exhaust memory.
func ReadRegularFileLimit(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if err := checkRegular(path, info, limit); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimited(path, file, limit)
}

// ReadRegularFSFileLimit is ReadRegularFileLimit for a file system.
func ReadRegularFSFileLimit(fsys fs.FS, name string, limit int64) ([]byte, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return nil, err
	}
	if err := checkRegular(name, info, limit); err != nil {
		return nil, err
	}
	file, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimited(name, file, limit)
}

func checkRegular(name string, info fs.FileInfo, limit int64) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", name, ErrNotRegularFile)
	}
	if info.Size() > limit {
		return fmt.Errorf("%s: %w: %d bytes, more than %d", name, ErrFileTooLarge, info.Size(), limit)
	}
	return nil
}

// readLimited reads at most limit bytes. The stat above can be stale, so the
// read itself is bounded too.
func readLimited(name string, r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: %w: more than %d bytes", name, ErrFileTooLarge, limit)
	}
	return data, nil
}
```

- [ ] **Step 5: Run the fsext tests to verify they pass**

Run: `go test ./internal/fsext/ -run 'TestReadRegular' -v`
Expected: PASS.

- [ ] **Step 6: Write the failing roles test**

`internal/roles/discover_unix_test.go`:

```go
//go:build unix

package roles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDiscoverRefusesSpecialAndOversizeFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, unix.Mkfifo(filepath.Join(dir, "fifo.md"), 0o644))
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(dir, "zero.md")))
	writeRole(t, dir, "huge", "---\nname: huge\ndescription: d\n---\n"+strings.Repeat("a", MaxBodyBytes+8<<10))
	writeRole(t, dir, "ok", "---\nname: ok\ndescription: d\n---\nbody\n")

	type result struct {
		found map[string]Role
		diags []Diagnostic
	}
	done := make(chan result, 1)
	go func() {
		found, diags := Discover([]Dir{{Path: dir, Source: SourceProject}}, nil)
		done <- result{found, diags}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Discover hung on a special file")
	}
	require.Contains(t, got.found, "ok", "the readable role still loads")
	messages := map[string]string{}
	for _, d := range got.diags {
		messages[filepath.Base(d.Path)] = d.Message
	}
	require.Contains(t, messages["fifo.md"], "not a regular file")
	require.Contains(t, messages["zero.md"], "not a regular file")
	require.Contains(t, messages["huge.md"], "file too large")
}
```

- [ ] **Step 7: Run it to verify it fails**

Run: `go test ./internal/roles/ -run TestDiscoverRefusesSpecialAndOversizeFiles`
Expected: FAIL with `Discover hung on a special file` (the FIFO open blocks).

- [ ] **Step 8: Read role files through the helper**

In `internal/roles/discover.go`, add the import `"github.com/charmbracelet/crush/internal/fsext"` and a constant after the `builtinFS` declaration:

```go
// maxRoleFileBytes bounds one role file: the largest body plus room for the
// frontmatter. A bigger file fails the body limit anyway, and the bound
// keeps a link to /dev/zero from exhausting memory.
const maxRoleFileBytes = MaxBodyBytes + 4<<10
```

In `readLayer`, replace:

```go
		content, err := fs.ReadFile(l.fsys, entry.Name())
```

with:

```go
		content, err := fsext.ReadRegularFSFileLimit(l.fsys, entry.Name(), maxRoleFileBytes)
```

- [ ] **Step 9: Run the roles tests to verify they pass**

Run: `go test ./internal/roles/`
Expected: PASS.

- [ ] **Step 10: Write the failing skills test**

`internal/skills/skills_unix_test.go`:

```go
//go:build unix

package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDiscoverRefusesSpecialAndOversizeSkills(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"fifo-skill", "zero-skill", "big-skill", "ok-skill"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o755))
	}
	require.NoError(t, unix.Mkfifo(filepath.Join(dir, "fifo-skill", SkillFileName), 0o644))
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(dir, "zero-skill", SkillFileName)))
	big := "---\nname: big-skill\ndescription: Big.\n---\n" + strings.Repeat("a", MaxSkillFileBytes)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big-skill", SkillFileName), []byte(big), 0o644))
	ok := "---\nname: ok-skill\ndescription: Fine.\n---\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok-skill", SkillFileName), []byte(ok), 0o644))

	type result struct {
		skills []*Skill
		states []*SkillState
	}
	done := make(chan result, 1)
	go func() {
		skills, states := DiscoverWithStates([]string{dir})
		done <- result{skills, states}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("skill discovery hung on a special file")
	}
	require.Len(t, got.skills, 1)
	require.Equal(t, "ok-skill", got.skills[0].Name)
	errored := 0
	for _, state := range got.states {
		if state.State == StateError {
			errored++
		}
	}
	require.Equal(t, 3, errored, "each refused file is a diagnostic")

	read := make(chan error, 1)
	fifo := filepath.Join(dir, "fifo-skill", SkillFileName)
	go func() {
		_, _, err := ReadContent([]*Skill{{Name: "fifo-skill", SkillFilePath: fifo}}, nil, "", fifo)
		read <- err
	}()
	select {
	case err := <-read:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ReadContent hung on a FIFO")
	}
}
```

- [ ] **Step 11: Run it to verify it fails**

Run: `go test ./internal/skills/ -run TestDiscoverRefusesSpecialAndOversizeSkills`
Expected: FAIL to compile with `undefined: MaxSkillFileBytes`.

- [ ] **Step 12: Bound skill reads**

In `internal/skills/skills.go`, add to the const block after `MaxCompatibilityLength = 500`:

```go
	// MaxSkillFileBytes bounds one SKILL.md. A larger file is a diagnostic,
	// so a link to /dev/zero cannot exhaust memory at startup.
	MaxSkillFileBytes = 256 << 10
```

Add the import `"github.com/charmbracelet/crush/internal/fsext"`, and in `Parse` replace:

```go
	content, err := os.ReadFile(path)
```

with:

```go
	content, err := fsext.ReadRegularFileLimit(path, MaxSkillFileBytes)
```

In `internal/skills/catalog.go`, add the same import and in `ReadContent` replace:

```go
	content, err := os.ReadFile(skill.SkillFilePath)
```

with:

```go
	content, err := fsext.ReadRegularFileLimit(skill.SkillFilePath, MaxSkillFileBytes)
```

Remove the `"os"` import from either file if the compiler reports it unused (`skills.go` still uses `os.DirEntry`).

- [ ] **Step 13: Run the package tests to verify they pass**

Run: `go test ./internal/fsext/ ./internal/roles/ ./internal/skills/`
Expected: PASS.

- [ ] **Step 14: Format and commit**

```bash
gofumpt -w internal/fsext/regular_file.go internal/fsext/regular_file_test.go internal/fsext/regular_file_unix_test.go internal/roles/discover.go internal/roles/discover_unix_test.go internal/skills/skills.go internal/skills/catalog.go internal/skills/skills_unix_test.go
git add internal/fsext/regular_file.go internal/fsext/regular_file_test.go internal/fsext/regular_file_unix_test.go internal/roles/discover.go internal/roles/discover_unix_test.go internal/skills/skills.go internal/skills/catalog.go internal/skills/skills_unix_test.go
git commit -m "fix: refuse special and oversize role and skill files"
```

---

### Task 2: `hookedTool` reports its MCP server (bug A)

**Files:**
- Modify: `internal/agent/hooked_tool.go` (after `SetProviderOptions`)
- Test: `internal/agent/hooked_tool_test.go`

**Interfaces:**
- Consumes: `roleMCPTool` (test type in `internal/agent/role_agents_test.go`), `fakeTool`, `newRunner` (in `hooked_tool_test.go`), `filterToolsForChannel`, `dropDisabledMCPTools`, `toolNames`.
- Produces: `func (h *hookedTool) MCP() string`.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/hooked_tool_test.go` (add imports `"github.com/charmbracelet/crush/internal/agent/tools/mcp"` and `"github.com/charmbracelet/crush/internal/roles"`):

```go
// TestHookedToolKeepsMCPIdentity pins bug A: the Lead's tools are always
// hook-wrapped, so the wrapper must report its MCP server or both MCP filters
// treat the Lead's MCP tools as built-ins and its palette drifts from its
// teammates'.
func TestHookedToolKeepsMCPIdentity(t *testing.T) {
	t.Parallel()
	runner := newRunner(t, `exit 0`)
	list := []fantasy.AgentTool{
		&roleMCPTool{name: "mcp_off_send", server: "off"},
		&roleMCPTool{name: "mcp_signal_send", server: "signal"},
		&roleMCPTool{name: "mcp_switchboard_send", server: "switchboard"},
		&fakeTool{name: "view"},
	}
	lead := wrapToolsWithHooks(list, runner, false)
	teammate := gateToolsForRole(&roles.Role{Name: "researcher"}, list)

	mcpTool, ok := lead[0].(interface{ MCP() string })
	require.True(t, ok)
	require.Equal(t, "off", mcpTool.MCP())
	builtin, ok := lead[3].(interface{ MCP() string })
	require.True(t, ok)
	require.Empty(t, builtin.MCP(), "a hooked built-in reports no MCP server")

	disabled := map[string]struct{}{"off": {}}
	require.Equal(t, []string{"mcp_signal_send", "mcp_switchboard_send", "view"},
		toolNames(dropDisabledMCPTools(lead, disabled)))
	require.Equal(t, toolNames(dropDisabledMCPTools(teammate, disabled)),
		toolNames(dropDisabledMCPTools(lead, disabled)), "Lead and teammate drop the same tools")

	states := map[string]mcp.ClientInfo{"signal": {Channel: true}, "switchboard": {Channel: true}}
	require.Equal(t, []string{"mcp_off_send", "mcp_switchboard_send", "view"},
		toolNames(filterToolsForChannel(lead, "switchboard", states)))
	require.Equal(t, toolNames(filterToolsForChannel(teammate, "switchboard", states)),
		toolNames(filterToolsForChannel(lead, "switchboard", states)), "Lead and teammate keep the same channel tools")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/agent/ -run TestHookedToolKeepsMCPIdentity`
Expected: FAIL at `require.True(t, ok)` (no `MCP` method on `*hookedTool`).

- [ ] **Step 3: Implement `MCP`**

In `internal/agent/hooked_tool.go`, after `SetProviderOptions`:

```go
// MCP reports the MCP server of the wrapped tool, or "" for a built-in
// tool, so the per-turn MCP filters see through the hook wrapper.
func (h *hookedTool) MCP() string {
	if mcpTool, ok := h.inner.(interface{ MCP() string }); ok {
		return mcpTool.MCP()
	}
	return ""
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TestHookedTool|TestRoleGate|TestWrapToolsWithHooks'`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent/hooked_tool.go internal/agent/hooked_tool_test.go
git add internal/agent/hooked_tool.go internal/agent/hooked_tool_test.go
git commit -m "fix: keep MCP identity through the hook wrapper"
```

---

### Task 3: Fork prefix on the first turn only (bug C)

**Files:**
- Modify: `internal/agent/team.go` (`teammatePrompt`)
- Test: `internal/agent/team_sibling_prefix_test.go`

**Interfaces:**
- Consumes: `teamRunner.teammatePrompt(ctx, member, task string, first bool) string`, `teamRunner.forkPrefix`, `teammateTaskMessage`, `newTeammateTestCoordinator`.
- Produces: no new names; `teammatePrompt` now returns no inherited context when `first` is false.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/team_sibling_prefix_test.go`:

```go
// TestForkPrefixFirstTurnOnly pins bug C: the inherited Lead transcript is
// already in a fork teammate's history after its first turn, so a later
// wake must not send up to 16 KB of it again.
func TestForkPrefixFirstTurnOnly(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	_, err = coord.messages.Create(t.Context(), lead.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "fix the failing tests"}},
	})
	require.NoError(t, err)
	member := agentteam.Member{ID: "member-1", Name: "db-layer", Role: agentteam.RoleTeammate, ParentID: lead.ID, Context: agentteam.ContextFork}

	first := runner.teammatePrompt(t.Context(), member, "Start.", true)
	require.Contains(t, first, "# Context inherited from the Team Lead")

	later := runner.teammatePrompt(t.Context(), member, "New messages.", false)
	require.Equal(t, teammateTaskMessage(member, "New messages."), later)
	require.NotContains(t, later, "fix the failing tests")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/agent/ -run TestForkPrefixFirstTurnOnly`
Expected: FAIL: `later` starts with `# Context inherited from the Team Lead`.

- [ ] **Step 3: Gate the fork prefix on `first`**

In `internal/agent/team.go`, replace the doc comment and body of `teammatePrompt`:

```go
// teammatePrompt is the message a teammate turn starts from. On the member's
// first turn only, it carries the inherited context a fork member gets, then
// its role block; every turn carries its name, then the task. Later turns
// already hold both in their history, so repeating them would only resend
// bytes.
//
// The inherited context goes first because it is the same bytes for every fork
// sibling of one Lead. The role block follows because it is the same bytes for
// every sibling with that role; the per-sibling name comes after both, so the
// shared prefix runs through the whole role block.
func (r *teamRunner) teammatePrompt(ctx context.Context, member agentteam.Member, task string, first bool) string {
	task = teammateTaskMessage(member, task)
	if !first {
		return task
	}
	if member.RoleName != "" {
		if role, refusal := resolveRole(r.c.cfg.Config(), member.RoleName); refusal == "" {
			task = roles.Block(*role) + task
		}
	}
	if member.Context == agentteam.ContextFork {
		return r.forkPrefix(ctx, member) + task
	}
	return task
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TestForkPrefixFirstTurnOnly|TestForkSiblingsShareInheritedContext|TestTeammatePrompt'`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/agent/team.go internal/agent/team_sibling_prefix_test.go
git add internal/agent/team.go internal/agent/team_sibling_prefix_test.go
git commit -m "fix: send a fork teammate the inherited context on its first turn only"
```

---

### Task 4: Teammates fire tool hooks (fix D)

**Files:**
- Modify: `internal/agent/coordinator.go` (`buildTools`, the `wrapToolsWithHooks` call and its comment)
- Modify: `internal/agent/hooked_tool.go` (`wrapToolsWithHooks` doc comment)
- Modify: `internal/agent/team_cache_test.go` (`newTeammateTestCoordinator`, new tests)

**Interfaces:**
- Consumes: `wrapToolsWithHooks(tools []fantasy.AgentTool, runner *hooks.Runner, isSubAgent bool)`, `roleSubAgent`, `roleTeammate`, `newRunner`, `toolByName` (in `team_tools_test.go`), `buildTeammateAgent`.
- Produces:
  - `func newTeammateTestCoordinatorWithHooks(t *testing.T, runner *hooks.Runner) *coordinator` (test helper; `newTeammateTestCoordinator` calls it with a permissive non-nil runner).
  - `buildTools` hook-wraps every role but `roleSubAgent`.

- [ ] **Step 1: Make the cache helper use a non-nil hook runner**

In `internal/agent/team_cache_test.go`, add the import `"github.com/charmbracelet/crush/internal/hooks"` and replace `newTeammateTestCoordinator` with:

```go
// newTeammateTestCoordinator builds the gate test coordinator with a live Team
// service, so the palette under test includes the Team tools the Lead gets.
// It installs a hook runner with no hooks, as production always does, so the
// Lead's tools are hook-wrapped like in a real session.
func newTeammateTestCoordinator(t *testing.T) *coordinator {
	t.Helper()
	return newTeammateTestCoordinatorWithHooks(t, hooks.NewRunner(hooks.Static(nil), t.TempDir(), t.TempDir()))
}

// newTeammateTestCoordinatorWithHooks is newTeammateTestCoordinator with the
// given hook runner. The Lead's tools are rebuilt afterwards because the gate
// coordinator builds them before the service and the runner exist, which is
// not the production order.
func newTeammateTestCoordinatorWithHooks(t *testing.T, runner *hooks.Runner) *coordinator {
	t.Helper()
	coord := newGateTestCoordinator(t, true)
	// The gate coordinator renders its prompt and tools in the background,
	// and buildTools reads the team service. Let that finish before
	// assigning the service, or the background read races with the write
	// below.
	require.NoError(t, coord.readyWg.Wait())
	coord.hookRunner = runner

	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, newTeamFakeRunner())
	require.NoError(t, err)
	coord.setTeam(service)

	lead := coord.coderAgent()
	require.NotNil(t, lead)
	leadTools, err := coord.buildTools(t.Context(), coord.cfg.Config().Agents[config.AgentCoder], roleLead, func() Model { return coord.mainModels().Large })
	require.NoError(t, err)
	lead.SetTools(leadTools)
	return coord
}
```

- [ ] **Step 2: Write the failing tests**

Append to `internal/agent/team_cache_test.go`:

```go
// TestTeammateToolRunsBlockingHook pins fix D: a teammate's tool call runs
// the user's PreToolUse hooks, so a blocking hook stops it.
func TestTeammateToolRunsBlockingHook(t *testing.T) {
	coord := newTeammateTestCoordinatorWithHooks(t, newRunner(t, `echo "blocked" >&2; exit 2`))
	teammate := buildTeammateAgent(t, coord, agentteam.Member{Name: "rev", Role: agentteam.RoleTeammate})

	view := toolByName(teammate.(*sessionAgent).tools.Copy(), tools.ViewToolName)
	require.NotNil(t, view)
	ctx := context.WithValue(agentteam.WithMember(t.Context(), "rev"), tools.SessionIDContextKey, "session-teammate")
	resp, err := view.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: tools.ViewToolName, Input: `{"file_path":"missing.go"}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Tool call blocked by hook")
}

// TestHookWrapFollowsRole pins which agents fire tool hooks: the Lead and
// teammates do, a dispatched subagent does not.
func TestHookWrapFollowsRole(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	parent := func() Model { return coord.mainModels().Large }
	agents := coord.cfg.Config().Agents

	lead, err := coord.buildTools(t.Context(), agents[config.AgentCoder], roleLead, parent)
	require.NoError(t, err)
	for _, tool := range lead {
		_, hooked := tool.(*hookedTool)
		require.True(t, hooked, "Lead tool %q must be hook-wrapped", tool.Info().Name)
	}
	// A read tool: later write tools also carry the scope gate outside the
	// hook wrapper (Task 9 pins that order).
	teammate, err := coord.buildTools(agentteam.WithMember(t.Context(), "rev"), agents[config.AgentCoder], roleTeammate, parent)
	require.NoError(t, err)
	_, hooked := toolByName(teammate, tools.ViewToolName).(*hookedTool)
	require.True(t, hooked, "a teammate's tools must be hook-wrapped")

	built, err := coord.buildTools(t.Context(), agents[config.AgentTask], roleSubAgent, parent)
	require.NoError(t, err)
	for _, tool := range built {
		_, hooked := tool.(*hookedTool)
		require.False(t, hooked, "subagent tool %q must not be hook-wrapped", tool.Info().Name)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/agent/ -run 'TestTeammateToolRunsBlockingHook|TestHookWrapFollowsRole'`
Expected: FAIL: the teammate's `view` runs (no "blocked by hook"), and `roleTeammate` tools are not `*hookedTool`.

- [ ] **Step 4: Wrap teammate tools with hooks**

In `internal/agent/coordinator.go`, at the end of `buildTools`, replace:

```go
	// Wrap tools with hook interception for the top-level agent only.
	// Subagents (the `agent` task tool, `agentic_fetch`, etc.) run
	// without hook interception to avoid firing the user's hook N times
	// per delegated turn. The top-level invocation of the subagent tool
	// itself is still wrapped from the coder's side.
	filteredTools = wrapToolsWithHooks(filteredTools, c.hookRunner, role.isSubAgent())
```

with:

```go
	// Wrap tools with hook interception for the Lead and for teammates,
	// which fire hooks with their own session id. Dispatched subagents
	// (the `agent` task tool, `agentic_fetch`, etc.) run without hook
	// interception to avoid firing the user's hook N times per delegated
	// turn; the subagent tool call itself is still wrapped on the caller's
	// side.
	filteredTools = wrapToolsWithHooks(filteredTools, c.hookRunner, role == roleSubAgent)
```

In `internal/agent/hooked_tool.go`, replace the `wrapToolsWithHooks` doc comment with:

```go
// wrapToolsWithHooks returns a tool slice with each entry wrapped in a
// hookedTool. It returns the original slice unchanged when runner is nil or
// when isSubAgent is true: dispatched subagents never fire hooks, and the
// subagent tool call itself is wrapped on the caller's side. Teammates are
// not subagents here; they fire hooks with their own session id.
```

- [ ] **Step 5: Run the agent tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TestTeammate|TestHookWrapFollowsRole|TestRoleTeammate|TestWrapToolsWithHooks|TestHookedTool'`
Expected: PASS. `TestTeammateToolPaletteMatchesLead` and `TestRoleTeammateKeepsLeadPrefix` now run with a non-nil hook runner and must still pass.

- [ ] **Step 6: Run the cache tests three times**

Run: `go test ./internal/agent/ -run 'Cache|Palette|Prefix|SystemPrompt' -count=3`
Expected: PASS.

- [ ] **Step 7: Format and commit**

```bash
gofumpt -w internal/agent/coordinator.go internal/agent/hooked_tool.go internal/agent/team_cache_test.go
git add internal/agent/coordinator.go internal/agent/hooked_tool.go internal/agent/team_cache_test.go
git commit -m "fix: run tool hooks for teammates"
```

---

### Task 5: Task role, verify and status fields

**Files:**
- Modify: `internal/agentteam/team.go` (`TaskStatus` consts, `Task`, `TaskView`, `CreateTaskRequest`, `UpdateTaskRequest`, `CreateTask`, `applyTaskUpdateLocked` `edit` case and tail, `projectTaskView`)
- Modify: `internal/agent/team_tools.go` (`teamTaskCreateParams`, `teamTaskUpdateParams`, `teamTaskCreateTool`, `teamTaskUpdateTool`)
- Test: `internal/agentteam/task_fields_test.go` (create)
- Test: `internal/agent/team_task_role_test.go` (create)

**Interfaces:**
- Consumes: `resolveRole(cfg *config.Config, name string) (*roles.Role, string)`, `memberNamePattern`.
- Produces:
  - `const agentteam.TaskInReview TaskStatus = "in_review"`
  - `Task` fields `Role string`, `Verify *bool`, `Verifies string`, `Attempts int`, `Escalated bool` (JSON `role`, `verify`, `verifies`, `attempts`, `escalated`, all `omitempty`).
  - `TaskView` fields `Role string`, `Verify bool` (effective), `Verifies string`, `Attempts int`, `Escalated bool`, all `omitempty`.
  - `CreateTaskRequest` fields `Role string`, `Verify *bool`.
  - `UpdateTaskRequest` fields `Role *string`, `Verify *bool`, `Notes string`.
  - unexported `const implementerRole = "implementer"`, `const verifierRole = "verifier"`, `const maxTaskSubject = 200`.
  - unexported `func (t Task) needsVerification() bool`, `func normalizeTaskRole(value string) (string, error)`.
  - Test helpers in package `agentteam`: `const pipelineSession = "session-a"`, `func newBareService(t *testing.T) *Service` (nil runner), `func createTask(t *testing.T, s *Service, req CreateTaskRequest) TaskView`, `func taskByID(t *testing.T, s *Service, id string) Task`, `func act(t *testing.T, s *Service, caller, taskID, action string, edit func(*UpdateTaskRequest)) TaskView`, `func actErr(s *Service, caller, taskID, action string, edit func(*UpdateTaskRequest)) error`.
  - Tool params: `team_task_create` gains `role`, `verify`; `team_task_update` gains `role`, `verify`, `notes`.

- [ ] **Step 1: Write the failing service tests**

`internal/agentteam/task_fields_test.go`:

```go
package agentteam

import (
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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/agentteam/ -run 'TestTaskVerifyDefaults|TestCreateTaskRecordsRoleAndVerify|TestEditTaskRoleAndVerify|TestLeadActionsClearEscalation|TestOldTeamFileLoads'`
Expected: FAIL to compile (`unknown field Role in struct literal of type CreateTaskRequest`).

- [ ] **Step 3: Add the status, fields and helpers**

In `internal/agentteam/team.go`, add to the task status const block:

```go
	// TaskInReview is finished work waiting for its verification task.
	TaskInReview TaskStatus = "in_review"
```

Append these fields to `Task`, after `SessionID`:

```go
	// Role names the role that should do the task. Empty keeps the task
	// manual: nothing starts it on its own.
	Role string `json:"role,omitempty"`
	// Verify says whether finished work goes to a verifier before it
	// completes. Nil means true for role implementer and false otherwise.
	Verify *bool `json:"verify,omitempty"`
	// Verifies is set on a verification task: the id of the task it checks.
	Verifies string `json:"verifies,omitempty"`
	// Attempts counts failed auto-starts, failed turns and failed
	// verifications.
	Attempts int `json:"attempts,omitempty"`
	// Escalated means the dispatcher no longer touches the task; the Lead
	// decides what happens next.
	Escalated bool `json:"escalated,omitempty"`
```

Append to `TaskView`, after `SessionID`:

```go
	Role      string `json:"role,omitempty"`
	Verify    bool   `json:"verify,omitempty"`
	Verifies  string `json:"verifies,omitempty"`
	Attempts  int    `json:"attempts,omitempty"`
	Escalated bool   `json:"escalated,omitempty"`
```

Append to `CreateTaskRequest`:

```go
	Role   string
	Verify *bool
```

Append to `UpdateTaskRequest`, after `Owner`:

```go
	// Role replaces the task's role on edit; an empty string clears it.
	Role *string
	// Verify replaces the task's verify setting on edit.
	Verify *bool
	// Notes describe what failed, for the fail action.
	Notes string
```

Add after `requiredText`:

```go
// Roles the pipeline treats specially, and the subject limit every task
// shares.
const (
	implementerRole = "implementer"
	verifierRole    = "verifier"
	maxTaskSubject  = 200
)

// needsVerification reports whether finished work on t goes to a verifier
// before it completes. A verification task is never verified itself.
func (t Task) needsVerification() bool {
	if t.Verifies != "" {
		return false
	}
	if t.Verify != nil {
		return *t.Verify
	}
	return t.Role == implementerRole
}

// normalizeTaskRole validates a task role. The tool layer already checked
// that the role exists; this keeps the stored name a valid member-name stem,
// short enough that "<role>-<n>" stays within the 64-byte name limit.
func normalizeTaskRole(value string) (string, error) {
	role := strings.TrimSpace(value)
	if role == "" {
		return "", nil
	}
	if !memberNamePattern.MatchString(role) || len(role) > 60 {
		return "", newError(CodeInvalidArgument, "role %q must be a lower-kebab-case role name of at most 60 characters", value)
	}
	return role, nil
}
```

Change the subject limit `200` to `maxTaskSubject` in the `requiredText(req.Subject, "subject", 200)` calls of `CreateTask` and of the `edit` case. In `CreateTask`, after the `description` validation, before `s.state.NextTask++`, add:

```go
		role, err := normalizeTaskRole(req.Role)
		if err != nil {
			return false, err
		}
```

In the `task := Task{...}` literal add:

```go
			Role:        role,
```

and after the literal:

```go
		if req.Verify != nil {
			task.Verify = new(*req.Verify)
		}
```

In `applyTaskUpdateLocked`, replace the `edit` case's emptiness check:

```go
		if req.Subject == "" && req.Description == "" && req.WriteScopes == nil {
			return taskUpdate{}, newError(CodeInvalidArgument, "task edit requires subject, description, or write_scopes")
		}
```

with:

```go
		if req.Subject == "" && req.Description == "" && req.WriteScopes == nil && req.Role == nil && req.Verify == nil {
			return taskUpdate{}, newError(CodeInvalidArgument, "task edit requires subject, description, write_scopes, role, or verify")
		}
```

and append at the end of the `edit` case, after the `WriteScopes` block:

```go
		if req.Role != nil {
			role, err := normalizeTaskRole(*req.Role)
			if err != nil {
				return taskUpdate{}, err
			}
			next.Role = role
		}
		if req.Verify != nil {
			next.Verify = new(*req.Verify)
		}
```

After the switch, replace:

```go
	next.Revision = current.Revision + 1
```

with:

```go
	next.Revision = current.Revision + 1
	// The Lead acting on a task takes it back from escalation: the
	// dispatcher may try it again from a clean count.
	if lead && (req.Action == "edit" || req.Action == "reassign" || req.Action == "reopen") {
		next.Escalated = false
		next.Attempts = 0
	}
```

In `projectTaskView`, add to the `view := TaskView{...}` literal:

```go
		Role:        task.Role,
		Verify:      task.needsVerification(),
		Verifies:    task.Verifies,
		Attempts:    task.Attempts,
		Escalated:   task.Escalated,
```

- [ ] **Step 4: Run the service tests to verify they pass**

Run: `go test ./internal/agentteam/`
Expected: PASS.

- [ ] **Step 5: Write the failing tool tests**

`internal/agent/team_task_role_test.go`:

```go
package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTeamTaskRoleValidation pins the tool half of task roles: a role must
// resolve, with the refusal spawn_teammate gives, and a valid one is stored
// with its verify default.
func TestTeamTaskRoleValidation(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	create := coord.teamTaskCreateTool("lead")
	update := coord.teamTaskUpdateTool("lead")

	response := runTeamToolRaw(t, create, map[string]any{"subject": "Build", "description": "d", "role": "nope"})
	require.True(t, response.IsError)
	require.Contains(t, response.Content, `unknown role "nope"; available: `)

	created := runTeamTool(t, create, map[string]any{"subject": "Build", "description": "d", "role": "implementer"})
	require.Contains(t, created, `"role":"implementer"`)
	require.Contains(t, created, `"verify":true`)

	unverified := runTeamTool(t, create, map[string]any{"subject": "Look", "description": "d", "role": "researcher", "verify": false})
	require.NotContains(t, unverified, `"verify":true`)

	response = runTeamToolRaw(t, update, map[string]any{
		"task_id": "task-1", "expected_revision": 1, "action": "edit", "role": "nope",
	})
	require.True(t, response.IsError)
	require.Contains(t, response.Content, `unknown role "nope"`)
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./internal/agent/ -run TestTeamTaskRoleValidation`
Expected: FAIL: the unknown role is accepted (no `role` parameter yet).

- [ ] **Step 7: Add the tool parameters and validation**

In `internal/agent/team_tools.go`, append to `teamTaskCreateParams`:

```go
	Role   string `json:"role,omitempty" description:"Optional role from the spawn_teammate catalog. A ready task that names a role starts a teammate of that role on its own; without one the task stays manual."`
	Verify *bool  `json:"verify,omitempty" description:"Whether a verifier checks the finished work before dependent tasks start. Defaults to true for role implementer, false otherwise."`
```

Append to `teamTaskUpdateParams`:

```go
	Role   *string `json:"role,omitempty" description:"Replacement role for edit; an empty string clears it."`
	Verify *bool   `json:"verify,omitempty" description:"Replacement verify setting for edit."`
	Notes  *string `json:"notes,omitempty" description:"What failed, required for the fail verdict on a verification task."`
```

Add this helper after `teamErrorWithState`:

```go
// taskRole resolves a task's role the way spawn_teammate does. An empty
// name is no role; the string is a refusal for the model.
func (c *coordinator) taskRole(name string) (string, string) {
	if strings.TrimSpace(name) == "" {
		return "", ""
	}
	if c.cfg == nil {
		return "", fmt.Sprintf("unknown role %q", name)
	}
	role, refusal := resolveRole(c.cfg.Config(), name)
	if refusal != "" {
		return "", refusal
	}
	return role.Name, ""
}
```

In `teamTaskCreateTool`, before `scopes, rewritten := c.relativizeWriteScopes(params.WriteScopes)`, add:

```go
			role, refusal := c.taskRole(params.Role)
			if refusal != "" {
				return fantasy.NewTextErrorResponse(refusal), nil
			}
```

and add to the `agentteam.CreateTaskRequest{...}` literal:

```go
				Role:        role,
				Verify:      params.Verify,
```

In `teamTaskUpdateTool`, after the `params.Owner` block, add:

```go
			if params.Role != nil {
				role, refusal := c.taskRole(*params.Role)
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				request.Role = &role
			}
			request.Verify = params.Verify
			if params.Notes != nil {
				request.Notes = *params.Notes
			}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TestTeamTask|TestTeamTools|TestTeammateToolPaletteMatchesLead|TestRoleTeammateKeepsLeadPrefix'`
Expected: PASS. The palette tests pin that the new parameters serialize the same for the Lead and every teammate.

- [ ] **Step 9: Format and commit**

```bash
gofumpt -w internal/agentteam/team.go internal/agentteam/task_fields_test.go internal/agent/team_tools.go internal/agent/team_task_role_test.go
git add internal/agentteam/team.go internal/agentteam/task_fields_test.go internal/agent/team_tools.go internal/agent/team_task_role_test.go
git commit -m "feat: let shared tasks name a role and a verify setting"
```

---

### Task 6: `agent_team_autostart` option wired to the service

**Files:**
- Modify: `internal/config/config.go` (`Options` after `AgentTeams`; getter after `GetAgentTeams`)
- Modify: `internal/shellconfig/options.go` (`optionSpecs`, after `"agent-teams"`)
- Modify: `schema.json` (regenerated)
- Modify: `internal/agentteam/team.go` (`Config`)
- Modify: `internal/agent/team.go` (`syncAgentTeams`, new methods)
- Test: `internal/config/options_agent_team_autostart_test.go` (create), `internal/shellconfig/agent_team_autostart_option_test.go` (create), `internal/agentteam/config_test.go` (create), `internal/agent/team_autostart_test.go` (create)

**Interfaces:**
- Consumes: `resolveRole`, `Options.GetAgentTeams()`.
- Produces:
  - `Options.AgentTeamAutostart *bool` (JSON `agent_team_autostart`), `func (o *Options) GetAgentTeamAutostart() bool` (nil means true).
  - `agentteam.Config` fields `Autostart func() bool` and `RoleExists func(name string) bool`; unexported `func (c Config) autostart() bool` (nil means on) and `func (c Config) roleExists(name string) bool` (nil means every role exists).
  - `func (c *coordinator) teamAutostart() bool`, `func (c *coordinator) teamRoleExists(name string) bool`.

- [ ] **Step 1: Write the failing tests**

`internal/config/options_agent_team_autostart_test.go`:

```go
package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsGetAgentTeamAutostart(t *testing.T) {
	t.Parallel()
	on, off := true, false
	require.True(t, (*Options)(nil).GetAgentTeamAutostart())
	require.True(t, (&Options{}).GetAgentTeamAutostart(), "autostart is on by default")
	require.False(t, (&Options{AgentTeamAutostart: &off}).GetAgentTeamAutostart())
	require.True(t, (&Options{AgentTeamAutostart: &on}).GetAgentTeamAutostart())

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{"agent_team_autostart":false}}`), &cfg))
	require.False(t, cfg.Options.GetAgentTeamAutostart())
}
```

`internal/shellconfig/agent_team_autostart_option_test.go`:

```go
package shellconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOption_AgentTeamAutostart pins the crushrc spelling of
// options.agent_team_autostart.
func TestOption_AgentTeamAutostart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "crushrc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte("option agent-team-autostart false\n"))
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))
	opts := result["options"].(map[string]any)
	require.Equal(t, false, opts["agent_team_autostart"])
}
```

`internal/agentteam/config_test.go`:

```go
package agentteam

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigPipelineDefaults(t *testing.T) {
	t.Parallel()
	require.True(t, Config{}.autostart(), "a nil Autostart means on")
	require.True(t, Config{}.roleExists("anything"), "a nil RoleExists accepts every role")
	require.False(t, Config{Autostart: func() bool { return false }}.autostart())
	require.False(t, Config{RoleExists: func(string) bool { return false }}.roleExists("gone"))
	require.NotNil(t, Config{Autostart: func() bool { return true }}.withDefaults().Autostart, "defaults keep the func")
}
```

`internal/agent/team_autostart_test.go`:

```go
package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTeamAutostartFollowsConfig pins that the service reads the option on
// every pass through the coordinator, so a reload applies without restart.
func TestTeamAutostartFollowsConfig(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	opts := coord.cfg.Config().Options
	require.True(t, coord.teamAutostart())

	off, on := false, true
	opts.AgentTeamAutostart = &off
	require.False(t, coord.teamAutostart())

	opts.AgentTeamAutostart = &on
	opts.AgentTeams = &off
	require.False(t, coord.teamAutostart(), "the pass does nothing with Agent Teams off")
}

func TestTeamRoleExists(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	require.True(t, coord.teamRoleExists("implementer"))
	require.False(t, coord.teamRoleExists("no-such-role"))
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/config/ ./internal/shellconfig/ ./internal/agentteam/ ./internal/agent/ -run 'AgentTeamAutostart|TestConfigPipelineDefaults|TestTeamAutostartFollowsConfig|TestTeamRoleExists'`
Expected: FAIL to compile (`unknown field AgentTeamAutostart`, `undefined: autostart`).

- [ ] **Step 3: Add the option**

In `internal/config/config.go`, after the `AgentTeams` field:

```go
	AgentTeamAutostart *bool `json:"agent_team_autostart,omitempty" jsonschema:"description=Start a teammate of the named role on its own when a shared task that names a role becomes ready. Tasks without a role stay manual.,default=true"`
```

After `GetAgentTeams`:

```go
// GetAgentTeamAutostart reports whether a ready shared task that names a
// role starts a teammate of that role on its own. It is on by default.
func (o *Options) GetAgentTeamAutostart() bool {
	if o == nil || o.AgentTeamAutostart == nil {
		return true
	}
	return *o.AgentTeamAutostart
}
```

In `internal/shellconfig/options.go`, after the `"agent-teams"` entry:

```go
	"agent-team-autostart":         {jsonKey: "agent_team_autostart", kind: optBool},
```

- [ ] **Step 4: Add the Config fields**

In `internal/agentteam/team.go`, replace `Config` with:

```go
// Config bounds a Team.
type Config struct {
	MaxMembers      int
	MaxTasks        int
	MaxMessageBytes int
	// Autostart reports whether a dispatch pass may start teammates. It is
	// read on every pass, so a config reload applies at once. Nil means on.
	Autostart func() bool
	// RoleExists reports whether a role name still resolves. Nil means every
	// role exists.
	RoleExists func(name string) bool
}

func (c Config) autostart() bool { return c.Autostart == nil || c.Autostart() }

func (c Config) roleExists(name string) bool { return c.RoleExists == nil || c.RoleExists(name) }
```

- [ ] **Step 5: Wire the coordinator**

In `internal/agent/team.go`, in `syncAgentTeams`, replace:

```go
		teamService, err := agentteam.New(teamPath, agentteam.Config{}, nil)
```

with:

```go
		teamService, err := agentteam.New(teamPath, agentteam.Config{
			Autostart:  c.teamAutostart,
			RoleExists: c.teamRoleExists,
		}, nil)
```

and add after `syncAgentTeams`:

```go
// teamAutostart reports whether the Team service may start teammates for
// ready tasks that name a role. The service calls it on every pass, so a
// config reload applies without a restart.
func (c *coordinator) teamAutostart() bool {
	opts := c.cfg.Config().Options
	return opts.GetAgentTeams() && opts.GetAgentTeamAutostart()
}

// teamRoleExists reports whether a task's role still resolves.
func (c *coordinator) teamRoleExists(name string) bool {
	_, refusal := resolveRole(c.cfg.Config(), name)
	return refusal == ""
}
```

- [ ] **Step 6: Regenerate the schema**

Run: `go run main.go schema > schema.json && git diff --stat schema.json`
Expected: `schema.json` changes by one `agent_team_autostart` property (about 5 lines). If anything else changes, stop and investigate before continuing.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/config/ ./internal/shellconfig/ ./internal/agentteam/ && go test ./internal/agent/ -run 'TestTeamAutostartFollowsConfig|TestTeamRoleExists|TestTeamPointer'`
Expected: PASS.

- [ ] **Step 8: Format and commit**

```bash
gofumpt -w internal/config/config.go internal/config/options_agent_team_autostart_test.go internal/shellconfig/options.go internal/shellconfig/agent_team_autostart_option_test.go internal/agentteam/team.go internal/agentteam/config_test.go internal/agent/team.go internal/agent/team_autostart_test.go
git add internal/config/config.go internal/config/options_agent_team_autostart_test.go internal/shellconfig/options.go internal/shellconfig/agent_team_autostart_option_test.go schema.json internal/agentteam/team.go internal/agentteam/config_test.go internal/agent/team.go internal/agent/team_autostart_test.go
git commit -m "feat: add the agent_team_autostart option"
```

---

### Task 7: The dispatch pass

**Files:**
- Create: `internal/agentteam/dispatch.go`
- Modify: `internal/agentteam/team.go` (`Task.RoleReported`, `Message.SessionID`, `inboxPendingLocked`, `SpawnTeammate`, `watch`, `settleTasksLocked`, `CreateTask`, `UpdateTask`)
- Test: `internal/agentteam/dispatch_test.go` (create)
- Test: `internal/agent/team_autostart_test.go` (extend)

**Interfaces:**
- Consumes: `Config.autostart()`, `Config.roleExists()`, `s.write`, `s.launch(ctx, runner, member, prompt) (MemberView, error)`, `s.failMember`, `s.statusLocked`, `s.scopeLocked`, `s.appendMessageLocked`, `taskReady`, `errShutdown`, test helpers from Task 5.
- Produces:
  - `const maxTaskAttempts = 3`
  - `type dispatchStart struct { member Member; taskID, sessionID string; revision int; prompt string }`
  - `func (s *Service) dispatch(ctx context.Context, root string)`
  - `func (s *Service) planDispatchLocked(root string) (starts []dispatchStart, leads []string, changed bool)`
  - `func (s *Service) dispatchFailed(start dispatchStart, err error)`
  - `func (s *Service) idleMemberLocked(role, sessionID string) (Member, bool)`
  - `func (s *Service) ownsActiveTaskLocked(memberID string) bool`
  - `func (s *Service) liveMembersLocked() int`
  - `func (s *Service) nextAutoNameLocked(role string) string`
  - `func (s *Service) addAutoMemberLocked(task Task) Member`
  - `func (s *Service) noteLeadLocked(sessionID, text string)`
  - `func wakeLeads(hook func(string), sessions []string)`
  - `func dispatchable(state State, task Task) bool`
  - `func (s *Service) hasDispatchableLocked(root string) bool` (cheap pre-check under `s.mu`, so a write with nothing to start takes no second file lock)
  - `func taskBrief(task Task) string`
  - `func escalationNote(task Task, reason string) string`
  - `Task.RoleReported bool` (JSON `role_reported,omitempty`), `Message.SessionID string` (JSON `session_id,omitempty`).
  - Test helpers: `newPipelineService(t, cfg Config) (*Service, *fakeRunner)`, `(*fakeRunner).memberNames() []string`, `(*fakeRunner).lastPrompt() string`, `memberStatus(s, name) Status`, `memberID(t, s, name) string`, `leadNotices(t, s, sessionID) []string`, `type refusingRunner`.

How the plan/start split uses the existing locking: `dispatch` first reads `hasDispatchableLocked` under `s.mu` alone and returns when there is nothing to start, so most writes cost no second file lock. Otherwise it calls `s.write(refusable, fn)`. `write` takes the in-process slot, the file lock, then `s.mu`, reloads, runs `fn`, saves, and releases all three before it returns. Inside `fn`, `planDispatchLocked` claims each task (owner, `in_progress`, revision bump), appends any spawned member, and sets `s.starting[member.Name] = true` for each claimed member, exactly as `watch` does before a resume. `starting` makes `statusLocked` report the member as running, so a concurrent pass neither picks it again nor under-counts the live cap. After `write` returns, `dispatch` calls `s.launch` for each planned start; `launch` already expects the caller to have set `starting`, checks `s.closed`, calls `runner.Start` without any lock, then records the run with its own `s.write(mustApply, ...)`. A start error removes `starting` (inside `launch`) and `dispatchFailed` releases the claim in a separate write. Concurrent passes serialize on `write`; the second sees the task owned and skips it.

- [ ] **Step 1: Write the failing dispatcher tests**

`internal/agentteam/dispatch_test.go`:

```go
package agentteam

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newPipelineService(t *testing.T, cfg Config) (*Service, *fakeRunner) {
	t.Helper()
	runner := newFakeRunner()
	service, err := New(filepath.Join(t.TempDir(), "team.json"), cfg, runner)
	require.NoError(t, err)
	return service, runner
}

func (r *fakeRunner) memberNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.members...)
}

func (r *fakeRunner) lastPrompt() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.prompts) == 0 {
		return ""
	}
	return r.prompts[len(r.prompts)-1]
}

func memberStatus(s *Service, name string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked(name)
}

func memberID(t *testing.T, s *Service, name string) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	member, ok := s.memberLocked(name)
	require.True(t, ok, "member %s", name)
	return member.ID
}

// leadNotices returns the runtime notices written for the Lead of sessionID.
func leadNotices(t *testing.T, s *Service, sessionID string) []string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var texts []string
	for _, message := range s.state.Messages {
		if message.From == noticeSender && message.To == leadName && message.SessionID == sessionID {
			texts = append(texts, message.Text)
		}
	}
	return texts
}

// refusingRunner fails every start, the way a model the config no longer
// serves does.
type refusingRunner struct {
	mu     sync.Mutex
	starts []string
}

func (r *refusingRunner) Start(_ context.Context, member Member, _ string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, member.Name)
	return nil, errors.New("provider unavailable")
}

func (r *refusingRunner) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.starts...)
}

func TestDispatchSpawnsAMemberForARoleTask(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "researcher-1", Description: "Busy", Prompt: "Work.", RoleName: "researcher", ParentID: pipelineSession,
	})
	require.NoError(t, err)

	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "Find the cause.", Role: "researcher", WriteScopes: []string{"docs"}})
	require.Equal(t, []string{"researcher-1", "researcher-2"}, runner.memberNames(),
		"a busy member of the role is not reused; the lowest unused name is")

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, memberID(t, s, "researcher-2"), got.OwnerID)
	require.Equal(t, 2, got.Revision)

	s.mu.Lock()
	spawned, _ := s.memberLocked("researcher-2")
	s.mu.Unlock()
	require.Equal(t, "researcher", spawned.RoleName)
	require.Equal(t, "Auto-started for task "+task.ID, spawned.Description)
	require.Equal(t, pipelineSession, spawned.ParentID)
	require.Equal(t, fmt.Sprintf("Task %s on the shared board was claimed for you: Research\n\nFind the cause.\n\n"+
		"Write scopes: docs\nComplete it with team_task_update (action \"complete\", expected_revision 2) when the acceptance criteria are met.",
		task.ID), runner.lastPrompt())
}

func TestDispatchReusesAnIdleMemberOfTheRole(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "rev", Description: "Researcher", Prompt: "Wait.", RoleName: "researcher", ParentID: pipelineSession,
	})
	require.NoError(t, err)
	runner.finish("rev")
	require.Eventually(t, func() bool { return memberStatus(s, "rev") == StatusInactive }, time.Second, 10*time.Millisecond)

	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Equal(t, []string{"rev", "rev"}, runner.memberNames())
	require.Equal(t, memberID(t, s, "rev"), taskByID(t, s, task.ID).OwnerID)
	require.Contains(t, runner.lastPrompt(), "Write scopes: none")
}

func TestDispatchNeverPicksAnotherSessionsMember(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "other", Description: "Other Lead's", Prompt: "Wait.", RoleName: "researcher", ParentID: "session-b",
	})
	require.NoError(t, err)
	runner.finish("other")
	require.Eventually(t, func() bool { return memberStatus(s, "other") == StatusInactive }, time.Second, 10*time.Millisecond)

	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Equal(t, memberID(t, s, "researcher-1"), taskByID(t, s, task.ID).OwnerID)
	require.Equal(t, []string{"other", "researcher-1"}, runner.memberNames())
}

func TestDispatchLeavesRoleLessTasksAlone(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Manual", Description: "d"})
	require.Empty(t, runner.memberNames())
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Empty(t, got.OwnerID)
}

func TestDispatchRespectsTheMemberCap(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{MaxMembers: 1})
	first := createTask(t, s, CreateTaskRequest{Subject: "One", Description: "d", Role: "researcher"})
	second := createTask(t, s, CreateTaskRequest{Subject: "Two", Description: "d", Role: "researcher"})
	require.Equal(t, []string{"researcher-1"}, runner.memberNames())
	require.Equal(t, TaskPending, taskByID(t, s, second.ID).Status, "at the cap the task waits")

	act(t, s, "researcher-1", first.ID, "complete", nil)
	runner.finish("researcher-1")
	require.Eventually(t, func() bool {
		return taskByID(t, s, second.ID).Status == TaskInProgress
	}, time.Second, 10*time.Millisecond, "a later pass starts the waiting task")
	require.Equal(t, []string{"researcher-1", "researcher-1"}, runner.memberNames())
}

func TestDispatchStartsATaskOnce(t *testing.T) {
	t.Parallel()
	var on atomic.Bool
	s, runner := newPipelineService(t, Config{Autostart: on.Load})
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Empty(t, runner.memberNames(), "autostart off starts nothing")

	on.Store(true)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { s.dispatch(context.Background(), pipelineSession) })
	}
	wg.Wait()
	require.Equal(t, []string{"researcher-1"}, runner.memberNames())
	require.Equal(t, TaskInProgress, taskByID(t, s, task.ID).Status)
}

func TestDispatchOptionOff(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Empty(t, runner.memberNames())
	require.Equal(t, TaskPending, taskByID(t, s, task.ID).Status)
}

func TestDispatchReportsARemovedRoleOnce(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{RoleExists: func(name string) bool { return name != "gone" }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Old", Description: "d", Role: "gone"})
	createTask(t, s, CreateTaskRequest{Subject: "Other", Description: "d"})
	s.dispatch(context.Background(), pipelineSession)

	require.Empty(t, runner.memberNames())
	notes := leadNotices(t, s, pipelineSession)
	require.Equal(t, []string{fmt.Sprintf(
		"Task %s (Old) names role \"gone\", which no longer exists. It is skipped; edit its role to continue.", task.ID,
	)}, notes)
}

func TestDispatchEscalatesAfterThreeFailedStarts(t *testing.T) {
	t.Parallel()
	runner := &refusingRunner{}
	s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, runner)
	require.NoError(t, err)
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	for range 3 {
		s.dispatch(context.Background(), pipelineSession)
	}

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Empty(t, got.OwnerID)
	require.Equal(t, 3, got.Attempts)
	require.True(t, got.Escalated)
	require.Equal(t, []string{"researcher-1", "researcher-2", "researcher-3"}, runner.names(),
		"a failed member is never picked again, and an escalated task is not started")
	require.Equal(t, StatusFailed, memberStatus(s, "researcher-1"))
	require.Equal(t, []string{fmt.Sprintf(
		"Task %s (Research) could not be started: provider unavailable. It is escalated; edit, reassign or reopen it to continue.", task.ID,
	)}, leadNotices(t, s, pipelineSession))
}

func TestDispatchFailedTurnsEscalate(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	for round := 1; round <= 3; round++ {
		require.Eventually(t, func() bool { return len(runner.memberNames()) == round }, time.Second, 10*time.Millisecond)
		runner.finishWithErr("researcher-1", errors.New("boom"))
		require.Eventually(t, func() bool { return taskByID(t, s, task.ID).Attempts == round }, time.Second, 10*time.Millisecond)
	}
	s.Shutdown()
	got := taskByID(t, s, task.ID)
	require.True(t, got.Escalated)
	require.Equal(t, TaskPending, got.Status)
	require.Len(t, runner.memberNames(), 3, "an escalated task is not started a fourth time")
}

func TestDispatchInterruptedTurnEscalates(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	runner.finishWithErr("researcher-1", context.Canceled)
	require.Eventually(t, func() bool { return taskByID(t, s, task.ID).Escalated }, time.Second, 10*time.Millisecond)
	s.Shutdown()
	require.Equal(t, []string{"researcher-1"}, runner.memberNames(), "an interrupted task does not restart on its own")
	require.Contains(t, leadNotices(t, s, pipelineSession)[0], "was interrupted")
}

func TestDispatchAfterShutdownStartsNothing(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	s.Shutdown()
	createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Empty(t, runner.memberNames())
}

func TestLeadNoticeReachesOnlyItsLead(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{RoleExists: func(string) bool { return false }})
	var woken []string
	var mu sync.Mutex
	s.SetLeadNotifyHook(func(sessionID string) {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, sessionID)
	})
	createTask(t, s, CreateTaskRequest{Subject: "Old", Description: "d", Role: "gone"})

	mu.Lock()
	require.Equal(t, []string{pipelineSession}, woken)
	mu.Unlock()
	require.Equal(t, 1, s.Tree(pipelineSession).UnreadByLead, "crush run waits for the Lead to read it")
	require.Empty(t, s.DrainInbox(leadName, "session-b"), "another Lead never sees it")
	pending := s.DrainInbox(leadName, pipelineSession)
	require.Len(t, pending, 1)
	require.Equal(t, noticeSender, pending[0].From)
}

// lockProbeRunner records whether Start ran while the dispatching service
// held its mutex or its write slot.
type lockProbeRunner struct {
	*fakeRunner
	service *Service
	key     string
	held    atomic.Bool
}

func (r *lockProbeRunner) Start(ctx context.Context, member Member, prompt string) (Run, error) {
	if r.service.mu.TryLock() {
		r.service.mu.Unlock()
	} else {
		r.held.Store(true)
	}
	if value, ok := stateLocks.Load(r.key); ok {
		slot := value.(chan struct{})
		select {
		case slot <- struct{}{}:
			<-slot
		default:
			r.held.Store(true)
		}
	}
	return r.fakeRunner.Start(ctx, member, prompt)
}

func TestDispatchStartsOutsideTheLock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	s, err := New(path, Config{}, nil)
	require.NoError(t, err)
	key, err := filepath.Abs(path)
	require.NoError(t, err)
	probe := &lockProbeRunner{fakeRunner: newFakeRunner(), service: s, key: key}
	s.SetRunner(probe)

	createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Equal(t, []string{"researcher-1"}, probe.memberNames())
	require.False(t, probe.held.Load(), "Runner.Start must run with no Team lock held")
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/agentteam/ -run 'TestDispatch|TestLeadNotice'`
Expected: FAIL to compile (`s.dispatch undefined`, `unknown field SessionID in struct literal`).

- [ ] **Step 3: Add the record fields and scope Lead notices by session**

In `internal/agentteam/team.go`, append to `Task` after `Escalated`:

```go
	// RoleReported records that the Lead was told the task's role no
	// longer exists, so it is told once.
	RoleReported bool `json:"role_reported,omitempty"`
```

Append to `Message` after `Delivered`:

```go
	// SessionID scopes a runtime notice to the Lead session it is for.
	// Member mail leaves it empty: the sender says which team it is from.
	SessionID string `json:"session_id,omitempty"`
```

In `inboxPendingLocked`, replace:

```go
	if name == leadName {
		member, ok := s.memberLocked(message.From)
		return ok && scope[member.ParentID]
	}
```

with:

```go
	if name == leadName {
		if message.From == noticeSender {
			return message.SessionID != "" && scope[message.SessionID]
		}
		member, ok := s.memberLocked(message.From)
		return ok && scope[member.ParentID]
	}
```

In the `edit` case of `applyTaskUpdateLocked`, inside the `if req.Role != nil` block, before `next.Role = role`, add:

```go
			if role != next.Role {
				next.RoleReported = false
			}
```

- [ ] **Step 4: Write the dispatch pass**

`internal/agentteam/dispatch.go`:

```go
package agentteam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// maxTaskAttempts is how many failed starts, failed turns or failed
// verifications a task takes before the dispatcher leaves it to the Lead.
const maxTaskAttempts = 3

// dispatchStart is one member start a pass planned under the lock.
type dispatchStart struct {
	member    Member
	taskID    string
	sessionID string
	revision  int
	prompt    string
}

// dispatch starts teammates for the ready tasks of root's team that name a
// role. It plans every claim and spawn in one write and starts the members
// after the write returns, so Runner.Start never runs under a lock. A pass
// that cannot get the state lock is skipped; the next write runs another.
func (s *Service) dispatch(ctx context.Context, root string) {
	if root == "" || !s.cfg.autostart() {
		return
	}
	// Most writes leave nothing to start. A cheap read under s.mu skips the
	// locked write, so a busy board does not take the file lock twice per
	// tool call.
	s.mu.Lock()
	worth := !s.closed && s.runner != nil && s.hasDispatchableLocked(root)
	s.mu.Unlock()
	if !worth {
		return
	}
	ctx = context.WithoutCancel(ctx)
	var (
		starts   []dispatchStart
		leads    []string
		runner   Runner
		leadHook func(sessionID string)
	)
	err := s.write(refusable, func() (bool, error) {
		if s.closed || s.runner == nil {
			return false, nil
		}
		runner = s.runner
		leadHook = s.leadNotifyHook
		var changed bool
		starts, leads, changed = s.planDispatchLocked(root)
		if changed {
			s.notifyLocked()
		}
		return changed, nil
	})
	if err != nil {
		slog.Debug("Skipped a Team dispatch pass", "session", root, "error", err)
		return
	}
	wakeLeads(leadHook, leads)
	for _, start := range starts {
		if _, err := s.launch(ctx, runner, start.member, start.prompt); err != nil {
			s.dispatchFailed(start, err)
		}
	}
}

// planDispatchLocked claims each dispatchable task of root's team for an
// idle member of its role, spawning one when there is none and the live
// count allows it. Each claimed member is marked starting, so it reads as
// running to every other pass until its start settles. Callers must hold
// s.mu inside write.
func (s *Service) planDispatchLocked(root string) (starts []dispatchStart, leads []string, changed bool) {
	scope := s.scopeLocked(leadName, root)
	for i := range s.state.Tasks {
		task := s.state.Tasks[i]
		if !scope[task.SessionID] || !dispatchable(s.state, task) {
			continue
		}
		if !s.cfg.roleExists(task.Role) {
			if !task.RoleReported {
				s.state.Tasks[i].RoleReported = true
				s.noteLeadLocked(task.SessionID, fmt.Sprintf(
					"Task %s (%s) names role %q, which no longer exists. It is skipped; edit its role to continue.",
					task.ID, task.Subject, task.Role))
				leads = append(leads, task.SessionID)
				changed = true
			}
			continue
		}
		member, ok := s.idleMemberLocked(task.Role, task.SessionID)
		if !ok {
			if s.liveMembersLocked() >= s.cfg.MaxMembers {
				continue
			}
			member = s.addAutoMemberLocked(task)
		}
		claimed := &s.state.Tasks[i]
		// A role that came back after a reload is reported again if it
		// ever goes away again.
		claimed.RoleReported = false
		claimed.OwnerID = member.ID
		claimed.Status = TaskInProgress
		claimed.Revision++
		s.starting[member.Name] = true
		starts = append(starts, dispatchStart{
			member:    member,
			taskID:    claimed.ID,
			sessionID: claimed.SessionID,
			revision:  claimed.Revision,
			prompt:    taskBrief(*claimed),
		})
		changed = true
	}
	return starts, leads, changed
}

// dispatchable reports whether the dispatcher may start task: pending,
// ready, unowned, naming a role, not escalated, and owned by a session.
func dispatchable(state State, task Task) bool {
	return task.Role != "" &&
		task.Status == TaskPending &&
		task.OwnerID == "" &&
		!task.Escalated &&
		task.SessionID != "" &&
		taskReady(state, task)
}

// hasDispatchableLocked reports whether a pass for root has anything to do:
// a dispatchable task whose role resolves, or whose missing role was not
// reported yet. Callers must hold s.mu.
func (s *Service) hasDispatchableLocked(root string) bool {
	scope := s.scopeLocked(leadName, root)
	for _, task := range s.state.Tasks {
		if !scope[task.SessionID] || !dispatchable(s.state, task) {
			continue
		}
		if !task.RoleReported || s.cfg.roleExists(task.Role) {
			return true
		}
	}
	return false
}

// idleMemberLocked returns the earliest-created member of role, spawned by
// sessionID, that is inactive, not failed, and owns no task in flight.
// Callers must hold s.mu.
func (s *Service) idleMemberLocked(role, sessionID string) (Member, bool) {
	for _, member := range s.state.Members {
		if member.RoleName != role || member.ParentID != sessionID || member.Failed {
			continue
		}
		if s.statusLocked(member.Name) != StatusInactive || s.ownsActiveTaskLocked(member.ID) {
			continue
		}
		return member, true
	}
	return Member{}, false
}

// ownsActiveTaskLocked reports whether memberID owns a task in progress or
// in review. Callers must hold s.mu.
func (s *Service) ownsActiveTaskLocked(memberID string) bool {
	for _, task := range s.state.Tasks {
		if task.OwnerID == memberID && (task.Status == TaskInProgress || task.Status == TaskInReview) {
			return true
		}
	}
	return false
}

// liveMembersLocked counts the members running or being created. The limit
// bounds the teammates working at once, not the durable roster. Callers
// must hold s.mu.
func (s *Service) liveMembersLocked() int {
	live := 0
	for _, member := range s.state.Members {
		switch s.statusLocked(member.Name) {
		case StatusRunning, StatusProvisioning:
			live++
		}
	}
	return live
}

// nextAutoNameLocked returns "<role>-<n>" with the lowest n no member uses.
// Names are permanent, so a failed or settled member keeps its number.
// Callers must hold s.mu.
func (s *Service) nextAutoNameLocked(role string) string {
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-%d", role, n)
		if !s.hasMemberLocked(name) {
			return name
		}
	}
}

// addAutoMemberLocked records a member the dispatcher spawns for task.
// Callers must hold s.mu.
func (s *Service) addAutoMemberLocked(task Task) Member {
	s.state.NextMember++
	member := Member{
		ID:          fmt.Sprintf("member-%d", s.state.NextMember),
		Name:        s.nextAutoNameLocked(task.Role),
		Role:        RoleTeammate,
		Description: "Auto-started for task " + task.ID,
		Provider:    "crush",
		Context:     ContextFresh,
		RoleName:    task.Role,
		ParentID:    task.SessionID,
		Status:      StatusProvisioning,
	}
	s.state.Members = append(s.state.Members, member)
	s.mine[member.Name] = true
	return member
}

// taskBrief is the message a dispatched member starts from. It is per-task
// text in a message, so the role block before it on a first turn stays the
// shared prefix of every sibling with that role.
func taskBrief(task Task) string {
	scopes := "none"
	if len(task.WriteScopes) > 0 {
		scopes = strings.Join(task.WriteScopes, ", ")
	}
	last := fmt.Sprintf("Complete it with team_task_update (action \"complete\", expected_revision %d) when the acceptance criteria are met.", task.Revision)
	if task.Verifies != "" {
		last = `Record your verdict with team_task_update, action "pass", or action "fail" with notes describing what failed.`
	}
	return fmt.Sprintf("Task %s on the shared board was claimed for you: %s\n\n%s\n\nWrite scopes: %s\n%s",
		task.ID, task.Subject, task.Description, scopes, last)
}

// escalationNote is the Lead notice for a task the dispatcher gave up on.
func escalationNote(task Task, reason string) string {
	return fmt.Sprintf("Task %s (%s) could not be started: %s. It is escalated; edit, reassign or reopen it to continue.",
		task.ID, task.Subject, reason)
}

// dispatchFailed releases a claim whose start failed. A failure counts as an
// attempt and escalates the task at the cap; a start refused by shutdown is
// released without counting, so a later Service retries it.
func (s *Service) dispatchFailed(start dispatchStart, err error) {
	shutdown := errors.Is(err, errShutdown)
	var (
		leads    []string
		leadHook func(sessionID string)
	)
	_ = s.write(mustApply, func() (bool, error) {
		leadHook = s.leadNotifyHook
		for i := range s.state.Tasks {
			task := &s.state.Tasks[i]
			if task.ID != start.taskID || task.SessionID != start.sessionID {
				continue
			}
			if task.OwnerID != start.member.ID || task.Status != TaskInProgress || task.Revision != start.revision {
				// Someone moved the task on since the claim; leave it.
				return false, nil
			}
			task.Revision++
			task.Status = TaskPending
			task.OwnerID = ""
			if !shutdown {
				task.Attempts++
				if task.Attempts >= maxTaskAttempts {
					task.Escalated = true
					s.noteLeadLocked(task.SessionID, escalationNote(*task, err.Error()))
					leads = append(leads, task.SessionID)
				}
			}
			s.notifyLocked()
			return true, nil
		}
		return false, nil
	})
	if shutdown {
		return
	}
	wakeLeads(leadHook, leads)
	s.failMember(start.member.Name, err)
}

// noteLeadLocked queues a runtime notice for the Lead of sessionID. The
// session on the message is what scopes it, since the runtime is not a
// member. Callers must hold s.mu and wake the Lead after the write.
func (s *Service) noteLeadLocked(sessionID, text string) {
	if sessionID == "" {
		return
	}
	s.appendMessageLocked(noticeSender, leadName, text)
	s.state.Messages[len(s.state.Messages)-1].SessionID = sessionID
}

// wakeLeads calls hook once per distinct session, outside every lock.
func wakeLeads(hook func(sessionID string), sessions []string) {
	if hook == nil {
		return
	}
	seen := make(map[string]bool, len(sessions))
	for _, id := range sessions {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		hook(id)
	}
}
```

- [ ] **Step 5: Count failed turns of role tasks**

In `internal/agentteam/team.go`, replace the body of the loop in `settleTasksLocked`:

```go
		task.Revision++
		if runErr == nil {
			task.Status = TaskCompleted
			settled = append(settled, task.ID+" completed")
			completed = append(completed, task.Subject)
		} else {
			task.Status = TaskPending
			task.OwnerID = ""
			settled = append(settled, task.ID+" released")
		}
```

with:

```go
		task.Revision++
		switch {
		case runErr != nil:
			task.Status = TaskPending
			task.OwnerID = ""
			settled = append(settled, task.ID+" released")
			s.countFailedTurnLocked(task, runErr)
		default:
			task.Status = TaskCompleted
			settled = append(settled, task.ID+" completed")
			completed = append(completed, task.Subject)
		}
```

and add after `settleTasksLocked`:

```go
// countFailedTurnLocked charges a failed turn to a task the dispatcher owns,
// so a teammate whose every turn fails is not restarted forever. An
// interrupted turn is the Lead's decision, so it escalates at once instead
// of restarting. A shutdown cancels every run and counts nothing. Callers
// must hold s.mu.
func (s *Service) countFailedTurnLocked(task *Task, runErr error) {
	if task.Role == "" || s.closed {
		return
	}
	if errors.Is(runErr, context.Canceled) {
		task.Escalated = true
		s.noteLeadLocked(task.SessionID, fmt.Sprintf(
			"Task %s (%s) was interrupted. It is escalated; edit, reassign or reopen it to continue.",
			task.ID, task.Subject))
		return
	}
	task.Attempts++
	if task.Attempts >= maxTaskAttempts {
		task.Escalated = true
		s.noteLeadLocked(task.SessionID, escalationNote(*task, runErr.Error()))
	}
}
```

The Lead notice from a settled turn needs no extra wake: `watch` already calls the Lead hook with `member.ParentID` after every settle.

- [ ] **Step 6: Run a pass after every write that can make work possible**

In `SpawnTeammate`, replace the live count inside the write:

```go
		live := 0
		for _, existing := range s.state.Members {
			switch s.statusLocked(existing.Name) {
			case StatusRunning, StatusProvisioning:
				live++
			}
		}
		if live >= s.cfg.MaxMembers {
```

with:

```go
		if s.liveMembersLocked() >= s.cfg.MaxMembers {
```

(keep the comment above it), and replace the tail:

```go
	view, err := s.startRun(ctx, member, req.Prompt)
	if err != nil {
		if errors.Is(err, errShutdown) {
			// Shutdown owns the Team now. Failing the member here would
			// write after Shutdown returned and leave a teammate a later
			// enable resumes as failed.
			return MemberView{}, err
		}
		s.failMember(member.Name, err)
		return s.viewFor(member.Name), nil
	}
	return view, nil
```

with:

```go
	view, err := s.startRun(ctx, member, req.Prompt)
	if err != nil {
		if errors.Is(err, errShutdown) {
			// Shutdown owns the Team now. Failing the member here would
			// write after Shutdown returned and leave a teammate a later
			// enable resumes as failed.
			return MemberView{}, err
		}
		s.failMember(member.Name, err)
		s.dispatch(ctx, req.ParentID)
		return s.viewFor(member.Name), nil
	}
	s.dispatch(ctx, req.ParentID)
	return view, nil
```

In `watch`, replace:

```go
	if leadHook != nil && !wasClosed {
		leadHook(member.ParentID)
	}
	if len(pending) == 0 {
		return
	}
```

with:

```go
	if leadHook != nil && !wasClosed {
		leadHook(member.ParentID)
	}
	// The settle may have freed a member or finished a blocker.
	if !wasClosed {
		s.dispatch(context.Background(), member.ParentID)
	}
	if len(pending) == 0 {
		return
	}
```

In `CreateTask`, replace the tail:

```go
	if err != nil {
		return TaskView{}, err
	}
	return view, nil
}
```

with:

```go
	if err != nil {
		return TaskView{}, err
	}
	s.dispatch(context.Background(), view.SessionID)
	return view, nil
}
```

In `UpdateTask`, replace:

```go
	if applied.completedSubject != "" && hook != nil {
		hook(applied.parentSessionID, applied.completedSubject)
	}
	return applied.view, nil
```

with:

```go
	if applied.completedSubject != "" && hook != nil {
		hook(applied.parentSessionID, applied.completedSubject)
	}
	s.dispatch(context.Background(), applied.parentSessionID)
	return applied.view, nil
```

- [ ] **Step 7: Run the agentteam tests to verify they pass**

Run: `go test ./internal/agentteam/ -race`
Expected: PASS, including every pre-existing test (role-less tasks never dispatch).

- [ ] **Step 8: Write the failing coordinator wiring test**

Append to `internal/agent/team_autostart_test.go` (add imports `"github.com/charmbracelet/crush/internal/agentteam"`):

```go
// TestSyncAgentTeamsPassesAutostart pins the wiring end to end: the service
// syncAgentTeams creates reads the option on every pass.
func TestSyncAgentTeamsPassesAutostart(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	opts := coord.cfg.Config().Options
	off, on := false, true
	opts.AgentTeams = &on
	opts.AgentTeamAutostart = &off
	require.NoError(t, coord.syncAgentTeams())
	team := coord.teamService()
	require.NotNil(t, team)
	t.Cleanup(team.Shutdown)
	runner := newTeamFakeRunner()
	team.SetRunner(runner)

	_, err := team.CreateTask("lead", "session-lead", agentteam.CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.NoError(t, err)
	runner.mu.Lock()
	require.Zero(t, runner.count, "autostart off starts nothing")
	runner.mu.Unlock()

	opts.AgentTeamAutostart = &on
	_, err = team.CreateTask("lead", "session-lead", agentteam.CreateTaskRequest{Subject: "Manual", Description: "d"})
	require.NoError(t, err)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, 1, runner.count, "the next pass reads the reloaded option")
}
```

- [ ] **Step 9: Run it to verify it passes**

Run: `go test ./internal/agent/ -run 'TestSyncAgentTeamsPassesAutostart|TestTeamAutostart|TestTeamRoleExists|TestTeamTools'`
Expected: PASS. A failure here means the Task 6 wiring does not reach the service.

- [ ] **Step 10: Format and commit**

```bash
gofumpt -w internal/agentteam/dispatch.go internal/agentteam/dispatch_test.go internal/agentteam/team.go internal/agent/team_autostart_test.go
git add internal/agentteam/dispatch.go internal/agentteam/dispatch_test.go internal/agentteam/team.go internal/agent/team_autostart_test.go
git commit -m "feat: start a teammate for each ready task that names a role"
```

---

### Task 8: Verification

**Files:**
- Create: `internal/agentteam/verify.go`
- Modify: `internal/agentteam/team.go` (`taskUpdate`, `UpdateTask`, `applyTaskUpdateLocked` `complete` and `reopen` cases, new `pass`/`fail` case, tail; `settleTasksLocked`)
- Test: `internal/agentteam/verify_test.go` (create)

**Interfaces:**
- Consumes: `maxTaskAttempts`, `verifierRole`, `maxTaskSubject`, `Task.needsVerification`, `s.noteLeadLocked`, `wakeLeads`, `s.dispatch`, `s.startRun`, `s.DrainInbox`, `resumePrompt`, `truncateText`, Task 5 and Task 7 test helpers.
- Produces:
  - `func (s *Service) newVerificationTaskLocked(original Task) Task`
  - `func (s *Service) applyVerdictLocked(scope map[string]bool, check Task, verifier Member, verdict, notes string) (completedSubject string, wake, leads []string)`
  - `func (s *Service) wake(ctx context.Context, name string)`
  - `func truncateRunes(s string, n int) string`
  - `taskUpdate` fields `wake []string`, `leadSessions []string`.
  - Actions `pass` and `fail` on `team_task_update`; `reopen` also accepts `in_review`.
  - Test helper `verificationTaskFor(t, s, originalID string) Task`.

- [ ] **Step 1: Write the failing tests**

`internal/agentteam/verify_test.go`:

```go
package agentteam

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// verificationTaskFor returns the newest verification task of originalID.
func verificationTaskFor(t *testing.T, s *Service, originalID string) Task {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var found Task
	for _, task := range s.state.Tasks {
		if task.Verifies == originalID {
			found = task
		}
	}
	require.NotEmpty(t, found.ID, "no verification task for %s", originalID)
	return found
}

func ownerName(t *testing.T, s *Service, taskID string) string {
	t.Helper()
	view, err := s.GetTask(leadName, pipelineSession, taskID)
	require.NoError(t, err)
	return view.OwnerName
}

func TestCompleteMovesToReviewAndCreatesTheCheck(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "Make it work.", Role: "implementer", WriteScopes: []string{"internal/x"}})
	act(t, s, leadName, task.ID, "claim", nil)
	view := act(t, s, leadName, task.ID, "complete", nil)
	require.Equal(t, TaskInReview, view.Status)

	check := verificationTaskFor(t, s, task.ID)
	require.Equal(t, "Verify: Build", check.Subject)
	require.Equal(t, "verifier", check.Role)
	require.Equal(t, TaskPending, check.Status)
	require.NotNil(t, check.Verify)
	require.False(t, *check.Verify)
	require.Empty(t, check.WriteScopes)
	require.Equal(t, pipelineSession, check.SessionID)
	require.Equal(t, fmt.Sprintf("Verify task %s: Build\n\nAcceptance criteria and description of the original task:\nMake it work.\n\n"+
		"Implemented by: lead\nWrite scopes: internal/x\nThe change is in the shared working directory; inspect it with git status and git diff.", task.ID),
		check.Description)
}

func TestVerificationSubjectIsTruncated(t *testing.T) {
	t.Parallel()
	s := newBareService(t)
	long := ""
	for range maxTaskSubject {
		long += "é"
	}
	task := createTask(t, s, CreateTaskRequest{Subject: long, Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	require.Len(t, []rune(verificationTaskFor(t, s, task.ID).Subject), maxTaskSubject)
}

func TestHandOffWithoutVerification(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	a := createTask(t, s, CreateTaskRequest{Subject: "A", Description: "d", Role: "researcher"})
	b := createTask(t, s, CreateTaskRequest{Subject: "B", Description: "d", Role: "researcher", BlockedBy: []string{a.ID}})
	require.Equal(t, TaskPending, taskByID(t, s, b.ID).Status)

	act(t, s, "researcher-1", a.ID, "complete", nil)
	require.Equal(t, TaskCompleted, taskByID(t, s, a.ID).Status)
	require.Equal(t, TaskInProgress, taskByID(t, s, b.ID).Status, "completing A starts B")
	require.Equal(t, []string{"researcher-1", "researcher-2"}, runner.memberNames())
}

func TestHandOffWaitsForVerification(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	a := createTask(t, s, CreateTaskRequest{Subject: "A", Description: "d", Role: "implementer"})
	b := createTask(t, s, CreateTaskRequest{Subject: "B", Description: "d", Role: "researcher", BlockedBy: []string{a.ID}})

	act(t, s, "implementer-1", a.ID, "complete", nil)
	require.Equal(t, TaskInReview, taskByID(t, s, a.ID).Status)
	require.Equal(t, TaskPending, taskByID(t, s, b.ID).Status, "an in-review blocker keeps B waiting")
	check := verificationTaskFor(t, s, a.ID)
	require.Equal(t, "verifier-1", ownerName(t, s, check.ID), "the verifier starts on its own")
	require.Contains(t, runner.lastPrompt(), `Record your verdict with team_task_update, action "pass", or action "fail" with notes describing what failed.`)

	act(t, s, "verifier-1", check.ID, "pass", nil)
	require.Equal(t, TaskCompleted, taskByID(t, s, check.ID).Status)
	require.Equal(t, TaskCompleted, taskByID(t, s, a.ID).Status)
	require.Equal(t, TaskInProgress, taskByID(t, s, b.ID).Status, "B starts only after pass")
	require.Equal(t, []string{"implementer-1", "verifier-1", "researcher-1"}, runner.memberNames())
}

func TestFailReopensAndWakesTheOwner(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", task.ID, "complete", nil)
	runner.finish("implementer-1")
	require.Eventually(t, func() bool { return memberStatus(s, "implementer-1") == StatusInactive }, time.Second, 10*time.Millisecond)
	check := verificationTaskFor(t, s, task.ID)
	before := runner.promptCount()

	act(t, s, "verifier-1", check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "TestX fails" })
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, memberID(t, s, "implementer-1"), got.OwnerID)
	require.Equal(t, 1, got.Attempts)
	require.Equal(t, TaskCompleted, taskByID(t, s, check.ID).Status)
	require.Equal(t, before+1, runner.promptCount(), "the owner is woken")
	require.Contains(t, runner.lastPrompt(), fmt.Sprintf(
		"verifier-1: Verification of task %s failed:\n\nTestX fails\n\nFix it and complete the task again.", task.ID))
}

func TestThirdFailEscalatesInReview(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	for attempt := 1; attempt <= 3; attempt++ {
		act(t, s, "implementer-1", task.ID, "complete", nil)
		check := verificationTaskFor(t, s, task.ID)
		act(t, s, ownerName(t, s, check.ID), check.ID, "fail", func(r *UpdateTaskRequest) {
			r.Notes = fmt.Sprintf("attempt %d broke", attempt)
		})
	}
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInReview, got.Status, "an escalated original stays in review")
	require.True(t, got.Escalated)
	require.Equal(t, 3, got.Attempts)
	notes := leadNotices(t, s, pipelineSession)
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], "failed verification 3 times")
	require.Contains(t, notes[0], "attempt 3 broke")

	act(t, s, leadName, task.ID, "reopen", nil)
	got = taskByID(t, s, task.ID)
	require.False(t, got.Escalated, "reopen by the Lead clears the escalation")
	require.Zero(t, got.Attempts)
}

func TestVerdictRules(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)

	require.Equal(t, CodeTaskInvalidTransition, ErrorCode(actErr(s, leadName, task.ID, "pass", nil)),
		"pass on a task that verifies nothing")
	require.Equal(t, CodeTaskUnauthorized, ErrorCode(actErr(s, leadName, check.ID, "pass", nil)),
		"only the verification task's owner decides")
	require.Equal(t, CodeInvalidArgument, ErrorCode(actErr(s, "verifier-1", check.ID, "fail", nil)),
		"fail needs notes")
	require.Equal(t, CodeTaskInvalidTransition, ErrorCode(actErr(s, "verifier-1", check.ID, "complete", nil)),
		"a verification task records a verdict instead of completing")
}

func TestNoVerdictReleasesAndEscalates(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "ver", Description: "Verifier", Prompt: "Check.", RoleName: "verifier", ParentID: pipelineSession,
	})
	require.NoError(t, err)

	for round := 1; round <= 3; round++ {
		act(t, s, "ver", check.ID, "claim", nil)
		runner.finish("ver")
		require.Eventually(t, func() bool {
			got := taskByID(t, s, check.ID)
			return got.Status == TaskPending && got.Attempts == round
		}, time.Second, 10*time.Millisecond, "a turn without a verdict releases the check")
		if round < 3 {
			require.NoError(t, s.SendMessage(context.Background(), leadName, pipelineSession, SendRequest{Target: "ver", Message: "Try again."}))
		}
	}
	require.True(t, taskByID(t, s, check.ID).Escalated)
	require.Equal(t, TaskInReview, taskByID(t, s, task.ID).Status, "no verdict never passes the work")
	require.Contains(t, leadNotices(t, s, pipelineSession)[0], "got no verdict")
}

func TestSettleSendsVerifiedWorkToReview(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	runner.finish("implementer-1")
	require.Eventually(t, func() bool { return taskByID(t, s, task.ID).Status == TaskInReview }, time.Second, 10*time.Millisecond)
	require.Equal(t, task.ID, verificationTaskFor(t, s, task.ID).Verifies)
}

func TestVerdictAfterOriginalMoved(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, task.ID, "reopen", nil)
	reopened := taskByID(t, s, task.ID)

	act(t, s, "verifier-1", check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "late" })
	require.Equal(t, TaskCompleted, taskByID(t, s, check.ID).Status)
	got := taskByID(t, s, task.ID)
	require.Equal(t, reopened.Revision, got.Revision, "a verdict leaves an original that left review alone")
	require.Zero(t, got.Attempts)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/agentteam/ -run 'TestComplete|TestVerification|TestHandOff|TestFail|TestThirdFail|TestVerdict|TestNoVerdict|TestSettleSends'`
Expected: FAIL (`complete` still completes; `unsupported task action "pass"`).

- [ ] **Step 3: Write the verification helpers**

`internal/agentteam/verify.go`:

```go
package agentteam

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// newVerificationTaskLocked builds the task that checks original, which just
// entered review, and takes its id. The caller appends it in the same write.
// Callers must hold s.mu.
func (s *Service) newVerificationTaskLocked(original Task) Task {
	s.state.NextTask++
	implementer := "none"
	if original.OwnerID == leadName {
		implementer = leadName
	}
	for _, member := range s.state.Members {
		if member.ID == original.OwnerID {
			implementer = member.Name
		}
	}
	scopes := "none"
	if len(original.WriteScopes) > 0 {
		scopes = strings.Join(original.WriteScopes, ", ")
	}
	return Task{
		ID:       fmt.Sprintf("task-%d", s.state.NextTask),
		Revision: 1,
		Subject:  truncateRunes("Verify: "+original.Subject, maxTaskSubject),
		Description: fmt.Sprintf("Verify task %s: %s\n\nAcceptance criteria and description of the original task:\n%s\n\n"+
			"Implemented by: %s\nWrite scopes: %s\nThe change is in the shared working directory; inspect it with git status and git diff.",
			original.ID, original.Subject, original.Description, implementer, scopes),
		Status:      TaskPending,
		BlockedBy:   []string{},
		WriteScopes: []string{},
		SessionID:   original.SessionID,
		Role:        verifierRole,
		Verify:      new(false),
		Verifies:    original.ID,
	}
}

// applyVerdictLocked applies a verifier's verdict to the task check
// verifies. An original that already left review (reopened, deleted) is
// left alone. It returns the completed subject for the todo hook, the
// members to wake, and the Lead sessions a notice was written for. Callers
// must hold s.mu.
func (s *Service) applyVerdictLocked(scope map[string]bool, check Task, verifier Member, verdict, notes string) (completedSubject string, wake, leads []string) {
	index, original, ok := s.taskIndexLocked(scope, check.Verifies)
	if !ok || original.Status != TaskInReview {
		return "", nil, nil
	}
	original.Revision++
	if verdict == "pass" {
		original.Status = TaskCompleted
		s.state.Tasks[index] = original
		return original.Subject, nil, nil
	}
	original.Attempts++
	if original.Attempts >= maxTaskAttempts {
		original.Escalated = true
		s.state.Tasks[index] = original
		s.noteLeadLocked(original.SessionID, fmt.Sprintf(
			"Task %s (%s) failed verification %d times and stays in review, escalated. Reopen it to try again, or delete it. Last notes:\n\n%s",
			original.ID, original.Subject, original.Attempts, notes))
		return "", nil, []string{original.SessionID}
	}
	original.Status = TaskInProgress
	s.state.Tasks[index] = original
	text := fmt.Sprintf("Verification of task %s failed:\n\n%s\n\nFix it and complete the task again.", original.ID, notes)
	if original.OwnerID == leadName {
		s.noteLeadLocked(original.SessionID, text)
		return "", nil, []string{original.SessionID}
	}
	for _, member := range s.state.Members {
		if member.ID == original.OwnerID {
			s.appendMessageLocked(verifier.Name, member.Name, text)
			return "", []string{member.Name}, nil
		}
	}
	return "", nil, nil
}

// wake starts an inactive teammate on the mail waiting for it, the way
// SendMessage does. A running teammate reads it at its next step.
func (s *Service) wake(ctx context.Context, name string) {
	s.mu.Lock()
	target, ok := s.memberLocked(name)
	running := s.statusLocked(name) == StatusRunning
	s.mu.Unlock()
	if !ok || running || target.Role == RoleLead {
		return
	}
	pending := s.DrainInbox(target.Name, target.ParentID)
	if len(pending) == 0 {
		return
	}
	if _, err := s.startRun(ctx, target, resumePrompt(pending)); err != nil {
		s.mu.Lock()
		s.notifyLocked()
		s.mu.Unlock()
		slog.Warn("Could not wake a teammate after a failed verification", "member", name, "error", err)
	}
}

// truncateRunes cuts s to at most n runes.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
```

- [ ] **Step 4: Apply review, verdicts and the new reopen rule**

In `internal/agentteam/team.go`, replace `taskUpdate` with:

```go
// taskUpdate is one applied task transition and what the caller must
// report once the write lands.
type taskUpdate struct {
	view             TaskView
	parentSessionID  string
	completedSubject string
	// wake names members to start once the write lands, such as the owner
	// a failed verification hands its task back to.
	wake []string
	// leadSessions are the Lead sessions a notice was written for.
	leadSessions []string
}
```

Replace `UpdateTask` with:

```go
// UpdateTask applies one compare-and-set task transition.
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
	// The hooks run outside every lock, and only once the record is
	// durable.
	if applied.completedSubject != "" && hook != nil {
		hook(applied.parentSessionID, applied.completedSubject)
	}
	wakeLeads(leadHook, applied.leadSessions)
	for _, name := range applied.wake {
		s.wake(context.Background(), name)
	}
	s.dispatch(context.Background(), applied.parentSessionID)
	return applied.view, nil
}
```

In `applyTaskUpdateLocked`, after `next := current`, add:

```go
	var (
		review  bool
		verdict string
		notes   string
	)
```

Replace the `complete` case with:

```go
	case "complete":
		if err := authorizeOwner(); err != nil {
			return taskUpdate{}, err
		}
		if current.Status != TaskInProgress {
			return taskUpdate{}, newError(CodeTaskInvalidTransition, "only an in-progress task can complete")
		}
		if current.Verifies != "" {
			return taskUpdate{}, newError(CodeTaskInvalidTransition,
				"a verification task records its verdict with action pass, or fail with notes")
		}
		if current.needsVerification() {
			next.Status = TaskInReview
			review = true
			break
		}
		next.Status = TaskCompleted
		completedSubject = next.Subject
	case "pass", "fail":
		if current.Verifies == "" {
			return taskUpdate{}, newError(CodeTaskInvalidTransition, "only a verification task takes %s", req.Action)
		}
		if !owner {
			return taskUpdate{}, newError(CodeTaskUnauthorized, "only the owner of verification task %q can record its verdict", current.ID)
		}
		if current.Status != TaskInProgress {
			return taskUpdate{}, newError(CodeTaskInvalidTransition, "a verdict needs the verification task in progress")
		}
		notes = strings.TrimSpace(req.Notes)
		if req.Action == "fail" && notes == "" {
			return taskUpdate{}, newError(CodeInvalidArgument, "fail requires notes describing what failed")
		}
		next.Status = TaskCompleted
		verdict = req.Action
```

In the `reopen` case replace:

```go
		if current.Status != TaskCompleted {
			return taskUpdate{}, newError(CodeTaskInvalidTransition, "only a completed task can reopen")
		}
```

with:

```go
		if current.Status != TaskCompleted && current.Status != TaskInReview {
			return taskUpdate{}, newError(CodeTaskInvalidTransition, "only a completed or in-review task can reopen")
		}
```

Replace the tail after `assertTaskGraph`:

```go
	// Written by index: an old file may hold the same id twice, and the
	// copy the caller acted on is the one in its scope.
	s.state.Tasks[index] = next
	return taskUpdate{
		view:             projectTaskView(s.state, next),
		parentSessionID:  parentSessionID,
		completedSubject: completedSubject,
	}, nil
```

with:

```go
	// Written by index: an old file may hold the same id twice, and the
	// copy the caller acted on is the one in its scope.
	s.state.Tasks[index] = next
	update := taskUpdate{parentSessionID: parentSessionID, completedSubject: completedSubject}
	if review {
		// The check is created in the same write, so no reader ever sees
		// work in review with nothing to review it.
		s.state.Tasks = append(s.state.Tasks, s.newVerificationTaskLocked(next))
	}
	if verdict != "" {
		subject, wake, leads := s.applyVerdictLocked(scope, next, member, verdict, notes)
		if subject != "" {
			update.completedSubject = subject
		}
		update.wake = wake
		update.leadSessions = leads
	}
	update.view = projectTaskView(s.state, next)
	return update, nil
```

- [ ] **Step 5: Apply the settle rules**

Replace `settleTasksLocked` with:

```go
// settleTasksLocked closes out the tasks a finished teammate left in flight.
// A successful turn completes them, or sends them to review when they are
// verified; a verification task is never completed this way, because a
// verifier that never decides must not pass work by default. A failed or
// interrupted turn releases them back to the board so they never stay
// half-done forever. It returns the report labels and the subjects of the
// completed tasks.
func (s *Service) settleTasksLocked(ownerID string, runErr error) (settled, completed []string) {
	if ownerID == "" || ownerID == leadName {
		return nil, nil
	}
	var review []int
	for i := range s.state.Tasks {
		task := &s.state.Tasks[i]
		if task.OwnerID != ownerID || task.Status != TaskInProgress {
			continue
		}
		task.Revision++
		switch {
		case runErr != nil:
			task.Status = TaskPending
			task.OwnerID = ""
			settled = append(settled, task.ID+" released")
			s.countFailedTurnLocked(task, runErr)
		case task.Verifies != "":
			task.Status = TaskPending
			task.OwnerID = ""
			task.Attempts++
			settled = append(settled, task.ID+" released without a verdict")
			if task.Attempts >= maxTaskAttempts {
				task.Escalated = true
				s.noteLeadLocked(task.SessionID, fmt.Sprintf(
					"Task %s (%s) got no verdict from its verifier %d times. It is escalated; edit, reassign or reopen it to continue.",
					task.ID, task.Subject, task.Attempts))
			}
		case task.needsVerification():
			task.Status = TaskInReview
			settled = append(settled, task.ID+" in review")
			review = append(review, i)
		default:
			task.Status = TaskCompleted
			settled = append(settled, task.ID+" completed")
			completed = append(completed, task.Subject)
		}
	}
	// Appended after the loop: an append can move the slice, and the loop
	// holds a pointer into it.
	for _, i := range review {
		s.state.Tasks = append(s.state.Tasks, s.newVerificationTaskLocked(s.state.Tasks[i]))
	}
	return settled, completed
}
```

- [ ] **Step 6: Run the agentteam tests to verify they pass**

Run: `go test ./internal/agentteam/ -race`
Expected: PASS, including the existing settle tests (role-less tasks still complete with the label `completed`).

- [ ] **Step 7: Format and commit**

```bash
gofumpt -w internal/agentteam/verify.go internal/agentteam/verify_test.go internal/agentteam/team.go
git add internal/agentteam/verify.go internal/agentteam/verify_test.go internal/agentteam/team.go
git commit -m "feat: verify finished implementer work before dependents start"
```

---

### Task 9: Enforced write scopes

**Files:**
- Create: `internal/agent/scope_gate.go`
- Modify: `internal/agent/coordinator.go` (`buildTools`, after the `wrapToolsWithHooks` call)
- Modify: `internal/agentteam/team.go` (new `ClaimedWriteScopes`)
- Test: `internal/agent/scope_gate_test.go` (create), `internal/agent/team_cache_test.go` (extend), `internal/agentteam/task_fields_test.go` (extend)

**Interfaces:**
- Consumes: `fakeTool`, `newRunner`, `roleMCPTool`, `tools.PatchFilePaths`, `tools.ApplyPatchParams`, `filepathext.SmartJoin`, `home.Long`, tool name constants (`tools.EditToolName`, `tools.MultiEditToolName`, `tools.WriteToolName`, `tools.ApplyPatchToolName`, `tools.RenameToolName`, `tools.ReplaceSymbolToolName`), `newTeammateTestCoordinator`.
- Produces:
  - `func (s *Service) ClaimedWriteScopes(member string) []string`
  - `var scopedWriteTools []string`
  - `type scopeGatedTool struct { inner fantasy.AgentTool; scopes func(context.Context) []string; workingDir string }` with `Info`, `MCP`, `ProviderOptions`, `SetProviderOptions`, `Run`
  - `func gateToolsForScopes(list []fantasy.AgentTool, scopes func(context.Context) []string, workingDir string) []fantasy.AgentTool`
  - `func (c *coordinator) claimedScopes(ctx context.Context) []string`
  - `func scopedPaths(tool, input string) ([]string, error)`, `func workspaceRelativePath(workingDir, path string) (string, bool)`, `func pathInScopes(rel string, scopes []string) bool`

- [ ] **Step 1: Write the failing service test**

Append to `internal/agentteam/task_fields_test.go` (add import `"context"`):

```go
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
```

- [ ] **Step 2: Write the failing gate tests**

`internal/agent/scope_gate_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
)

func fixedScopes(scopes ...string) func(context.Context) []string {
	return func(context.Context) []string { return scopes }
}

func scopedTool(t *testing.T, name, workingDir string, scopes ...string) (fantasy.AgentTool, *fakeTool) {
	t.Helper()
	inner := &fakeTool{name: name, resp: fantasy.NewTextResponse("ok")}
	return gateToolsForScopes([]fantasy.AgentTool{inner}, fixedScopes(scopes...), workingDir)[0], inner
}

func runScoped(t *testing.T, tool fantasy.AgentTool, input string) fantasy.ToolResponse {
	t.Helper()
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-1", Name: tool.Info().Name, Input: input})
	require.NoError(t, err)
	return resp
}

func filePathInput(path string) string {
	data, _ := json.Marshal(map[string]string{"file_path": path})
	return string(data)
}

func TestScopeGateWrapsOnlyWriteTools(t *testing.T) {
	t.Parallel()
	list := []fantasy.AgentTool{&fakeTool{name: tools.ViewToolName}, &fakeTool{name: tools.EditToolName}, &roleMCPTool{name: "mcp_x_y", server: "x"}}
	gated := gateToolsForScopes(list, fixedScopes("docs"), t.TempDir())
	require.Same(t, list[0], gated[0], "a read tool is not wrapped")
	require.Same(t, list[2], gated[2])
	wrapped, ok := gated[1].(*scopeGatedTool)
	require.True(t, ok)
	require.Equal(t, list[1].Info(), wrapped.Info(), "the gate must not change what the provider sees")
	require.Empty(t, wrapped.MCP())
}

func TestScopeGatePaths(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	cases := []struct {
		path string
		ok   bool
	}{
		{"internal/agent/x.go", true},
		{"./internal/agent/x.go", true},
		{filepath.Join(wd, "internal/agent/x.go"), true},
		{"internal/agent/sub/../x.go", true},
		{"docs/agent-teams.md", true},
		{"internal/agentteam/x.go", false},
		{"internal/agent/../ui/x.go", false},
		{"internal/ui/x.go", false},
		{"../outside.go", false},
		{"/etc/passwd", false},
	}
	for _, name := range []string{tools.EditToolName, tools.MultiEditToolName, tools.WriteToolName, tools.ReplaceSymbolToolName} {
		for _, tc := range cases {
			tool, inner := scopedTool(t, name, wd, "internal/agent", "docs/agent-teams.md")
			resp := runScoped(t, tool, filePathInput(tc.path))
			require.Equal(t, tc.ok, inner.called, "%s %s", name, tc.path)
			require.Equal(t, !tc.ok, resp.IsError, "%s %s", name, tc.path)
		}
	}

	tool, _ := scopedTool(t, tools.EditToolName, wd, "internal/agent", "docs/agent-teams.md")
	resp := runScoped(t, tool, filePathInput("internal/ui/x.go"))
	require.Equal(t, `path "internal/ui/x.go" is outside the write scopes of your claimed task: internal/agent, docs/agent-teams.md`, resp.Content)
}

func TestScopeGateApplyPatchChecksEveryFile(t *testing.T) {
	t.Parallel()
	patch := func(paths ...string) string {
		body := "*** Begin Patch\n"
		for _, p := range paths {
			body += "*** Add File: " + p + "\n+x\n"
		}
		body += "*** End Patch"
		data, _ := json.Marshal(tools.ApplyPatchParams{Patch: body})
		return string(data)
	}
	wd := t.TempDir()
	tool, inner := scopedTool(t, tools.ApplyPatchToolName, wd, "internal/agent")
	require.False(t, runScoped(t, tool, patch("internal/agent/a.go", "internal/agent/b.go")).IsError)
	require.True(t, inner.called)

	tool, inner = scopedTool(t, tools.ApplyPatchToolName, wd, "internal/agent")
	resp := runScoped(t, tool, patch("internal/agent/a.go", "internal/ui/b.go"))
	require.True(t, resp.IsError)
	require.False(t, inner.called, "one file outside the scopes refuses the whole patch")
	require.Contains(t, resp.Content, `path "internal/ui/b.go"`)
}

func TestScopeGateRefusesRenameUnderScopes(t *testing.T) {
	t.Parallel()
	tool, inner := scopedTool(t, tools.RenameToolName, t.TempDir(), "internal/agent")
	resp := runScoped(t, tool, `{"symbol":"A","new_name":"B"}`)
	require.True(t, resp.IsError)
	require.False(t, inner.called)
	require.Contains(t, resp.Content, "Use edit")
}

func TestScopeGateWithoutScopesPasses(t *testing.T) {
	t.Parallel()
	for _, name := range scopedWriteTools {
		tool, inner := scopedTool(t, name, t.TempDir())
		runScoped(t, tool, `{"file_path":"/anywhere/x.go"}`)
		require.True(t, inner.called, "%s with no claimed scopes is not restricted", name)
	}
}

func TestScopeGateRefusalRunsNoHook(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	runner := newRunner(t, "touch '"+marker+"'")
	inner := &fakeTool{name: tools.EditToolName, resp: fantasy.NewTextResponse("ok")}
	tool := gateToolsForScopes(wrapToolsWithHooks([]fantasy.AgentTool{inner}, runner, false), fixedScopes("internal/agent"), t.TempDir())[0]

	require.True(t, runScoped(t, tool, filePathInput("internal/ui/x.go")).IsError)
	_, err := os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist, "a refused call must not run a hook")

	require.False(t, runScoped(t, tool, filePathInput("internal/agent/x.go")).IsError)
	_, err = os.Stat(marker)
	require.NoError(t, err, "an allowed call still runs its hook")
}

// TestClaimedScopesReadTheCallerTasks pins the coordinator half: the scopes
// come from the tasks the calling member owns in progress, and the Lead has
// none.
func TestClaimedScopesReadTheCallerTasks(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	team := coord.teamService()
	_, err := team.SpawnTeammate(t.Context(), "lead", agentteam.SpawnRequest{Name: "impl", Description: "d", Prompt: "p", ParentID: teamTestSessionID})
	require.NoError(t, err)
	task, err := team.CreateTask("lead", teamTestSessionID, agentteam.CreateTaskRequest{Subject: "A", Description: "d", WriteScopes: []string{"internal/agent"}})
	require.NoError(t, err)
	_, err = team.UpdateTask("impl", agentteam.UpdateTaskRequest{TaskID: task.ID, ExpectedRevision: task.Revision, Action: "claim"})
	require.NoError(t, err)

	require.Equal(t, []string{"internal/agent"}, coord.claimedScopes(agentteam.WithMember(t.Context(), "impl")))
	require.Empty(t, coord.claimedScopes(t.Context()), "the Lead is never scope-gated")
}
```

Append to `internal/agent/team_cache_test.go`:

```go
// TestTeammateWrapperOrderKeepsLeadPrefix pins the wrapper order and the
// cache rule together: for every role, a teammate's write tools are role
// gate, then scope gate, then hook wrapper, and its tools block and system
// prompt still match the Lead's byte for byte.
func TestTeammateWrapperOrderKeepsLeadPrefix(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	lead := coord.coderAgent()
	require.NotNil(t, lead)
	leadTools := lead.(*sessionAgent).tools.Copy()
	edit := toolByName(leadTools, tools.EditToolName)
	_, hooked := edit.(*hookedTool)
	require.True(t, hooked, "the Lead's tools are hook-wrapped, as in production")

	plain, err := runner.teammateAgent(agentteam.WithMember(t.Context(), "plain"), agentteam.Member{Name: "plain", Role: agentteam.RoleTeammate})
	require.NoError(t, err)
	for _, role := range configuredRoles(coord.cfg.Config()) {
		member := agentteam.Member{Name: "t-" + role.Name, Role: agentteam.RoleTeammate, RoleName: role.Name}
		agent, err := runner.teammateAgent(agentteam.WithMember(t.Context(), member.Name), member)
		require.NoError(t, err)
		teammateTools := agent.(*sessionAgent).tools.Copy()
		require.Equal(t, toolNames(leadTools), toolNames(teammateTools), role.Name)
		for i, tool := range teammateTools {
			want, err := json.Marshal(leadTools[i].Info())
			require.NoError(t, err)
			have, err := json.Marshal(tool.Info())
			require.NoError(t, err)
			require.Equal(t, string(want), string(have), "%s: tool %q", role.Name, tool.Info().Name)
		}
		require.Equal(t, plain.SystemPrompt(), agent.SystemPrompt(), role.Name)

		gate, ok := toolByName(teammateTools, tools.EditToolName).(*roleGatedTool)
		require.True(t, ok, "%s: role gate is outermost", role.Name)
		scope, ok := gate.inner.(*scopeGatedTool)
		require.True(t, ok, "%s: scope gate is next", role.Name)
		_, ok = scope.inner.(*hookedTool)
		require.True(t, ok, "%s: hook wrapper is innermost", role.Name)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/agentteam/ -run TestClaimedWriteScopes; go test ./internal/agent/ -run 'TestScopeGate|TestClaimedScopes|TestTeammateWrapperOrder'`
Expected: FAIL to compile (`undefined: ClaimedWriteScopes`, `undefined: gateToolsForScopes`).

- [ ] **Step 4: Add `ClaimedWriteScopes`**

In `internal/agentteam/team.go`, after `GetTask`:

```go
// ClaimedWriteScopes returns the union of the write scopes of the tasks
// member owns in progress, in task order. The Lead and unknown members have
// none, so their calls are never scope-gated.
func (s *Service) ClaimedWriteScopes(member string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, ok := s.memberLocked(member)
	if !ok || owner.Role == RoleLead {
		return nil
	}
	seen := map[string]bool{}
	var scopes []string
	for _, task := range s.state.Tasks {
		if task.OwnerID != owner.ID || task.Status != TaskInProgress {
			continue
		}
		for _, scope := range task.WriteScopes {
			if !seen[scope] {
				seen[scope] = true
				scopes = append(scopes, scope)
			}
		}
	}
	return scopes
}
```

- [ ] **Step 5: Write the scope gate**

`internal/agent/scope_gate.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/home"
)

// scopedWriteTools are the file-writing tools a teammate's claimed write
// scopes apply to. bash is not among them: its writes cannot be known
// before it runs.
var scopedWriteTools = []string{
	tools.EditToolName,
	tools.MultiEditToolName,
	tools.WriteToolName,
	tools.ApplyPatchToolName,
	tools.RenameToolName,
	tools.ReplaceSymbolToolName,
}

// scopeGatedTool refuses a write outside the write scopes of the task the
// calling teammate claimed. It keeps the inner tool's Info, provider
// options and MCP identity, so the tools block stays byte-identical to the
// Lead's and the cache is shared. It sits outside the hook wrapper, so a
// refused call runs no hook and raises no permission prompt.
type scopeGatedTool struct {
	inner      fantasy.AgentTool
	scopes     func(ctx context.Context) []string
	workingDir string
}

// gateToolsForScopes wraps the scoped write tools in list.
func gateToolsForScopes(list []fantasy.AgentTool, scopes func(context.Context) []string, workingDir string) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, len(list))
	for i, tool := range list {
		if slices.Contains(scopedWriteTools, tool.Info().Name) {
			tool = &scopeGatedTool{inner: tool, scopes: scopes, workingDir: workingDir}
		}
		out[i] = tool
	}
	return out
}

func (g *scopeGatedTool) Info() fantasy.ToolInfo { return g.inner.Info() }

// MCP reports the MCP server of the wrapped tool, or "" for a built-in
// tool, so MCP filtering sees through the scope gate.
func (g *scopeGatedTool) MCP() string {
	if mcpTool, ok := g.inner.(interface{ MCP() string }); ok {
		return mcpTool.MCP()
	}
	return ""
}

func (g *scopeGatedTool) ProviderOptions() fantasy.ProviderOptions {
	return g.inner.ProviderOptions()
}

func (g *scopeGatedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	g.inner.SetProviderOptions(opts)
}

func (g *scopeGatedTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	scopes := g.scopes(ctx)
	if len(scopes) == 0 {
		return g.inner.Run(ctx, call)
	}
	name := g.inner.Info().Name
	listed := strings.Join(scopes, ", ")
	if name == tools.RenameToolName {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"lsp_rename is refused while your claimed task has write scopes (%s): the files a rename changes are not known in advance. Use edit on each file instead.",
			listed)), nil
	}
	paths, err := scopedPaths(name, call.Input)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"could not read the file paths of this %s call to check them against your write scopes: %s", name, err)), nil
	}
	for _, path := range paths {
		rel, inside := workspaceRelativePath(g.workingDir, path)
		if inside && pathInScopes(rel, scopes) {
			continue
		}
		shown := path
		if inside {
			shown = rel
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"path %q is outside the write scopes of your claimed task: %s", shown, listed)), nil
	}
	return g.inner.Run(ctx, call)
}

// scopedPaths returns the files a call writes: every path in a patch's
// file headers, or the call's file_path.
func scopedPaths(tool, input string) ([]string, error) {
	if tool == tools.ApplyPatchToolName {
		var params tools.ApplyPatchParams
		if err := json.Unmarshal([]byte(input), &params); err != nil {
			return nil, err
		}
		return tools.PatchFilePaths(params.Patch), nil
	}
	var params struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return nil, err
	}
	if params.FilePath == "" {
		return nil, nil
	}
	return []string{params.FilePath}, nil
}

// workspaceRelativePath resolves path the way the edit tools do, then
// returns it relative to workingDir in slash form. It reports false for a
// path that escapes the working directory.
func workspaceRelativePath(workingDir, path string) (string, bool) {
	abs := filepath.Clean(filepathext.SmartJoin(workingDir, home.Long(path)))
	rel, err := filepath.Rel(filepath.Clean(workingDir), abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// pathInScopes reports whether rel equals a scope or lies under a scope
// directory. Scopes are normalized without a trailing slash.
func pathInScopes(rel string, scopes []string) bool {
	for _, scope := range scopes {
		if rel == scope || strings.HasPrefix(rel, scope+"/") {
			return true
		}
	}
	return false
}

// claimedScopes returns the write scopes of the tasks the calling teammate
// owns in progress. The Lead is never scope-gated.
func (c *coordinator) claimedScopes(ctx context.Context) []string {
	team := c.teamService()
	if team == nil {
		return nil
	}
	member := agentteam.MemberFromContext(ctx)
	if member == agentteam.LeadName {
		return nil
	}
	return team.ClaimedWriteScopes(member)
}
```

- [ ] **Step 6: Wrap teammate write tools**

In `internal/agent/coordinator.go`, in `buildTools`, after:

```go
	filteredTools = wrapToolsWithHooks(filteredTools, c.hookRunner, role == roleSubAgent)
```

add:

```go
	// A teammate's write tools are held to the write scopes of the task it
	// claimed. The gate goes outside the hook wrapper, so a refused call
	// runs no hook; the role gate SetTools adds goes outside both.
	if role == roleTeammate {
		filteredTools = gateToolsForScopes(filteredTools, c.claimedScopes, c.cfg.WorkingDir())
	}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/agentteam/ -run TestClaimedWriteScopes && go test ./internal/agent/ -run 'TestScopeGate|TestClaimedScopes|TestTeammate|TestRoleTeammate|TestHookWrapFollowsRole'`
Expected: PASS.

- [ ] **Step 8: Format and commit**

```bash
gofumpt -w internal/agent/scope_gate.go internal/agent/scope_gate_test.go internal/agent/coordinator.go internal/agent/team_cache_test.go internal/agentteam/team.go internal/agentteam/task_fields_test.go
git add internal/agent/scope_gate.go internal/agent/scope_gate_test.go internal/agent/coordinator.go internal/agent/team_cache_test.go internal/agentteam/team.go internal/agentteam/task_fields_test.go
git commit -m "feat: hold teammate file edits to their claimed write scopes"
```

---

### Task 10: Policy sentence, status display and docs

**Files:**
- Modify: `internal/agentteam/team.go` (`Policy`)
- Modify: `internal/agent/testdata/coder_prompt_agent_teams_on.golden`, `internal/agent/testdata/lean_prompt_default.golden`, `internal/agent/testdata/lean_prompt_anthropic.golden`, `internal/agent/testdata/lean_prompt_gpt.golden`
- Modify: `internal/ui/model/team.go` (`teamTaskRank`, `teamTaskLine`, `teamTaskGlyph`)
- Modify: `internal/cmd/team.go` (`renderTeam` task loop, `taskGlyph`)
- Modify: `docs/agent-teams.md`, `docs/hooks/README.md`, `internal/skills/builtin/crush-config/SKILL.md`
- Test: `internal/agentteam/config_test.go` (extend), `internal/ui/model/team_test.go` (extend), `internal/cmd/team_test.go` (extend)

**Interfaces:**
- Consumes: `agentteam.TaskInReview`, `TaskView.Role`, `TaskView.Verifies`, `TaskView.Attempts`, `TaskView.Escalated`.
- Produces: no new names.

- [ ] **Step 1: Write the failing tests**

Append to `internal/agentteam/config_test.go` (add import `"strings"` only if you use it; the test below does not):

```go
func TestPolicyStartsRoleTasks(t *testing.T) {
	t.Parallel()
	require.Contains(t, Policy, "A ready task that names a role starts on its own; tasks without a role stay manual.")
	require.NotContains(t, Policy, "Task readiness never starts an owner.")
}
```

Append to `internal/cmd/team_test.go`:

```go
func TestRenderTeamPipeline(t *testing.T) {
	t.Parallel()
	off := false
	state := agentteam.State{TeamID: "team-1", Tasks: []agentteam.Task{
		{ID: "task-1", Revision: 3, Subject: "Build it", Description: "d", Status: agentteam.TaskInReview, Role: "implementer", BlockedBy: []string{}, WriteScopes: []string{}},
		{ID: "task-2", Revision: 1, Subject: "Verify: Build it", Description: "d", Status: agentteam.TaskPending, Role: "verifier", Verify: &off, Verifies: "task-1", Attempts: 3, Escalated: true, BlockedBy: []string{}, WriteScopes: []string{}},
	}}
	output := renderTeam(state)
	require.Contains(t, output, "◇ task-1   in_review    Build it")
	require.Contains(t, output, "      role implementer")
	require.Contains(t, output, "      verifies task-1")
	require.Contains(t, output, "      ! escalated after 3 attempts")
}
```

Append to `internal/ui/model/team_test.go`:

```go
func TestTeamInfoShowsReviewAndEscalation(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com = common.DefaultCommon(stubConfigWorkspace{})
	u.session = &session.Session{ID: "session-lead"}
	u.teamState = agentteam.State{TeamID: "team-1", Tasks: []agentteam.Task{
		{ID: "task-1", Subject: "Ready", Status: agentteam.TaskPending, SessionID: "session-lead"},
		{ID: "task-2", Subject: "Build it", Status: agentteam.TaskInReview, SessionID: "session-lead"},
		{ID: "task-3", Subject: "Verify it", Status: agentteam.TaskPending, Escalated: true, SessionID: "session-lead"},
	}}
	info := u.teamInfo(80)
	require.Contains(t, info, "◇ task-2 Build it")
	require.Contains(t, info, "task-3 Verify it · escalated")
	require.Less(t, strings.Index(info, "task-2"), strings.Index(info, "task-1"), "review sorts before ready work")
}
```

(add `"strings"` to that file's imports).

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/agentteam/ -run TestPolicyStartsRoleTasks; go test ./internal/cmd/ -run TestRenderTeamPipeline; go test ./internal/ui/model/ -run TestTeamInfoShowsReviewAndEscalation`
Expected: FAIL on the missing sentence, `◇`, `role implementer` and `· escalated`.

- [ ] **Step 3: Replace the Policy sentence and the goldens**

Run:

```bash
perl -pi -e 's/Task readiness never starts an owner\./A ready task that names a role starts on its own; tasks without a role stay manual./' internal/agentteam/team.go internal/agent/testdata/coder_prompt_agent_teams_on.golden internal/agent/testdata/lean_prompt_default.golden internal/agent/testdata/lean_prompt_anthropic.golden internal/agent/testdata/lean_prompt_gpt.golden
git diff --stat
```

Expected: exactly those five files change, one line each.

- [ ] **Step 4: Show review and escalation in the sidebar**

In `internal/ui/model/team.go`, replace `teamTaskRank`, `teamTaskLine` and `teamTaskGlyph` with:

```go
// teamTaskRank orders live tasks: work in flight first, then work waiting
// for its verifier, then ready work, then everything still blocked.
func teamTaskRank(task agentteam.TaskView) int {
	switch {
	case task.Status == agentteam.TaskInProgress:
		return 0
	case task.Status == agentteam.TaskInReview:
		return 1
	case task.Ready:
		return 2
	default:
		return 3
	}
}

func teamTaskLine(t *styles.Styles, task agentteam.TaskView, width int) string {
	line := fmt.Sprintf("  %s %s %s", teamTaskGlyph(task), task.ID, task.Subject)
	if task.OwnerName != "" {
		line += " · " + task.OwnerName
	}
	if task.Escalated {
		line += " · escalated"
	}
	return t.Resource.AdditionalText.Render(ansi.Truncate(line, width, "…"))
}

func teamTaskGlyph(task agentteam.TaskView) string {
	switch {
	case task.Status == agentteam.TaskCompleted:
		return "✓"
	case task.Status == agentteam.TaskInProgress:
		return "◐"
	case task.Status == agentteam.TaskInReview:
		return "◇"
	case task.Ready:
		return "○"
	default:
		return "!"
	}
}
```

- [ ] **Step 5: Show them in `crush team`**

In `internal/cmd/team.go`, in `renderTeam`'s task loop, after the `owner` line block, add:

```go
		if task.Role != "" {
			fmt.Fprintf(&sb, "      role %s\n", task.Role)
		}
		if task.Verifies != "" {
			fmt.Fprintf(&sb, "      verifies %s\n", task.Verifies)
		}
		if task.Escalated {
			fmt.Fprintf(&sb, "      ! escalated after %d attempts\n", task.Attempts)
		}
```

and in `taskGlyph` add a case before `case task.Ready:`:

```go
	case task.Status == agentteam.TaskInReview:
		return "◇"
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/agentteam/ ./internal/cmd/ ./internal/ui/model/ && go test ./internal/agent/ -run 'Golden|TeamPolicy|Prompt'`
Expected: PASS.

- [ ] **Step 7: Update the docs**

In `docs/agent-teams.md`, insert this section before `## Durable state`:

```markdown
## Pipeline

A shared task can name a role. `team_task_create` and the `edit` action of
`team_task_update` take `role` (any role from the `spawn_teammate` catalog)
and `verify`. A task without a role stays manual: nothing starts it.

### Auto-start

When a task that names a role becomes ready (pending, unowned, every blocker
completed), the team service starts a teammate on it. It reuses the
earliest-created idle teammate of that role spawned by the same Lead session,
or spawns one named `<role>-<n>` with the lowest free number, within the
live-member limit. At the limit the task waits for a later pass. The teammate
gets a short brief with the task, its write scopes and how to complete it.

A start that fails, or a turn that fails, puts the task back to pending and
counts an attempt. After three attempts the task is escalated: the
dispatcher leaves it alone and the Lead gets a message. An interrupted turn
escalates at once. A task whose role no longer exists is skipped and
reported to the Lead once. The Lead takes a task back from escalation with
`edit`, `reassign` or `reopen`, which also reset its attempts.

Auto-start is on by default. Turn it off with `option agent-team-autostart
false` in `crushrc`, or `"agent_team_autostart": false` in `crush.json`; the
setting is read on every pass, so a reload applies at once.

### Verification

`verify` defaults to true for role `implementer` and false otherwise. When a
verified task completes (by `complete`, or by its owner's turn ending), it
moves to `in_review` and the service creates a verification task, role
`verifier`, that names the original. Dependents wait until the original is
`completed`. The verifier records its verdict with `team_task_update`:
`pass` completes both tasks; `fail` (with `notes`) completes the
verification task and hands the original back to its owner, with the notes,
as `in_progress`. The third failed verification escalates the original,
which stays `in_review` until the Lead reopens it. A verifier that ends its
turn without a verdict releases the verification task and counts an
attempt; nothing passes by default.

The verifier runs every check itself, but it shares the working directory
with everyone else, so it sees whatever is on disk when it runs.

### Write scopes

A teammate's `edit`, `multiedit`, `write`, `apply_patch` and
`lsp_replace_symbol` calls are checked against the write scopes of the tasks
it owns in progress; a path outside them is refused with a tool error, before
any hook or permission prompt. `lsp_rename` is refused while scopes apply,
because the files it changes are not known in advance. A teammate with no
scoped task is not restricted, and the Lead never is. `bash` is not covered:
a command can write anywhere. Per-teammate worktrees are the planned fix.

### Hooks

Teammates fire `PreToolUse`, `PostToolUse` and `PostToolUseFailure` with
their own session id, like the Lead. Dispatched subagents still fire none.

None of this changes a tool definition or a system prompt per agent: the
brief, the verification task and every notice travel as messages.
```

In the same file, in `## Durable state`, replace:

```markdown
`TEAM_TASK_STALE_REVISION`. Readiness follows `blocked_by`, and write
scopes produce advisory overlap warnings for in-progress tasks.
```

with:

```markdown
`TEAM_TASK_STALE_REVISION`. Readiness follows `blocked_by`: a blocker counts
only once it is `completed`, so an `in_review` blocker keeps its dependents
waiting. Write scopes produce overlap warnings for in-progress tasks and are
enforced for teammates' file tools (see [Pipeline](#pipeline)).
```

and append to `## Differences from DSH`:

```markdown
- A shared task can name a role, and a ready one starts a teammate of that
  role on its own; DSH task readiness never starts an owner. Tasks without a
  role behave as in DSH.
- Implementer work goes through a verification task before its dependents
  start.
```

In `docs/hooks/README.md`, replace the paragraph that begins `**Scope**: tool and turn events only fire for the **top-level agent**.` through `hooked.` with:

```markdown
**Scope**: tool events (`PreToolUse`, `PostToolUse`, `PostToolUseFailure`)
fire for the **top-level agent** and for Agent Teams teammates, each with its
own session id. Turn events fire for the top-level agent only. Dispatched
subagents (the task tool, `agentic_fetch`, and friends) fire none of them,
so a notification hook on `TurnEnd` speaks once per prompt rather than once
per delegated turn. A `TurnEnd` hook runs once for every ending the harness
considers, so a prompt that continues runs it twice at the default cap; read
`stop_hook_active` (see [TurnEnd and continuation](#turnend-and-continuation))
to tell the second call apart. The outer subagent tool call itself _is_
hooked. A teammate call refused by its write scopes runs no hook.
```

In `internal/skills/builtin/crush-config/SKILL.md`, replace:

```markdown
- **Boolean keys** (value optional, defaults `true`): `debug`, `debug-lsp`,
  `auto-lsp`, `progress`.
```

with:

```markdown
- **Boolean keys** (value optional, defaults `true`): `debug`, `debug-lsp`,
  `auto-lsp`, `progress`, `agent-teams`, `agent-team-autostart` (start a
  teammate on its own when a ready shared task names a role; default `true`).
```

and add a row to the crushrc to crush.json mapping table, after the `option max-background-jobs 200` row:

```markdown
| `option agent-team-autostart false`  | `options.agent_team_autostart = false`                 |
```

Run `git diff -- docs internal/skills | grep '^+' | grep -c $'\xe2\x80\x94'` and confirm it prints `0` (the hooks README tables already contain em dashes; leave those).

- [ ] **Step 8: Run the skill tests**

Run: `go test ./internal/skills/ ./internal/agent/ -run 'Builtin|Skill'`
Expected: PASS.

- [ ] **Step 9: Format and commit**

```bash
gofumpt -w internal/agentteam/team.go internal/agentteam/config_test.go internal/ui/model/team.go internal/ui/model/team_test.go internal/cmd/team.go internal/cmd/team_test.go
git add internal/agentteam/team.go internal/agentteam/config_test.go internal/agent/testdata/coder_prompt_agent_teams_on.golden internal/agent/testdata/lean_prompt_default.golden internal/agent/testdata/lean_prompt_anthropic.golden internal/agent/testdata/lean_prompt_gpt.golden internal/ui/model/team.go internal/ui/model/team_test.go internal/cmd/team.go internal/cmd/team_test.go docs/agent-teams.md docs/hooks/README.md internal/skills/builtin/crush-config/SKILL.md
git commit -m "docs: describe the agent team pipeline and show its states"
```

---

### Task 11: Whole-branch verification

**Files:** none changed unless a check fails; fix in the task that owns the code and amend nothing, add a `fix:` commit.

- [ ] **Step 1: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: no output, exit 0.

- [ ] **Step 2: Full test suite with the race detector on the touched packages**

Run: `go test ./... && go test -race ./internal/agentteam/ ./internal/agent/ ./internal/fsext/ ./internal/roles/ ./internal/skills/`
Expected: PASS.

- [ ] **Step 3: Cache tests three times**

Run: `go test ./internal/agent/ -run 'Cache|Palette|Prefix|SystemPrompt|WrapperOrder|RoleTeammate|Golden' -count=3`
Expected: PASS all three runs.

- [ ] **Step 4: Formatting and log style**

Run: `gofumpt -l . && ./scripts/check_log_capitalization.sh`
Expected: `gofumpt -l` prints nothing; the script exits 0.

- [ ] **Step 5: No em dashes added**

Run: `git diff main...HEAD -- . ':!docs/superpowers' | grep '^+' | grep -c $'\xe2\x80\x94'`
Expected: `0`.

- [ ] **Step 6: Lint, if available**

Run: `task lint` (or `golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m` when `task` is absent)
Expected: no findings in files this branch touched.

- [ ] **Step 7: Commit any fix**

If a step failed and you fixed it:

```bash
git add <the files you fixed>
git commit -m "fix: <what the check caught>"
```

---

## Spec coverage

| Spec section | Task |
| --- | --- |
| 1. Task fields, `TaskView`, tool params, role validation, Lead clears escalation | 5 |
| 1. Dispatch pass, member choice, spawn naming, cap, brief, race, attempts, option, removed role | 6, 7 |
| 1. Policy sentence | 10 |
| 2. `in_review`, verification task, readiness, `pass`/`fail`, settle rules | 8 |
| 2. `in_review` and escalated in list, sidebar, `crush team` | 5 (list via `TaskView`), 10 |
| 3. Enforced write scopes | 9 |
| 4. Teammate hooks (fix D) | 4 |
| 5. Bugs A, B, C | 2, 1, 3 |
| 6. Errors table | 5, 7, 8, 9 |
| 7.1 Dispatcher tests | 7 |
| 7.2 Hand-off tests | 8 |
| 7.3 Verification tests | 8 (Lead clearing also in 5) |
| 7.4 Scope gate tests | 9 |
| 7.5 Cache tests with a non-nil hook runner | 4, 9 |
| 7.6 Fix tests A, B, C, D | 2, 1, 3, 4 |
| 7.7 Compatibility | 5 |
| 8. Documentation | 10 |
