package config

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/catwalk/v3/pkg/catwalk"
)

// copilotLongContextSuffix marks the catalog entries that run with the
// Copilot long-context tier. The API has no separate model ID for that tier:
// it limits and bills a request by its prompt size, so the suffix is stripped
// before the request leaves Crush.
const copilotLongContextSuffix = "[long]"

// copilotLongContextWindows holds the prompt budget of the long-context tier
// per model, in tokens. The Copilot API reports it as the long_context
// token_prices context_max of GET /models with X-GitHub-Api-Version
// 2026-06-01; catwalk publishes only the default tier, which is why the
// catalog alone shows the smaller window. Refresh this table when GitHub adds
// or retires a long-context model.
var copilotLongContextWindows = map[string]int64{
	"claude-haiku-5.5":  936_000,
	"claude-opus-5.5":   1_000_000,
	"claude-sonnet-5.5": 936_000,
	"gemini-3.8-flash":  983_040,
	"gemini-4-argon":    983_040,
	"gpt-6-astra":       1_050_000,
	"gpt-6-luna":        872_000,
	"gpt-6-sol":         872_000,
	"gpt-6.1-sol":       922_000,
	"gpt-6.1-terra":     1_000_000,
}

// CopilotBaseModelID returns the API model ID for a Copilot catalog entry,
// dropping the long-context marker.
func CopilotBaseModelID(id string) string {
	return strings.TrimSuffix(id, copilotLongContextSuffix)
}

// withCopilotLongContextModels appends a long-context entry for every Copilot
// model that supports that tier, so both windows stay selectable. The default
// entries are left untouched.
func withCopilotLongContextModels(models []catwalk.Model) []catwalk.Model {
	if len(models) == 0 {
		return models
	}
	out := make([]catwalk.Model, 0, len(models))
	out = append(out, models...)
	existing := make(map[string]bool, len(models))
	for _, model := range models {
		existing[model.ID] = true
	}
	for _, model := range models {
		window, ok := copilotLongContextWindows[model.ID]
		if !ok || existing[model.ID+copilotLongContextSuffix] {
			continue
		}
		variant := model
		variant.ID = model.ID + copilotLongContextSuffix
		variant.Name = fmt.Sprintf("%s (%s context)", model.Name, copilotWindowLabel(window))
		variant.ContextWindow = window
		out = append(out, variant)
	}
	return out
}

// copilotWindowLabel renders a token budget the way model pickers show it:
// 936K, 1M, 1.05M.
func copilotWindowLabel(tokens int64) string {
	if tokens >= 1_000_000 {
		label := strconv.FormatFloat(float64(tokens)/1_000_000, 'f', 2, 64)
		label = strings.TrimSuffix(strings.TrimRight(label, "0"), ".")
		return label + "M"
	}
	return strconv.FormatInt(tokens/1000, 10) + "K"
}
