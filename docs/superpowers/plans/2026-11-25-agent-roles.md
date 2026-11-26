# Agent Roles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the Lead give a teammate or subagent a named role (prompt, tool allow-list, optional model) loaded from built-ins and markdown files, without changing the cached request prefix.

**Architecture:** A new leaf package `internal/roles` parses and resolves role files (built-in < global < project, replacement or overlay). `config.SetupAgents` turns each role into a `config.Agent` carrying `Role *roles.Role`. In `internal/agent`, a `roleGatedTool` applied inside `sessionAgent.SetTools` refuses disallowed calls at run time, the role block rides in the first message (and in the post-compaction state message), and `spawn_teammate`, `agent_dispatch` and `agent` gain role selection.

**Tech Stack:** Go, `gopkg.in/yaml.v3` (already a dependency), `embed`, `charm.land/fantasy`, testify `require`.

**Spec:** `docs/superpowers/specs/2026-11-25-agent-roles-design.md`. Line numbers below are at `main` d4b395c97 and drift as earlier tasks land; find anchors by the quoted code, not the number.

## Global Constraints

- A role never changes the tool list (names, order, serialized `Info()`) or the system prompt of any agent. Role text goes only in the first message and in the post-compaction state message.
- A role-less teammate or subagent produces exactly today's request bytes.
- No new module dependencies.
- Format every Go file with `gofumpt -w <file>` (fall back to `goimports`, then `gofmt`).
- No em dashes in source, comments, role files or docs.
- Log messages start with a capital letter (`task lint:log`).
- Comments on their own line start with a capital letter, end with a period, wrap at 78 columns.
- Tests use testify `require`, `t.Parallel()` where the package allows it, `t.TempDir()` for directories, `t.Setenv()` for environment.
- File permissions in octal (`0o644`, `0o755`).
- JSON tags snake_case.
- Reserved role names: `coder`, `task`, `plan`. Role name pattern `^[a-z0-9]+(-[a-z0-9]+)*$`. Description at most 200 bytes, one line. Body at most 8 KiB.
- Search order, lowest to highest: built-in, `$CRUSH_AGENTS_DIR` or `~/.config/crush/agents`, `<git root>/.crush/agents`, `<working dir>/.crush/agents`.
- Commit only when the user has authorized commits for this branch; use semantic commit subjects.

## Review Focus

1. A role file saved with CRLF line endings or a UTF-8 BOM: it must load like an LF file (Task 1 test `TestParseCRLFAndBOM`).
2. `tools: ""` or `tools: []`: must be a diagnostic, not a role that silently allows only team tools (Task 1 test `TestParseRejectsEmptyTools`).
3. An MCP server that connects after the role loaded: a `mcp_*` pattern must allow its tools at call time (Task 4 test `TestRoleGateMatchesLateMCPTool`).
4. Agents directories that do not exist (the common case): no diagnostics, built-ins still load (Task 2 test `TestDiscoverMissingDirs`).
5. A teammate resumed after a restart from `team.json` with `role_name`: tools stay gated and the role block is not repeated on later turns (Task 6 tests `TestRoleTeammateGatedOnRebuild` and `TestTeammatePromptRoleBlockFirstTurnOnly`).

---

### Task 1: Role model and file parsing (`internal/roles`, no IO)

**Files:**
- Create: `internal/roles/roles.go`
- Create: `internal/roles/parse.go`
- Test: `internal/roles/roles_test.go`
- Test: `internal/roles/parse_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Source string` with `SourceBuiltin = "builtin"`, `SourceGlobal = "global"`, `SourceProject = "project"`.
  - `type Role struct { Name, Description string; Tools []string; Model, Provider, Reasoning, Body string; Source Source; Path string }` (`Tools == nil` means every tool).
  - `type Dir struct { Path string; Source Source }`
  - `type Diagnostic struct { Path, Message string }` with `func (d Diagnostic) String() string`.
  - `func (r Role) Allows(tool string) bool`
  - `func (r Role) ReadOnly(writeTools []string) bool`
  - `func (r Role) PinsModel() bool`
  - `func Block(r Role) string`
  - `func Catalog(list []Role) string`
  - unexported `parse(content []byte, path string, source Source) (definition, error)` and `func (d definition) apply(base *Role) (Role, error)` used by Task 2.

- [ ] **Step 1: Write the failing tests for `Role`**

`internal/roles/roles_test.go`:

```go
package roles

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllows(t *testing.T) {
	t.Parallel()
	all := Role{Name: "implementer"}
	require.True(t, all.Allows("edit"), "nil Tools allows every tool")

	r := Role{Name: "reviewer", Tools: []string{"view", "mcp_*"}}
	require.True(t, r.Allows("view"))
	require.True(t, r.Allows("mcp_tavily_tavily_search"))
	require.False(t, r.Allows("edit"))
	require.False(t, r.Allows("viewer"), "exact names do not match prefixes")
}

func TestReadOnly(t *testing.T) {
	t.Parallel()
	write := []string{"apply_patch", "edit", "multiedit", "write", "lsp_rename", "lsp_replace_symbol", "worktree"}
	require.False(t, Role{}.ReadOnly(write), "nil Tools is a write role")
	require.True(t, Role{Tools: []string{"view", "bash", "lsp_definition"}}.ReadOnly(write))
	require.False(t, Role{Tools: []string{"view", "lsp_*"}}.ReadOnly(write), "lsp_* matches lsp_rename")
}

func TestBlockIsStableAndSorted(t *testing.T) {
	t.Parallel()
	r := Role{Name: "reviewer", Body: "Review things.", Tools: []string{"view", "bash"}}
	want := "## Your role: reviewer\n\nReview things.\n\nAllowed tools: bash, view\nTeam coordination tools are always available.\n\n---\n\n"
	require.Equal(t, want, Block(r))
	require.Equal(t, Block(r), Block(r))
	require.Equal(t, []string{"view", "bash"}, r.Tools, "Block must not sort the caller's slice")

	all := Role{Name: "implementer", Body: "Build."}
	require.Contains(t, Block(all), "Allowed tools: all tools\n")
}

func TestCatalog(t *testing.T) {
	t.Parallel()
	list := []Role{
		{Name: "verifier", Description: "Checks.", Source: SourceBuiltin},
		{Name: "reviewer", Description: "Reviews.", Source: SourceProject, Model: "m"},
		{Name: "architect", Description: "Designs.", Source: SourceGlobal, Model: "m"},
	}
	want := "- architect (global): Designs.\n- reviewer (project, pins model): Reviews.\n- verifier (builtin): Checks.\n"
	require.Equal(t, want, Catalog(list))
}
```

- [ ] **Step 2: Write the failing tests for parsing**

`internal/roles/parse_test.go`:

```go
package roles

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const reviewerFile = `---
name: reviewer
description: Reviews diffs.
tools: view, grep , bash
model: m1
provider: p1
reasoning: high
---
Review the diff.
`

func TestParseReplacement(t *testing.T) {
	t.Parallel()
	def, err := parse([]byte(reviewerFile), "/x/reviewer.md", SourceGlobal)
	require.NoError(t, err)
	role, err := def.apply(nil)
	require.NoError(t, err)
	require.Equal(t, Role{
		Name: "reviewer", Description: "Reviews diffs.",
		Tools: []string{"view", "grep", "bash"},
		Model: "m1", Provider: "p1", Reasoning: "high",
		Body: "Review the diff.", Source: SourceGlobal, Path: "/x/reviewer.md",
	}, role)
}

func TestParseToolsYAMLList(t *testing.T) {
	t.Parallel()
	content := "---\nname: r\ndescription: d\ntools:\n  - view\n  - mcp_*\n---\nbody\n"
	def, err := parse([]byte(content), "r.md", SourceGlobal)
	require.NoError(t, err)
	role, err := def.apply(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"view", "mcp_*"}, role.Tools)
}

func TestParseCRLFAndBOM(t *testing.T) {
	t.Parallel()
	content := "\uFEFF---\r\nname: r\r\ndescription: d\r\n---\r\nbody\r\n"
	def, err := parse([]byte(content), "r.md", SourceGlobal)
	require.NoError(t, err)
	role, err := def.apply(nil)
	require.NoError(t, err)
	require.Equal(t, "body", role.Body)
	require.Nil(t, role.Tools)
}

func TestParseRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ path, content, msg string }{
		"no frontmatter":   {"r.md", "just text", "frontmatter"},
		"bad name":         {"R.md", "---\nname: R\ndescription: d\n---\nb", "lower-kebab-case"},
		"reserved":         {"coder.md", "---\nname: coder\ndescription: d\n---\nb", "reserved"},
		"stem mismatch":    {"other.md", "---\nname: r\ndescription: d\n---\nb", "file name"},
		"no description":   {"r.md", "---\nname: r\n---\nb", "description is required"},
		"multiline desc":   {"r.md", "---\nname: r\ndescription: \"a\\nb\"\n---\nb", "one line"},
		"long description": {"r.md", "---\nname: r\ndescription: " + longText(201) + "\n---\nb", "more than 200"},
		"long body":        {"r.md", "---\nname: r\ndescription: d\n---\n" + longText(8<<10+1), "more than 8192"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := parse([]byte(tc.content), tc.path, SourceGlobal)
			require.ErrorContains(t, err, tc.msg)
		})
	}
}

func TestParseRejectsEmptyTools(t *testing.T) {
	t.Parallel()
	for _, tools := range []string{`""`, `[]`, `" , "`} {
		content := "---\nname: r\ndescription: d\ntools: " + tools + "\n---\nb"
		_, err := parse([]byte(content), "r.md", SourceGlobal)
		require.ErrorContains(t, err, "tools is empty", "tools: %s", tools)
	}
}

func TestOverlay(t *testing.T) {
	t.Parallel()
	base := Role{
		Name: "reviewer", Description: "Reviews.", Tools: []string{"view"},
		Model: "m0", Provider: "p0", Reasoning: "low", Body: "Base body.",
		Source: SourceBuiltin, Path: "builtin/reviewer.md",
	}

	def, err := parse([]byte("---\nname: reviewer\nmodel: m1\n---\n"), "/g/reviewer.md", SourceGlobal)
	require.NoError(t, err)
	got, err := def.apply(&base)
	require.NoError(t, err)
	require.Equal(t, "Base body.", got.Body)
	require.Equal(t, "Reviews.", got.Description)
	require.Equal(t, []string{"view"}, got.Tools)
	require.Equal(t, "m1", got.Model)
	require.Empty(t, got.Provider, "setting model replaces the whole model group")
	require.Empty(t, got.Reasoning, "setting model replaces the whole model group")
	require.Equal(t, SourceGlobal, got.Source)
	require.Equal(t, "/g/reviewer.md", got.Path)

	def, err = parse([]byte("---\nname: reviewer\ndescription: Stricter.\ntools: view, grep\n---\n"), "/p/reviewer.md", SourceProject)
	require.NoError(t, err)
	got, err = def.apply(&base)
	require.NoError(t, err)
	require.Equal(t, "Stricter.", got.Description)
	require.Equal(t, []string{"view", "grep"}, got.Tools)
	require.Equal(t, "m0", got.Model, "an overlay that sets no model field keeps the base group")
	require.Equal(t, "p0", got.Provider)

	base.Tools[0] = "mutated"
	require.Equal(t, []string{"view", "grep"}, got.Tools, "apply must not share the base slice")
}

func TestOverlayNeedsBase(t *testing.T) {
	t.Parallel()
	def, err := parse([]byte("---\nname: reviewer\nmodel: m1\n---\n"), "/g/reviewer.md", SourceGlobal)
	require.NoError(t, err)
	_, err = def.apply(nil)
	require.ErrorContains(t, err, "overlay")
}

func longText(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/roles/`
Expected: FAIL to compile (`undefined: Role`, `undefined: parse`).

- [ ] **Step 4: Implement `roles.go`**

```go
// Package roles loads agent role definitions: a named prompt with a tool
// allow-list and an optional model, from built-ins embedded in the binary
// and markdown files on disk.
package roles

import (
	"fmt"
	"slices"
	"strings"
)

// Source says where a role definition came from.
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceGlobal  Source = "global"
	SourceProject Source = "project"
)

// Role is one resolved role definition.
type Role struct {
	Name        string
	Description string
	// Tools holds the allow-list as written: exact tool names, or a prefix
	// ending in "*". Nil allows every tool.
	Tools     []string
	Model     string
	Provider  string
	Reasoning string
	Body      string
	Source    Source
	Path      string
}

// Dir is one directory of role files and the source it counts as.
type Dir struct {
	Path   string
	Source Source
}

// Diagnostic is one problem found while loading role files.
type Diagnostic struct {
	Path    string
	Message string
}

func (d Diagnostic) String() string {
	return d.Path + ": " + d.Message
}

// Allows reports whether the role lets an agent call the named tool.
func (r Role) Allows(tool string) bool {
	if r.Tools == nil {
		return true
	}
	for _, pattern := range r.Tools {
		if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
			if strings.HasPrefix(tool, prefix) {
				return true
			}
			continue
		}
		if pattern == tool {
			return true
		}
	}
	return false
}

// ReadOnly reports whether the role allows none of writeTools. A role with
// no allow-list can write.
func (r Role) ReadOnly(writeTools []string) bool {
	if r.Tools == nil {
		return false
	}
	return !slices.ContainsFunc(writeTools, r.Allows)
}

// PinsModel reports whether the role sets any field of its model group.
func (r Role) PinsModel() bool {
	return r.Model != "" || r.Provider != "" || r.Reasoning != ""
}

// Block renders the text a role adds to an agent's first message. It
// depends only on the role, so every agent with the same role sends the
// same bytes and siblings share them in the provider's cache.
func Block(r Role) string {
	allowed := "all tools"
	if r.Tools != nil {
		names := slices.Clone(r.Tools)
		slices.Sort(names)
		allowed = strings.Join(names, ", ")
	}
	return fmt.Sprintf("## Your role: %s\n\n%s\n\nAllowed tools: %s\nTeam coordination tools are always available.\n\n---\n\n", r.Name, r.Body, allowed)
}

// Catalog renders one line per role, sorted by name, for the delegation
// tool descriptions. A project role that pins a model says so, so a cloned
// repository cannot move spend to another model unnoticed.
func Catalog(list []Role) string {
	sorted := slices.Clone(list)
	slices.SortFunc(sorted, func(a, b Role) int { return strings.Compare(a.Name, b.Name) })
	var b strings.Builder
	for _, r := range sorted {
		origin := string(r.Source)
		if r.Source == SourceProject && r.PinsModel() {
			origin += ", pins model"
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", r.Name, origin, r.Description)
	}
	return b.String()
}
```

- [ ] **Step 5: Implement `parse.go`**

```go
package roles

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxDescriptionBytes = 200
	MaxBodyBytes        = 8 << 10
)

var (
	namePattern   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	reservedNames = []string{"coder", "task", "plan"}
)

// frontmatter is a role file's header. Pointer fields tell an overlay
// which fields the file sets.
type frontmatter struct {
	Name        string    `yaml:"name"`
	Description *string   `yaml:"description"`
	Tools       *toolList `yaml:"tools"`
	Model       *string   `yaml:"model"`
	Provider    *string   `yaml:"provider"`
	Reasoning   *string   `yaml:"reasoning"`
}

// toolList accepts a comma-separated string or a YAML list.
type toolList []string

func (t *toolList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*t = trimTools(strings.Split(node.Value, ","))
	case yaml.SequenceNode:
		var items []string
		if err := node.Decode(&items); err != nil {
			return err
		}
		*t = trimTools(items)
	default:
		return errors.New("tools must be a comma-separated string or a list")
	}
	return nil
}

func trimTools(items []string) toolList {
	out := toolList{}
	for _, item := range items {
		if name := strings.TrimSpace(item); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// definition is one parsed file before it is resolved against the role of
// the same name below it.
type definition struct {
	fm     frontmatter
	body   string
	path   string
	source Source
}

// overlay reports whether the file inherits from the role below it. A file
// without a body is an overlay.
func (d definition) overlay() bool {
	return d.body == ""
}

func parse(content []byte, path string, source Source) (definition, error) {
	header, body, err := splitFrontmatter(string(content))
	if err != nil {
		return definition{}, err
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(header), &fm); err != nil {
		return definition{}, fmt.Errorf("parsing frontmatter: %w", err)
	}
	def := definition{fm: fm, body: strings.TrimSpace(body), path: path, source: source}
	return def, def.validate()
}

func (d definition) validate() error {
	stem := strings.TrimSuffix(filepath.Base(d.path), ".md")
	switch {
	case !namePattern.MatchString(d.fm.Name):
		return fmt.Errorf("name %q must be lower-kebab-case", d.fm.Name)
	case slices.Contains(reservedNames, d.fm.Name):
		return fmt.Errorf("name %q is reserved", d.fm.Name)
	case d.fm.Name != stem:
		return fmt.Errorf("name %q must match the file name %q", d.fm.Name, stem)
	case len(d.body) > MaxBodyBytes:
		return fmt.Errorf("body is %d bytes, more than %d", len(d.body), MaxBodyBytes)
	case d.fm.Tools != nil && len(*d.fm.Tools) == 0:
		return errors.New("tools is empty; omit it to allow every tool")
	}
	if d.fm.Description == nil {
		if d.overlay() {
			return nil
		}
		return errors.New("description is required")
	}
	desc := strings.TrimSpace(*d.fm.Description)
	switch {
	case desc == "":
		return errors.New("description is empty")
	case strings.Contains(desc, "\n"):
		return errors.New("description must be one line")
	case len(desc) > MaxDescriptionBytes:
		return fmt.Errorf("description is %d bytes, more than %d", len(desc), MaxDescriptionBytes)
	}
	return nil
}

// apply resolves d against base, the role of the same name below it, or
// nil when there is none. A replacement ignores base; an overlay takes
// every field it leaves out from base, and treats model, provider and
// reasoning as one group.
func (d definition) apply(base *Role) (Role, error) {
	if !d.overlay() {
		r := Role{
			Name:        d.fm.Name,
			Description: trimmed(d.fm.Description),
			Model:       trimmed(d.fm.Model),
			Provider:    trimmed(d.fm.Provider),
			Reasoning:   trimmed(d.fm.Reasoning),
			Body:        d.body,
			Source:      d.source,
			Path:        d.path,
		}
		if d.fm.Tools != nil {
			r.Tools = slices.Clone([]string(*d.fm.Tools))
		}
		return r, nil
	}
	if base == nil {
		return Role{}, errors.New("an overlay (a file with no body) needs a role of the same name below it")
	}
	r := *base
	r.Tools = slices.Clone(base.Tools)
	r.Source, r.Path = d.source, d.path
	if d.fm.Description != nil {
		r.Description = trimmed(d.fm.Description)
	}
	if d.fm.Tools != nil {
		r.Tools = slices.Clone([]string(*d.fm.Tools))
	}
	if d.fm.Model != nil || d.fm.Provider != nil || d.fm.Reasoning != nil {
		r.Model, r.Provider, r.Reasoning = trimmed(d.fm.Model), trimmed(d.fm.Provider), trimmed(d.fm.Reasoning)
	}
	return r, nil
}

func trimmed(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// splitFrontmatter extracts the YAML header and the body. It mirrors the
// skills parser: a BOM is stripped and line endings are normalized first.
func splitFrontmatter(content string) (header, body string, err error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	lines := strings.Split(content, "\n")
	start := slices.IndexFunc(lines, func(line string) bool {
		return strings.TrimSpace(line) != ""
	})
	if start == -1 || strings.TrimSpace(lines[start]) != "---" {
		return "", "", errors.New("no YAML frontmatter found")
	}
	end := slices.IndexFunc(lines[start+1:], func(line string) bool {
		return strings.TrimSpace(line) == "---"
	})
	if end == -1 {
		return "", "", errors.New("unclosed YAML frontmatter")
	}
	end += start + 1
	return strings.Join(lines[start+1:end], "\n"), strings.Join(lines[end+1:], "\n"), nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `gofumpt -w internal/roles && go test ./internal/roles/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/roles
git commit -m "feat(roles): add the role model and role file parser"
```

---

### Task 2: Discovery, overlays across layers, and the built-in roles

**Files:**
- Create: `internal/roles/discover.go`
- Create: `internal/roles/builtin/architect.md`, `researcher.md`, `reviewer.md`, `implementer.md`, `verifier.md`
- Test: `internal/roles/discover_test.go`

**Interfaces:**
- Consumes: Task 1 (`parse`, `definition.apply`, `Role`, `Dir`, `Diagnostic`).
- Produces: `func Discover(dirs []Dir, knownTools []string) (map[string]Role, []Diagnostic)`. `dirs` are ordered lowest priority first; built-ins are always the bottom layer. `knownTools` nil skips the unknown-name check.

- [ ] **Step 1: Write the built-in files**

Copy the five fenced files of the spec's Appendix A verbatim (the content between the four-backtick fences, starting at `---`) into `internal/roles/builtin/<name>.md`.

- [ ] **Step 2: Write the failing tests**

`internal/roles/discover_test.go`:

```go
package roles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var testWriteTools = []string{"apply_patch", "edit", "multiedit", "write", "lsp_rename", "lsp_replace_symbol", "worktree"}

func writeRole(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644))
}

func TestDiscoverBuiltins(t *testing.T) {
	t.Parallel()
	found, diags := Discover(nil, nil)
	require.Empty(t, diags)
	require.ElementsMatch(t, []string{"architect", "researcher", "reviewer", "implementer", "verifier"}, keys(found))
	headings := map[string][]string{
		"architect":   {"### Goal", "### Task breakdown", "### Open questions"},
		"researcher":  {"### Question", "### Answer", "### Evidence"},
		"reviewer":    {"### Verdict", "### Findings", "### Questions"},
		"implementer": {"### Files changed", "### Commands run", "### Acceptance criteria"},
		"verifier":    {"### Criteria", "### Verdict", "### Notes"},
	}
	for name, role := range found {
		require.Equal(t, SourceBuiltin, role.Source, name)
		require.False(t, role.PinsModel(), "built-in %s must not pin a model", name)
		require.NotContains(t, role.Body, "\u2014", "built-in %s contains an em dash", name)
		for _, h := range headings[name] {
			require.Contains(t, role.Body, h, "built-in %s", name)
		}
	}
	for _, name := range []string{"architect", "researcher", "reviewer", "verifier"} {
		require.True(t, found[name].ReadOnly(testWriteTools), "%s must be read-only", name)
	}
	require.False(t, found["implementer"].ReadOnly(testWriteTools))
}

func TestDiscoverMissingDirs(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope")
	found, diags := Discover([]Dir{{Path: missing, Source: SourceGlobal}}, nil)
	require.Empty(t, diags)
	require.Len(t, found, 5)
}

func TestDiscoverPriorityAndOverlay(t *testing.T) {
	t.Parallel()
	global := filepath.Join(t.TempDir(), "global")
	root := filepath.Join(t.TempDir(), "root")
	wd := filepath.Join(t.TempDir(), "wd")

	writeRole(t, global, "reviewer", "---\nname: reviewer\nmodel: strong\nprovider: p\n---\n")
	writeRole(t, global, "docs-writer", "---\nname: docs-writer\ndescription: Writes docs.\n---\nWrite docs.\n")
	writeRole(t, root, "docs-writer", "---\nname: docs-writer\ndescription: Root docs.\n---\nRoot body.\n")
	writeRole(t, wd, "docs-writer", "---\nname: docs-writer\nreasoning: high\n---\n")

	found, diags := Discover([]Dir{
		{Path: global, Source: SourceGlobal},
		{Path: root, Source: SourceProject},
		{Path: wd, Source: SourceProject},
	}, nil)
	require.Empty(t, diags)

	reviewer := found["reviewer"]
	require.Equal(t, "strong", reviewer.Model)
	require.Equal(t, SourceGlobal, reviewer.Source)
	require.Contains(t, reviewer.Body, "You are the reviewer.", "the overlay keeps the built-in body")

	docs := found["docs-writer"]
	require.Equal(t, "Root body.", docs.Body, "the git-root replacement wins over the global file")
	require.Equal(t, "Root docs.", docs.Description)
	require.Equal(t, "high", docs.Reasoning, "the working-dir overlay stacks on the root file")
	require.Equal(t, filepath.Join(wd, "docs-writer.md"), docs.Path)
}

func TestDiscoverDiagnostics(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRole(t, dir, "orphan", "---\nname: orphan\nmodel: m\n---\n")
	writeRole(t, dir, "broken", "no header")
	writeRole(t, dir, "picky", "---\nname: picky\ndescription: d\ntools: view, not_a_tool, mcp_x_y, lsp_*\n---\nBody.\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644))

	found, diags := Discover([]Dir{{Path: dir, Source: SourceGlobal}}, []string{"view", "lsp_definition"})
	require.NotContains(t, found, "orphan")
	require.NotContains(t, found, "broken")
	require.Equal(t, []string{"view", "mcp_x_y", "lsp_*"}, found["picky"].Tools,
		"unknown exact names are dropped; MCP names and patterns are kept")

	var text []string
	for _, d := range diags {
		text = append(text, d.String())
	}
	joined := strings.Join(text, "\n")
	require.Contains(t, joined, "orphan.md: an overlay")
	require.Contains(t, joined, "broken.md: no YAML frontmatter")
	require.Contains(t, joined, `picky.md: unknown tool "not_a_tool" dropped`)
	require.NotContains(t, joined, "notes.txt")
}

func TestDiscoverBuiltinToolsAreKnown(t *testing.T) {
	t.Parallel()
	// Mirrors config.AllToolNames(); config imports roles, so the list is
	// repeated here and Task 3 pins the real list from the config side.
	known := []string{
		"view", "read", "ls", "glob", "grep", "bash", "sourcegraph", "todos",
		"fetch", "download", "list_mcp_resources", "read_mcp_resource",
		"job_output", "job_kill", "lsp_diagnostics", "lsp_definition",
		"lsp_references", "lsp_symbols", "lsp_call_hierarchy",
	}
	_, diags := Discover(nil, known)
	require.Empty(t, diags)
}

func keys(m map[string]Role) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/roles/ -run Discover`
Expected: FAIL to compile (`undefined: Discover`).

- [ ] **Step 4: Implement `discover.go`**

```go
package roles

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed builtin/*.md
var builtinFS embed.FS

// layer is one directory of role files at one priority.
type layer struct {
	fsys    fs.FS
	display func(name string) string
	source  Source
}

// Discover loads the built-in roles, then every directory in dirs in order,
// lowest priority first. A file with a body replaces the role of the same
// name below it; a file without one overlays it. Exact tool names missing
// from knownTools are dropped with a diagnostic, unless they name an MCP
// tool, which may connect later. A nil knownTools skips that check.
func Discover(dirs []Dir, knownTools []string) (map[string]Role, []Diagnostic) {
	builtin, err := fs.Sub(builtinFS, "builtin")
	if err != nil {
		panic(err)
	}
	layers := []layer{{
		fsys:    builtin,
		display: func(name string) string { return path.Join("builtin", name) },
		source:  SourceBuiltin,
	}}
	for _, dir := range dirs {
		root := dir.Path
		layers = append(layers, layer{
			fsys: os.DirFS(root),
			display: func(name string) string {
				if name == "." {
					return root
				}
				return filepath.Join(root, name)
			},
			source: dir.Source,
		})
	}

	found := map[string]Role{}
	var diags []Diagnostic
	for _, l := range layers {
		defs, layerDiags := readLayer(l)
		diags = append(diags, layerDiags...)
		for _, def := range defs {
			var base *Role
			if existing, ok := found[def.fm.Name]; ok {
				base = &existing
			}
			role, err := def.apply(base)
			if err != nil {
				diags = append(diags, Diagnostic{Path: def.path, Message: err.Error()})
				continue
			}
			role.Tools, diags = dropUnknownTools(role, knownTools, diags)
			found[role.Name] = role
		}
	}
	for _, d := range diags {
		slog.Warn("Problem in role file", "path", d.Path, "reason", d.Message)
	}
	return found, diags
}

// readLayer parses every .md file of one layer, in name order.
func readLayer(l layer) ([]definition, []Diagnostic) {
	entries, err := fs.ReadDir(l.fsys, ".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []Diagnostic{{Path: l.display("."), Message: err.Error()}}
	}
	var defs []definition
	var diags []Diagnostic
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		display := l.display(entry.Name())
		content, err := fs.ReadFile(l.fsys, entry.Name())
		if err != nil {
			diags = append(diags, Diagnostic{Path: display, Message: err.Error()})
			continue
		}
		def, err := parse(content, display, l.source)
		if err != nil {
			diags = append(diags, Diagnostic{Path: display, Message: err.Error()})
			continue
		}
		defs = append(defs, def)
	}
	return defs, diags
}

// dropUnknownTools removes exact tool names that knownTools does not list.
// Patterns and MCP tool names are kept: MCP servers connect after load.
func dropUnknownTools(role Role, knownTools []string, diags []Diagnostic) ([]string, []Diagnostic) {
	if knownTools == nil || role.Tools == nil {
		return role.Tools, diags
	}
	kept := make([]string, 0, len(role.Tools))
	for _, name := range role.Tools {
		if strings.HasSuffix(name, "*") || strings.HasPrefix(name, "mcp_") || slices.Contains(knownTools, name) {
			kept = append(kept, name)
			continue
		}
		diags = append(diags, Diagnostic{Path: role.Path, Message: fmt.Sprintf("unknown tool %q dropped", name)})
	}
	return kept, diags
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `gofumpt -w internal/roles && go test ./internal/roles/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/roles
git commit -m "feat(roles): discover role files and ship five built-in roles"
```

---

### Task 3: Config wiring and `crush_info`

**Files:**
- Modify: `internal/config/config.go` (`Agent` struct at `:1258-1279`, `Config` struct at `:1347`, `SetupAgents` at `:1771-1808`, `subAgentWriteTools` at `:1740-1748`)
- Modify: `internal/config/load.go` (`setDefaults` near `:946-947`, add `GlobalAgentsDir` and `RoleDirs` next to `GlobalSkillsDirs` at `:1652`)
- Modify: `internal/agent/tools/crush_info.go` (`buildCrushInfo` at `:44-60`)
- Test: `internal/config/roles_test.go` (new)
- Test: `internal/agent/tools/crush_info_test.go`

**Interfaces:**
- Consumes: `roles.Discover`, `roles.Role`, `roles.Dir`, `roles.Diagnostic`.
- Produces:
  - `config.Agent.Role *roles.Role` (`json:"-"`); nil for coder, task, plan.
  - `config.Config.RoleDirs []roles.Dir` and `config.Config.RoleDiagnostics []roles.Diagnostic` (both `json:"-"`).
  - `func config.SubAgentWriteTools() []string`
  - `func config.GlobalAgentsDir() string`, `func config.RoleDirs(workingDir string) []roles.Dir`
  - Every role is `cfg.Agents[role.Name]` with `AllowedTools` equal to the coder's list and `AllowedMCP` equal to the coder's.

- [ ] **Step 1: Write the failing tests**

`internal/config/roles_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/roles"
)

func TestSetupAgentsAddsRoles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs-writer.md"),
		[]byte("---\nname: docs-writer\ndescription: Writes docs.\ntools: view, bogus\n---\nWrite docs.\n"), 0o644))

	cfg := &Config{Options: &Options{}, RoleDirs: []roles.Dir{{Path: dir, Source: roles.SourceGlobal}}}
	cfg.SetupAgents()

	for _, name := range []string{AgentCoder, AgentTask, AgentPlan} {
		require.Nil(t, cfg.Agents[name].Role, name)
	}
	for _, name := range []string{"architect", "researcher", "reviewer", "implementer", "verifier", "docs-writer"} {
		agent, ok := cfg.Agents[name]
		require.True(t, ok, name)
		require.NotNil(t, agent.Role, name)
		require.Equal(t, name, agent.ID)
		require.Equal(t, cfg.Agents[AgentCoder].AllowedTools, agent.AllowedTools, name)
	}
	require.Equal(t, []string{"view"}, cfg.Agents["docs-writer"].Role.Tools)
	require.Len(t, cfg.RoleDiagnostics, 1)
	require.Contains(t, cfg.RoleDiagnostics[0].String(), `unknown tool "bogus" dropped`)
}

func TestBuiltinRoleToolsAreRealTools(t *testing.T) {
	t.Parallel()
	_, diags := roles.Discover(nil, AllToolNames())
	require.Empty(t, diags, "every exact tool name in a built-in role must exist")
}

func TestRoleDirsOrder(t *testing.T) {
	wd := t.TempDir()
	t.Setenv("CRUSH_AGENTS_DIR", filepath.Join(wd, "global-agents"))
	dirs := RoleDirs(wd)
	require.Equal(t, roles.Dir{Path: filepath.Join(wd, "global-agents"), Source: roles.SourceGlobal}, dirs[0])
	require.Equal(t, roles.Dir{Path: filepath.Join(wd, ".crush", "agents"), Source: roles.SourceProject}, dirs[len(dirs)-1],
		"the working directory is the highest priority")
}
```

Add to `internal/agent/tools/crush_info_test.go`, built the same way as `TestCrushInfo_MinimalConfig`:

```go
func TestCrushInfoListsRoles(t *testing.T) {
	t.Parallel()

	c := &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	}
	c.SetupAgents()
	out := buildCrushInfo(config.NewTestStore(c), nil, nil, nil, nil, nil)
	require.Contains(t, out, "[roles]\n")
	require.Contains(t, out, "reviewer = builtin, model inherits\n")
}
```

If `writeSkills` dereferences the nil tracker once `SetupAgents` has run, pass the same arguments `TestCrushInfo_MinimalConfig` passes; it already calls `buildCrushInfo(cfg, nil, nil, nil, nil, nil)`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -run 'Roles|RoleDirs|BuiltinRole' && go test ./internal/agent/tools/ -run CrushInfoListsRoles`
Expected: FAIL to compile (`unknown field RoleDirs`, `undefined: RoleDirs`).

- [ ] **Step 3: Implement the config fields**

In `internal/config/config.go`, import `"github.com/charmbracelet/crush/internal/roles"`. Add to `Agent` after `ContextPaths`:

```go
	// Role is the role definition this agent was built from; nil for the
	// coder, task and plan agents. It never enters the JSON config.
	Role *roles.Role `json:"-"`
```

Add to `Config` after `Agents map[string]Agent \`json:"-"\``:

```go
	// RoleDirs are the directories role files load from, lowest priority
	// first. setDefaults fills them; SetupAgents reads them.
	RoleDirs []roles.Dir `json:"-"`
	// RoleDiagnostics are the problems the last SetupAgents found in role
	// files. crush_info lists them.
	RoleDiagnostics []roles.Diagnostic `json:"-"`
```

After `SubAgentExcludedTools()` add:

```go
// SubAgentWriteTools returns the tools that change files. A role that
// allows none of them runs on the read-only subagent variant.
func SubAgentWriteTools() []string {
	return slices.Clone(subAgentWriteTools)
}
```

At the end of `SetupAgents`, replace `c.Agents = agents` with:

```go
	found, diagnostics := roles.Discover(c.RoleDirs, allToolNames())
	c.RoleDiagnostics = diagnostics
	for name, role := range found {
		agents[name] = Agent{
			ID:           name,
			Name:         name,
			Description:  role.Description,
			Model:        SelectedModelTypeMain,
			ContextPaths: c.Options.ContextPaths,
			AllowedTools: allowedTools,
			AllowedMCP:   coder.AllowedMCP,
			Role:         &role,
		}
	}
	c.Agents = agents
```

If `c.Options` can be nil here (check the top of `SetupAgents`, which already reads `c.Options.DisabledTools`), keep the same assumption the function already makes.

- [ ] **Step 4: Implement the directories**

In `internal/config/load.go`, below `GlobalSkillsDirs`:

```go
// GlobalAgentsDir returns the directory global role files load from.
func GlobalAgentsDir() string {
	if dir := os.Getenv("CRUSH_AGENTS_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home.Config(), appName, "agents")
}

// RoleDirs returns the role directories for a working directory, lowest
// priority first: the global directory, the git root's .crush/agents, then
// the working directory's, so a subdirectory's roles win in a monorepo.
func RoleDirs(workingDir string) []roles.Dir {
	dirs := []roles.Dir{{Path: GlobalAgentsDir(), Source: roles.SourceGlobal}}
	if root := worktreeRoot(workingDir); root != "" && root != workingDir {
		dirs = append(dirs, roles.Dir{Path: filepath.Join(root, ".crush", "agents"), Source: roles.SourceProject})
	}
	return append(dirs, roles.Dir{Path: filepath.Join(workingDir, ".crush", "agents"), Source: roles.SourceProject})
}
```

In `setDefaults(workingDir, dataDir string)`, after the line `c.Options.SkillsPaths = append(c.Options.SkillsPaths, ProjectSkillsDir(workingDir)...)`, add:

```go
	c.RoleDirs = RoleDirs(workingDir)
```

- [ ] **Step 5: Implement `writeRoles` in `crush_info.go`**

Call `writeRoles(&b, cfg)` right after `writeSkills(...)` in `buildCrushInfo`, and add:

```go
func writeRoles(b *strings.Builder, cfg *config.ConfigStore) {
	c := cfg.Config()
	var names []string
	for name, agent := range c.Agents {
		if agent.Role != nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 && len(c.RoleDiagnostics) == 0 {
		return
	}
	slices.Sort(names)
	b.WriteString("[roles]\n")
	for _, name := range names {
		role := c.Agents[name].Role
		model := "inherits"
		if role.Model != "" {
			model = role.Model
			if role.Provider != "" {
				model = role.Provider + "/" + role.Model
			}
		}
		fmt.Fprintf(b, "%s = %s, model %s\n", name, role.Source, model)
	}
	for _, d := range c.RoleDiagnostics {
		fmt.Fprintf(b, "! %s\n", d.String())
	}
	b.WriteString("\n")
}
```

- [ ] **Step 6: Run the tests and fix fallout**

Run: `gofumpt -w internal/config internal/agent/tools && go test ./internal/config/ ./internal/agent/tools/`
Expected: PASS. Tests that count `cfg.Agents` or snapshot `crush_info` output may now fail because five role agents exist; update those expectations (for golden files: `go test ./internal/agent/tools/ -update`), and check each updated golden only gained the `[roles]` section.

Then run: `go test ./internal/agent/ -run 'Preset|AgentTool'`
Expected: `availableSubAgentPresets` now lists role agents; that is fixed in Task 5. If a test here fails only because of the extra names, note it and leave it for Task 5 Step 6.

- [ ] **Step 7: Commit**

```bash
git add internal/config internal/agent/tools
git commit -m "feat(config): load role files into the agent definitions"
```

---

### Task 4: The role gate, role-aware session agents, and shared helpers

**Files:**
- Create: `internal/agent/role_agents.go`
- Modify: `internal/agent/agent.go` (`SessionAgentOptions` near `:537-540`, `sessionAgent` struct near `:347`, `NewSessionAgent` near `:566`, `SetTools` at `:4845`)
- Modify: `internal/agent/compaction_state.go` (`compactionStateText` at `:228-234`)
- Modify: `internal/agent/coordinator.go` (`buildAgentWithModels`, the `NewSessionAgent(SessionAgentOptions{...})` literal near `:1405`)
- Test: `internal/agent/role_agents_test.go` (new)

**Interfaces:**
- Consumes: `config.Agent.Role`, `roles.Role.Allows`, `roles.Block`, `roles.Catalog`.
- Produces (all in package `agent`):
  - `type roleGatedTool struct` and `func gateToolsForRole(role *roles.Role, list []fantasy.AgentTool) []fantasy.AgentTool`
  - `var teamCoordinationTools []string`
  - `SessionAgentOptions.Role *roles.Role`; `sessionAgent.role *roles.Role`
  - `func withRoleBlock(role *roles.Role, state string) string`
  - `func resolveRole(cfg *config.Config, name string) (*roles.Role, string)`; the string is a model-facing refusal.
  - `func roleModelParams(role *roles.Role) AgentDispatchParams`
  - `func roleCatalog(cfg *config.Config) string`

- [ ] **Step 1: Write the failing tests**

`internal/agent/role_agents_test.go`:

```go
package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/roles"
)

type noInput struct{}

func recordingTool(name string, ran map[string]bool) fantasy.AgentTool {
	return fantasy.NewAgentTool(name, "test tool "+name,
		func(_ context.Context, _ noInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ran[name] = true
			return fantasy.NewTextResponse("ok"), nil
		})
}

func runTool(t *testing.T, tool fantasy.AgentTool) fantasy.ToolResponse {
	t.Helper()
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-1", Name: tool.Info().Name, Input: "{}"})
	require.NoError(t, err)
	return resp
}

func TestRoleGateRefusesDisallowedTool(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	role := &roles.Role{Name: "reviewer", Tools: []string{"view"}}
	gated := gateToolsForRole(role, []fantasy.AgentTool{
		recordingTool("view", ran),
		recordingTool("edit", ran),
		recordingTool(SendMessageToolName, ran),
		recordingTool(TeamTaskUpdateToolName, ran),
	})
	for _, tool := range gated {
		runTool(t, tool)
	}
	require.True(t, ran["view"])
	require.False(t, ran["edit"], "a disallowed tool must not run")
	require.True(t, ran[SendMessageToolName], "team coordination tools always pass")
	require.True(t, ran[TeamTaskUpdateToolName], "team coordination tools always pass")

	resp := runTool(t, gated[1])
	require.True(t, resp.IsError)
	require.Equal(t, `tool "edit" is not available to role "reviewer". Allowed tools: view`, resp.Content)
}

func TestRoleGateMatchesLateMCPTool(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	role := &roles.Role{Name: "researcher", Tools: []string{"view", "mcp_*"}}
	gated := gateToolsForRole(role, []fantasy.AgentTool{recordingTool("mcp_tavily_tavily_search", ran)})
	runTool(t, gated[0])
	require.True(t, ran["mcp_tavily_tavily_search"])
}

func TestRoleGateKeepsToolInfo(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	inner := recordingTool("edit", ran)
	gated := gateToolsForRole(&roles.Role{Name: "r", Tools: []string{"view"}}, []fantasy.AgentTool{inner})
	require.Equal(t, inner.Info(), gated[0].Info(), "the gate must not change what the provider sees")
	again := gateToolsForRole(&roles.Role{Name: "r", Tools: []string{"view"}}, gated)
	_, double := again[0].(*roleGatedTool).inner.(*roleGatedTool)
	require.False(t, double, "re-gating must not stack wrappers")
	require.Same(t, inner, gateToolsForRole(nil, []fantasy.AgentTool{inner})[0], "no role, no wrapper")
}

func TestSetToolsAppliesRoleGate(t *testing.T) {
	t.Parallel()
	ran := map[string]bool{}
	agent := NewSessionAgent(SessionAgentOptions{Role: &roles.Role{Name: "reviewer", Tools: []string{"view"}}}).(*sessionAgent)
	agent.SetTools([]fantasy.AgentTool{recordingTool("edit", ran)})
	runTool(t, agent.tools.Copy()[0])
	require.False(t, ran["edit"])
}

func TestWithRoleBlock(t *testing.T) {
	t.Parallel()
	role := &roles.Role{Name: "reviewer", Body: "Review."}
	require.Equal(t, "state", withRoleBlock(nil, "state"))
	require.Equal(t, roles.Block(*role)+"state", withRoleBlock(role, "state"))
	require.Equal(t, roles.Block(*role), withRoleBlock(role, ""), "a role alone still produces a state message")
}

func TestResolveRole(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Options: &config.Options{}}
	cfg.SetupAgents()

	role, refusal := resolveRole(cfg, "reviewer")
	require.Empty(t, refusal)
	require.Equal(t, "reviewer", role.Name)

	_, refusal = resolveRole(cfg, "task")
	require.Contains(t, refusal, `unknown role "task"`)
	_, refusal = resolveRole(cfg, "nope")
	require.Contains(t, refusal, "available: architect, implementer, researcher, reviewer, verifier")
}

func TestRoleCatalog(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Options: &config.Options{}}
	cfg.SetupAgents()
	catalog := roleCatalog(cfg)
	require.Contains(t, catalog, "\n\nRoles (pass one as `role`):\n- architect (builtin): ")
	require.Equal(t, catalog, roleCatalog(cfg), "the catalog must be byte-stable")
}
```

If `NewSessionAgent` with an otherwise-empty `SessionAgentOptions` panics, build the agent the way `newGateTestCoordinator` in `internal/agent` tests does and set the role on the options it passes.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'RoleGate|SetToolsAppliesRoleGate|WithRoleBlock|ResolveRole|RoleCatalog'`
Expected: FAIL to compile (`undefined: gateToolsForRole`).

- [ ] **Step 3: Implement `role_agents.go`**

```go
package agent

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/roles"
)

// teamCoordinationTools pass every role gate: a reviewer still has to claim
// its task and report to the Lead.
var teamCoordinationTools = []string{
	SendMessageToolName,
	ListAgentsToolName,
	WaitAgentToolName,
	TeamTaskCreateToolName,
	TeamTaskListToolName,
	TeamTaskGetToolName,
	TeamTaskUpdateToolName,
}

// roleGatedTool refuses calls the role does not allow. It keeps the inner
// tool's Info and provider options, so the tools block a role agent sends
// stays byte-identical to an agent without a role and the cache is shared.
type roleGatedTool struct {
	inner fantasy.AgentTool
	role  *roles.Role
}

// gateToolsForRole wraps every tool in list with the role's gate. A nil
// role returns list unchanged, so agents without a role are untouched.
func gateToolsForRole(role *roles.Role, list []fantasy.AgentTool) []fantasy.AgentTool {
	if role == nil {
		return list
	}
	out := make([]fantasy.AgentTool, len(list))
	for i, tool := range list {
		if gated, ok := tool.(*roleGatedTool); ok {
			tool = gated.inner
		}
		out[i] = &roleGatedTool{inner: tool, role: role}
	}
	return out
}

func (g *roleGatedTool) Info() fantasy.ToolInfo { return g.inner.Info() }

func (g *roleGatedTool) ProviderOptions() fantasy.ProviderOptions {
	return g.inner.ProviderOptions()
}

func (g *roleGatedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	g.inner.SetProviderOptions(opts)
}

func (g *roleGatedTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	name := g.inner.Info().Name
	if g.role.Allows(name) || slices.Contains(teamCoordinationTools, name) {
		return g.inner.Run(ctx, call)
	}
	return fantasy.NewTextErrorResponse(fmt.Sprintf(
		"tool %q is not available to role %q. Allowed tools: %s",
		name, g.role.Name, strings.Join(g.role.Tools, ", "),
	)), nil
}

// withRoleBlock puts the role block in front of the post-compaction state
// text, so an agent keeps its role after its first message is summarized.
func withRoleBlock(role *roles.Role, state string) string {
	if role == nil {
		return state
	}
	return roles.Block(*role) + state
}

// configuredRoles returns every role agent in cfg.
func configuredRoles(cfg *config.Config) []roles.Role {
	if cfg == nil {
		return nil
	}
	var out []roles.Role
	for _, agent := range cfg.Agents {
		if agent.Role != nil {
			out = append(out, *agent.Role)
		}
	}
	return out
}

// resolveRole looks a role up by name. The string is a refusal for the
// model when the name is unknown.
func resolveRole(cfg *config.Config, name string) (*roles.Role, string) {
	name = strings.TrimSpace(name)
	if cfg != nil {
		if agent, ok := cfg.Agents[name]; ok && agent.Role != nil {
			role := *agent.Role
			return &role, ""
		}
	}
	var names []string
	for _, role := range configuredRoles(cfg) {
		names = append(names, role.Name)
	}
	sort.Strings(names)
	return nil, fmt.Sprintf("unknown role %q; available: %s", name, strings.Join(names, ", "))
}

// roleModelParams turns a role's model group into dispatch override params.
func roleModelParams(role *roles.Role) AgentDispatchParams {
	return AgentDispatchParams{Model: role.Model, Provider: role.Provider, Reasoning: role.Reasoning}
}

// roleCatalog is the role list appended to the delegation tools'
// descriptions. It changes only when config reloads, so the tools block
// stays byte-stable within a session.
func roleCatalog(cfg *config.Config) string {
	list := configuredRoles(cfg)
	if len(list) == 0 {
		return ""
	}
	return "\n\nRoles (pass one as `role`):\n" + strings.TrimRight(roles.Catalog(list), "\n")
}
```

- [ ] **Step 4: Wire the role into `sessionAgent`**

In `internal/agent/agent.go`:
- Add to `SessionAgentOptions` (next to `LazyTools`):

```go
	// Role restricts the tools this agent may call and is re-attached
	// after compaction. Nil for agents without a role.
	Role *roles.Role
```

- Add `role *roles.Role` to the `sessionAgent` struct next to `tools`.
- In `NewSessionAgent`, set `role: opts.Role,` and change `tools: csync.NewSliceFrom(opts.Tools),` to `tools: csync.NewSliceFrom(gateToolsForRole(opts.Role, opts.Tools)),`.
- Change `SetTools`:

```go
func (a *sessionAgent) SetTools(tools []fantasy.AgentTool) {
	// Every path that replaces the palette goes through here, so the role
	// gate cannot be skipped by a rebuild after an MCP or model refresh.
	a.tools.SetSlice(gateToolsForRole(a.role, tools))
}
```

- Import `"github.com/charmbracelet/crush/internal/roles"`.

In `internal/agent/compaction_state.go`, change the last line of `compactionStateText`:

```go
	state, listed := renderCompactionState(todos, snapshot, a.compactionStateFiles(ctx, sessionID), skills)
	return withRoleBlock(a.role, state), listed
```

The caller at `agent.go:2661` already stores the state message whenever `stateText != ""` (`agent.go:2955-2956`), so a role alone is enough to produce one; no change is needed there.

In `internal/agent/coordinator.go` `buildAgentWithModels`, add `Role: agent.Role,` to the `SessionAgentOptions` literal.

- [ ] **Step 5: Run the tests**

Run: `gofumpt -w internal/agent && go test ./internal/agent/ -run 'RoleGate|SetToolsAppliesRoleGate|WithRoleBlock|ResolveRole|RoleCatalog|Compaction|TeammateToolPalette|TeammateSystemPrompt'`
Expected: PASS. The existing cache tests must still pass unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/agent
git commit -m "feat(agent): gate role agents' tools at call time and keep roles across compaction"
```

---

### Task 5: Roles for subagents (`agent_dispatch` and `agent`)

**Files:**
- Modify: `internal/agent/agent_dispatch.go` (`AgentDispatchParams` at `:68-80`, `AgentDispatchResponseMetadata` at `:94`, `agentDispatchTool` at `:412-510`, rehydration near `:1030-1060`)
- Modify: `internal/agent/agent_dispatch_status.go` (`dispatchRecord` at `:32-47`, record creation at `:111-122`)
- Modify: `internal/agent/agent_dispatch_model.go` (add `roleTaskAgent` below `overrideTaskAgent`)
- Modify: `internal/agent/agent_tool.go` (`AgentParams` at `:25-31`, `agentTool` description at `:150-152`, `resolveAgentToolVariant` at `:198-217`, `availableSubAgentPresets` at `:251-262`)
- Test: `internal/agent/role_subagent_test.go` (new)

**Interfaces:**
- Consumes: Task 4 (`resolveRole`, `roleModelParams`, `roleCatalog`, `withRoleBlock`), `config.SubAgentWriteTools()`, existing `c.resolveModelOverride`, `c.overrideModel`, `c.taskAgentConfig`, `c.buildTaskAgent`.
- Produces:
  - `AgentDispatchParams.Role string` (`json:"role,omitempty"`), `AgentParams.Role string` (`json:"role,omitempty"`).
  - `AgentDispatchResponseMetadata.Role string` (`json:"role,omitempty"`); `dispatchRecord.Role string`.
  - `func (c *coordinator) roleTaskAgent(ctx context.Context, role *roles.Role, access string, params AgentDispatchParams, parentModel Model) (SessionAgent, config.SelectedModel, bool, string)`
  - `func roleAccess(role *roles.Role, requested string) (string, string)` returning `(access, refusal)`.

- [ ] **Step 1: Write the failing tests**

`internal/agent/role_subagent_test.go`:

```go
package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/roles"
)

func TestRoleAccess(t *testing.T) {
	t.Parallel()
	read := &roles.Role{Name: "reviewer", Tools: []string{"view"}}
	write := &roles.Role{Name: "implementer"}

	access, refusal := roleAccess(read, "")
	require.Equal(t, accessRead, access)
	require.Empty(t, refusal)

	_, refusal = roleAccess(read, accessWrite)
	require.Equal(t, `role "reviewer" is read-only; access "write" is not allowed`, refusal)

	access, _ = roleAccess(write, "")
	require.Equal(t, accessWrite, access)
	access, _ = roleAccess(write, accessRead)
	require.Equal(t, accessRead, access, "access can narrow a write role")
}

// TestRoleSubAgentSharesVariantPrefix pins the cache rule for subagents: a
// role picks one of the two existing variants and changes neither its tools
// block nor its system prompt.
func TestRoleSubAgentSharesVariantPrefix(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	variants, err := coord.taskAgents(t.Context(), false)
	require.NoError(t, err)

	for name, access := range map[string]string{"reviewer": accessRead, "implementer": accessWrite} {
		role, refusal := resolveRole(coord.cfg.Config(), name)
		require.Empty(t, refusal)
		agent, _, _, refusal := coord.roleTaskAgent(t.Context(), role, "", AgentDispatchParams{}, coord.mainModels().Large)
		require.Empty(t, refusal, name)

		base := variants[access].(*sessionAgent)
		got := agent.(*sessionAgent)
		require.Equal(t, agentToolNames(t, base), agentToolNames(t, got), name)
		for i, tool := range base.tools.Copy() {
			want, err := json.Marshal(tool.Info())
			require.NoError(t, err)
			have, err := json.Marshal(got.tools.Copy()[i].Info())
			require.NoError(t, err)
			require.Equal(t, string(want), string(have), "%s: tool %q", name, tool.Info().Name)
		}
		require.Equal(t, base.SystemPrompt(), got.SystemPrompt(), name)
		require.Equal(t, name, got.role.Name)
	}
}

func TestAgentPresetsExcludeRoles(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Options: &config.Options{}}
	cfg.SetupAgents()
	require.NotContains(t, availableSubAgentPresets(cfg), "reviewer")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'RoleAccess|RoleSubAgent|AgentPresetsExcludeRoles'`
Expected: FAIL to compile (`undefined: roleAccess`).

- [ ] **Step 3: Implement `roleAccess` and `roleTaskAgent`**

Append to `internal/agent/agent_dispatch_model.go`:

```go
// roleAccess picks the subagent variant for a role. A role that allows no
// file-editing tool runs read-only; access may narrow a write role to read
// but never widen a read-only one.
func roleAccess(role *roles.Role, requested string) (string, string) {
	readOnly := role.ReadOnly(config.SubAgentWriteTools())
	switch requested {
	case "":
		if readOnly {
			return accessRead, ""
		}
		return accessWrite, ""
	case accessRead:
		return accessRead, ""
	case accessWrite:
		if readOnly {
			return "", fmt.Sprintf("role %q is read-only; access %q is not allowed", role.Name, requested)
		}
		return accessWrite, ""
	default:
		return "", fmt.Sprintf("unknown access %q", requested)
	}
}

// roleTaskAgent builds a task subagent for a role. It runs on one of the
// two existing variants, so its tools block and system prompt match an
// agent without a role; the role's gate and block ride on top. The model
// is the dispatch's override if it has one, then the role's, then the
// shared subagent model.
func (c *coordinator) roleTaskAgent(ctx context.Context, role *roles.Role, access string, params AgentDispatchParams, parentModel Model) (SessionAgent, config.SelectedModel, bool, string) {
	access, refusal := roleAccess(role, access)
	if refusal != "" {
		return nil, config.SelectedModel{}, false, refusal
	}
	explicit := strings.TrimSpace(params.Model) != "" || strings.TrimSpace(params.Provider) != "" || strings.TrimSpace(params.Reasoning) != ""
	if !explicit && role.PinsModel() {
		params = roleModelParams(role)
	}
	sel, hasOverride, refusal := c.resolveModelOverride(params, parentModel)
	if refusal != "" {
		return nil, config.SelectedModel{}, false, fmt.Sprintf("role %q: %s", role.Name, refusal)
	}
	var modelsFn func() ModelSet
	if hasOverride {
		entry, err := c.overrideModel(sel)
		if err != nil {
			return nil, sel, true, "failed to set up the requested model: " + err.Error()
		}
		modelsFn = func() ModelSet {
			return ModelSet{Large: entry.current(c), Small: c.subAgentModels().Small}
		}
	}
	agentCfg, canWrite := c.taskAgentConfig(access)
	agentCfg.Role = role
	var ready errgroup.Group
	agent, err := c.buildTaskAgent(ctx, &ready, agentCfg, canWrite, modelsFn)
	if err != nil {
		return nil, sel, hasOverride, "failed to start a subagent for role " + role.Name + ": " + err.Error()
	}
	if err := ready.Wait(); err != nil {
		return nil, sel, hasOverride, "failed to start a subagent for role " + role.Name + ": " + err.Error()
	}
	return agent, sel, hasOverride, ""
}
```

Add the `roles` and `errgroup` imports if missing.

- [ ] **Step 4: Wire `agent_dispatch`**

- Add to `AgentDispatchParams` after `Access`:

```go
	Role string `json:"role,omitempty" description:"Optional role from the catalog below. It sets the subagent's tool allow-list, prompt and default model; access can only narrow it"`
```

- Add `Role string \`json:"role,omitempty"\`` to `AgentDispatchResponseMetadata`.
- In `agentDispatchTool`, change the description argument to `strings.TrimSpace(agentDispatchToolDescription)+roleCatalog(c.cfg.Config())`.
- In the handler, replace the block from `agent, refusal := resolveSubAgent(agents, params.Access, readOnly)` through the end of the override block (`if overrideAgent != nil { agent = overrideAgent }`) with:

```go
			access := accessOrRead(params.Access)
			prompt := params.Prompt
			var (
				agent       SessionAgent
				sel         config.SelectedModel
				hasOverride bool
			)
			if strings.TrimSpace(params.Role) != "" {
				role, refusal := resolveRole(c.cfg.Config(), params.Role)
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				if refusal := planModeRoleRefusal(readOnly, role, params.Access); refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				agent, sel, hasOverride, refusal = c.roleTaskAgent(ctx, role, params.Access, params, parentModel())
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				access, _ = roleAccess(role, params.Access)
				prompt = roles.Block(*role) + params.Prompt
			} else {
				var refusal string
				agent, refusal = resolveSubAgent(agents, params.Access, readOnly)
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				// A model override swaps the shared subagent for a dedicated
				// one running on the requested model. It is resolved before the
				// label is taken, so a refused override leaves the label free
				// and the call can be retried immediately.
				overrideAgent, overrideSel, override, refusal := c.overrideDispatchAgent(ctx, access, params, parentModel())
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				if overrideAgent != nil {
					agent = overrideAgent
				}
				sel, hasOverride = overrideSel, override
			}
			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}
```

Keep the original `agentMessageID` check only once (move it as shown). Then in the `c.runSubAgent(ctx, subAgentParams{...})` call change `Prompt: params.Prompt,` to `Prompt: prompt,`, and after building `meta` add `meta.Role = strings.TrimSpace(params.Role)`.

A read-only parent (plan mode) refuses a write role the same way `resolveSubAgent` refuses `access=write` there (`agent_tool.go:79-81`), rather than narrowing it silently. Add to `agent_dispatch_model.go`:

```go
// planModeRoleRefusal refuses a role that would write from a read-only
// parent, as resolveSubAgent refuses access=write there.
func planModeRoleRefusal(readOnly bool, role *roles.Role, access string) string {
	if !readOnly {
		return ""
	}
	if want, _ := roleAccess(role, access); want == accessWrite {
		return fmt.Sprintf(`plan mode subagents are read-only; role %q can write, so pass access "read" or pick a read-only role`, role.Name)
	}
	return ""
}
```

and to `role_subagent_test.go`:

```go
func TestPlanModeRoleRefusal(t *testing.T) {
	t.Parallel()
	write := &roles.Role{Name: "implementer"}
	read := &roles.Role{Name: "reviewer", Tools: []string{"view"}}
	require.Empty(t, planModeRoleRefusal(false, write, ""))
	require.Empty(t, planModeRoleRefusal(true, read, ""))
	require.Empty(t, planModeRoleRefusal(true, write, accessRead))
	require.Contains(t, planModeRoleRefusal(true, write, ""), "plan mode subagents are read-only")
}
```

Use `planModeRoleRefusal` the same way in `resolveAgentToolVariant` (Step 6) before calling `roleTaskAgent`, passing its `readOnly` argument.

- [ ] **Step 5: Wire resume after restart**

In `agent_dispatch_status.go`:
- Add `Role string` to `dispatchRecord` after `Reasoning`.
- In the record literal at `:111-122`, add `Role: strings.TrimSpace(params.Role),` and change `Access: accessOrRead(params.Access),` to:

```go
				Access:    recordAccess(params),
```

with, in the same file:

```go
// recordAccess keeps a role dispatch's access as asked, so a resume lets
// the role pick its variant again instead of forcing read.
func recordAccess(params AgentDispatchParams) string {
	if strings.TrimSpace(params.Role) != "" {
		return params.Access
	}
	return accessOrRead(params.Access)
}
```

- In the `AgentDispatchToolName` result case, after `record.Reasoning = meta.Reasoning` add `if meta.Role != "" { record.Role = meta.Role }`.

In `agent_dispatch.go` rehydration (the block starting `agents, err := c.taskAgents(ctx, false)`), insert before it:

```go
	if record.Role != "" {
		role, refusal := resolveRole(c.cfg.Config(), record.Role)
		if refusal != "" {
			return nil, fmt.Errorf("subagent %q cannot be resumed: role %q no longer exists", label, record.Role)
		}
		params := AgentDispatchParams{Model: record.Model, Provider: record.Provider, Reasoning: record.Reasoning}
		agent, _, _, refusal := c.roleTaskAgent(ctx, role, record.Access, params, c.mainModel())
		if refusal != "" {
			return nil, fmt.Errorf("subagent %q cannot be resumed: %s", label, refusal)
		}
		return c.reopenDispatched(ctx, parentSessionID, label, record, agent)
	}
```

The enclosing function is `func (c *coordinator) rehydrateDispatch(ctx context.Context, parentSessionID, label string) (*dispatchedAgent, error)`. Move everything from `sessionID := c.sessions.CreateAgentToolSessionID(record.MessageID, record.CallID)` to its end into:

```go
// reopenDispatched registers a rehydrated subagent under its label, seeded
// with what its session has already cost.
func (c *coordinator) reopenDispatched(ctx context.Context, parentSessionID, label string, record *dispatchRecord, agent SessionAgent) (*dispatchedAgent, error) {
	// The moved lines, unchanged.
}
```

and end `rehydrateDispatch` with `return c.reopenDispatched(ctx, parentSessionID, label, record, agent)`. Replace the `// The moved lines, unchanged.` comment with the moved code. The local variable holding the record in `rehydrateDispatch` must be named `record` where the new block runs; if it has another name, use it.

- [ ] **Step 6: Wire the `agent` tool**

- Add to `AgentParams` after `Access`:

```go
	Role string `json:"role,omitempty" description:"Optional role from the catalog below. It sets the subagent's tool allow-list, prompt and default model; access can only narrow it"`
```

- In `agentTool`, replace

```go
	description := agentToolDescription
	if presets := availableSubAgentPresets(c.cfg.Config()); len(presets) > 0 {
		description += "\n\nAvailable presets: " + strings.Join(presets, ", ") + "."
	}
```

with `description := agentToolDescription + roleCatalog(c.cfg.Config())`.

- At the top of `resolveAgentToolVariant`, add:

```go
	roleName := cmp.Or(strings.TrimSpace(params.Role), roleNameIfRole(c.cfg.Config(), params.Agent))
	if roleName != "" {
		role, refusal := resolveRole(c.cfg.Config(), roleName)
		if refusal != "" {
			return nil, refusal
		}
		if refusal := planModeRoleRefusal(readOnly, role, params.Access); refusal != "" {
			return nil, refusal
		}
		agent, _, _, refusal := c.roleTaskAgent(ctx, role, params.Access, AgentDispatchParams{}, c.mainModel())
		return agent, refusal
	}
```

and in `agent_tool.go`:

```go
// roleNameIfRole returns name when it names a role, so the agent tool's
// preset parameter accepts role names too.
func roleNameIfRole(cfg *config.Config, name string) string {
	if cfg == nil {
		return ""
	}
	if agent, ok := cfg.Agents[name]; ok && agent.Role != nil {
		return name
	}
	return ""
}
```

- The prompt: in the `agentTool` handler, after `agent, refusal := c.resolveAgentToolVariant(...)`, compute the role block the same way and prepend it to `params.Prompt` in the `subAgentParams` literal:

```go
			prompt := params.Prompt
			if name := cmp.Or(strings.TrimSpace(params.Role), roleNameIfRole(c.cfg.Config(), params.Agent)); name != "" {
				if role, refusal := resolveRole(c.cfg.Config(), name); refusal == "" {
					prompt = roles.Block(*role) + params.Prompt
				}
			}
```

and use `Prompt: prompt,`.

- In `availableSubAgentPresets`, add `|| preset.Role != nil` to the skip condition.

- [ ] **Step 7: Run the tests and fix fallout**

Run: `gofumpt -w internal/agent && go test ./internal/agent/`
Expected: PASS. Tool-description golden files that include the `agent` or `agent_dispatch` description change once (catalog added, preset line removed): regenerate with `go test ./internal/agent/ -update` and check the diff contains only those lines.

- [ ] **Step 8: Commit**

```bash
git add internal/agent
git commit -m "feat(agent): let agent_dispatch and agent run a subagent in a role"
```

---

### Task 6: Roles for teammates

**Files:**
- Modify: `internal/agentteam/team.go` (`Member` at `:192-207`, `MemberView` at `:210-221`, `SpawnRequest` at `:266-276`, `viewLocked` at `:862-880`, `SpawnTeammate` member literal at `:953-965`, `Policy` at `:46-64`)
- Modify: `internal/agent/team_tools.go` (`spawnTeammateParams` at `:33-44`, `spawnTeammateTool` at `:210-252`)
- Modify: `internal/agent/team.go` (`teammatePrompt` at `:61-66`, `Start` at `:176-235`, `teammateAgent` at `:273-311`, `teammateModels` at `:317-337`, `teammateSession` at `:342-350`)
- Modify: `internal/cmd/team.go` (`renderTeam` at `:138-150`)
- Modify: `internal/ui/model/team.go` (member line at `:144`)
- Test: `internal/agent/role_teammate_test.go` (new), `internal/agentteam/team_test.go`, `internal/cmd/team_test.go`

**Interfaces:**
- Consumes: Task 4 (`resolveRole`, `roleModelParams`, `roleCatalog`), `roles.Block`.
- Produces:
  - `agentteam.SpawnRequest.RoleName`, `agentteam.Member.RoleName` (`json:"role_name,omitempty"`), `agentteam.MemberView.RoleName` (`json:"role_name,omitempty"`).
  - `spawnTeammateParams.Role string` (`json:"role,omitempty"`).
  - `func (r *teamRunner) teammatePrompt(ctx context.Context, member agentteam.Member, task string, first bool) string`
  - `func (r *teamRunner) teammateSession(ctx context.Context, member agentteam.Member) (session.Session, bool, error)`; the bool is true when the session was created.

- [ ] **Step 1: Write the failing tests**

`internal/agent/role_teammate_test.go`:

```go
package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/roles"
)

// TestRoleTeammateKeepsLeadPrefix pins the cache rule for teammates: a
// teammate with any role sends the Lead's exact tools block and system
// prompt.
func TestRoleTeammateKeepsLeadPrefix(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	lead := coord.coderAgent()
	require.NotNil(t, lead)

	plain, err := runner.teammateAgent(agentteam.WithMember(t.Context(), "plain"), agentteam.Member{Name: "plain", Role: agentteam.RoleTeammate})
	require.NoError(t, err)

	for _, name := range []string{"reviewer", "implementer"} {
		member := agentteam.Member{Name: "t-" + name, Role: agentteam.RoleTeammate, RoleName: name}
		agent, err := runner.teammateAgent(agentteam.WithMember(t.Context(), member.Name), member)
		require.NoError(t, err)

		require.Equal(t, agentToolNames(t, lead), agentToolNames(t, agent), name)
		leadTools := lead.(*sessionAgent).tools.Copy()
		for i, tool := range agent.(*sessionAgent).tools.Copy() {
			want, err := json.Marshal(leadTools[i].Info())
			require.NoError(t, err)
			have, err := json.Marshal(tool.Info())
			require.NoError(t, err)
			require.Equal(t, string(want), string(have), "%s: tool %q", name, tool.Info().Name)
		}
		require.Equal(t, plain.SystemPrompt(), agent.SystemPrompt(), name)
		require.Equal(t, name, agent.(*sessionAgent).role.Name)
	}
}

func TestRoleTeammateGatedOnRebuild(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	member := agentteam.Member{Name: "rev", Role: agentteam.RoleTeammate, RoleName: "reviewer", SessionID: "existing"}
	agent, err := runner.teammateAgent(agentteam.WithMember(t.Context(), member.Name), member)
	require.NoError(t, err)
	for _, tool := range agent.(*sessionAgent).tools.Copy() {
		_, gated := tool.(*roleGatedTool)
		require.True(t, gated, "tool %q must be gated", tool.Info().Name)
	}
}

func TestRoleTeammateMissingRoleFails(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	member := agentteam.Member{Name: "gone", Role: agentteam.RoleTeammate, RoleName: "no-such-role"}
	_, err := runner.teammateAgent(agentteam.WithMember(t.Context(), member.Name), member)
	require.ErrorContains(t, err, `role "no-such-role" no longer exists`)
}

func TestTeammatePromptRoleBlockFirstTurnOnly(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	role, refusal := resolveRole(coord.cfg.Config(), "implementer")
	require.Empty(t, refusal)

	a := agentteam.Member{Name: "impl-a", Role: agentteam.RoleTeammate, RoleName: "implementer", Context: agentteam.ContextFresh}
	b := agentteam.Member{Name: "impl-b", Role: agentteam.RoleTeammate, RoleName: "implementer", Context: agentteam.ContextFresh}

	first := runner.teammatePrompt(t.Context(), a, "Do task 1.", true)
	require.True(t, strings.HasPrefix(first, roles.Block(*role)+`You are teammate "impl-a".`))

	sibling := runner.teammatePrompt(t.Context(), b, "Do task 2.", true)
	shared := commonPrefixLen(first, sibling)
	require.GreaterOrEqual(t, shared, len(roles.Block(*role)), "siblings with one role share the whole role block")

	later := runner.teammatePrompt(t.Context(), a, "Next.", false)
	require.NotContains(t, later, "## Your role:")

	plain := agentteam.Member{Name: "plain", Role: agentteam.RoleTeammate, Context: agentteam.ContextFresh}
	require.Equal(t, teammateTaskMessage(plain, "x"), runner.teammatePrompt(t.Context(), plain, "x", true), "no role, no change")
}

func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}
```

In `internal/agentteam/team_test.go`, add a test that spawns with `SpawnRequest{..., RoleName: "reviewer"}` and asserts the returned `MemberView.RoleName == "reviewer"` and that the saved `team.json` contains `"role_name": "reviewer"`. Copy the setup of the nearest existing `SpawnTeammate` test in that file.

In `internal/cmd/team_test.go`, add a case to the `renderTeam` test with a member `{Name: "rev", RoleName: "reviewer", ...}` and assert the output line contains `rev (reviewer)`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'RoleTeammate|TeammatePromptRoleBlock' && go test ./internal/agentteam/ ./internal/cmd/`
Expected: FAIL to compile (`unknown field RoleName`, too many arguments to `teammatePrompt`).

- [ ] **Step 3: `agentteam` fields and policy**

- Add `RoleName string \`json:"role_name,omitempty"\`` to `Member` after `Reasoning`, to `MemberView` after `Reasoning`, and `RoleName string` to `SpawnRequest` after `Reasoning`.
- In `SpawnTeammate`'s member literal add `RoleName: req.RoleName,`; in `viewLocked` add `RoleName: member.RoleName,`.
- In `Policy`, change `Cap a fan-out at what the task actually needs.` to `Cap a fan-out at what the task actually needs. Pick a role from the spawn_teammate catalog when one fits the brief.`. Run `grep -rn "Cap a fan-out" internal` and update any test that pins the old sentence.

- [ ] **Step 4: `spawn_teammate`**

- Add to `spawnTeammateParams` after `Context`:

```go
	Role string `json:"role,omitempty" description:"Optional role from the catalog in this tool's description. It sets the teammate's tool allow-list, role prompt and default model."`
```

- In `spawnTeammateTool`, append `+roleCatalog(c.cfg.Config())` to the description string.
- In the handler, after `request := agentteam.SpawnRequest{...}`, add:

```go
			if strings.TrimSpace(params.Role) != "" {
				role, refusal := resolveRole(c.cfg.Config(), params.Role)
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				request.RoleName = role.Name
				// A role's own model is checked now, so a model the config
				// cannot serve fails the spawn instead of the first turn.
				if strings.TrimSpace(params.Model) == "" && strings.TrimSpace(params.Reasoning) == "" && role.PinsModel() {
					if _, _, refusal := c.resolveModelOverride(roleModelParams(role), parentModel()); refusal != "" {
						return fantasy.NewTextErrorResponse(fmt.Sprintf("role %q: %s", role.Name, refusal)), nil
					}
				}
			}
```

- [ ] **Step 5: `teammateAgent`, `teammateModels`, `teammateSession`, `teammatePrompt`, `Start`**

In `internal/agent/team.go`:

- In `teammateAgent`, after `agentCfg, ok := r.c.cfg.Config().Agents[config.AgentCoder]` and its check, add:

```go
	var role *roles.Role
	if member.RoleName != "" {
		resolved, refusal := resolveRole(r.c.cfg.Config(), member.RoleName)
		if refusal != "" {
			return nil, fmt.Errorf("teammate %q: role %q no longer exists", member.Name, member.RoleName)
		}
		role = resolved
		agentCfg.Role = role
	}
```

  and change `models, err := r.teammateModels(member, lead.Model())` to `models, err := r.teammateModels(member, role, lead.Model())`.

- Change `teammateModels` to take the role and fall back to it:

```go
func (r *teamRunner) teammateModels(member agentteam.Member, role *roles.Role, parent Model) (func() ModelSet, error) {
	params := AgentDispatchParams{
		Model:     member.Model,
		Provider:  member.ModelProvider,
		Reasoning: member.Reasoning,
	}
	if strings.TrimSpace(member.Model) == "" {
		if role == nil || !role.PinsModel() {
			return nil, nil
		}
		params = roleModelParams(role)
	}
	sel, ok, refusal := r.c.resolveModelOverride(params, parent)
```

  keeping the rest of the function as it is (the refusal, `!ok` and `overrideModel` handling below).

- Change `teammateSession` to report creation:

```go
func (r *teamRunner) teammateSession(ctx context.Context, member agentteam.Member) (session.Session, bool, error) {
	if member.SessionID != "" {
		existing, err := r.c.sessions.Get(ctx, member.SessionID)
		if err == nil {
			return existing, false, nil
		}
	}
	created, err := r.c.sessions.CreateTaskSession(ctx, r.teammateSessionID(member), member.ParentID, "Teammate "+member.Name)
	return created, err == nil, err
}
```

- Change `teammatePrompt`:

```go
// teammatePrompt is the message a teammate turn starts from: the inherited
// context a fork member gets, then, on its first turn only, its role block,
// then its name, then the task.
//
// The inherited context goes first because it is the same bytes for every fork
// sibling of one Lead. The role block follows because it is the same bytes for
// every sibling with that role; the per-sibling name comes after both, so the
// shared prefix runs through the whole role block.
func (r *teamRunner) teammatePrompt(ctx context.Context, member agentteam.Member, task string, first bool) string {
	task = teammateTaskMessage(member, task)
	if first && member.RoleName != "" {
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

- In `Start`, change `sess, err := r.teammateSession(memberCtx, member)` to `sess, created, err := r.teammateSession(memberCtx, member)` and `promptText = r.teammatePrompt(memberCtx, member, promptText)` to `promptText = r.teammatePrompt(memberCtx, member, promptText, created)`.
- Run `grep -rn "teammatePrompt(\|teammateSession(\|teammateModels(" internal/agent` and update every other caller, tests included, to the new signatures (tests that build a first turn pass `true`).
- Import `"github.com/charmbracelet/crush/internal/roles"`.

- [ ] **Step 6: Roster views**

In `internal/cmd/team.go` `renderTeam`, replace `member.Name` in the `Fprintf` with `memberLabel(member)` and add:

```go
// memberLabel names a member with its role, as in "rev (reviewer)".
func memberLabel(member agentteam.Member) string {
	if member.RoleName == "" {
		return member.Name
	}
	return member.Name + " (" + member.RoleName + ")"
}
```

In `internal/ui/model/team.go` near `:144`, pass the same label to `teamMemberLine` instead of `member.Name` (define a local helper with the same body; the UI package must not import `internal/cmd`). Keep the cache key at `:98` on `member.Name` and append `member.RoleName` to it so the sidebar refreshes when a role appears.

- [ ] **Step 7: Run the tests and fix fallout**

Run: `gofumpt -w internal/agent internal/agentteam internal/cmd internal/ui/model && go test ./internal/agent/ ./internal/agentteam/ ./internal/cmd/ ./internal/ui/...`
Expected: PASS. Regenerate UI or prompt golden files only where the diff is the role label, the policy sentence, or the spawn catalog (`go test ./internal/ui/... -update`, `go test ./internal/agent/ -update`).

- [ ] **Step 8: Commit**

```bash
git add internal/agent internal/agentteam internal/cmd internal/ui
git commit -m "feat(team): spawn teammates in a role"
```

---

### Task 7: Documentation

**Files:**
- Modify: `docs/agent-teams.md` (new `## Roles` section after `## Model surface`)
- Modify: `internal/skills/builtin/crush-config/SKILL.md` (and any reference file it points to for options)

**Interfaces:**
- Consumes: the behavior of Tasks 1 to 6.
- Produces: user-facing docs.

- [ ] **Step 1: Write the Roles section of `docs/agent-teams.md`**

Cover, in this order, with one example each: what a role is; the five built-ins and their one-line jobs; the search order (built-in, `$CRUSH_AGENTS_DIR` or `~/.config/crush/agents`, `<git root>/.crush/agents`, `<working dir>/.crush/agents`); the file format table from the spec (section 2); replacement versus overlay, with the four-line overlay that pins a model on `reviewer`; how `role` works on `spawn_teammate`, `agent_dispatch` and `agent`, and the model precedence; the cache rule (roles never change the tools block or system prompt, siblings share the role block); diagnostics in `crush_info` under `[roles]`.

- [ ] **Step 2: Teach `crush-config` the agent file format**

Add a short "Agent roles" section with the file location, the frontmatter fields, the overlay example, and a pointer to `docs/agent-teams.md`.

- [ ] **Step 3: Check the docs**

Run: `grep -n $'\u2014' docs/agent-teams.md internal/skills/builtin/crush-config/SKILL.md`
Expected: no output.
Run: `go test ./internal/skills/`
Expected: PASS (builtin skills still parse).

- [ ] **Step 4: Commit**

```bash
git add docs/agent-teams.md internal/skills/builtin/crush-config
git commit -m "docs: document agent roles"
```

---

### Task 8: Whole-branch verification and the eval comparison

**Files:**
- Create: `evals/experiments/2026-11-25-agent-roles.yaml`

**Interfaces:**
- Consumes: the finished branch.
- Produces: verification output and, with the user's go-ahead, an eval comparison for the PR description.

- [ ] **Step 1: Full checks**

Run: `go build . && go test ./... && task lint`
Expected: build succeeds, all tests PASS, lint clean. Fix anything this branch caused.

- [ ] **Step 2: Cache regression check**

Run: `go test ./internal/agent/ -run 'Teammate|RoleSubAgent|RoleTeammate|TeammatePrompt' -count=3`
Expected: PASS on all three counts.

- [ ] **Step 3: Write the experiment file**

```yaml
date: 2026-11-25
name: agent-roles
variants:
  - name: control
    ref: <full sha of main, from: git rev-parse main>
  - name: roles
    ref: <full sha of the branch head, from: git rev-parse HEAD>
routes: [deepseek-flash]
repetitions: 3
primary_metric: pass_rate
margin: 0.1
baseline: evals/baselines/2026-11-19-deepseek-flash.json
detectable_effect: 0
```

Fill both refs with the real shas before saving.

- [ ] **Step 4: Plan the comparison**

Run: `go run ./cmd/crush-eval --help` and `go run ./cmd/crush-eval plan --help` to confirm the argument shape, then `go run ./cmd/crush-eval plan evals/experiments/2026-11-25-agent-roles.yaml`.
Expected: it prints the detectable effect and fills `detectable_effect`.

- [ ] **Step 5: Run the batch only with the user's go-ahead**

The batch is about 162 model runs. Ask the user before starting it. With approval, run `go run ./cmd/crush-eval batch evals/experiments/2026-11-25-agent-roles.yaml` (confirm flags with `batch --help`), then `go run ./cmd/crush-eval compare` on its output (confirm with `compare --help`). Paste the comparison into the PR description.

- [ ] **Step 6: Commit**

```bash
git add evals/experiments/2026-11-25-agent-roles.yaml
git commit -m "chore(evals): add the agent roles experiment"
```
