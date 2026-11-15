package tools

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type DiagnosticsParams struct {
	FilePath string `json:"file_path,omitempty" description:"The path to the file to get diagnostics for (leave empty for project diagnostics)"`
}

const DiagnosticsToolName = "lsp_diagnostics"

//go:embed diagnostics.md
var diagnosticsDescription string

func NewDiagnosticsTool(lspManager *lsp.Manager) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		DiagnosticsToolName,
		diagnosticsDescription,
		func(ctx context.Context, params DiagnosticsParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			notifyLSPs(ctx, lspManager, params.FilePath)
			output := getDiagnostics(params.FilePath, lspManager)
			return fantasy.NewTextResponse(output), nil
		},
	)
}

// openInLSPs ensures LSP servers are running and aware of the file, but does
// not notify changes or wait for fresh diagnostics. Use this for read-only
// operations like view where the file content hasn't changed.
//
// It returns the servers that could still publish for this file, which is
// the only set worth waiting on: a server that already had the file open
// receives no notification, and one that already published a result has
// nothing left to send.
func openInLSPs(
	ctx context.Context,
	manager *lsp.Manager,
	filepath string,
) []*lsp.Client {
	if filepath == "" || manager == nil {
		return nil
	}

	manager.Start(ctx, filepath)

	var pending []*lsp.Client
	for client := range manager.Clients().Seq() {
		if !client.HandlesFile(filepath) {
			continue
		}
		notified, err := client.OpenFileForRead(ctx, filepath)
		if err != nil {
			slog.Warn("Failed to open file in LSP", "file", filepath, "error", err)
		}
		if !notified && client.HasDiagnosticsFor(filepath) {
			continue
		}
		pending = append(pending, client)
	}
	return pending
}

// waitForLSPDiagnostics waits briefly for diagnostics publication after a file
// has been opened. Intended for read-only situations where viewing up-to-date
// files matters but latency should remain low (i.e. when using the view tool).
func waitForLSPDiagnostics(
	ctx context.Context,
	clients []*lsp.Client,
	timeout time.Duration,
) {
	if len(clients) == 0 || timeout <= 0 {
		return
	}

	var wg sync.WaitGroup
	for client := range slices.Values(clients) {
		wg.Go(func() {
			client.WaitForDiagnostics(ctx, timeout)
		})
	}
	wg.Wait()
}

// notifyLSPs notifies LSP servers that a file has changed and waits for
// updated diagnostics. Use this after edit/multiedit operations.
// When filepath is empty, refreshes all open files across all LSP clients
// and sends a workspace-level change notification for full re-analysis.
func notifyLSPs(
	ctx context.Context,
	manager *lsp.Manager,
	filepath string,
) {
	if manager == nil {
		return
	}
	if filepath == "" {
		// No specific file — refresh all open files for all clients.
		var wg sync.WaitGroup
		for client := range manager.Clients().Seq() {
			wg.Go(func() {
				client.RefreshOpenFiles(ctx)
				if err := client.NotifyWorkspaceChange(ctx); err != nil {
					slog.WarnContext(ctx, "Failed to notify workspace change", "error", err)
				}
				client.WaitForDiagnostics(ctx, 5*time.Second)
			})
		}
		wg.Wait()
		return
	}

	manager.Start(ctx, filepath)

	var wg sync.WaitGroup
	for client := range manager.Clients().Seq() {
		if !client.HandlesFile(filepath) {
			continue
		}
		_ = client.OpenFileOnDemand(ctx, filepath)
		_ = client.NotifyChange(ctx, filepath)
		wg.Go(func() {
			client.WaitForDiagnostics(ctx, 5*time.Second)
		})
	}
	wg.Wait()
}

// diagnosticLine is one formatted diagnostic and the severity it was
// reported at.
type diagnosticLine struct {
	severity protocol.DiagnosticSeverity
	text     string
}

func getDiagnostics(filePath string, manager *lsp.Manager) string {
	file, project := collectDiagnostics(filePath, manager)
	out := renderDiagnostics(file, project, file, project)
	slog.Debug("Diagnostics", "output", out)
	return out
}

// getActionableDiagnostics renders what a tool result carries after a file
// was read or changed: its errors and warnings, and errors anywhere else in
// the project (a changed signature breaks callers in other files).
//
// Hints, information and other files' warnings are left to lsp_diagnostics.
// A tool result is re-read on every later request, and on a real session
// log those lines were most of what edits and views returned.
func getActionableDiagnostics(filePath string, manager *lsp.Manager) string {
	file, project := collectDiagnostics(filePath, manager)
	shownFile, shownProject := actionableDiagnostics(file, project)
	return renderDiagnostics(shownFile, shownProject, file, project)
}

// collectDiagnostics gathers the formatted diagnostics of the given file and
// of every other file a running server reported, sorting both.
func collectDiagnostics(filePath string, manager *lsp.Manager) (file, project []diagnosticLine) {
	if manager == nil {
		return nil, nil
	}

	for lspName, client := range manager.Clients().Seq2() {
		for location, diags := range client.GetDiagnostics() {
			path, err := location.Path()
			if err != nil {
				slog.Error("Failed to convert diagnostic location URI to path", "uri", location, "error", err)
				continue
			}
			isCurrentFile := path == filePath
			for _, diag := range diags {
				line := diagnosticLine{
					severity: diag.Severity,
					text:     formatDiagnostic(path, diag, lspName),
				}
				if isCurrentFile {
					file = append(file, line)
				} else {
					project = append(project, line)
				}
			}
		}
	}

	sortDiagnostics(file)
	sortDiagnostics(project)
	return file, project
}

// actionableDiagnostics selects the lines a tool result keeps: errors and
// warnings in the file itself, and errors elsewhere in the project.
func actionableDiagnostics(file, project []diagnosticLine) (shownFile, shownProject []diagnosticLine) {
	for _, line := range file {
		if actionableError(line.severity) || line.severity == protocol.SeverityWarning {
			shownFile = append(shownFile, line)
		}
	}
	for _, line := range project {
		if actionableError(line.severity) {
			shownProject = append(shownProject, line)
		}
	}
	return shownFile, shownProject
}

// actionableError reports whether a severity is one a change can break. The
// protocol leaves an omitted severity to the client, so severity 0 is read as
// an error: showing a line that turned out to be prose costs less than hiding
// a real failure. The full rendering keeps counting it as it always has.
func actionableError(severity protocol.DiagnosticSeverity) bool {
	return severity == protocol.SeverityError || severity == 0
}

// renderDiagnostics writes the blocks from shown and the summary counts from
// all, so a caller that hides lines still reports the real totals. It returns
// "" when nothing is shown, leaving a tool result without an empty block.
func renderDiagnostics(shownFile, shownProject, allFile, allProject []diagnosticLine) string {
	if len(shownFile) == 0 && len(shownProject) == 0 {
		return ""
	}

	var output strings.Builder
	writeDiagnostics(&output, "file_diagnostics", shownFile)
	writeDiagnostics(&output, "project_diagnostics", shownProject)

	output.WriteString("\n<diagnostic_summary>\n")
	fmt.Fprintf(&output, "Current file: %d errors, %d warnings\n",
		countSeverity(allFile, protocol.SeverityError),
		countSeverity(allFile, protocol.SeverityWarning),
	)
	fmt.Fprintf(&output, "Project: %d errors, %d warnings\n",
		countSeverity(allProject, protocol.SeverityError),
		countSeverity(allProject, protocol.SeverityWarning),
	)
	output.WriteString("</diagnostic_summary>\n")

	return output.String()
}

func writeDiagnostics(output *strings.Builder, tag string, in []diagnosticLine) {
	if len(in) == 0 {
		return
	}
	lines := make([]string, len(in))
	for i, line := range in {
		lines[i] = line.text
	}
	output.WriteString("\n<" + tag + ">\n")
	if len(lines) > 10 {
		output.WriteString(strings.Join(lines[:10], "\n"))
		fmt.Fprintf(output, "\n... and %d more diagnostics", len(lines)-10)
	} else {
		output.WriteString(strings.Join(lines, "\n"))
	}
	output.WriteString("\n</" + tag + ">\n")
}

func sortDiagnostics(in []diagnosticLine) {
	sort.Slice(in, func(i, j int) bool {
		iIsError := in[i].severity == protocol.SeverityError
		jIsError := in[j].severity == protocol.SeverityError
		if iIsError != jIsError {
			return iIsError // Errors come first
		}
		return in[i].text < in[j].text // Then alphabetically
	})
}

func formatDiagnostic(pth string, diagnostic protocol.Diagnostic, source string) string {
	severity := "Info"
	switch diagnostic.Severity {
	case protocol.SeverityError:
		severity = "Error"
	case protocol.SeverityWarning:
		severity = "Warn"
	case protocol.SeverityHint:
		severity = "Hint"
	}

	location := fmt.Sprintf("%s:%d:%d", pth, diagnostic.Range.Start.Line+1, diagnostic.Range.Start.Character+1)

	sourceInfo := source
	if diagnostic.Source != "" {
		sourceInfo += " " + diagnostic.Source
	}

	codeInfo := ""
	if diagnostic.Code != nil {
		codeInfo = fmt.Sprintf("[%v]", diagnostic.Code)
	}

	tagsInfo := ""
	if len(diagnostic.Tags) > 0 {
		var tags []string
		for _, tag := range diagnostic.Tags {
			switch tag {
			case protocol.Unnecessary:
				tags = append(tags, "unnecessary")
			case protocol.Deprecated:
				tags = append(tags, "deprecated")
			}
		}
		if len(tags) > 0 {
			tagsInfo = fmt.Sprintf(" (%s)", strings.Join(tags, ", "))
		}
	}

	return fmt.Sprintf("%s: %s [%s]%s%s %s",
		severity,
		location,
		sourceInfo,
		codeInfo,
		tagsInfo,
		diagnostic.Message)
}

func countSeverity(diagnostics []diagnosticLine, severity protocol.DiagnosticSeverity) int {
	count := 0
	for _, diag := range diagnostics {
		if diag.severity == severity {
			count++
		}
	}
	return count
}
