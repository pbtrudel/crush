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
