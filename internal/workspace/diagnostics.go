package workspace

import (
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// addDiagnostics adds one server's diagnostics to out, keyed by file path.
// URIs that do not name a file are skipped.
func addDiagnostics(out map[string]lsp.DiagnosticCounts, diags map[protocol.DocumentURI][]protocol.Diagnostic) {
	for uri, list := range diags {
		path, err := uri.Path()
		if err != nil || path == "" {
			continue
		}
		counts := out[path]
		for _, d := range list {
			countSeverity(&counts, d.Severity)
		}
		out[path] = counts
	}
}

func countSeverity(c *lsp.DiagnosticCounts, s protocol.DiagnosticSeverity) {
	switch s {
	case protocol.SeverityError:
		c.Error++
	case protocol.SeverityWarning:
		c.Warning++
	case protocol.SeverityInformation:
		c.Information++
	case protocol.SeverityHint:
		c.Hint++
	}
}
