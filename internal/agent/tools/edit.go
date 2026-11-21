package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

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

type EditParams struct {
	FilePath   string `json:"file_path" description:"The absolute path to the file to modify"`
	OldString  string `json:"old_string" description:"The text to replace"`
	NewString  string `json:"new_string" description:"The text to replace it with"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences of old_string (default false)"`
}

type EditPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

type EditResponseMetadata struct {
	Additions  int    `json:"additions"`
	Removals   int    `json:"removals"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
	// UnreadEdit marks an edit applied to a file the read guard refused.
	UnreadEdit bool `json:"unread_edit,omitempty"`
}

const EditToolName = "edit"

//go:embed edit.md
var editDescription string

type editContext struct {
	ctx         context.Context
	permissions permission.Service
	files       history.Service
	filetracker filetracker.Service
	workingDir  string
	// withoutRead is the edit_without_read option: an exact, unique match
	// applies to a file the read guard refuses.
	withoutRead bool
}

func NewEditTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
	editWithoutRead bool,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		EditToolName,
		editDescription,
		func(ctx context.Context, params EditParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			params.FilePath = filepathext.SmartJoin(workingDir, home.Long(params.FilePath))

			var response fantasy.ToolResponse
			var err error

			editCtx := editContext{
				ctx:         ctx,
				permissions: permissions,
				files:       files,
				filetracker: filetracker,
				workingDir:  workingDir,
				withoutRead: editWithoutRead,
			}

			if params.OldString == "" {
				response, err = createNewFile(editCtx, params.FilePath, params.NewString, call)
			} else if params.NewString == "" {
				response, err = deleteContent(editCtx, params.FilePath, params.OldString, params.ReplaceAll, call)
			} else {
				response, err = replaceContent(editCtx, params.FilePath, params.OldString, params.NewString, params.ReplaceAll, call)
			}

			if err != nil {
				return response, err
			}
			if response.IsError {
				// Return early if there was an error during content replacement
				// This prevents unnecessary LSP diagnostics processing
				return response, nil
			}

			notifyLSPs(ctx, lspManager, params.FilePath)

			text := fmt.Sprintf("<result>\n%s\n</result>\n", response.Content)
			text += getActionableDiagnostics(params.FilePath, lspManager)
			response.Content = text
			return response, nil
		},
	)
}

func createNewFile(edit editContext, filePath, content string, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	fileInfo, err := os.Stat(filePath)
	if err == nil {
		if fileInfo.IsDir() {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("file already exists: %s", filePath)), nil
	} else if !os.IsNotExist(err) {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to access file: %s", err)), nil
	}

	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for creating a new file")
	}

	_, additions, removals := diff.GenerateDiff(
		"",
		content,
		strings.TrimPrefix(filePath, edit.workingDir),
	)
	p, err := edit.permissions.Request(
		edit.ctx,
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        fsext.PathOrPrefix(filePath, edit.workingDir),
			ToolCallID:  call.ID,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Create file %s", filePath),
			Params: EditPermissionsParams{
				FilePath:   filePath,
				OldContent: "",
				NewContent: content,
			},
		},
	)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !p {
		return permissionDeniedWithMetadata(EditResponseMetadata{
			OldContent: "",
			NewContent: content,
			Additions:  additions,
			Removals:   removals,
		}), nil
	}

	if err = writeFileWithParents(filePath, []byte(content)); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to write file: %s", err)), nil
	}

	// File can't be in the history so we create a new file history
	_, err = edit.files.Create(edit.ctx, sessionID, filePath, "")
	if err != nil {
		slog.Error("Error creating file history", "error", err)
	}

	// Add the new content to the file history
	_, err = edit.files.CreateVersion(edit.ctx, sessionID, filePath, content)
	if err != nil {
		// Log error but don't fail the operation
		slog.Error("Error creating file history version", "error", err)
	}

	edit.filetracker.RecordRead(edit.ctx, sessionID, filePath)

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse("File created: "+filePath),
		EditResponseMetadata{
			OldContent: "",
			NewContent: content,
			Additions:  additions,
			Removals:   removals,
		},
	), nil
}

// findAndReplace performs a find-and-replace on content. When replaceAll is
// false it requires exactly one match. If an exact match fails, it falls back
// to whitespace-normalized matching and, failing that, returns a diagnostic
// hint describing why the replacement could not be made. The returned boolean
// reports whether the replacement relied on the whitespace-normalized
// fallback rather than an exact match.
func findAndReplace(content, old, new string, replaceAll bool) (string, bool, error) {
	if replaceAll {
		if strings.Contains(content, old) {
			return strings.ReplaceAll(content, old, new), false, nil
		}
	} else {
		index := strings.Index(content, old)
		switch {
		case index == -1:
			// Fall through to the fuzzy fallback below.
		case index != strings.LastIndex(content, old):
			return "", false, &editMatchError{kind: ErrorKindMultipleMatches, msg: multipleMatchesMessage(content, old)}
		default:
			return content[:index] + new + content[index+len(old):], false, nil
		}
	}

	if result, ok := normalizedReplace(content, old, new, replaceAll); ok {
		return result, true, nil
	}
	return "", false, notFoundError(content, old)
}

// maxListedMatches bounds how many match lines an error message names.
const maxListedMatches = 10

// multipleMatchesMessage reports the "several matches" failure along with
// the number of matches and the lines they start on, so the model can add
// the context it needs on its next call instead of guessing again.
func multipleMatchesMessage(content, old string) string {
	msg := "old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true"
	if old == "" {
		return msg
	}
	var lines []string
	count := 0
	for offset := 0; offset <= len(content); {
		index := strings.Index(content[offset:], old)
		if index == -1 {
			break
		}
		index += offset
		count++
		if len(lines) < maxListedMatches {
			lines = append(lines, strconv.Itoa(1+strings.Count(content[:index], "\n")))
		}
		offset = index + len(old)
	}
	if count < 2 {
		return msg
	}
	// The two sentences read as one paragraph, so the first has to end
	// before the count starts.
	sep := " "
	if !strings.HasSuffix(msg, ".") {
		sep = ". "
	}
	if count > len(lines) {
		return fmt.Sprintf("%s%sIt matches %d times, the first %d on lines %s.", msg, sep, count, len(lines), humanList(lines))
	}
	return fmt.Sprintf("%s%sIt matches %d times, starting on lines %s.", msg, sep, count, humanList(lines))
}

// humanList formats a short list the way the error messages read, as in
// "12, 40 and 77".
func humanList(items []string) string {
	if len(items) <= 2 {
		return strings.Join(items, " and ")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// withWhitespaceNote appends the whitespace auto-correction note to a tool
// response message when the edit did not match the file byte-for-byte.
func withWhitespaceNote(message string, whitespaceCorrected bool) string {
	if !whitespaceCorrected {
		return message
	}
	return message + "\n" + whitespaceCorrectedNote
}

// notFoundError builds the "old_string not found" error, appending a
// diagnostic hint when one is available to help the caller self-correct.
func notFoundError(content, old string) error {
	msg := "old_string not found in file. Make sure it matches exactly, including whitespace and line breaks"
	if hint := diagnoseMismatch(content, old); hint != "" {
		msg += "\n\n" + hint
	}
	return &editMatchError{kind: ErrorKindTextNotFound, msg: msg}
}

// editMatchError is a find-and-replace that could not be applied. The
// message is what the model reads; the kind is what the session log
// records, so a report never has to match the wording.
type editMatchError struct {
	kind ErrorKind
	msg  string
}

func (e *editMatchError) Error() string { return e.msg }

// errorKindOf returns the kind of a failed edit, or "" for a failure the
// session log does not classify.
func errorKindOf(err error) ErrorKind {
	if matchErr, ok := errors.AsType[*editMatchError](err); ok {
		return matchErr.kind
	}
	return ""
}

// editErrorResponse turns a failed edit into its response, recording the
// failure's kind when it has one.
func editErrorResponse(err error) fantasy.ToolResponse {
	resp := fantasy.NewTextErrorResponse(err.Error())
	if kind := errorKindOf(err); kind != "" {
		resp = withErrorKind(resp, kind)
	}
	return resp
}

// commitFileChange writes newContent to filePath, updates the file history,
// and records the read in the file tracker. It skips the read record when
// recordRead is false, which is what an edit to a file not read first does:
// the model never read the file and the record must not claim it did.
// Callers must convert line endings before calling this function.
func commitFileChange(edit editContext, sessionID, filePath, oldContent, newContent string, recordRead bool) error {
	if err := os.WriteFile(filePath, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	file, err := edit.files.GetByPathAndSession(edit.ctx, filePath, sessionID)
	if err != nil {
		_, err = edit.files.Create(edit.ctx, sessionID, filePath, oldContent)
		if err != nil {
			slog.Error("Error creating file history", "error", err)
		}
	}
	if file.Content != oldContent {
		// User manually changed the content; store an intermediate version.
		if _, err := edit.files.CreateVersion(edit.ctx, sessionID, filePath, oldContent); err != nil {
			slog.Error("Error creating file history version", "error", err)
		}
	}
	if _, err := edit.files.CreateVersion(edit.ctx, sessionID, filePath, newContent); err != nil {
		slog.Error("Error creating file history version", "error", err)
	}

	if recordRead {
		edit.filetracker.RecordRead(edit.ctx, sessionID, filePath)
	}
	return nil
}

// existingFile is a file an edit tool opened: its content, and what the read
// guard says about reading it first.
type existingFile struct {
	sessionID string
	// content has Unix line endings.
	content string
	isCrlf  bool
	guard   readGuard
}

// loadExistingFile opens a file for an edit. With the option off, a file the
// read guard refuses is refused here, before the content is read, exactly as
// it was before the option existed. With the option on, the content comes
// back with the guard, and the caller decides whether an exact, unique match
// lets the edit apply.
func loadExistingFile(edit editContext, filePath, sessionError string) (existingFile, fantasy.ToolResponse, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return existingFile{}, fantasy.NewTextErrorResponse(fmt.Sprintf("file not found: %s", filePath)), nil
		}
		return existingFile{}, fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to access file: %s", err)), nil
	}

	if fileInfo.IsDir() {
		return existingFile{}, fantasy.NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
	}

	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return existingFile{}, fantasy.ToolResponse{}, fmt.Errorf("%s", sessionError)
	}

	var guard readGuard
	lastRead := edit.filetracker.LastReadTime(edit.ctx, sessionID, filePath)
	modTime := fileInfo.ModTime().Truncate(time.Second)
	switch {
	case lastRead.IsZero():
		guard = readGuard{kind: ErrorKindNotRead, text: "you must read the file before editing it. Use the View tool first"}
	case modTime.After(lastRead):
		guard = readGuard{kind: ErrorKindChangedSinceRead, text: fmt.Sprintf(
			"file %s has been modified since it was last read (mod time: %s, last read: %s)",
			filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339),
		)}
	}

	if guard.refused() && !editWithoutReadEnabled(edit.withoutRead) {
		return existingFile{}, guard.refusal(false), nil
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return existingFile{}, fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to read file: %s", err)), nil
	}

	file := existingFile{sessionID: sessionID, guard: guard}
	file.content, file.isCrlf = fsext.ToUnixLineEndings(string(content))
	return file, fantasy.ToolResponse{}, nil
}

func deleteContent(edit editContext, filePath, oldString string, replaceAll bool, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	file, resp, err := loadExistingFile(edit, filePath, "session ID is required for deleting content")
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if resp.Content != "" || resp.IsError {
		return resp, nil
	}
	sessionID, oldContent, isCrlf := file.sessionID, file.content, file.isCrlf

	newContent, whitespaceCorrected, err := findAndReplace(oldContent, oldString, "", replaceAll)
	if err != nil {
		return editErrorResponse(err), nil
	}
	degraded, refused := edit.mayEditUnread(file.guard, replaceAll, whitespaceCorrected)
	if refused {
		return file.guard.refusal(true), nil
	}

	_, additions, removals := diff.GenerateDiff(
		oldContent,
		newContent,
		strings.TrimPrefix(filePath, edit.workingDir),
	)

	p, err := edit.permissions.Request(
		edit.ctx,
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        fsext.PathOrPrefix(filePath, edit.workingDir),
			ToolCallID:  call.ID,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Delete content from file %s", filePath),
			Params: EditPermissionsParams{
				FilePath:   filePath,
				OldContent: oldContent,
				NewContent: newContent,
			},
		},
	)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !p {
		return permissionDeniedWithMetadata(EditResponseMetadata{
			OldContent: oldContent,
			NewContent: newContent,
			Additions:  additions,
			Removals:   removals,
		}), nil
	}

	writeContent := newContent
	if isCrlf {
		writeContent, _ = fsext.ToWindowsLineEndings(writeContent)
	}

	if err := commitFileChange(edit, sessionID, filePath, oldContent, writeContent, !degraded); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to write file: %s", err)), nil
	}

	message := withWhitespaceNote("Content deleted from file: "+filePath, whitespaceCorrected)
	if degraded {
		message += "\n" + unreadEditNote
	}
	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(message),
		EditResponseMetadata{
			OldContent: oldContent,
			NewContent: writeContent,
			Additions:  additions,
			Removals:   removals,
			UnreadEdit: degraded,
		},
	), nil
}

func replaceContent(edit editContext, filePath, oldString, newString string, replaceAll bool, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	file, resp, err := loadExistingFile(edit, filePath, "session ID is required for editing a file")
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if resp.Content != "" || resp.IsError {
		return resp, nil
	}
	sessionID, oldContent, isCrlf := file.sessionID, file.content, file.isCrlf

	result, whitespaceCorrected, err := findAndReplace(oldContent, oldString, newString, replaceAll)
	if err != nil {
		return editErrorResponse(err), nil
	}
	degraded, refused := edit.mayEditUnread(file.guard, replaceAll, whitespaceCorrected)
	if refused {
		return file.guard.refusal(true), nil
	}
	if result == oldContent {
		return fantasy.NewTextErrorResponse("new content is the same as old content. No changes made."), nil
	}

	_, additions, removals := diff.GenerateDiff(
		oldContent,
		result,
		strings.TrimPrefix(filePath, edit.workingDir),
	)

	p, err := edit.permissions.Request(
		edit.ctx,
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        fsext.PathOrPrefix(filePath, edit.workingDir),
			ToolCallID:  call.ID,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Replace content in file %s", filePath),
			Params: EditPermissionsParams{
				FilePath:   filePath,
				OldContent: oldContent,
				NewContent: result,
			},
		},
	)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !p {
		return permissionDeniedWithMetadata(EditResponseMetadata{
			OldContent: oldContent,
			NewContent: result,
			Additions:  additions,
			Removals:   removals,
		}), nil
	}

	writeContent := result
	if isCrlf {
		writeContent, _ = fsext.ToWindowsLineEndings(writeContent)
	}

	if err := commitFileChange(edit, sessionID, filePath, oldContent, writeContent, !degraded); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to write file: %s", err)), nil
	}

	message := withWhitespaceNote("Content replaced in file: "+filePath, whitespaceCorrected)
	if degraded {
		message += "\n" + unreadEditNote
	}
	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(message),
		EditResponseMetadata{
			OldContent: oldContent,
			NewContent: writeContent,
			Additions:  additions,
			Removals:   removals,
			UnreadEdit: degraded,
		},
	), nil
}
