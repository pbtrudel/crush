package lsp

import (
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// CountByFile adds one server's diagnostics to out, keyed by file path.
// URIs that do not name a file are skipped, and counts for a file
// already in out are added to rather than replaced, so several servers
// reporting on the same file accumulate.
func CountByFile(out map[string]DiagnosticCounts, diags map[protocol.DocumentURI][]protocol.Diagnostic) {
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

// countSeverity adds one diagnostic to counts.
func countSeverity(c *DiagnosticCounts, s protocol.DiagnosticSeverity) {
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
