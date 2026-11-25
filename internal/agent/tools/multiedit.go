package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
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

type MultiEditOperation struct {
	OldString  string `json:"old_string" description:"The text to replace"`
	NewString  string `json:"new_string" description:"The text to replace it with"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences of old_string (default false)."`
}

// multiEditList is the edits array. Some models send it as a JSON string
// holding the array; decoding that too saves a failed call and a retry.
type multiEditList []MultiEditOperation

func (l *multiEditList) UnmarshalJSON(data []byte) error {
	var edits []MultiEditOperation
	if err := json.Unmarshal(data, &edits); err == nil {
		*l = edits
		return nil
	}
	var encoded string
	if err := json.Unmarshal(data, &encoded); err != nil {
		return fmt.Errorf("edits must be an array of {old_string, new_string}: %w", err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(encoded)), &edits); err != nil {
		return fmt.Errorf("edits must be an array of {old_string, new_string}: %w", err)
	}
	*l = edits
	return nil
}

type MultiEditParams struct {
	FilePath string        `json:"file_path" description:"The absolute path to the file to modify"`
	Edits    multiEditList `json:"edits" description:"Array of edit operations to perform sequentially on the file"`
}

type MultiEditPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

type FailedEdit struct {
	Index int                `json:"index"`
	Error string             `json:"error"`
	Kind  ErrorKind          `json:"error_kind,omitempty"`
	Edit  MultiEditOperation `json:"edit"`
}

type MultiEditResponseMetadata struct {
	Additions    int          `json:"additions"`
	Removals     int          `json:"removals"`
	OldContent   string       `json:"old_content,omitempty"`
	NewContent   string       `json:"new_content,omitempty"`
	EditsApplied int          `json:"edits_applied"`
	EditsFailed  []FailedEdit `json:"edits_failed,omitempty"`
	// UnreadEdit marks a batch applied to a file the read guard refused.
	UnreadEdit bool `json:"unread_edit,omitempty"`
}

const MultiEditToolName = "multiedit"

//go:embed multiedit.md
var multieditDescription string

func NewMultiEditTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
	editWithoutRead bool,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		MultiEditToolName,
		multieditDescription,
		func(ctx context.Context, params MultiEditParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			if len(params.Edits) == 0 {
				return fantasy.NewTextErrorResponse("at least one edit operation is required"), nil
			}

			params.FilePath = filepathext.SmartJoin(workingDir, home.Long(params.FilePath))

			// Validate all edits before applying any
			if err := validateEdits(params.Edits); err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			var response fantasy.ToolResponse
			var err error

			editCtx := editContext{
				ctx:         ctx,
				permissions: permissions,
				files:       files,
				filetracker: filetracker,
				workingDir:  workingDir,
				callID:      call.ID,
				withoutRead: editWithoutRead,
			}
			// Handle file creation case (first edit has empty old_string)
			if len(params.Edits) > 0 && params.Edits[0].OldString == "" {
				response, err = processMultiEditWithCreation(editCtx, params, call)
			} else {
				response, err = processMultiEditExistingFile(editCtx, params, call)
			}

			if err != nil {
				return response, err
			}

			if response.IsError {
				return response, nil
			}

			// Notify LSP clients about the change
			notifyLSPs(ctx, lspManager, params.FilePath)

			// Wait for LSP diagnostics and add them to the response
			text := fmt.Sprintf("<result>\n%s\n</result>\n", response.Content)
			text += getActionableDiagnostics(params.FilePath, lspManager)
			response.Content = text
			return response, nil
		},
	)
}

func validateEdits(edits multiEditList) error {
	for i, edit := range edits {
		// Only the first edit can have empty old_string (for file creation)
		if i > 0 && edit.OldString == "" {
			return fmt.Errorf("edit %d: old_string is empty; every edit after the first must quote the exact text it replaces (only edit 1 may be empty, to create the file)", i+1)
		}
	}
	return nil
}

// applyEditsToContent applies edits sequentially, collecting the ones that
// failed. It also reports whether any edit only matched after whitespace
// normalization.
func applyEditsToContent(currentContent string, edits []MultiEditOperation, startIndex int) (string, []FailedEdit, bool) {
	var failedEdits []FailedEdit
	var whitespaceCorrected bool
	for i, edit := range edits {
		newContent, corrected, err := applyEditToContent(currentContent, edit)
		if err != nil {
			failedEdits = append(failedEdits, FailedEdit{
				Index: startIndex + i + 1,
				Error: err.Error(),
				Kind:  errorKindOf(err),
				Edit:  edit,
			})
			continue
		}
		whitespaceCorrected = whitespaceCorrected || corrected
		currentContent = newContent
	}
	return currentContent, failedEdits, whitespaceCorrected
}

// failedEditsText renders the edits of a batch that could not be applied, so
// the model reads which one failed and why. A failure that carries a hint
// keeps it on lines indented under its bullet.
func failedEditsText(failed []FailedEdit) string {
	if len(failed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Failed edits:")
	for _, edit := range failed {
		lines := strings.Split(edit.Error, "\n")
		fmt.Fprintf(&b, "\n- edit %d: %s", edit.Index, lines[0])
		for _, line := range lines[1:] {
			b.WriteString("\n")
			if line != "" {
				b.WriteString("  " + line)
			}
		}
	}
	return b.String()
}

func processMultiEditWithCreation(edit editContext, params MultiEditParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	// First edit creates the file
	firstEdit := params.Edits[0]
	if firstEdit.OldString != "" {
		return fantasy.NewTextErrorResponse("first edit must have empty old_string for file creation"), nil
	}

	// Check if file already exists
	if _, err := os.Stat(params.FilePath); err == nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("file already exists: %s", params.FilePath)), nil
	} else if !os.IsNotExist(err) {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to access file: %s", err)), nil
	}

	currentContent, failedEdits, whitespaceCorrected := applyEditsToContent(firstEdit.NewString, params.Edits[1:], 1)

	// Get session and message IDs
	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for creating a new file")
	}

	// Check permissions
	_, additions, removals := diff.GenerateDiff("", currentContent, strings.TrimPrefix(params.FilePath, edit.workingDir))

	editsApplied := len(params.Edits) - len(failedEdits)
	var description string
	if len(failedEdits) > 0 {
		description = fmt.Sprintf("Create file %s with %d of %d edits (%d failed)", params.FilePath, editsApplied, len(params.Edits), len(failedEdits))
	} else {
		description = fmt.Sprintf("Create file %s with %d edits", params.FilePath, editsApplied)
	}
	p, err := edit.permissions.Request(edit.ctx, permission.CreatePermissionRequest{
		SessionID:   sessionID,
		Path:        fsext.PathOrPrefix(params.FilePath, edit.workingDir),
		ToolCallID:  call.ID,
		ToolName:    MultiEditToolName,
		Action:      "write",
		Description: description,
		Params: MultiEditPermissionsParams{
			FilePath:   params.FilePath,
			OldContent: "",
			NewContent: currentContent,
		},
	})
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !p {
		return permissionDeniedWithMetadata(MultiEditResponseMetadata{
			OldContent:   "",
			NewContent:   currentContent,
			Additions:    additions,
			Removals:     removals,
			EditsApplied: editsApplied,
			EditsFailed:  failedEdits,
		}), nil
	}

	if err = writeFileWithParents(params.FilePath, []byte(currentContent)); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to write file: %s", err)), nil
	}

	// Update file history
	_, err = edit.files.Create(edit.ctx, sessionID, params.FilePath, "")
	if err != nil {
		slog.Error("Error creating file history", "error", err)
	}

	_, err = edit.files.CreateVersion(history.WithToolCallID(edit.ctx, edit.callID), sessionID, params.FilePath, currentContent)
	if err != nil {
		slog.Error("Error creating file history version", "error", err)
	}

	edit.filetracker.RecordRead(edit.ctx, sessionID, params.FilePath)

	var message string
	if len(failedEdits) > 0 {
		message = fmt.Sprintf("File created with %d of %d edits: %s (%d edit(s) failed)", editsApplied, len(params.Edits), params.FilePath, len(failedEdits))
	} else {
		message = fmt.Sprintf("File created with %d edits: %s", len(params.Edits), params.FilePath)
	}
	message = withWhitespaceNote(message, whitespaceCorrected)
	if text := failedEditsText(failedEdits); text != "" {
		message += "\n\n" + text
	}

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(message),
		MultiEditResponseMetadata{
			OldContent:   "",
			NewContent:   currentContent,
			Additions:    additions,
			Removals:     removals,
			EditsApplied: editsApplied,
			EditsFailed:  failedEdits,
		},
	), nil
}

func processMultiEditExistingFile(edit editContext, params MultiEditParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	file, resp, err := loadExistingFile(edit, params.FilePath, "session ID is required for editing a file", firstOldString(params.Edits))
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if resp.Content != "" || resp.IsError {
		return resp, nil
	}
	sessionID, oldContent, isCrlf := file.sessionID, file.content, file.isCrlf

	currentContent, failedEdits, whitespaceCorrected := applyEditsToContent(oldContent, params.Edits, 0)

	// A batch applies to a file the guard refuses only when every edit is
	// an exact, single match as the content evolves there. One edit that
	// fails, needs the whitespace fallback or asks for replace_all refuses
	// the whole call, with the guard's own refusal.
	var degraded, refused bool
	if file.guard.refused() {
		replaceAll := false
		for _, op := range params.Edits {
			replaceAll = replaceAll || op.ReplaceAll
		}
		degraded, refused = edit.mayEditUnread(file.guard, replaceAll, whitespaceCorrected || len(failedEdits) > 0)
		if refused {
			return refusedBatch(edit, file.guard, params.FilePath, firstOldString(params.Edits), failedEdits), nil
		}
	}

	// Check if content actually changed
	if oldContent == currentContent {
		// If we have failed edits, report them
		if len(failedEdits) > 0 {
			message := fmt.Sprintf("no changes made - all %d edit(s) failed", len(failedEdits))
			if text := failedEditsText(failedEdits); text != "" {
				message += "\n\n" + text
			}
			resp := fantasy.WithResponseMetadata(
				fantasy.NewTextErrorResponse(message),
				MultiEditResponseMetadata{
					EditsApplied: 0,
					EditsFailed:  failedEdits,
				},
			)
			// The call failed because its edits did, so it takes the first
			// failed edit's kind. It is set after the metadata, which would
			// otherwise replace it.
			if kind := failedEdits[0].Kind; kind != "" {
				resp = withErrorKind(resp, kind)
			}
			return resp, nil
		}
		return fantasy.NewTextErrorResponse("no changes made - all edits resulted in identical content"), nil
	}

	// Generate diff and check permissions
	_, additions, removals := diff.GenerateDiff(oldContent, currentContent, strings.TrimPrefix(params.FilePath, edit.workingDir))

	editsApplied := len(params.Edits) - len(failedEdits)
	var description string
	if len(failedEdits) > 0 {
		description = fmt.Sprintf("Apply %d of %d edits to file %s (%d failed)", editsApplied, len(params.Edits), params.FilePath, len(failedEdits))
	} else {
		description = fmt.Sprintf("Apply %d edits to file %s", editsApplied, params.FilePath)
	}
	p, err := edit.permissions.Request(edit.ctx, permission.CreatePermissionRequest{
		SessionID:   sessionID,
		Path:        fsext.PathOrPrefix(params.FilePath, edit.workingDir),
		ToolCallID:  call.ID,
		ToolName:    MultiEditToolName,
		Action:      "write",
		Description: description,
		Params: MultiEditPermissionsParams{
			FilePath:   params.FilePath,
			OldContent: oldContent,
			NewContent: currentContent,
		},
	})
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !p {
		return permissionDeniedWithMetadata(MultiEditResponseMetadata{
			OldContent:   oldContent,
			NewContent:   currentContent,
			Additions:    additions,
			Removals:     removals,
			EditsApplied: editsApplied,
			EditsFailed:  failedEdits,
		}), nil
	}

	writeContent := currentContent
	if isCrlf {
		writeContent, _ = fsext.ToWindowsLineEndings(writeContent)
	}

	if err := commitFileChange(edit, sessionID, params.FilePath, oldContent, writeContent, !degraded); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to write file: %s", err)), nil
	}

	var message string
	if len(failedEdits) > 0 {
		message = fmt.Sprintf("Applied %d of %d edits to file: %s (%d edit(s) failed)", editsApplied, len(params.Edits), params.FilePath, len(failedEdits))
	} else {
		message = fmt.Sprintf("Applied %d edits to file: %s", len(params.Edits), params.FilePath)
	}
	message = withWhitespaceNote(message, whitespaceCorrected)
	if degraded {
		message += "\n" + unreadEditNote
	}
	if text := failedEditsText(failedEdits); text != "" {
		message += "\n\n" + text
	}

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(message),
		MultiEditResponseMetadata{
			OldContent:   oldContent,
			NewContent:   currentContent,
			Additions:    additions,
			Removals:     removals,
			EditsApplied: editsApplied,
			EditsFailed:  failedEdits,
			UnreadEdit:   degraded,
		},
	), nil
}

// refusedBatch is the refusal of a batch the read guard keeps: the guard's
// sentence, and the edits of the batch that failed, so the model learns
// which one did not match. When a match failure is why the batch was
// refused, the refusal takes that failure's kind.
func refusedBatch(edit editContext, guard readGuard, filePath, oldString string, failed []FailedEdit) fantasy.ToolResponse {
	resp := edit.refuseWithContent(guard, filePath, oldString, true)
	if text := failedEditsText(failed); text != "" {
		resp.Content += "\n\n" + text
	}
	for _, edit := range failed {
		if edit.Kind == ErrorKindTextNotFound || edit.Kind == ErrorKindMultipleMatches {
			return withErrorKind(resp, edit.Kind)
		}
	}
	return resp
}

// firstOldString is the text the batch's first real edit replaces, which is
// what a refusal centres its window on.
func firstOldString(edits multiEditList) string {
	for _, edit := range edits {
		if edit.OldString != "" {
			return edit.OldString
		}
	}
	return ""
}

// applyEditToContent applies a single edit, reporting whether it only matched
// after whitespace normalization.
func applyEditToContent(content string, edit MultiEditOperation) (string, bool, error) {
	if edit.OldString == "" && edit.NewString == "" {
		return content, false, nil
	}

	if edit.OldString == "" {
		return "", false, fmt.Errorf("old_string cannot be empty for content replacement")
	}

	return findAndReplace(content, edit.OldString, edit.NewString, edit.ReplaceAll)
}
