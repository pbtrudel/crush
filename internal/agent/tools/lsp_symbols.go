package tools

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type SymbolsParams struct {
	FilePath string `json:"file_path" description:"The path to the file to get symbols for"`
}

const SymbolsToolName = "lsp_symbols"

//go:embed lsp_symbols.md
var symbolsDescription string

func NewSymbolsTool(cfg *config.ConfigStore, lspManager *lsp.Manager) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		SymbolsToolName,
		symbolsDescription,
		func(ctx context.Context, params SymbolsParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}
			lspManager.Start(ctx, params.FilePath)

			client := findLSPClient(lspManager, params.FilePath)
			if client == nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"no LSP client handles file %s: %s", params.FilePath, lspMissReason(cfg, params.FilePath))), nil
			}

			symbols, err := client.DocumentSymbols(ctx, params.FilePath)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to get document symbols: %s", err)), nil
			}
			if len(symbols) == 0 {
				return fantasy.NewTextResponse(fmt.Sprintf("No symbols found in %s", params.FilePath)), nil
			}

			return fantasy.NewTextResponse(formatSymbols(symbols, 0)), nil
		},
	)
}

func formatSymbols(symbols []protocol.DocumentSymbolResult, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)
	for _, sym := range symbols {
		rng := sym.GetRange()
		line := rng.Start.Line + 1
		kind := symbolKindString(sym)
		fmt.Fprintf(&b, "%s%s %s (line %d)\n", prefix, kind, sym.GetName(), line)
		if ds, ok := sym.(*protocol.DocumentSymbol); ok && len(ds.Children) > 0 {
			b.WriteString(formatDocumentSymbolChildren(ds.Children, indent+1))
		}
	}
	return b.String()
}

func formatDocumentSymbolChildren(children []protocol.DocumentSymbol, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)
	for i := range children {
		c := &children[i]
		kind := symbolKindString(c)
		fmt.Fprintf(&b, "%s%s %s (line %d)\n", prefix, kind, c.Name, c.Range.Start.Line+1)
		if len(c.Children) > 0 {
			b.WriteString(formatDocumentSymbolChildren(c.Children, indent+1))
		}
	}
	return b.String()
}

func symbolKindString(sym protocol.DocumentSymbolResult) string {
	var kind protocol.SymbolKind
	switch s := sym.(type) {
	case *protocol.DocumentSymbol:
		kind = s.Kind
	case *protocol.SymbolInformation:
		kind = s.Kind
	default:
		return "Symbol"
	}
	if name, ok := symbolKindNames[kind]; ok {
		return name
	}
	return "Symbol"
}

var symbolKindNames = map[protocol.SymbolKind]string{
	protocol.File:          "File",
	protocol.Module:        "Module",
	protocol.Namespace:     "Namespace",
	protocol.Package:       "Package",
	protocol.Class:         "Class",
	protocol.Method:        "Method",
	protocol.Property:      "Property",
	protocol.Field:         "Field",
	protocol.Constructor:   "Constructor",
	protocol.Enum:          "Enum",
	protocol.Interface:     "Interface",
	protocol.Function:      "Function",
	protocol.Variable:      "Variable",
	protocol.Constant:      "Constant",
	protocol.String:        "String",
	protocol.Number:        "Number",
	protocol.Boolean:       "Boolean",
	protocol.Array:         "Array",
	protocol.Object:        "Object",
	protocol.Key:           "Key",
	protocol.Null:          "Null",
	protocol.EnumMember:    "EnumMember",
	protocol.Struct:        "Struct",
	protocol.Event:         "Event",
	protocol.Operator:      "Operator",
	protocol.TypeParameter: "TypeParameter",
}

// lspMissReason explains why no client handled a file, so the model does not
// retry a path the LSP tools cannot serve. It names the workspace for a path
// outside it, and the extension when no configured server handles it.
func lspMissReason(cfg *config.ConfigStore, filePath string) string {
	abs := filePath
	if resolved, err := filepath.Abs(filePath); err == nil {
		abs = resolved
	}
	workspace := ""
	if cfg != nil {
		workspace = cfg.WorkingDir()
	}
	if workspace != "" {
		relative, err := filepath.Rel(workspace, abs)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Sprintf(
				"it is outside the workspace %s; LSP tools only work on workspace files, use view and edit instead",
				workspace)
		}
	}
	ext := filepath.Ext(abs)
	if ext == "" {
		return "no language server is configured for files without an extension; use view and edit instead"
	}
	if cfg != nil && lspConfiguredForExtension(cfg.Config().LSP, ext) {
		return fmt.Sprintf(
			"the language server for %s files is not running; check the [lsp] section of crush_info, or use view and edit instead",
			ext)
	}
	return fmt.Sprintf(
		"no language server is configured for %s files; use view and edit instead", ext)
}

// lspConfiguredForExtension reports whether an enabled LSP server lists ext,
// with or without its leading dot.
func lspConfiguredForExtension(servers map[string]config.LSPConfig, ext string) bool {
	trimmed := strings.TrimPrefix(ext, ".")
	for _, server := range servers {
		if server.Disabled {
			continue
		}
		if slices.Contains(server.FileTypes, trimmed) || slices.Contains(server.FileTypes, ext) {
			return true
		}
	}
	return false
}
