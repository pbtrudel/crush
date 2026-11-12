package config

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/claudeagent"
)

// claudeAgentProbeTimeout bounds the first-run probe. It only runs once: the
// result is cached and reused on later starts.
const claudeAgentProbeTimeout = 15 * time.Second

var (
	claudeAgentOnce    sync.Once
	claudeAgentCatalog *claudeagent.Catalog
)

// claudeAgentDataDir is where the Claude Agent provider keeps its cached model
// list and session records.
func claudeAgentDataDir() string {
	return filepath.Join(filepath.Dir(GlobalConfigData()), "claude-agent")
}

// ClaudeAgentConnection maps the user's provider settings onto what the CLI
// reads. It is also used by the coordinator when it builds the provider.
func ClaudeAgentConnection(cfg *Config) claudeagent.Connection {
	pc, ok := cfg.Providers.Get(claudeagent.Name)
	if !ok {
		return claudeagent.Connection{}
	}
	conn := claudeagent.Connection{
		BaseURL: pc.BaseURL,
		APIKey:  pc.APIKey,
		Headers: pc.ExtraHeaders,
		Body:    pc.ExtraBody,
	}
	if token, ok := pc.ProviderOptions["auth_token"].(string); ok {
		conn.AuthToken = token
	}
	if timeout, ok := pc.ProviderOptions["header_timeout"].(float64); ok {
		conn.HeaderTimeout = int64(timeout)
	}
	if timeout, ok := pc.ProviderOptions["chunk_timeout"].(float64); ok {
		conn.ChunkTimeout = int64(timeout)
	}
	return conn
}

// ClaudeAgentProvider returns the catalog entry for the Claude Agent provider.
// It is omitted when the CLI is not installed, since the provider cannot work
// without it.
func ClaudeAgentProvider(known []catwalk.Provider) (catwalk.Provider, bool) {
	catalog := claudeAgentCatalogFor()
	if catalog == nil {
		return catwalk.Provider{}, false
	}
	return claudeagent.ProviderEntry(catalog, anthropicModels(known)), true
}

// claudeAgentCatalogFor loads the cached catalog, probing once when there is
// none. Probing starts the CLI without sending a prompt, so it costs no tokens.
func claudeAgentCatalogFor() *claudeagent.Catalog {
	claudeAgentOnce.Do(func() {
		dir := claudeAgentDataDir()
		if cached := claudeagent.LoadCatalog(dir); cached != nil {
			claudeAgentCatalog = cached
			return
		}
		executable, err := claudeagent.FindExecutable()
		if err != nil {
			slog.Debug("Claude Agent provider unavailable", "error", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), claudeAgentProbeTimeout)
		defer cancel()
		catalog, err := claudeagent.Probe(ctx, executable, workingDirForProbe(), claudeagent.Connection{})
		if err != nil {
			slog.Warn("Claude Agent provider could not read the Claude Code login", "error", err)
			return
		}
		if err := claudeagent.SaveCatalog(dir, catalog); err != nil {
			slog.Debug("Could not cache the Claude Agent model list", "error", err)
		}
		claudeAgentCatalog = catalog
	})
	return claudeAgentCatalog
}

func workingDirForProbe() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// anthropicModels indexes the Anthropic catalog by model ID so the Claude Agent
// models can show the same versioned names, context windows, and prices. A
// subscription login ignores the prices, but an API key login is billed at
// those rates.
func anthropicModels(known []catwalk.Provider) func(string) (catwalk.Model, bool) {
	for _, provider := range known {
		if provider.ID != catwalk.InferenceProviderAnthropic {
			continue
		}
		index := make(map[string]catwalk.Model, len(provider.Models))
		for _, model := range provider.Models {
			index[model.ID] = model
		}
		return func(id string) (catwalk.Model, bool) {
			model, ok := index[id]
			return model, ok
		}
	}
	return nil
}
