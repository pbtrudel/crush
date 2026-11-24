package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/permission"
)

//go:embed templates/agentic_fetch.md
var agenticFetchToolDescription string

// agenticFetchValidationResult holds the validated parameters from the tool call context.
type agenticFetchValidationResult struct {
	SessionID      string
	AgentMessageID string
}

// validateAgenticFetchParams validates the tool call parameters and extracts required context values.
func validateAgenticFetchParams(ctx context.Context, params tools.AgenticFetchParams) (agenticFetchValidationResult, error) {
	if params.Prompt == "" {
		return agenticFetchValidationResult{}, errors.New("prompt is required")
	}

	sessionID := tools.GetSessionFromContext(ctx)
	if sessionID == "" {
		return agenticFetchValidationResult{}, errors.New("session id missing from context")
	}

	agentMessageID := tools.GetMessageFromContext(ctx)
	if agentMessageID == "" {
		return agenticFetchValidationResult{}, errors.New("agent message id missing from context")
	}

	return agenticFetchValidationResult{
		SessionID:      sessionID,
		AgentMessageID: agentMessageID,
	}, nil
}

//go:embed templates/agentic_fetch_prompt.md.tpl
var agenticFetchPromptTmpl []byte

func (c *coordinator) agenticFetchTool(_ context.Context, client *http.Client) (fantasy.AgentTool, error) {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxIdleConns = 100
		transport.MaxIdleConnsPerHost = 10
		transport.IdleConnTimeout = 90 * time.Second

		client = &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		}
	}

	return fantasy.NewParallelAgentTool(
		tools.AgenticFetchToolName,
		agenticFetchToolDescription,
		func(ctx context.Context, params tools.AgenticFetchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			validationResult, err := validateAgenticFetchParams(ctx, params)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			// Determine description based on mode.
			var description string
			if params.URL != "" {
				description = fmt.Sprintf("Fetch and analyze content from URL: %s", params.URL)
			} else {
				description = "Search the web and analyze results"
			}

			p, err := c.permissions.Request(
				ctx,
				permission.CreatePermissionRequest{
					SessionID:   validationResult.SessionID,
					Path:        c.cfg.WorkingDir(),
					ToolCallID:  call.ID,
					ToolName:    tools.AgenticFetchToolName,
					Action:      "fetch",
					Description: description,
					Params:      tools.AgenticFetchPermissionsParams(params),
				},
			)
			if err != nil {
				return fantasy.ToolResponse{}, err
			}
			if !p {
				return tools.NewPermissionDeniedResponse(), nil
			}

			tmpDir, err := os.MkdirTemp(c.cfg.Config().Options.DataDirectory, "crush-fetch-*")
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to create temporary directory: %s", err)), nil
			}
			// The subagent owns the folder for as long as it runs, which can
			// outlast this call once it is handed to the background. The run
			// removes it through Cleanup; only a call that never starts a run
			// removes it here.
			handedOff := false
			defer func() {
				if !handedOff {
					_ = os.RemoveAll(tmpDir)
				}
			}()

			var fullPrompt string

			if params.URL != "" {
				// URL mode: fetch the URL content first. A guessed address that
				// is not there is worth one search, not a dead call.
				content, err := tools.FetchURLAndConvert(ctx, client, params.URL)
				if err != nil {
					fallback, ok := fetchFallbackPrompt(params.Prompt, params.URL, err, tools.SearchBlocked())
					if !ok {
						return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to fetch URL: %s", err)), nil
					}
					fullPrompt = fallback
				} else if hasLargeContent := len(content) > tools.LargeContentThreshold; hasLargeContent {
					tempFile, err := os.CreateTemp(tmpDir, "page-*.md")
					if err != nil {
						return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to create temporary file: %s", err)), nil
					}
					tempFilePath := tempFile.Name()

					if _, err := tempFile.WriteString(content); err != nil {
						tempFile.Close()
						return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to write content to file: %s", err)), nil
					}
					tempFile.Close()

					fullPrompt = fmt.Sprintf("%s\n\nThe web page from %s has been saved to: %s\n\nUse the view and grep tools to analyze this file and extract the requested information.", params.Prompt, params.URL, tempFilePath)
				} else {
					fullPrompt = fmt.Sprintf("%s\n\nWeb page URL: %s\n\n<webpage_content>\n%s\n</webpage_content>", params.Prompt, params.URL, content)
				}
			} else {
				// Search mode: let the subagent search and fetch as needed.
				var ok bool
				fullPrompt, ok = searchModePrompt(params.Prompt, tools.SearchBlocked())
				if !ok {
					return fantasy.NewTextResponse(searchBlockedMessage), nil
				}
			}

			promptOpts := []prompt.Option{
				prompt.WithWorkingDir(tmpDir),
			}

			promptTemplate, err := prompt.NewPrompt("agentic_fetch", string(agenticFetchPromptTmpl), promptOpts...)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error creating prompt: %s", err)
			}

			small := c.smallOnlyModels().Small

			systemPrompt, err := promptTemplate.Build(ctx, small.Model.Provider(), small.Model.Model(), c.cfg)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error building system prompt: %s", err)
			}

			smallProviderCfg, ok := c.cfg.Config().Providers.Get(small.ModelCfg.Provider)
			if !ok {
				return fantasy.ToolResponse{}, errors.New("small model provider not configured")
			}

			fetchTools := fetchSubAgentTools(c, tmpDir, client, tools.SearchBlocked())

			// Subagent tools run without hook interception. The top-level
			// `agentic_fetch` call itself is already wrapped from the coder's
			// side; firing hooks again for every inner tool call would run
			// the user's hooks N times per delegated turn.

			agent := NewSessionAgent(SessionAgentOptions{
				Models:               c.smallOnlyModels,
				SystemPromptPrefix:   smallProviderCfg.SystemPromptPrefix,
				SystemPrompt:         systemPrompt,
				Cfg:                  c.cfg,
				Prompts:              c.promptTexts,
				DisableAutoSummarize: c.cfg.Config().Options.DisableAutoSummarize,
				Sessions:             c.sessions,
				Messages:             c.messages,
				Tools:                fetchTools,
				titles:               &c.titles,
				limits:               &c.limits,
			})

			handedOff = true
			return c.runSubAgentMaybeBackground(ctx, tools.AgenticFetchToolName, accessRead, subAgentParams{
				Agent:          agent,
				SessionID:      validationResult.SessionID,
				AgentMessageID: validationResult.AgentMessageID,
				ToolCallID:     call.ID,
				Prompt:         fullPrompt,
				SessionTitle:   "Fetch Analysis",
				SessionSetup: func(sessionID string) {
					c.permissions.AutoApproveSession(sessionID)
				},
				Cleanup: func() { _ = os.RemoveAll(tmpDir) },
			})
		},
	), nil
}

// searchBlockedMessage answers a search-mode call while the search backend is
// blocked. The subagent would have nothing to search with, and the caller
// usually has a search tool of its own.
const searchBlockedMessage = "Web search is blocked for this machine for a few minutes. " +
	"Use a search tool you have directly, or call agentic_fetch again with a known URL."

// searchModePrompt builds the prompt for a search-mode call. It reports false
// when the search backend is blocked, so the caller gets an answer instead of
// a subagent that cannot do its job.
func searchModePrompt(prompt string, blocked bool) (string, bool) {
	if blocked {
		return "", false
	}
	return fmt.Sprintf("%s\n\nUse the web_search tool to find relevant information. Break down the question into smaller, focused searches if needed. After searching, use web_fetch to get detailed content from the most relevant results.", prompt), true
}

// fetchSubAgentTools is the palette a fetch subagent runs with. Search is left
// out while the search backend is blocked, so the subagent cannot spend a step
// on a search that cannot work.
func fetchSubAgentTools(c *coordinator, tmpDir string, client *http.Client, searchBlocked bool) []fantasy.AgentTool {
	fetchTools := []fantasy.AgentTool{
		tools.NewWebFetchTool(tmpDir, client),
		tools.NewGlobTool(tmpDir, c.cfg.Config().Tools.Glob),
		tools.NewGrepTool(tmpDir, c.cfg.Config().Tools.Grep),
		tools.NewSourcegraphTool(client),
		tools.NewViewTool(c.lspManager, c.permissions, c.filetracker, nil, tmpDir),
		tools.NewReadTool(c.lspManager, c.permissions, c.filetracker, nil, tmpDir),
	}
	if !searchBlocked {
		fetchTools = append(fetchTools, tools.NewWebSearchTool(client))
	}
	// Keep the sub-agent's palette sorted by name like every other tool list
	// in the codebase; a stable order is what lets the provider's prompt cache
	// match the tools block between steps.
	slices.SortFunc(fetchTools, func(a, b fantasy.AgentTool) int {
		return strings.Compare(a.Info().Name, b.Info().Name)
	})
	return fetchTools
}

// fetchFallbackPrompt turns a guessed URL that will not come back into one
// search. It reports false when a fallback is not worth it: a fault that is
// not a missing page, or a block that would stop the search anyway.
func fetchFallbackPrompt(prompt, url string, fetchErr error, searchBlocked bool) (string, bool) {
	if searchBlocked {
		return "", false
	}
	var statusErr *tools.FetchStatusError
	if !errors.As(fetchErr, &statusErr) {
		return "", false
	}
	if statusErr.Status != http.StatusNotFound && statusErr.Status != http.StatusGone {
		return "", false
	}
	return fmt.Sprintf("The URL %s returned %d, so search for the answer instead.\n\n%s",
		url, statusErr.Status, prompt), true
}
