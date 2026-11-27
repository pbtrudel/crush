package workspace

import (
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// countSeverity adds one diagnostic to counts.
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
