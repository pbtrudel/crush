package route

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// disabledTools is the built-in tool set a suite run never has: no web
// access, and nothing that could search for the answer. The names match
// Crush's built-in tool names.
var disabledTools = []string{"fetch", "agentic_fetch", "download", "sourcegraph", "web_search", "web_fetch"}

// WriteGlobalConfig writes the generated global config of a route: its
// provider block and the suite's options, with an optional variant overlay
// merged into the options. Provider config values stay as $NAME text; the
// secrets themselves stay in the environment.
func WriteGlobalConfig(path string, r *Route, overlay map[string]any) error {
	provider := make(map[string]any, len(r.ProviderConfig))
	for k, v := range r.ProviderConfig {
		provider[k] = v
	}
	// Field names from internal/config: providers (config.go:1098),
	// options (config.go:1104), disabled_tools (config.go:510),
	// disable_provider_auto_update (config.go:511) and disable_metrics
	// (config.go:514). No mcp and no hooks block is written at all.
	options := map[string]any{
		"disable_metrics":              true,
		"disable_provider_auto_update": true,
		"disabled_tools":               disabledTools,
	}
	for k, v := range overlay {
		options[k] = v
	}
	doc := map[string]any{
		"providers": map[string]any{r.Provider: provider},
		"options":   options,
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode global config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write global config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("tighten global config: %w", err)
	}
	return nil
}
