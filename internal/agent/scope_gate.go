package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/gitutil"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/shell"
)

// scopedWriteTools are the file-writing tools a teammate's claimed write
// scopes apply to before they run. bash is gated separately: its writes
// cannot be known in advance, so in a task worktree they are checked after
// it runs.
var scopedWriteTools = []string{
	tools.EditToolName,
	tools.MultiEditToolName,
	tools.WriteToolName,
	tools.DownloadToolName,
	tools.ApplyPatchToolName,
	tools.RenameToolName,
	tools.ReplaceSymbolToolName,
}

// scopeGatedTool refuses a write outside the write scopes of the task the
// calling teammate claimed. It keeps the inner tool's Info, provider
// options and MCP identity, so the tools block stays byte-identical to the
// Lead's and the cache is shared. It sits outside the hook wrapper, so a
// refused call runs no hook and raises no permission prompt.
type scopeGatedTool struct {
	inner  fantasy.AgentTool
	scopes func(ctx context.Context) []string
	// workspace reads the worktree the caller works in now. A background
	// bash job's check runs when the job finishes, so it reads the
	// workspace again rather than trusting where it ran. It is nil in
	// tests that place the workspace on the call's context.
	workspace  func(ctx context.Context) (agentteam.Workspace, bool)
	workingDir string
}

// gateToolsForScopes wraps the scoped write tools and bash in list.
func gateToolsForScopes(list []fantasy.AgentTool, scopes func(context.Context) []string, workspace func(context.Context) (agentteam.Workspace, bool), workingDir string) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, len(list))
	for i, tool := range list {
		if name := tool.Info().Name; slices.Contains(scopedWriteTools, name) || name == tools.BashToolName {
			tool = &scopeGatedTool{inner: tool, scopes: scopes, workspace: workspace, workingDir: workingDir}
		}
		out[i] = tool
	}
	return out
}

func (g *scopeGatedTool) Info() fantasy.ToolInfo { return g.inner.Info() }

// MCP reports the MCP server of the wrapped tool, or "" for a built-in
// tool, so MCP filtering sees through the scope gate.
func (g *scopeGatedTool) MCP() string {
	if mcpTool, ok := g.inner.(interface{ MCP() string }); ok {
		return mcpTool.MCP()
	}
	return ""
}

func (g *scopeGatedTool) ProviderOptions() fantasy.ProviderOptions {
	return g.inner.ProviderOptions()
}

func (g *scopeGatedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	g.inner.SetProviderOptions(opts)
}

func (g *scopeGatedTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	scopes := g.scopes(ctx)
	if len(scopes) == 0 {
		return g.inner.Run(ctx, call)
	}
	name := g.inner.Info().Name
	if name == tools.BashToolName {
		return g.runBash(ctx, call, scopes)
	}
	listed := strings.Join(scopes, ", ")
	if name == tools.RenameToolName {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"lsp_rename is refused while your claimed task has write scopes (%s): the files a rename changes are not known in advance. Use edit on each file instead.",
			listed)), nil
	}
	paths, err := scopedPaths(name, call.Input)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"could not read the file paths of this %s call to check them against your write scopes: %s", name, err)), nil
	}
	// A teammate in a task worktree resolves its paths there, so its
	// scopes are checked there too.
	workingDir := tools.WorkingDirFrom(ctx, g.workingDir)
	for _, path := range paths {
		rel, inside := workspaceRelativePath(workingDir, path)
		if inside && pathInScopes(rel, scopes) {
			continue
		}
		shown := path
		if inside {
			shown = rel
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"path %q is outside the write scopes of your claimed task: %s", shown, listed)), nil
	}
	return g.inner.Run(ctx, call)
}

// scopedPaths returns the files a call writes: every path in a patch's
// file headers, or the call's file_path.
func scopedPaths(tool, input string) ([]string, error) {
	if tool == tools.ApplyPatchToolName {
		var params tools.ApplyPatchParams
		if err := json.Unmarshal([]byte(input), &params); err != nil {
			return nil, err
		}
		return tools.PatchFilePaths(params.Patch), nil
	}
	var params struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return nil, err
	}
	if params.FilePath == "" {
		return nil, nil
	}
	return []string{params.FilePath}, nil
}

// workspaceRelativePath resolves path the way the edit tools do, then
// returns it relative to workingDir in slash form. It reports false for a
// path that escapes the working directory.
func workspaceRelativePath(workingDir, path string) (string, bool) {
	abs := filepath.Clean(filepathext.SmartJoin(workingDir, home.Long(path)))
	rel, err := filepath.Rel(filepath.Clean(workingDir), abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// pathInScopes reports whether rel equals a scope or lies under a scope
// directory. Scopes are normalized without a trailing slash.
func pathInScopes(rel string, scopes []string) bool {
	for _, scope := range scopes {
		if rel == scope || strings.HasPrefix(rel, scope+"/") {
			return true
		}
	}
	return false
}

// claimedScopes returns the write scopes of the tasks the calling teammate
// owns in progress. The Lead is never scope-gated.
func (c *coordinator) claimedScopes(ctx context.Context) []string {
	team := c.teamService()
	if team == nil {
		return nil
	}
	member := agentteam.MemberFromContext(ctx)
	if member == agentteam.LeadName {
		return nil
	}
	return team.ClaimedWriteScopes(member)
}

// bashSnapshotCaps bound what one bash call saves to undo its changes: a
// changed file over a cap is reported, not restored.
var bashSnapshotCaps = gitutil.SnapshotCaps{PerFile: 1 << 20, Total: 32 << 20}

// bashCheckTimeout bounds the git work of one bash scope check, so a wedged
// git cannot hang a finished job or a settle that waits for it.
const bashCheckTimeout = 30 * time.Second

// runBash runs a teammate's bash call with its write scopes held by git.
// The worktree's changed files are recorded before the call and compared
// after it, and a change outside the scopes is undone and reported ahead of
// the command's output. A background job is checked when it finishes,
// against the record taken when it started. A teammate with no worktree
// keeps ungated bash.
func (g *scopeGatedTool) runBash(ctx context.Context, call fantasy.ToolCall, scopes []string) (fantasy.ToolResponse, error) {
	ws, ok := workspaceFrom(ctx)
	if !ok {
		return g.inner.Run(ctx, call)
	}
	before, err := snapshotBashWorktree(ctx, ws.Root)
	if err != nil {
		slog.Warn("Could not record a task worktree before a bash call", "dir", ws.Root, "error", err)
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"bash is refused: could not record your task worktree to hold this command to your write scopes (%s): %s",
			strings.Join(scopes, ", "), err)), nil
	}
	resp, runErr := g.inner.Run(ctx, call)
	// The record outlives the call's context: a background job finishes
	// after the turn that started it may be over.
	finishCtx := context.WithoutCancel(ctx)
	if job, ok := runningJob(resp); ok {
		note, deferred := job.SetFinisher(func() string {
			return g.finishBashJob(finishCtx, ws, before)
		})
		if deferred {
			return resp, runErr
		}
		return withScopeErr(resp, runErr, note)
	}
	return withScopeErr(resp, runErr, checkBashScopes(finishCtx, ws, scopes, before))
}

// snapshotBashWorktree records the worktree for a bash call, under the lock
// a commit of the same worktree takes.
func snapshotBashWorktree(ctx context.Context, root string) (gitutil.FileSnapshot, error) {
	unlock := gitutil.LockWorktree(root)
	defer unlock()
	return gitutil.SnapshotFiles(ctx, root, bashSnapshotCaps)
}

// finishBashJob is the finisher a background job runs when it ends. It
// reads the member's claimed scopes and workspace again, because the task
// may have been released or moved meanwhile: a check against a worktree the
// member no longer holds would restore paths there for nothing, so it is
// skipped and the completion output says so.
func (g *scopeGatedTool) finishBashJob(ctx context.Context, started agentteam.Workspace, before gitutil.FileSnapshot) string {
	scopes := g.scopes(ctx)
	if len(scopes) == 0 {
		return bashCheckSkipped("your claimed task no longer holds them")
	}
	current, ok := g.currentWorkspace(ctx)
	if !ok || !dirExists(current.Root) {
		return bashCheckSkipped("your task worktree is gone")
	}
	if current.Root != started.Root {
		return bashCheckSkipped(fmt.Sprintf("your task worktree is now %s, not the one the command ran in", current.Root))
	}
	return checkBashScopes(ctx, current, scopes, before)
}

// currentWorkspace reads the caller's workspace as it is now.
func (g *scopeGatedTool) currentWorkspace(ctx context.Context) (agentteam.Workspace, bool) {
	if g.workspace != nil {
		return g.workspace(ctx)
	}
	return workspaceFrom(ctx)
}

// bashCheckSkipped is the note a finished background job carries when its
// changes cannot be held to the scopes any more.
func bashCheckSkipped(reason string) string {
	return fmt.Sprintf("This finished background job was not checked against your write scopes: %s, so nothing was restored. Check git status in your worktree and undo any change outside them.", reason)
}

// dirExists reports whether path is a directory on disk.
func dirExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("Could not stat a task worktree", "dir", path, "error", err)
		}
		return false
	}
	return info.IsDir()
}

// runningJob returns the background job a bash response started, while it
// still runs.
func runningJob(resp fantasy.ToolResponse) (*shell.BackgroundShell, bool) {
	if resp.Metadata == "" {
		return nil, false
	}
	var meta tools.BashResponseMetadata
	if err := json.Unmarshal([]byte(resp.Metadata), &meta); err != nil || meta.ShellID == "" {
		return nil, false
	}
	job, ok := shell.GetBackgroundShellManager().Get(meta.ShellID)
	if !ok || job.IsDone() {
		return nil, false
	}
	return job, true
}

// checkBashScopes compares the worktree with before, restores the changed
// paths outside scopes, and returns the note for the model, or "" when the
// command stayed inside them.
func checkBashScopes(ctx context.Context, ws agentteam.Workspace, scopes []string, before gitutil.FileSnapshot) string {
	ctx, cancel := context.WithTimeout(ctx, bashCheckTimeout)
	defer cancel()
	// A commit of this worktree takes the same lock, so a check never
	// runs its snapshots and restore beside one.
	unlock := gitutil.LockWorktree(ws.Root)
	defer unlock()
	listed := strings.Join(scopes, ", ")
	after, err := gitutil.SnapshotFiles(ctx, ws.Root, gitutil.SnapshotCaps{})
	if err == nil {
		var changed []string
		changed, err = gitutil.ChangedPaths(ctx, ws.Root, before, after)
		if err == nil {
			return restoreOutOfScope(ctx, ws, scopes, before, changed)
		}
	}
	slog.Warn("Could not check a bash call against its write scopes", "dir", ws.Root, "error", err)
	return fmt.Sprintf("Could not check this command's changes against the write scopes of your claimed task (%s): %s. Check git status in your worktree and undo any change outside them.",
		listed, err)
}

// restoreOutOfScope restores the changed paths outside scopes and describes
// what it did. Status paths are relative to the worktree root, scopes to
// the working directory inside it.
func restoreOutOfScope(ctx context.Context, ws agentteam.Workspace, scopes []string, before gitutil.FileSnapshot, changed []string) string {
	prefix := ""
	if rel, err := filepath.Rel(ws.Root, ws.Dir); err == nil && rel != "." {
		prefix = filepath.ToSlash(rel) + "/"
	}
	var outside []string
	for _, path := range changed {
		if rel, inDir := strings.CutPrefix(path, prefix); inDir && pathInScopes(rel, scopes) {
			continue
		}
		outside = append(outside, path)
	}
	if len(outside) == 0 {
		return ""
	}
	result := gitutil.RestorePaths(ctx, ws.Root, before, outside)
	var sb strings.Builder
	fmt.Fprintf(&sb, "This command changed files outside the write scopes of your claimed task (%s).", strings.Join(scopes, ", "))
	if len(result.Restored) > 0 {
		fmt.Fprintf(&sb, " Restored: %s.", strings.Join(result.Restored, ", "))
	}
	if len(result.Unrestored) > 0 {
		fmt.Fprintf(&sb, " Not restored (could not be restored safely: over the 1 MiB per-file or 32 MiB per-call save cap, or the restore failed): %s.",
			strings.Join(result.Unrestored, ", "))
	}
	sb.WriteString(" Change only files inside your scopes. The command's output follows.")
	return sb.String()
}

// withScopeErr reports note with resp: on success it leads the content as an
// error result, and on a failed call it extends the error, so the model
// still learns what was restored.
func withScopeErr(resp fantasy.ToolResponse, runErr error, note string) (fantasy.ToolResponse, error) {
	if note == "" {
		return resp, runErr
	}
	if runErr != nil {
		return resp, fmt.Errorf("%w. %s", runErr, note)
	}
	return withScopeNote(resp, note), nil
}

// withScopeNote marks resp as an error led by note, keeping its metadata
// so the UI still renders the command.
func withScopeNote(resp fantasy.ToolResponse, note string) fantasy.ToolResponse {
	if note == "" {
		return resp
	}
	resp.Content = note + "\n\n" + resp.Content
	resp.IsError = true
	return resp
}
