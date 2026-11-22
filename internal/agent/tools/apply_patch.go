package tools

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/diff"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/permission"
)

// ApplyPatchToolName is the tool that applies the *** Begin Patch format.
const ApplyPatchToolName = "apply_patch"

//go:embed apply_patch.md
var applyPatchDescription string

// ApplyPatchParams is the tool's single argument: the whole patch.
type ApplyPatchParams struct {
	Patch string `json:"patch" description:"The patch, starting with *** Begin Patch and ending with *** End Patch"`
}

// ApplyPatchResponseMetadata is what the session log keeps of a patch.
type ApplyPatchResponseMetadata struct {
	Files     []string `json:"files"`
	Additions int      `json:"additions"`
	Removals  int      `json:"removals"`
}

// NewApplyPatchTool builds the tool that applies the *** Begin Patch format,
// the shape OpenAI's GPT models are trained on. It is offered in place of edit
// and multiedit when options.apply_patch is on and the model is a GPT one;
// everything it writes goes through the same read guard, permission check,
// file tracker and LSP diagnostics as edit.
func NewApplyPatchTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ApplyPatchToolName,
		applyPatchDescription,
		func(ctx context.Context, params ApplyPatchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if strings.TrimSpace(params.Patch) == "" {
				return withErrorKind(fantasy.NewTextErrorResponse("patch is required"), ErrorKindInvalidArguments), nil
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for applying a patch")
			}

			edit := editContext{ctx: ctx, permissions: permissions, files: files, filetracker: filetracker, workingDir: workingDir}
			resp, written, err := applyPatch(edit, sessionID, params.Patch, call)
			if err != nil || resp.IsError {
				return resp, err
			}

			for _, path := range written {
				notifyLSPs(ctx, lspManager, path)
			}

			text := fmt.Sprintf("<result>\n%s\n</result>\n", resp.Content)
			for _, path := range written {
				text += getActionableDiagnostics(path, lspManager)
			}
			resp.Content = text
			return resp, nil
		},
	)
}

// applyPatch parses a patch, resolves every section against the working tree,
// asks permission for each file, and only then writes. Nothing is written
// until the whole patch resolves, so a patch that fails its third file leaves
// the first two alone. It returns the paths it wrote so the caller can notify
// the LSP servers.
func applyPatch(edit editContext, sessionID, patch string, call fantasy.ToolCall) (fantasy.ToolResponse, []string, error) {
	ops, err := parseApplyPatch(patch)
	if err != nil {
		return withErrorKind(fantasy.NewTextErrorResponse(err.Error()), ErrorKindInvalidArguments), nil, nil
	}

	plans, err := planPatch(edit, sessionID, ops)
	if err != nil {
		return editErrorResponse(err), nil, nil
	}

	for _, plan := range plans {
		granted, err := edit.permissions.Request(edit.ctx, permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        fsext.PathOrPrefix(plan.path, edit.workingDir),
			ToolCallID:  call.ID,
			ToolName:    ApplyPatchToolName,
			Action:      "write",
			Description: plan.description(),
			Params: EditPermissionsParams{
				FilePath:   plan.path,
				OldContent: plan.oldContent,
				NewContent: plan.newContent,
			},
		})
		if err != nil {
			return fantasy.ToolResponse{}, nil, err
		}
		if !granted {
			return permissionDeniedWithMetadata(ApplyPatchResponseMetadata{Files: planPaths(plans)}), nil, nil
		}
	}

	meta, written, err := writePatchPlans(edit, sessionID, plans)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil, nil
	}

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(strings.Join(patchSummary(plans), "\n")),
		meta,
	), written, nil
}

// patchPlan is one section of a patch resolved against the working tree: the
// content it starts from, the content it ends with, and where it goes.
type patchPlan struct {
	op         patchOp
	path       string
	moveTo     string
	oldContent string
	newContent string
	isCrlf     bool
}

// description is what the permission prompt says this section does.
func (p patchPlan) description() string {
	switch p.op.kind {
	case patchOpAdd:
		return fmt.Sprintf("Create file %s", p.path)
	case patchOpDelete:
		return fmt.Sprintf("Delete file %s", p.path)
	default:
		if p.moveTo != "" {
			return fmt.Sprintf("Move file %s to %s", p.path, p.moveTo)
		}
		return fmt.Sprintf("Patch file %s", p.path)
	}
}

func planPaths(plans []patchPlan) []string {
	paths := make([]string, 0, len(plans))
	for _, plan := range plans {
		paths = append(paths, plan.path)
	}
	return paths
}

// patchSummary says what the patch did, one line per file.
func patchSummary(plans []patchPlan) []string {
	lines := make([]string, 0, len(plans))
	for _, plan := range plans {
		switch plan.op.kind {
		case patchOpAdd:
			lines = append(lines, "Added "+plan.path)
		case patchOpDelete:
			lines = append(lines, "Deleted "+plan.path)
		default:
			if plan.moveTo != "" {
				lines = append(lines, fmt.Sprintf("Moved %s to %s", plan.path, plan.moveTo))
				continue
			}
			lines = append(lines, "Patched "+plan.path)
		}
	}
	return lines
}

// planPatch resolves every section of a patch without writing anything. The
// read guard in loadExistingFile is what makes a patch as safe as an edit: an
// update, a delete or a move of a file the session never read is refused, and
// so is one that changed on disk since the read.
func planPatch(edit editContext, sessionID string, ops []patchOp) ([]patchPlan, error) {
	plans := make([]patchPlan, 0, len(ops))
	for _, op := range ops {
		path := filepathext.SmartJoin(edit.workingDir, home.Long(op.path))
		plan := patchPlan{op: op, path: path}

		switch op.kind {
		case patchOpAdd:
			if _, err := os.Stat(path); err == nil {
				return nil, fmt.Errorf("file already exists: %s", path)
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to access file: %w", err)
			}
			plan.newContent = patchContent(op.content)
		case patchOpUpdate, patchOpDelete:
			file, resp, err := loadExistingFile(edit, path, "session ID is required for patching a file")
			if err != nil {
				return nil, err
			}
			if resp.Content != "" {
				return nil, &editMatchError{kind: MetadataErrorKind(resp.Metadata), msg: resp.Content}
			}
			plan.oldContent, plan.isCrlf = file.content, file.isCrlf
			if op.kind == patchOpDelete {
				plans = append(plans, plan)
				continue
			}
			newContent, err := applyPatchHunks(path, file.content, op.hunks)
			if err != nil {
				return nil, err
			}
			plan.newContent = newContent

			if op.moveTo != "" {
				plan.moveTo = filepathext.SmartJoin(edit.workingDir, home.Long(op.moveTo))
				if _, err := os.Stat(plan.moveTo); err == nil {
					return nil, fmt.Errorf("cannot move %s to %s: the destination already exists", path, plan.moveTo)
				} else if !os.IsNotExist(err) {
					return nil, fmt.Errorf("failed to access file: %w", err)
				}
			} else if newContent == file.content {
				return nil, fmt.Errorf("patch made no changes to %s", path)
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// writePatchPlans performs the writes a resolved patch asked for. Every plan
// has already passed the guards and the permission prompt.
func writePatchPlans(edit editContext, sessionID string, plans []patchPlan) (ApplyPatchResponseMetadata, []string, error) {
	meta := ApplyPatchResponseMetadata{Files: planPaths(plans)}
	written := make([]string, 0, len(plans))
	for _, plan := range plans {
		switch plan.op.kind {
		case patchOpAdd:
			if err := writeFileWithParents(plan.path, []byte(plan.newContent)); err != nil {
				return meta, written, err
			}
			if _, err := edit.files.Create(edit.ctx, sessionID, plan.path, ""); err != nil {
				return meta, written, fmt.Errorf("failed to record file history: %w", err)
			}
			if _, err := edit.files.CreateVersion(edit.ctx, sessionID, plan.path, plan.newContent); err != nil {
				return meta, written, fmt.Errorf("failed to record file history: %w", err)
			}
			edit.filetracker.RecordRead(edit.ctx, sessionID, plan.path)
			written = append(written, plan.path)
		case patchOpDelete:
			if err := os.Remove(plan.path); err != nil {
				return meta, written, fmt.Errorf("failed to delete file: %w", err)
			}
		case patchOpUpdate:
			writeContent := plan.newContent
			if plan.isCrlf {
				writeContent, _ = fsext.ToWindowsLineEndings(writeContent)
			}
			if plan.moveTo != "" {
				if err := writeFileWithParents(plan.moveTo, []byte(writeContent)); err != nil {
					return meta, written, err
				}
				if err := commitFileChange(edit, sessionID, plan.moveTo, "", writeContent, true); err != nil {
					return meta, written, err
				}
				if err := os.Remove(plan.path); err != nil {
					return meta, written, fmt.Errorf("failed to remove the moved file: %w", err)
				}
				written = append(written, plan.moveTo)
			} else {
				if err := commitFileChange(edit, sessionID, plan.path, plan.oldContent, writeContent, true); err != nil {
					return meta, written, err
				}
				written = append(written, plan.path)
			}
		}

		additions, removals := diffCounts(plan)
		meta.Additions += additions
		meta.Removals += removals
	}
	return meta, written, nil
}

func diffCounts(plan patchPlan) (additions, removals int) {
	_, additions, removals = diff.GenerateDiff(
		plan.oldContent,
		plan.newContent,
		plan.path,
	)
	return additions, removals
}

// patchOp is one section of a parsed patch.
type patchOp struct {
	kind    patchOpKind
	path    string
	moveTo  string
	content []string
	hunks   []patchHunk
}

// patchOpKind is what a patch section does to its file.
type patchOpKind int

const (
	patchOpAdd patchOpKind = iota
	patchOpUpdate
	patchOpDelete
)

// patchHunk is one @@ section of an update: the context and removed lines it
// looks for, and the context and added lines it puts in their place.
type patchHunk []patchLine

// patchLine is one line of a hunk: unchanged context (' '), removed ('-') or
// added ('+').
type patchLine struct {
	kind byte
	text string
}

const (
	patchBeginMarker = "*** Begin Patch"
	patchEndMarker   = "*** End Patch"
	patchAddFile     = "*** Add File: "
	patchUpdateFile  = "*** Update File: "
	patchDeleteFile  = "*** Delete File: "
	patchMoveTo      = "*** Move to: "
	patchHunkMarker  = "@@"
	patchDirective   = "***"
	patchContextKind = ' '
	patchRemoveKind  = '-'
	patchAddLineKind = '+'
	patchHintLines   = 10
)

// PatchFilePaths lists the files a patch touches, in patch order, including a
// move's destination. It reads the section directives only, so the agent can
// follow a patch's paths (changed files, stale reads) without applying it.
func PatchFilePaths(patch string) []string {
	var paths []string
	for line := range strings.SplitSeq(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		for _, prefix := range []string{patchAddFile, patchUpdateFile, patchDeleteFile, patchMoveTo} {
			if path, found := strings.CutPrefix(line, prefix); found {
				if path = strings.TrimSpace(path); path != "" {
					paths = append(paths, path)
				}
			}
		}
	}
	return paths
}

// parseApplyPatch turns a patch into its sections. It accepts what the GPT
// models actually emit: blank lines between sections, a "@@ heading" after the
// hunk marker, and empty lines inside an add as empty content lines.
func parseApplyPatch(patch string) ([]patchOp, error) {
	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")

	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || strings.TrimSpace(lines[i]) != patchBeginMarker {
		return nil, fmt.Errorf("patch must start with %q", patchBeginMarker)
	}
	i++

	var ops []patchOp
	for ; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == patchEndMarker:
			if len(ops) == 0 {
				return nil, fmt.Errorf("patch has no sections")
			}
			return ops, nil
		case strings.TrimSpace(line) == "":
			continue
		case strings.HasPrefix(line, patchAddFile):
			op := patchOp{kind: patchOpAdd, path: strings.TrimSpace(line[len(patchAddFile):])}
			if op.path == "" {
				return nil, fmt.Errorf("an Add File section needs a path")
			}
			i++
			for ; i < len(lines); i++ {
				body := lines[i]
				if strings.HasPrefix(body, patchDirective) {
					i--
					break
				}
				if body == "" {
					op.content = append(op.content, "")
					continue
				}
				if !strings.HasPrefix(body, string(patchAddLineKind)) {
					return nil, fmt.Errorf("invalid Add File line %q: every content line starts with +", body)
				}
				op.content = append(op.content, body[1:])
			}
			op.content = trimEmptyTail(op.content)
			ops = append(ops, op)
		case strings.HasPrefix(line, patchUpdateFile):
			op, next, err := parseUpdateSection(lines, i)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
			i = next - 1
		case strings.HasPrefix(line, patchDeleteFile):
			path := strings.TrimSpace(line[len(patchDeleteFile):])
			if path == "" {
				return nil, fmt.Errorf("a Delete File section needs a path")
			}
			ops = append(ops, patchOp{kind: patchOpDelete, path: path})
		default:
			return nil, fmt.Errorf("unknown patch line %q", line)
		}
	}
	return nil, fmt.Errorf("patch is missing %q", patchEndMarker)
}

// parseUpdateSection reads an update section from its directive line, through
// an optional Move to line and its hunks, and returns the index it stopped at.
func parseUpdateSection(lines []string, start int) (patchOp, int, error) {
	line := lines[start]
	op := patchOp{kind: patchOpUpdate, path: strings.TrimSpace(line[len(patchUpdateFile):])}
	if op.path == "" {
		return op, 0, fmt.Errorf("an Update File section needs a path")
	}

	i := start + 1
	if i < len(lines) && strings.HasPrefix(lines[i], patchMoveTo) {
		op.moveTo = strings.TrimSpace(lines[i][len(patchMoveTo):])
		if op.moveTo == "" {
			return op, 0, fmt.Errorf("a Move to line needs a path")
		}
		i++
	}

	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, patchDirective) {
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, patchHunkMarker) {
			return op, 0, fmt.Errorf("expected %q or a hunk in %s, got %q", "@@", op.path, line)
		}
		var hunk patchHunk
		i++
		for ; i < len(lines); i++ {
			body := lines[i]
			if strings.HasPrefix(body, patchHunkMarker) || strings.HasPrefix(body, patchDirective) {
				i--
				break
			}
			if body == "" {
				// A blank line that only separates two hunks is not a
				// context line, or every such patch would demand a
				// blank line the file does not have.
				if hunkEndsAt(lines, i+1) {
					break
				}
				hunk = append(hunk, patchLine{kind: patchContextKind})
				continue
			}
			switch body[0] {
			case patchContextKind, patchRemoveKind, patchAddLineKind:
				hunk = append(hunk, patchLine{kind: body[0], text: body[1:]})
			default:
				return op, 0, fmt.Errorf("invalid hunk line %q: every line starts with a space, - or +", body)
			}
		}
		op.hunks = append(op.hunks, hunk)
	}

	if len(op.hunks) == 0 && op.moveTo == "" {
		return op, 0, fmt.Errorf("update for %s has no hunks", op.path)
	}
	return op, i, nil
}

// trimEmptyTail drops the blank content lines a model leaves before the next
// directive. An empty line in the middle of an add is real content and stays.
func trimEmptyTail(content []string) []string {
	end := len(content)
	for end > 0 && content[end-1] == "" {
		end--
	}
	return content[:end]
}

// hunkEndsAt reports whether the next non-blank line after a blank one starts
// a new hunk or section, which makes the blank a separator rather than a
// context line.
func hunkEndsAt(lines []string, from int) bool {
	for i := from; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		return strings.HasPrefix(lines[i], patchHunkMarker) || strings.HasPrefix(lines[i], patchDirective)
	}
	return true
}

// patchContent renders the lines of an Add File section.
func patchContent(content []string) string {
	if len(content) == 0 {
		return ""
	}
	return strings.Join(content, "\n") + "\n"
}

// applyPatchHunks rewrites content with every hunk of an update section. Each
// hunk must match the file exactly; a hunk that finds no match fails the
// whole patch with a text-not-found kind, the same kind an edit's miss gets.
func applyPatchHunks(path, content string, hunks []patchHunk) (string, error) {
	lines, endsWithNewline := splitPatchLines(content)
	offset := 0
	for _, hunk := range hunks {
		oldLines, newLines := hunkSides(hunk)
		at, found := findLineSequence(lines, oldLines, offset)
		if !found {
			return "", &editMatchError{
				kind: ErrorKindTextNotFound,
				msg: fmt.Sprintf(
					"patch context not found in %s. Re-read the file and send the patch again with its exact lines.\n\n%s",
					path, hunkHint(hunk),
				),
			}
		}
		lines = spliceLines(lines, at, len(oldLines), newLines)
		offset = at + len(newLines)
	}
	return joinPatchLines(lines, endsWithNewline), nil
}

// hunkSides splits a hunk into the lines it looks for and the lines it writes,
// in order.
func hunkSides(hunk patchHunk) (oldLines, newLines []string) {
	for _, line := range hunk {
		if line.kind != patchAddLineKind {
			oldLines = append(oldLines, line.text)
		}
		if line.kind != patchRemoveKind {
			newLines = append(newLines, line.text)
		}
	}
	return oldLines, newLines
}

// findLineSequence finds needle in lines at or after from. An empty needle
// matches at from, which places a hunk that only adds lines.
func findLineSequence(lines, needle []string, from int) (int, bool) {
	if len(needle) == 0 {
		return min(from, len(lines)), true
	}
	for i := from; i+len(needle) <= len(lines); i++ {
		if slices.Equal(lines[i:i+len(needle)], needle) {
			return i, true
		}
	}
	return 0, false
}

func spliceLines(lines []string, at, remove int, insert []string) []string {
	out := make([]string, 0, len(lines)-remove+len(insert))
	out = append(out, lines[:at]...)
	out = append(out, insert...)
	out = append(out, lines[at+remove:]...)
	return out
}

// splitPatchLines splits normalized (LF) content and remembers whether it
// ended with a newline, so an edit never adds or drops one.
func splitPatchLines(content string) (lines []string, endsWithNewline bool) {
	if content == "" {
		return nil, false
	}
	endsWithNewline = strings.HasSuffix(content, "\n")
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n"), endsWithNewline
}

func joinPatchLines(lines []string, endsWithNewline bool) string {
	joined := strings.Join(lines, "\n")
	if endsWithNewline && joined != "" {
		joined += "\n"
	}
	return joined
}

// hunkHint renders a hunk's lines for the failure message, bounded so a large
// hunk does not fill the result.
func hunkHint(hunk patchHunk) string {
	var sb strings.Builder
	for i, line := range hunk {
		if i == patchHintLines {
			fmt.Fprintf(&sb, "... and %d more lines", len(hunk)-patchHintLines)
			break
		}
		sb.WriteByte(line.kind)
		sb.WriteString(line.text)
		sb.WriteByte('\n')
	}
	return strings.TrimSuffix(sb.String(), "\n")
}
