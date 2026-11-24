package tools

import (
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/skills"
)

type ReadMCPResourceParams struct {
	MCPName string `json:"mcp_name" description:"The MCP server name"`
	URI     string `json:"uri" description:"The resource URI to read"`
}

type ReadMCPResourcePermissionsParams struct {
	MCPName string `json:"mcp_name"`
	URI     string `json:"uri"`
}

const ReadMCPResourceToolName = "read_mcp_resource"

//go:embed read_mcp_resource.md
var readMCPResourceDescription string

func NewReadMCPResourceTool(cfg *config.ConfigStore, permissions permission.Service) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		ReadMCPResourceToolName,
		readMCPResourceDescription,
		func(ctx context.Context, params ReadMCPResourceParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			params.MCPName = strings.TrimSpace(params.MCPName)
			params.URI = strings.TrimSpace(params.URI)
			// Builtin skills are advertised with a crush:// URI and live in
			// the binary, not on an MCP server. View reads them the same way.
			if strings.HasPrefix(params.URI, crushBuiltinScheme) {
				return readBuiltinSkillResource(params.URI), nil
			}
			if params.MCPName == "" {
				return fantasy.NewTextErrorResponse("mcp_name parameter is required"), nil
			}
			if params.URI == "" {
				return fantasy.NewTextErrorResponse("uri parameter is required"), nil
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for reading MCP resources")
			}

			relPath := filepathext.SmartJoin(cfg.WorkingDir(), cmp.Or(params.URI, "mcp-resource"))
			p, err := permissions.Request(
				ctx,
				permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        relPath,
					ToolCallID:  call.ID,
					ToolName:    ReadMCPResourceToolName,
					Action:      "read",
					Description: fmt.Sprintf("Read MCP resource from %s", params.MCPName),
					Params:      ReadMCPResourcePermissionsParams(params),
				},
			)
			if err != nil {
				return fantasy.ToolResponse{}, err
			}
			if !p {
				return NewPermissionDeniedResponse(), nil
			}

			contents, err := mcp.ReadResource(ctx, cfg, params.MCPName, params.URI)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			if len(contents) == 0 {
				return fantasy.NewTextResponse(""), nil
			}

			var textParts []string
			for _, content := range contents {
				if content == nil {
					continue
				}
				if content.Text != "" {
					textParts = append(textParts, content.Text)
					continue
				}
				if len(content.Blob) > 0 {
					textParts = append(textParts, string(content.Blob))
					continue
				}
				slog.Debug("MCP resource content missing text/blob", "uri", content.URI)
			}

			if len(textParts) == 0 {
				return fantasy.NewTextResponse(""), nil
			}

			return fantasy.NewTextResponse(strings.Join(textParts, "\n")), nil
		},
	)
}

// crushBuiltinScheme marks a URI Crush serves from the binary instead of an
// MCP server.
const crushBuiltinScheme = "crush://"

// readBuiltinSkillResource reads one embedded skill file. A path that leaves
// the skills tree, or a crush:// URI outside it, is refused: those are not
// files this tool serves.
func readBuiltinSkillResource(uri string) fantasy.ToolResponse {
	if !strings.HasPrefix(uri, skills.BuiltinPrefix) {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"only %s resources are built in; %s belongs to no MCP server", skills.BuiltinPrefix, uri))
	}
	relative := strings.TrimPrefix(uri, skills.BuiltinPrefix)
	segments := strings.Split(relative, "/")
	if relative == "" || strings.HasPrefix(relative, "/") {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("builtin resource not found: %s", uri))
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("builtin resource path cannot leave %s: %s", skills.BuiltinPrefix, uri))
		}
	}
	data, err := fs.ReadFile(skills.BuiltinFS(), "builtin/"+relative)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("builtin resource not found: %s", uri))
	}
	return fantasy.NewTextResponse(string(data))
}
