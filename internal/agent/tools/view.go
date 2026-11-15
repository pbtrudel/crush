package tools

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/skills"
)

//go:embed view.md.tpl
var viewDescriptionTmpl []byte

var viewDescriptionTpl = template.Must(
	template.New("viewDescription").
		Parse(string(viewDescriptionTmpl)),
)

type viewDescriptionData struct {
	DefaultReadLimit int
	MaxViewSizeKB    int
}

func viewDescription() string {
	return renderTemplate(viewDescriptionTpl, viewDescriptionData{
		DefaultReadLimit: DefaultReadLimit,
		MaxViewSizeKB:    MaxViewSize / 1024,
	})
}

type ViewParams struct {
	FilePath  string   `json:"file_path,omitempty" description:"The path to the file to read"`
	Offset    int      `json:"offset,omitempty" description:"The line number to start reading from (0-based)"`
	Limit     int      `json:"limit,omitempty" description:"The number of lines to read (defaults to 200)"`
	FilePaths []string `json:"file_paths,omitempty" description:"Several files to read in one call (at most 10), each from its first line up to limit lines. Use instead of file_path when you need more than one file."`
}

type ViewPermissionsParams struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

type ViewResourceType string

const (
	ViewResourceUnset ViewResourceType = ""
	ViewResourceSkill ViewResourceType = "skill"
)

type ViewResponseMetadata struct {
	FilePath            string           `json:"file_path"`
	Content             string           `json:"content"`
	ResourceType        ViewResourceType `json:"resource_type,omitempty"`
	ResourceName        string           `json:"resource_name,omitempty"`
	ResourceDescription string           `json:"resource_description,omitempty"`
}

const (
	ViewToolName     = "view"
	ReadToolName     = "read"
	MaxViewSize      = 200 * 1024 // 200KB
	DefaultReadLimit = 200
	MaxLineLength    = 2000

	// maxViewFilePaths bounds one multi-file call: a model that cannot issue
	// parallel calls would otherwise trade one huge request for another.
	maxViewFilePaths = 10
	// maxMultiViewChars bounds the combined text of a multi-file call. It
	// stays under the per-result token budget, so a call that respects this
	// limit is never cut a second time by the backstop.
	maxMultiViewChars = 40_000
)

type contentTooLargeError struct {
	Size int
	Max  int
}

func (e contentTooLargeError) Error() string {
	return fmt.Sprintf("content section is too large (%d bytes). Maximum size is %d bytes", e.Size, e.Max)
}

// NewViewTool returns the view tool, which reads a file by path.
func NewViewTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	filetracker filetracker.Service,
	skillTracker *skills.Tracker,
	workingDir string,
	skillsPaths ...string,
) fantasy.AgentTool {
	return newViewLikeTool(ViewToolName, lspManager, permissions, filetracker, skillTracker, workingDir, skillsPaths...)
}

// NewReadTool returns the read alias of the view tool. Models occasionally
// reach for "read" instead of Crush's "view"; registering the alias keeps
// those calls working instead of failing as an unknown tool.
func NewReadTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	filetracker filetracker.Service,
	skillTracker *skills.Tracker,
	workingDir string,
	skillsPaths ...string,
) fantasy.AgentTool {
	return newViewLikeTool(ReadToolName, lspManager, permissions, filetracker, skillTracker, workingDir, skillsPaths...)
}

// viewReader holds everything a file read needs: the tool name for permission
// requests, the LSP manager for diagnostics, and the workspace root that
// decides whether a path lies outside it.
type viewReader struct {
	name         string
	lspManager   *lsp.Manager
	permissions  permission.Service
	filetracker  filetracker.Service
	skillTracker *skills.Tracker
	workingDir   string
	skillsPaths  []string
}

// newViewLikeTool builds the file-reading tool under name. View and its read
// alias share one implementation; the name only changes the advertised tool
// and the ToolName reported on permission requests.
func newViewLikeTool(
	name string,
	lspManager *lsp.Manager,
	permissions permission.Service,
	filetracker filetracker.Service,
	skillTracker *skills.Tracker,
	workingDir string,
	skillsPaths ...string,
) fantasy.AgentTool {
	view := viewReader{
		name:         name,
		lspManager:   lspManager,
		permissions:  permissions,
		filetracker:  filetracker,
		skillTracker: skillTracker,
		workingDir:   workingDir,
		skillsPaths:  skillsPaths,
	}
	return fantasy.NewAgentTool(
		name,
		viewDescription(),
		func(ctx context.Context, params ViewParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if len(params.FilePaths) > 0 {
				return view.readFiles(ctx, params, call)
			}
			return view.readFile(ctx, params, call)
		},
	)
}

// checkReadAccess resolves a file path and confirms it may be read, asking
// for permission when it lies outside the working directory. Every
// filesystem touch in this package goes through it first, including the
// sizing pass a multi-file call does before reading.
func (v viewReader) checkReadAccess(ctx context.Context, params ViewParams, call fantasy.ToolCall) (string, bool, fantasy.ToolResponse, error) {
	filePath := filepath.Clean(filepathext.SmartJoin(v.workingDir, home.Long(params.FilePath)))

	absWorkingDir, err := filepath.Abs(v.workingDir)
	if err != nil {
		return "", false, fantasy.ToolResponse{}, fmt.Errorf("error resolving working directory: %w", err)
	}
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return "", false, fantasy.ToolResponse{}, fmt.Errorf("error resolving file path: %w", err)
	}

	relPath, err := filepath.Rel(absWorkingDir, absFilePath)
	isOutsideWorkDir := err != nil || strings.HasPrefix(relPath, "..")
	isSkillFile := isInSkillsPath(absFilePath, v.skillsPaths)

	sessionID := GetSessionFromContext(ctx)
	if sessionID == "" {
		return "", false, fantasy.ToolResponse{}, fmt.Errorf("session ID is required for accessing files outside working directory")
	}

	if isOutsideWorkDir && !isSkillFile {
		granted, permReqErr := v.permissions.Request(
			ctx,
			permission.CreatePermissionRequest{
				SessionID:   sessionID,
				Path:        absFilePath,
				ToolCallID:  call.ID,
				ToolName:    v.name,
				Action:      "read",
				Description: fmt.Sprintf("Read file outside working directory: %s", absFilePath),
				Params: ViewPermissionsParams{
					FilePath: params.FilePath,
					Offset:   params.Offset,
					Limit:    params.Limit,
				},
			},
		)
		if permReqErr != nil {
			return "", false, fantasy.ToolResponse{}, permReqErr
		}
		if !granted {
			return "", false, NewPermissionDeniedResponse(), nil
		}
	}
	return absFilePath, true, fantasy.ToolResponse{}, nil
}

// readFile reads one file exactly as a single-file call always has:
// permission outside the workspace, file tracking, diagnostics and the
// response metadata that describes the file.
func (v viewReader) readFile(ctx context.Context, params ViewParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if params.FilePath == "" {
		return fantasy.NewTextErrorResponse("file_path is required"), nil
	}

	// Handle builtin skill files (crush: prefix).
	if strings.HasPrefix(params.FilePath, skills.BuiltinPrefix) {
		return readBuiltinFile(params, v.skillTracker)
	}

	// Handle relative paths
	filePath, allowed, resp, err := v.checkReadAccess(ctx, params, call)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !allowed {
		return resp, nil
	}
	return v.readFileAuthorized(ctx, params, call, filePath)
}

// readFileAuthorized does the read after checkReadAccess has granted it, so a
// multi-file call asks for permission once per file, not twice.
func (v viewReader) readFileAuthorized(ctx context.Context, params ViewParams, call fantasy.ToolCall, filePath string) (fantasy.ToolResponse, error) {
	isSkillFile := isInSkillsPath(filePath, v.skillsPaths)
	sessionID := GetSessionFromContext(ctx)

	// Check if file exists
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Try to offer suggestions for similarly named files
			dir := filepath.Dir(filePath)
			base := filepath.Base(filePath)

			dirEntries, dirErr := os.ReadDir(dir)
			if dirErr == nil {
				var suggestions []string
				for _, entry := range dirEntries {
					if strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(base)) ||
						strings.Contains(strings.ToLower(base), strings.ToLower(entry.Name())) {
						suggestions = append(suggestions, filepath.Join(dir, entry.Name()))
						if len(suggestions) >= 3 {
							break
						}
					}
				}

				if len(suggestions) > 0 {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("File not found: %s\n\nDid you mean one of these?\n%s",
						filePath, strings.Join(suggestions, "\n"))), nil
				}
			}

			return fantasy.NewTextErrorResponse(fmt.Sprintf("File not found: %s", filePath)), nil
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to access file: %s", err)), nil
	}

	// Check if it's a directory
	if fileInfo.IsDir() {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Path is a directory, not a file: %s", filePath)), nil
	}

	// Set default limit if not provided (no limit for SKILL.md files)
	if params.Limit <= 0 {
		if isSkillFile {
			params.Limit = 1000000 // Effectively no limit for skill files
		} else {
			params.Limit = DefaultReadLimit
		}
	}

	isSupportedImage, mimeType := getImageMimeType(filePath)
	if isSupportedImage {
		if fileInfo.Size() > MaxViewSize {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("Image file is too large (%d bytes). Maximum size is %d bytes",
				fileInfo.Size(), MaxViewSize)), nil
		}
		if !GetSupportsImagesFromContext(ctx) {
			return UnsupportedMediaResponse(GetModelNameFromContext(ctx)), nil
		}

		imageData, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to read image file: %s", readErr)), nil
		}

		// Some tools save files with a mismatched extension
		// (e.g. pinchtab writes JPEG bytes to a .png file).
		// Providers like Anthropic strictly validate the
		// media type against the base64 magic bytes and 400
		// on mismatch, so prefer the sniffed type whenever
		// it identifies a supported image format.
		mimeType = sniffImageMimeType(imageData, mimeType)

		return fantasy.NewImageResponse(imageData, mimeType), nil
	}

	// Read the file content
	maxContentSize := MaxViewSize
	if isSkillFile {
		maxContentSize = 0
	}
	content, hasMore, err := readTextFile(filePath, params.Offset, params.Limit, maxContentSize)
	if err != nil {
		var tooLarge contentTooLargeError
		if errors.As(err, &tooLarge) {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("Content section is too large (%d bytes). Maximum size is %d bytes",
				tooLarge.Size, tooLarge.Max)), nil
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to read file: %s", err)), nil
	}
	if !utf8.ValidString(content) {
		return fantasy.NewTextErrorResponse("File content is not valid UTF-8"), nil
	}

	waitForLSPDiagnostics(ctx, openInLSPs(ctx, v.lspManager, filePath), 300*time.Millisecond)
	output := "<file>\n"
	output += addLineNumbers(content, params.Offset+1)

	if hasMore {
		output += fmt.Sprintf("\n\n(File has more lines. Use 'offset' parameter to read beyond line %d)",
			params.Offset+len(strings.Split(content, "\n")))
	}
	output += "\n</file>\n"
	output += getActionableDiagnostics(filePath, v.lspManager)
	v.filetracker.RecordRead(ctx, sessionID, filePath)

	meta := ViewResponseMetadata{
		FilePath: filePath,
		Content:  content,
	}
	if isSkillFile {
		if skill, err := skills.Parse(filePath); err == nil {
			meta.ResourceType = ViewResourceSkill
			meta.ResourceName = skill.Name
			meta.ResourceDescription = skill.Description
			v.skillTracker.MarkLoaded(skill.Name)
		}
	}

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(output),
		meta,
	), nil
}

// readFiles answers one call that asked for several files. Each file is read
// exactly as a call of its own would read it, so permissions, file tracking
// and diagnostics behave the same, and one unreadable file only costs its own
// block. The result carries no metadata: it describes no single file.
func (v viewReader) readFiles(ctx context.Context, params ViewParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if params.FilePath != "" {
		return fantasy.NewTextErrorResponse(
			"file_paths cannot be combined with file_path; pass one file in file_path or several in file_paths",
		), nil
	}
	if params.Offset != 0 {
		return fantasy.NewTextErrorResponse(
			"file_paths cannot be combined with offset; every file starts at its first line",
		), nil
	}
	if len(params.FilePaths) > maxViewFilePaths {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"file_paths reads at most %d files in one call, got %d",
			maxViewFilePaths, len(params.FilePaths),
		)), nil
	}

	var (
		output    strings.Builder
		notRead   []string
		imageData []byte
		imageType string
	)
	for i, path := range params.FilePaths {
		one := params
		one.FilePath = path
		one.FilePaths = nil

		// Permission comes before any filesystem touch, sizing included.
		absPath, allowed, denied, err := v.checkReadAccess(ctx, one, call)
		if err != nil {
			return denied, err
		}
		if !allowed {
			block := fmt.Sprintf("==> %s <==\n%s", path, denied.Content)
			if output.Len() > 0 {
				output.WriteString("\n\n")
			}
			output.WriteString(block)
			continue
		}

		// Size the file before reading it: a file that will not be shown must
		// not load an image or be recorded as read.
		if i > 0 && v.skipsForBudget(output.Len(), absPath) {
			notRead = params.FilePaths[i:]
			break
		}

		resp, err := v.readFileAuthorized(ctx, one, call, absPath)
		if err != nil {
			return resp, err
		}

		text := resp.Content
		block := fmt.Sprintf("==> %s <==\n%s", path, text)
		// A file that grew between the sizing and the read can still land
		// here; the block is what the budget is measured on, so it wins.
		if i > 0 && output.Len()+len("\n\n")+len(block) > maxMultiViewChars {
			notRead = params.FilePaths[i:]
			break
		}
		if len(resp.Data) > 0 {
			if len(imageData) == 0 {
				// One media payload fits in a tool result, so the first
				// image stays attached, and only because its block is
				// actually included in the combined text.
				imageData, imageType = resp.Data, resp.MediaType
				if text == "" {
					block = fmt.Sprintf("==> %s <==\n(image: %s)", path, resp.MediaType)
				}
			} else {
				block = fmt.Sprintf("==> %s <==\n(image: view this file on its own)", path)
			}
		}
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(block)
	}

	if len(notRead) > 0 {
		fmt.Fprintf(&output, "\n\nNot read, output budget reached: %s. Read them in another call.",
			strings.Join(notRead, ", "))
	}

	response := fantasy.NewTextResponse(output.String())
	if len(imageData) > 0 {
		response = fantasy.NewImageResponse(imageData, imageType)
		response.Content = output.String()
	}
	return response, nil
}

// skipsForBudget reports whether reading path would push the combined text
// past the multi-file budget. It estimates the rendered block (only the first
// DefaultReadLimit lines come back, with line numbers on top), not the file's
// raw size, so a long file that returns a small section is not skipped by
// mistake and a short-line file is not read just to be dropped. Sizing reads
// at most a small prefix: no permission request, no image load, no record
// that the file was read. A path that cannot be sized, such as a builtin
// skill file, is read: losing a file to a failed stat would cost more than
// one oversized block.
func (v viewReader) skipsForBudget(soFar int, absPath string) bool {
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return false
	}
	if isImage, _ := getImageMimeType(absPath); isImage {
		// An image contributes only a caption to the text budget.
		return false
	}
	estimated, ok := estimateTextBlock(absPath, info.Size())
	if !ok {
		return false
	}
	return soFar+len("\n\n")+estimated > maxMultiViewChars
}

// estimateTextBlock estimates how many bytes of rendered text a file will
// contribute: the first DefaultReadLimit lines, plus the line-number gutter.
// The average line length is measured on a small prefix, which is what the
// returned section is read from anyway.
func estimateTextBlock(path string, size int64) (int, bool) {
	const prefixBytes = 8192
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close() //nolint:errcheck
	prefix := make([]byte, min(int64(prefixBytes), size))
	read, err := file.Read(prefix)
	if err != nil && read == 0 {
		return 0, false
	}
	prefix = prefix[:read]
	lines := max(1, strings.Count(string(prefix), "\n"))
	average := max(1, len(prefix)/lines)
	linesReturned := int64(DefaultReadLimit)
	if total := size / int64(average); total < linesReturned {
		linesReturned = max(total, 1)
	}
	return int(min(int64(average+7)*linesReturned, size)), true
}

func addLineNumbers(content string, startLine int) string {
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")

	var result []string
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")

		lineNum := i + startLine
		numStr := fmt.Sprintf("%d", lineNum)

		if len(numStr) >= 6 {
			result = append(result, fmt.Sprintf("%s|%s", numStr, line))
		} else {
			paddedNum := fmt.Sprintf("%6s", numStr)
			result = append(result, fmt.Sprintf("%s|%s", paddedNum, line))
		}
	}

	return strings.Join(result, "\n")
}

func readTextFile(filePath string, offset, limit, maxContentSize int) (string, bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", false, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	skipped := 0
	for skipped < offset {
		_, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return "", false, nil
			}
			return "", false, err
		}
		skipped++
	}

	lines := make([]string, 0, min(limit, DefaultReadLimit))
	contentSize := 0

	for len(lines) < limit {
		lineText, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", false, err
		}
		lineText = strings.TrimSuffix(lineText, "\n")
		lineText = strings.TrimSuffix(lineText, "\r")
		if len(lineText) > MaxLineLength {
			// Truncate at a rune boundary to avoid splitting
			// multi-byte characters.
			lineText = strings.ToValidUTF8(lineText[:MaxLineLength], "") + "..."
		}
		projectedSize := contentSize + len(lineText)
		if len(lines) > 0 {
			projectedSize++
		}
		if maxContentSize > 0 && projectedSize > maxContentSize {
			return "", false, contentTooLargeError{Size: projectedSize, Max: maxContentSize}
		}
		contentSize = projectedSize
		lines = append(lines, lineText)
		if err == io.EOF {
			break
		}
	}

	// Peek one more line only when we filled the limit.
	hasMore := false
	if len(lines) == limit {
		lineText, peekErr := reader.ReadString('\n')
		hasMore = len(lineText) > 0 || peekErr == nil
	}

	return strings.Join(lines, "\n"), hasMore, nil
}

func getImageMimeType(filePath string) (bool, string) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".jpg", ".jpeg":
		return true, "image/jpeg"
	case ".png":
		return true, "image/png"
	case ".gif":
		return true, "image/gif"
	case ".webp":
		return true, "image/webp"
	default:
		return false, ""
	}
}

// sniffImageMimeType returns the content-sniffed MIME type when it identifies
// a supported image format. Otherwise it returns the provided fallback, which
// is usually the extension-derived type. Providers that validate the image
// media type against the base64 magic bytes (e.g. Anthropic) reject mismatched
// requests with a 400, so trusting the filename alone is unsafe.
func sniffImageMimeType(data []byte, fallback string) string {
	sniffed := http.DetectContentType(data)
	// http.DetectContentType may return the MIME with a ";" parameter
	// (e.g. "image/svg+xml; charset=utf-8") although current image sniffers
	// return bare types; strip defensively.
	if i := strings.IndexByte(sniffed, ';'); i >= 0 {
		sniffed = strings.TrimSpace(sniffed[:i])
	}
	switch sniffed {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return sniffed
	}
	return fallback
}

// isInSkillsPath checks if filePath is within any of the configured skills
// directories. Returns true for files that can be read without permission
// prompts and without size limits.
//
// Note that symlinks are resolved to prevent path traversal attacks via
// symbolic links.
func isInSkillsPath(filePath string, skillsPaths []string) bool {
	if len(skillsPaths) == 0 {
		return false
	}

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}

	evalFilePath, err := filepath.EvalSymlinks(absFilePath)
	if err != nil {
		return false
	}

	for _, skillsPath := range skillsPaths {
		absSkillsPath, err := filepath.Abs(skillsPath)
		if err != nil {
			continue
		}

		evalSkillsPath, err := filepath.EvalSymlinks(absSkillsPath)
		if err != nil {
			continue
		}

		relPath, err := filepath.Rel(evalSkillsPath, evalFilePath)
		if err == nil && !strings.HasPrefix(relPath, "..") {
			return true
		}
	}

	return false
}

// readBuiltinFile reads a file from the embedded builtin skills filesystem.
func readBuiltinFile(params ViewParams, skillTracker *skills.Tracker) (fantasy.ToolResponse, error) {
	embeddedPath := "builtin/" + strings.TrimPrefix(params.FilePath, skills.BuiltinPrefix)
	builtinFS := skills.BuiltinFS()

	data, err := fs.ReadFile(builtinFS, embeddedPath)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Builtin file not found: %s", params.FilePath)), nil
	}

	content := string(data)
	if !utf8.ValidString(content) {
		return fantasy.NewTextErrorResponse("File content is not valid UTF-8"), nil
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 1000000 // Effectively no limit for skill files.
	}

	lines := strings.Split(content, "\n")
	offset := min(params.Offset, len(lines))
	lines = lines[offset:]

	hasMore := len(lines) > limit
	if hasMore {
		lines = lines[:limit]
	}

	output := "<file>\n"
	output += addLineNumbers(strings.Join(lines, "\n"), offset+1)
	if hasMore {
		output += fmt.Sprintf("\n\n(File has more lines. Use 'offset' parameter to read beyond line %d)",
			offset+len(lines))
	}
	output += "\n</file>\n"

	meta := ViewResponseMetadata{
		FilePath: params.FilePath,
		Content:  strings.Join(lines, "\n"),
	}
	if skill, err := skills.ParseContent(data); err == nil {
		meta.ResourceType = ViewResourceSkill
		meta.ResourceName = skill.Name
		meta.ResourceDescription = skill.Description
		skillTracker.MarkLoaded(skill.Name)
	}

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(output),
		meta,
	), nil
}
