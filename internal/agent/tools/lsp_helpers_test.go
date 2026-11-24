package tools

import (
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/lsp"
)

func TestGetSymbolOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		symbol string
		want   int
	}{
		{"bare symbol", "Bar", 0},
		{"dot qualified", "foo.Bar", 4},
		{"double colon qualified", "Class::method", 7},
		{"backslash qualified", `ns\Func`, 3},
		{"nested dots", "a.b.C", 4},
		{"empty", "", 0},
		{"single char", "x", 0},
		{"dot at end", "foo.", 4},
		{"colon at end", "foo::", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getSymbolOffset(tt.symbol)
			require.Equal(t, tt.want, got, "getSymbolOffset(%q)", tt.symbol)
		})
	}
}

// TestGetSymbolOffset_DoesNotOvershoot verifies that the offset lands
// on the start of the final component, never past it.
func TestGetSymbolOffset_DoesNotOvershoot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		symbol   string
		expected string // the substring starting at the offset
	}{
		{"foo.Bar", "Bar"},
		{"Class::method", "method"},
		{`ns\Func`, "Func"},
		{"a.b.c.D", "D"},
		{"Bar", "Bar"},
	}

	for _, tc := range cases {
		offset := getSymbolOffset(tc.symbol)
		require.LessOrEqual(t, offset, len(tc.symbol),
			"offset %d exceeds symbol length %d for %q", offset, len(tc.symbol), tc.symbol)
		got := tc.symbol[offset:]
		require.Equal(t, tc.expected, got,
			"getSymbolOffset(%q) = %d, remainder = %q, want %q",
			tc.symbol, offset, got, tc.expected)
	}
}

// newLSPMissStore returns a config store whose workspace is dir.
func newLSPMissStore(t *testing.T, dir string) *config.ConfigStore {
	t.Helper()
	cfg, err := config.Init(t.Context(), dir, "", false, config.WithoutProviderUpdate())
	require.NoError(t, err)
	return cfg
}

// TestSymbolsToolExplainsPathOutsideWorkspace pins that a path the LSP tools
// cannot serve says so, instead of a bare miss.
func TestSymbolsToolExplainsPathOutsideWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	cfg := newLSPMissStore(t, workspace)
	outside := filepath.Join(t.TempDir(), "a.go")
	require.NoError(t, os.WriteFile(outside, []byte("package a\n"), 0o644))

	tool := NewSymbolsTool(cfg, lsp.NewManager(cfg))
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{
		ID:    "s1",
		Name:  SymbolsToolName,
		Input: `{"file_path":"` + outside + `"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no LSP client handles file "+outside+": it is outside the workspace "+workspace)
	require.Contains(t, resp.Content, "LSP tools only work on workspace files, use view and edit instead")
}

// TestSymbolsToolExplainsMissingLanguageServer pins that a workspace file
// whose extension no configured server handles names the extension.
func TestSymbolsToolExplainsMissingLanguageServer(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	cfg := newLSPMissStore(t, workspace)
	inside := filepath.Join(workspace, "a.zzz")
	require.NoError(t, os.WriteFile(inside, []byte("zzz\n"), 0o644))

	tool := NewSymbolsTool(cfg, lsp.NewManager(cfg))
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{
		ID:    "s1",
		Name:  SymbolsToolName,
		Input: `{"file_path":"` + inside + `"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no language server is configured for .zzz files")
}
