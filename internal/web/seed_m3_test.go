package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

// TestSeedM3FeedsTheRoutes proves the m3 webfake scenario reaches the
// browser: the command list carries the seeded skill, and the systems,
// session and snapshot answers carry the seeded data.
func TestSeedM3FeedsTheRoutes(t *testing.T) {
	t.Parallel()
	fake := webtest.NewFake()
	require.NoError(t, webtest.SeedM3(fake, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)))
	g, err := Start(t.Context(), Options{
		Workspace: fake,
		LoadCommands: func(*config.Config) ([]commands.CustomCommand, error) {
			return webtest.M3Commands(), nil
		},
		// The MCP list asks for Docker availability; pin it so the test
		// never runs the host's docker.
		DockerMCPAvailable: func() bool { return false },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	client := authedClient(t, g)
	get := func(path string, v any) {
		t.Helper()
		rsp, body := doJSON(t, client, http.MethodGet, g.URL()+path, "")
		rsp.Body.Close()
		require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
		require.NoError(t, json.Unmarshal(body, v), string(body))
	}

	var cmds struct {
		Commands []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"commands"`
	}
	get("/api/commands", &cmds)
	ids := make([]string, 0, len(cmds.Commands))
	for _, cmd := range cmds.Commands {
		ids = append(ids, cmd.ID)
	}
	require.Equal(t, []string{"user:fix", "project:hello", "user:go-tests"}, ids)
	require.Equal(t, "skill", cmds.Commands[2].Kind)

	var skills struct {
		Skills []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"skills"`
	}
	get("/api/skills", &skills)
	require.Equal(t, []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}{{Name: "go-tests", Description: "Run the Go tests and report the failures"}}, skills.Skills)

	var prompts struct {
		Prompts []struct {
			ID   string `json:"id"`
			Args []struct {
				ID       string `json:"id"`
				Required bool   `json:"required"`
			} `json:"args"`
		} `json:"prompts"`
	}
	get("/api/mcp/prompts", &prompts)
	require.Equal(t, []string{"github:triage", "notes:summary"}, []string{prompts.Prompts[0].ID, prompts.Prompts[1].ID})
	require.Equal(t, "topic", prompts.Prompts[0].Args[0].ID)
	require.True(t, prompts.Prompts[0].Args[0].Required)

	var mcps struct {
		Servers []struct {
			Name      string `json:"name"`
			State     string `json:"state"`
			Tools     int    `json:"tools"`
			Error     string `json:"error"`
			NeedsAuth bool   `json:"needs_auth"`
		} `json:"servers"`
	}
	get("/api/mcp", &mcps)
	require.Len(t, mcps.Servers, 2)
	require.Equal(t, "github", mcps.Servers[0].Name)
	require.Equal(t, 12, mcps.Servers[0].Tools)
	require.Equal(t, "notes", mcps.Servers[1].Name)
	require.Equal(t, "connection refused", mcps.Servers[1].Error)
	require.True(t, mcps.Servers[1].NeedsAuth)

	var lsps struct {
		Servers []struct {
			Name        string `json:"name"`
			State       string `json:"state"`
			Diagnostics struct {
				Error   int `json:"error"`
				Warning int `json:"warning"`
			} `json:"diagnostics"`
		} `json:"servers"`
	}
	get("/api/lsp", &lsps)
	require.Len(t, lsps.Servers, 2)
	require.Equal(t, "gopls", lsps.Servers[0].Name)
	require.Equal(t, "2", lsps.Servers[0].State)
	require.Equal(t, 2, lsps.Servers[0].Diagnostics.Error)
	require.Equal(t, 1, lsps.Servers[0].Diagnostics.Warning)
	require.Equal(t, "4", lsps.Servers[1].State)

	var sessions []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	get("/api/sessions", &sessions)
	require.Equal(t, []string{"s-2", "s-3", "s-4", "s1"}, []string{sessions[0].ID, sessions[1].ID, sessions[2].ID, sessions[3].ID})
	require.Equal(t, "Tokenizer port", sessions[0].Title)

	var snap Snapshot
	get("/api/snapshot", &snap)
	require.Equal(t, "m1", snap.Model.ID)
	require.True(t, snap.Model.AgentReady)
	require.True(t, snap.AgentReady)
	require.Empty(t, snap.PlanReadySessionID)
}
