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
			// An allow-list emptied by the drop would leave a role that
			// can call nothing but team tools, so it is skipped instead.
			if role.Tools != nil && len(role.Tools) == 0 {
				diags = append(diags, Diagnostic{Path: role.Path, Message: "no known tools left after dropping unknown names; role skipped"})
				continue
			}
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
