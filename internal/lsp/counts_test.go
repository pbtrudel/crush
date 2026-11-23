package lsp

import (
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// TestCountByFileSumsPerPath pins how one server's diagnostics fold
// into per-file counts: severities are counted separately, URIs that
// do not name a file are skipped, and a second server reporting on the
// same file adds to its counts instead of replacing them.
func TestCountByFileSumsPerPath(t *testing.T) {
	t.Parallel()
	out := map[string]DiagnosticCounts{}
	CountByFile(out, map[protocol.DocumentURI][]protocol.Diagnostic{
		protocol.URIFromPath("/repo/a.go"): {
			{Severity: protocol.SeverityError},
			{Severity: protocol.SeverityWarning},
			{Severity: protocol.SeverityWarning},
			{Severity: protocol.SeverityInformation},
		},
		"": {{Severity: protocol.SeverityError}},
	})
	// A second server reporting on the same file adds to its counts.
	CountByFile(out, map[protocol.DocumentURI][]protocol.Diagnostic{
		protocol.URIFromPath("/repo/a.go"): {{Severity: protocol.SeverityHint}},
	})
	require.Equal(t, map[string]DiagnosticCounts{
		"/repo/a.go": {Error: 1, Warning: 2, Information: 1, Hint: 1},
	}, out)
}
