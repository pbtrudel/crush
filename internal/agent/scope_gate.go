package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/home"
)

// scopedWriteTools are the file-writing tools a teammate's claimed write
// scopes apply to. bash is not among them: its writes cannot be known
// before it runs.
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
	inner      fantasy.AgentTool
	scopes     func(ctx context.Context) []string
	workingDir string
}

// gateToolsForScopes wraps the scoped write tools in list.
func gateToolsForScopes(list []fantasy.AgentTool, scopes func(context.Context) []string, workingDir string) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, len(list))
	for i, tool := range list {
		if slices.Contains(scopedWriteTools, tool.Info().Name) {
			tool = &scopeGatedTool{inner: tool, scopes: scopes, workingDir: workingDir}
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
	for _, path := range paths {
		rel, inside := workspaceRelativePath(g.workingDir, path)
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
