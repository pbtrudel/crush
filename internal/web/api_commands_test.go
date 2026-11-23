package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// testCommands is the custom command set every command test lists: one
// with a placeholder, one without arguments.
func testCommands(*config.Config) ([]commands.CustomCommand, error) {
	return []commands.CustomCommand{
		{
			ID:        "user:fix",
			Name:      "user:fix",
			Content:   "Fix $FILE",
			Arguments: []commands.Argument{{ID: "FILE", Title: "FILE", Required: true}},
		},
		{ID: "project:hello", Name: "project:hello", Content: "Hello"},
	}, nil
}

// startCommands starts a gateway that lists testCommands and a fake skill
// catalog with one user-invocable skill.
// ranPrompt reports whether the fake recorded any agent run at all.
func ranPrompt(f *webtest.Fake) bool {
	return slices.ContainsFunc(f.CallLog(), func(call string) bool {
		return strings.HasPrefix(call, "AgentRun:")
	})
}

func startCommands(t *testing.T) (*Gateway, *webtest.Fake) {
	t.Helper()
	g, f := startWith(t, Options{LoadCommands: testCommands})
	f.AddSkill(skills.CatalogEntry{
		ID:            "/skills/review/SKILL.md",
		Name:          "review",
		Description:   "Reviews the diff.",
		UserInvocable: true,
	}, "# Skill")
	return g, f
}

func TestListCommandsMergesCustomAndSkills(t *testing.T) {
	t.Parallel()
	g, _ := startCommands(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/commands", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))

	var answer struct {
		Commands []struct {
			ID          string          `json:"id"`
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Kind        string          `json:"kind"`
			Args        json.RawMessage `json:"args"`
		} `json:"commands"`
	}
	require.NoError(t, json.Unmarshal(body, &answer))
	require.Len(t, answer.Commands, 3)
	byID := make(map[string]int, len(answer.Commands))
	for i, cmd := range answer.Commands {
		byID[cmd.ID] = i
	}

	fix := answer.Commands[byID["user:fix"]]
	require.Equal(t, "user:fix", fix.Title)
	require.Equal(t, "custom", fix.Kind)
	require.JSONEq(t, `[{"id":"FILE","title":"FILE","required":true}]`, string(fix.Args))

	hello := answer.Commands[byID["project:hello"]]
	require.Equal(t, "custom", hello.Kind)
	require.JSONEq(t, `[]`, string(hello.Args))

	review := answer.Commands[byID["user:review"]]
	require.Equal(t, "skill", review.Kind)
	require.Equal(t, "user:review", review.Title)
	require.Equal(t, "Reviews the diff.", review.Description)
	require.JSONEq(t, `[]`, string(review.Args))
}

func TestRunCommandSubstitutesOnce(t *testing.T) {
	t.Parallel()
	g, f := startCommands(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/commands/run",
		`{"session_id":"s1","id":"user:fix","args":{"FILE":"$OTHER <b>"}}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return slices.Contains(f.CallLog(), "AgentRun:s1:Fix $OTHER <b>")
	}, 2*time.Second, 10*time.Millisecond, "the prompt reaches the run unexpanded and unescaped")
}

func TestRunCommandRequiresArgs(t *testing.T) {
	t.Parallel()
	g, f := startCommands(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/commands/run",
		`{"session_id":"s1","id":"user:fix","args":{}}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "missing_args")
	require.Contains(t, string(body), "FILE")
	require.False(t, ranPrompt(f))
}

func TestRunCommandRejectsOversizedArgs(t *testing.T) {
	t.Parallel()
	g, f := startCommands(t)
	payload, err := json.Marshal(map[string]any{
		"session_id": "s1",
		"id":         "user:fix",
		"args":       map[string]string{"FILE": strings.Repeat("x", 64<<10+1)},
	})
	require.NoError(t, err)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/commands/run", string(payload))
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "args_too_large")
	require.False(t, ranPrompt(f))
}

func TestRunCommandUnknownCommand(t *testing.T) {
	t.Parallel()
	g, _ := startCommands(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/commands/run",
		`{"session_id":"s1","id":"user:nope"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_command")
}

// TestRunSkillRunsInvocation pins that a skill runs the loaded-skill prompt
// the TUI builds, so the agent sees the skill exactly as it does in the
// terminal.
func TestRunSkillRunsInvocation(t *testing.T) {
	t.Parallel()
	g, f := startCommands(t)
	// The gateway builds skill commands from the catalog, so the invocation
	// text is the one that catalog entry formats.
	want := commands.FromSkillCatalog([]skills.CatalogEntry{{
		ID:            "/skills/review/SKILL.md",
		Name:          "review",
		Description:   "Reviews the diff.",
		UserInvocable: true,
	}})[0].Skill.FormatInvocation()
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/commands/run",
		`{"session_id":"s1","id":"user:review"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return slices.Contains(f.CallLog(), "AgentRun:s1:"+want)
	}, 2*time.Second, 10*time.Millisecond, "a skill runs as its invocation prompt")
}

func TestRunCommandAgentStarting(t *testing.T) {
	t.Parallel()
	g, f := startCommands(t)
	f.ReadyErr = workspace.ErrAgentNotInitialized
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/commands/run",
		`{"session_id":"s1","id":"user:fix","args":{"FILE":"a.go"}}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode)
	require.Contains(t, string(body), "agent_starting")
	require.False(t, ranPrompt(f))
}

func TestAttachSkillReturnsMarkdown(t *testing.T) {
	t.Parallel()
	g, _ := startCommands(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/skills/review/attach", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.JSONEq(t, `{"attachment":{"name":"review","mime_type":"text/markdown","content":"# Skill"}}`, string(body))
}

func TestAttachUnknownSkill(t *testing.T) {
	t.Parallel()
	g, _ := startCommands(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/skills/nope/attach", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_skill")
}

func TestListMCPPrompts(t *testing.T) {
	t.Parallel()
	g, f := startCommands(t)
	f.AddMCPPrompt(commands.MCPPrompt{
		ID:        "fs:do-x",
		ClientID:  "fs",
		PromptID:  "do-x",
		Title:     "Do X",
		Arguments: []commands.Argument{{ID: "target", Title: "target", Required: true}},
	}, "Do X")
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/mcp/prompts", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var answer struct {
		Prompts []struct {
			ID       string          `json:"id"`
			ClientID string          `json:"client_id"`
			PromptID string          `json:"prompt_id"`
			Title    string          `json:"title"`
			Args     json.RawMessage `json:"args"`
		} `json:"prompts"`
	}
	require.NoError(t, json.Unmarshal(body, &answer))
	require.Len(t, answer.Prompts, 1)
	require.Equal(t, "fs:do-x", answer.Prompts[0].ID)
	require.Equal(t, "fs", answer.Prompts[0].ClientID)
	require.Equal(t, "do-x", answer.Prompts[0].PromptID)
	require.Equal(t, "Do X", answer.Prompts[0].Title)
	require.JSONEq(t, `[{"id":"target","title":"target","required":true}]`, string(answer.Prompts[0].Args))
}

func TestMCPPromptRunSendsText(t *testing.T) {
	t.Parallel()
	g, f := startCommands(t)
	f.AddMCPPrompt(commands.MCPPrompt{ID: "fs:empty", ClientID: "fs", PromptID: "empty"}, "")
	f.AddMCPPrompt(commands.MCPPrompt{ID: "fs:do-x", ClientID: "fs", PromptID: "do-x", Title: "Do X"}, "Do X")

	// An empty prompt has nothing to send, so it stops at 204.
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/prompts/run",
		`{"session_id":"s1","client_id":"fs","prompt_id":"empty"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.False(t, ranPrompt(f))

	rsp, body = doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/prompts/run",
		`{"session_id":"s1","client_id":"fs","prompt_id":"do-x"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return slices.Contains(f.CallLog(), "AgentRun:s1:Do X")
	}, 2*time.Second, 10*time.Millisecond)
}

func TestMCPPromptRunUnknownPrompt(t *testing.T) {
	t.Parallel()
	g, _ := startCommands(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/mcp/prompts/run",
		`{"session_id":"s1","client_id":"fs","prompt_id":"nope"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_prompt")
}

func TestSkillsToggleAndReload(t *testing.T) {
	t.Parallel()
	g, f := startCommands(t)
	c := authedClient(t, g)

	for _, tc := range []struct {
		path    string
		call    string
		disable bool
	}{
		{"/api/skills/review/disable", "SkillSetDisabled:review:true", true},
		{"/api/skills/review/enable", "SkillSetDisabled:review:false", false},
	} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+tc.path, "")
		rsp.Body.Close()
		require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
		require.Contains(t, f.CallLog(), tc.call)

		rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/skills", "")
		rsp.Body.Close()
		require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
		var answer struct {
			Skills []struct {
				Name     string `json:"name"`
				Disabled bool   `json:"disabled"`
			} `json:"skills"`
		}
		require.NoError(t, json.Unmarshal(body, &answer))
		require.Len(t, answer.Skills, 1)
		require.Equal(t, "review", answer.Skills[0].Name)
		require.Equal(t, tc.disable, answer.Skills[0].Disabled)
	}

	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/skills/reload", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.Contains(t, f.CallLog(), "ReloadSkills")
}
