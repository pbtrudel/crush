package agent

import (
	"strings"

	"github.com/charmbracelet/crush/internal/config"
)

// applyPatchFor reports whether this agent's model should be given
// apply_patch in place of edit and multiedit: options.apply_patch is on and
// the model is one the *** Begin Patch format is trained on. Every other
// combination keeps the palette it had before the option existed, which is
// why the option defaults off.
func (c *coordinator) applyPatchFor(agentCfg config.Agent) bool {
	if c.cfg == nil || !c.cfg.Config().Options.GetApplyPatch() {
		return false
	}
	selected, ok := c.cfg.Config().Models[agentCfg.Model]
	if !ok {
		return false
	}
	return isPatchFormatModel(selected.Model)
}

// isPatchFormatModel reports whether a model ID names an OpenAI GPT model.
// Providers prefix their catalog ids inconsistently (openrouter serves
// "openai/gpt-5", openai "gpt-5", gateways add ":exacto" or ":free"), so the
// base name after the last slash and before the first colon is what
// identifies the family.
func isPatchFormatModel(modelID string) bool {
	base := modelID
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.Index(base, ":"); i > 0 {
		base = base[:i]
	}
	for _, prefix := range []string{"gpt-", "chatgpt-", "codex-", "o1", "o3", "o4"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}
