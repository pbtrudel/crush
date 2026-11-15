package tools

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// testDiagnostic builds a formatted diagnostic line the way collectDiagnostics
// would, so the renderer can be exercised without an LSP server.
func testDiagnostic(severity protocol.DiagnosticSeverity, text string) diagnosticLine {
	return diagnosticLine{severity: severity, text: text}
}

func TestGetActionableDiagnosticsRendering(t *testing.T) {
	t.Parallel()

	file := []diagnosticLine{
		testDiagnostic(protocol.SeverityError, "Error e1"),
		testDiagnostic(protocol.SeverityWarning, "Warn w1"),
		testDiagnostic(protocol.SeverityHint, "Hint h1"),
		testDiagnostic(protocol.SeverityInformation, "Info i1"),
	}
	project := []diagnosticLine{
		testDiagnostic(protocol.SeverityError, "Error pe"),
		testDiagnostic(protocol.SeverityWarning, "Warn pw"),
		testDiagnostic(protocol.SeverityHint, "Hint ph"),
	}

	shownFile, shownProject := actionableDiagnostics(file, project)
	out := renderDiagnostics(shownFile, shownProject, file, project)

	require.Contains(t, out, "Error e1")
	require.Contains(t, out, "Warn w1")
	require.Contains(t, out, "Error pe")
	require.NotContains(t, out, "Hint h1")
	require.NotContains(t, out, "Info i1")
	require.NotContains(t, out, "Warn pw")
	require.NotContains(t, out, "Hint ph")
	require.Contains(t, out, "Current file: 1 errors, 1 warnings")
	require.Contains(t, out, "Project: 1 errors, 1 warnings")
}

func TestGetActionableDiagnosticsEmpty(t *testing.T) {
	t.Parallel()

	t.Run("only hints anywhere", func(t *testing.T) {
		t.Parallel()
		file := []diagnosticLine{
			testDiagnostic(protocol.SeverityHint, "Hint h1"),
			testDiagnostic(protocol.SeverityHint, "Hint h2"),
		}
		project := []diagnosticLine{
			testDiagnostic(protocol.SeverityHint, "Hint ph"),
		}
		shownFile, shownProject := actionableDiagnostics(file, project)
		require.Empty(t, shownFile)
		require.Empty(t, shownProject)
		require.Empty(t, renderDiagnostics(shownFile, shownProject, file, project))
	})

	t.Run("only a project warning", func(t *testing.T) {
		t.Parallel()
		project := []diagnosticLine{
			testDiagnostic(protocol.SeverityWarning, "Warn pw"),
		}
		shownFile, shownProject := actionableDiagnostics(nil, project)
		require.Empty(t, shownFile)
		require.Empty(t, shownProject)
		require.Empty(t, renderDiagnostics(shownFile, shownProject, nil, project))
	})

	t.Run("only file information", func(t *testing.T) {
		t.Parallel()
		file := []diagnosticLine{
			testDiagnostic(protocol.SeverityInformation, "Info i1"),
		}
		shownFile, shownProject := actionableDiagnostics(file, nil)
		require.Empty(t, shownFile)
		require.Empty(t, shownProject)
		require.Empty(t, renderDiagnostics(shownFile, shownProject, file, nil))
	})
}

func TestRenderDiagnosticsKeepsEverything(t *testing.T) {
	t.Parallel()

	file := []diagnosticLine{
		testDiagnostic(protocol.SeverityError, "Error e1"),
		testDiagnostic(protocol.SeverityWarning, "Warn w1"),
		testDiagnostic(protocol.SeverityHint, "Hint h1"),
		testDiagnostic(protocol.SeverityInformation, "Info i1"),
	}
	project := []diagnosticLine{
		testDiagnostic(protocol.SeverityError, "Error pe"),
		testDiagnostic(protocol.SeverityWarning, "Warn pw"),
		testDiagnostic(protocol.SeverityHint, "Hint ph"),
	}

	out := renderDiagnostics(file, project, file, project)

	for _, want := range []string{
		"Error e1", "Warn w1", "Hint h1", "Info i1",
		"Error pe", "Warn pw", "Hint ph",
	} {
		require.Contains(t, out, want)
	}
}

func TestRenderDiagnosticsCapsAtTenPerBlock(t *testing.T) {
	t.Parallel()

	project := make([]diagnosticLine, 0, 12)
	for i := range 12 {
		project = append(project, testDiagnostic(
			protocol.SeverityError,
			"Error pe"+strings.Repeat("x", i),
		))
	}

	shownFile, shownProject := actionableDiagnostics(nil, project)
	out := renderDiagnostics(shownFile, shownProject, nil, project)

	require.Contains(t, out, "... and 2 more diagnostics")
	require.Equal(t, 10, strings.Count(out, "Error pe"))
}
