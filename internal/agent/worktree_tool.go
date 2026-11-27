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
