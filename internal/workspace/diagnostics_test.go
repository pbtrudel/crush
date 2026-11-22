package workspace

import (
	"testing"

	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestAddDiagnosticsCountsByPathAndSeverity(t *testing.T) {
	t.Parallel()
	out := map[string]lsp.DiagnosticCounts{}
	addDiagnostics(out, map[protocol.DocumentURI][]protocol.Diagnostic{
		protocol.URIFromPath("/repo/a.go"): {
			{Severity: protocol.SeverityError},
			{Severity: protocol.SeverityWarning},
			{Severity: protocol.SeverityWarning},
		},
		"": {{Severity: protocol.SeverityError}},
	})
	// A second server reporting on the same file adds to its counts.
	addDiagnostics(out, map[protocol.DocumentURI][]protocol.Diagnostic{
		protocol.URIFromPath("/repo/a.go"): {{Severity: protocol.SeverityHint}},
	})
	require.Equal(t, map[string]lsp.DiagnosticCounts{
		"/repo/a.go": {Error: 1, Warning: 2, Hint: 1},
	}, out)
}
