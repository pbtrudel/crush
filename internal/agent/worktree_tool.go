package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/filepathext"
)

// lspWorktreeRefusal is what an LSP tool returns to a teammate in a task
// worktree. The language servers index the Lead's tree, so their answers
// would describe files the teammate is not editing.
const lspWorktreeRefusal = "LSP tools are not available in a task worktree: the language servers serve the Lead's tree only. Use view, grep and bash in your worktree instead."

// teammateWriteDispatchRefusal is what a teammate whose own edits are held to
// a task gets when it asks for a write subagent, freshly or by resuming one.
// The subagent's tools are not held to the task, so it would edit the Lead's
// tree outside the teammate's scopes.
const teammateWriteDispatchRefusal = "a teammate with a task worktree or write scopes cannot dispatch a write subagent; do the edits yourself"

// worktreeTool runs a teammate's tool call in the git worktree of the task
// it holds. The workspace is looked up on every call, because a teammate
// can claim its task in the middle of a turn, and it reaches the tool on
// the call's context. The tool keeps the Info, provider options and MCP
// identity of the Lead's copy, so the tools block stays byte-identical and
// the cache is shared.
type worktreeTool struct {
	inner     fantasy.AgentTool
	workspace func(ctx context.Context) (agentteam.Workspace, bool)
}

// placeToolsInWorktrees wraps every tool in list.
func placeToolsInWorktrees(list []fantasy.AgentTool, workspace func(context.Context) (agentteam.Workspace, bool)) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, len(list))
	for i, tool := range list {
		out[i] = &worktreeTool{inner: tool, workspace: workspace}
	}
	return out
}

func (w *worktreeTool) Info() fantasy.ToolInfo { return w.inner.Info() }

// MCP reports the MCP server of the wrapped tool, or "" for a built-in
// tool, so MCP filtering sees through the wrapper.
func (w *worktreeTool) MCP() string {
	if mcpTool, ok := w.inner.(interface{ MCP() string }); ok {
		return mcpTool.MCP()
	}
	return ""
}

func (w *worktreeTool) ProviderOptions() fantasy.ProviderOptions {
	return w.inner.ProviderOptions()
}

func (w *worktreeTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	w.inner.SetProviderOptions(opts)
}

func (w *worktreeTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	ws, ok := w.workspace(ctx)
	if !ok {
		return w.inner.Run(ctx, call)
	}
	name := w.inner.Info().Name
	if strings.HasPrefix(name, "lsp_") {
		return fantasy.NewTextErrorResponse(lspWorktreeRefusal), nil
	}
	if name == tools.BashToolName {
		if refusal := refuseBashOutsideWorktree(ws, call.Input); refusal != "" {
			return fantasy.NewTextErrorResponse(refusal), nil
		}
	}
	ctx = tools.WithWorkingDir(ctx, ws.Dir)
	ctx = withWorkspace(ctx, ws)
	return w.inner.Run(ctx, call)
}

// refuseBashOutsideWorktree reports a bash call whose working_dir would
// start the shell outside the task worktree. bash resolves a relative
// working_dir inside the call's working directory, the worktree, so the
// same resolution here decides where the shell would start. A shell outside
// the worktree would write where the write scopes are not checked.
func refuseBashOutsideWorktree(ws agentteam.Workspace, input string) string {
	var params tools.BashParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		// bash reports its own parse failure; nothing to check here.
		return ""
	}
	dir := filepathext.SmartJoin(ws.Dir, cmp.Or(params.WorkingDir, ws.Dir))
	rel, err := filepath.Rel(ws.Root, dir)
	if err == nil && filepath.IsLocal(rel) {
		return ""
	}
	return fmt.Sprintf("working_dir %q is outside the task worktree %s: a teammate's shell runs inside its worktree, where the write scopes are checked", dir, ws.Root)
}

type workspaceKey struct{}

// withWorkspace puts the teammate's workspace on ctx for the scope gate,
// which needs the worktree root as well as the working directory.
func withWorkspace(ctx context.Context, ws agentteam.Workspace) context.Context {
	return context.WithValue(ctx, workspaceKey{}, ws)
}

// workspaceFrom returns the workspace withWorkspace put on ctx.
func workspaceFrom(ctx context.Context) (agentteam.Workspace, bool) {
	ws, ok := ctx.Value(workspaceKey{}).(agentteam.Workspace)
	return ws, ok && ws.Root != ""
}

// memberWorkspace returns the workspace of the teammate making the call.
// The Lead and a member with no scoped task work in the Lead's tree.
func (c *coordinator) memberWorkspace(ctx context.Context) (agentteam.Workspace, bool) {
	team := c.teamService()
	if team == nil {
		return agentteam.Workspace{}, false
	}
	member := agentteam.MemberFromContext(ctx)
	if member == agentteam.LeadName {
		return agentteam.Workspace{}, false
	}
	return team.MemberWorkspace(member)
}

// refuseTeammateWriteDispatch reports whether the caller is a teammate whose
// edits are held to a task: it holds a task worktree, or it has claimed write
// scopes. A subagent it dispatches gets neither, so writes it performs escape
// both the worktree and the scope gate. The Lead and a teammate with neither
// are free to dispatch.
func (c *coordinator) refuseTeammateWriteDispatch(ctx context.Context) bool {
	if agentteam.IsLead(ctx) {
		return false
	}
	if _, ok := c.memberWorkspace(ctx); ok {
		return true
	}
	return len(c.claimedScopes(ctx)) > 0
}

// withCallerWorkspace carries the caller's working directory and task
// worktree onto a detached subagent's run context. A dispatched run starts
// from a fresh background context, so without this a read subagent of a
// teammate in a task worktree would list and read the Lead's tree.
func withCallerWorkspace(runCtx, callerCtx context.Context) context.Context {
	runCtx = tools.WithWorkingDir(runCtx, tools.WorkingDirFrom(callerCtx, ""))
	if ws, ok := workspaceFrom(callerCtx); ok {
		runCtx = withWorkspace(runCtx, ws)
	}
	return runCtx
}
