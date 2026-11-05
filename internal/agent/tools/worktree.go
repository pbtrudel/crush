package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/gitutil"
)

// WorktreeToolName is the name of the Git worktree management tool.
const WorktreeToolName = "worktree"

// WorktreeParams are the inputs for the worktree tool.
type WorktreeParams struct {
	Action       string `json:"action" description:"One of: create, list, remove."`
	Name         string `json:"name,omitempty" description:"Objective slug for create, e.g. fix-login-timeout."`
	Base         string `json:"base,omitempty" description:"Base branch or ref for create. Defaults to HEAD."`
	Path         string `json:"path,omitempty" description:"Worktree path for remove. Defaults to the current worktree."`
	Force        bool   `json:"force,omitempty" description:"Remove even with uncommitted changes."`
	DeleteBranch bool   `json:"delete_branch,omitempty" description:"Also delete the local branch on remove."`
}

type worktreeResponse struct {
	Action            string             `json:"action"`
	Path              string             `json:"path,omitempty"`
	Branch            string             `json:"branch,omitempty"`
	Base              string             `json:"base,omitempty"`
	AlreadyInWorktree bool               `json:"alreadyInWorktree,omitempty"`
	Worktrees         []gitutil.Worktree `json:"worktrees,omitempty"`
	Removed           bool               `json:"removed,omitempty"`
	DeletedBranch     string             `json:"deletedBranch,omitempty"`
	Instructions      string             `json:"instructions,omitempty"`
}

// NewWorktreeTool creates the tool that manages isolated Git worktrees.
func NewWorktreeTool(workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		WorktreeToolName,
		"Manage Git worktrees for isolated implementation work. Create one worktree per task before editing, work inside its absolute path, and remove it after the branch is pushed and merged.",
		func(ctx context.Context, params WorktreeParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			switch strings.ToLower(strings.TrimSpace(params.Action)) {
			case "create":
				return worktreeCreate(ctx, workingDir, params)
			case "list":
				return worktreeList(ctx, workingDir)
			case "remove":
				return worktreeRemove(ctx, workingDir, params)
			default:
				return fantasy.NewTextErrorResponse("action must be one of: create, list, remove"), nil
			}
		},
	)
}

func worktreeCreate(ctx context.Context, workingDir string, params WorktreeParams) (fantasy.ToolResponse, error) {
	current, linked, err := gitutil.CurrentWorktree(ctx, workingDir)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if linked {
		return worktreeJSON(worktreeResponse{
			Action:            "create",
			Path:              current.Path,
			Branch:            current.Branch,
			AlreadyInWorktree: true,
			Instructions:      "You are already in an isolated worktree. Work inside this path; do not create another worktree.",
		})
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return fantasy.NewTextErrorResponse("name is required for create"), nil
	}
	base := strings.TrimSpace(params.Base)
	if base == "" {
		base = "HEAD"
	}
	path, branch, err := gitutil.CreateWorktreeFrom(ctx, workingDir, name, base)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return worktreeJSON(worktreeResponse{
		Action:       "create",
		Path:         path,
		Branch:       branch,
		Base:         base,
		Instructions: "Work only inside this path. Use absolute paths for file tools and pass working_dir to bash. Do not edit the main checkout.",
	})
}

func worktreeList(ctx context.Context, workingDir string) (fantasy.ToolResponse, error) {
	worktrees, err := gitutil.ListWorktrees(ctx, workingDir)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return worktreeJSON(worktreeResponse{Action: "list", Worktrees: worktrees})
}

func worktreeRemove(ctx context.Context, workingDir string, params WorktreeParams) (fantasy.ToolResponse, error) {
	path := strings.TrimSpace(params.Path)
	if path == "" {
		current, linked, err := gitutil.CurrentWorktree(ctx, workingDir)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		if !linked {
			return fantasy.NewTextErrorResponse("path is required when removing from the main checkout"), nil
		}
		path = current.Path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	branch := ""
	if worktrees, err := gitutil.ListWorktrees(ctx, workingDir); err == nil {
		for _, worktree := range worktrees {
			if filepath.Clean(worktree.Path) == filepath.Clean(abs) {
				branch = worktree.Branch
				break
			}
		}
	}

	if err := gitutil.RemoveWorktree(ctx, workingDir, abs, params.Force); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	response := worktreeResponse{Action: "remove", Path: abs, Branch: branch, Removed: true}
	if params.DeleteBranch && branch != "" {
		if err := gitutil.DeleteBranch(ctx, workingDir, branch, params.Force); err != nil {
			response.Instructions = "Worktree removed, but the branch could not be deleted: " + err.Error()
		} else {
			response.DeletedBranch = branch
		}
	}
	return worktreeJSON(response)
}

func worktreeJSON(value worktreeResponse) (fantasy.ToolResponse, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return fantasy.NewTextResponse(string(data)), nil
}
