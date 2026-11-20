package agent

import (
	"path"
	"strings"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/agent/hyper"
)

// vendorFamily returns the model family whose API has a rule about the
// reasoning it produced in earlier turns, matched on the model's base name
// so a vendor prefix or another family's name in the path cannot decide it.
func vendorFamily(modelID string) string {
	id := strings.ToLower(path.Base(modelID))
	switch {
	case strings.HasPrefix(id, "glm-"):
		return "glm"
	case strings.HasPrefix(id, "kimi"):
		return "kimi"
	case strings.HasPrefix(id, "qwen"):
		return "qwen"
	case strings.HasPrefix(id, "mimo"):
		return "mimo"
	case strings.HasPrefix(id, "deepseek"):
		return "deepseek"
	}
	return ""
}

// vendorReasoningProvider reports whether the provider is one whose vendor
// documents the keep-earlier-reasoning fields. Other hosts get nothing:
// their handling is unverified, and Fireworks rejects thinking next to
// reasoning_effort.
func vendorReasoningProvider(providerID string) bool {
	switch providerID {
	case string(catwalk.InferenceProviderZAI),
		string(catwalk.InferenceProviderZhipu),
		string(catwalk.InferenceProviderZhipuCoding),
		string(catwalk.InferenceProviderMoonshot),
		string(catwalk.InferenceKimiCoding),
		string(catwalk.InferenceProviderAlibabaSingapore),
		string(catwalk.InferenceProviderAlibabaUS),
		hyper.Name:
		return true
	}
	return false
}

// addVendorReasoningFlag adds the field that asks the vendor to keep the
// reasoning of earlier turns: Z.AI's thinking.clear_thinking, Moonshot's
// thinking.keep "all", or Alibaba's top-level preserve_thinking. MiMo, Kimi
// and DeepSeek also require the reasoning back in the history, which Crush
// already sends.
func addVendorReasoningFlag(extraBody map[string]any, modelID string) {
	switch vendorFamily(modelID) {
	case "glm":
		// Z.AI documents clear_thinking inside the thinking object. A host
		// that sends thinking as a boolean gets it at the top level, which
		// the host may forward.
		if thinking, ok := extraBody["thinking"].(map[string]any); ok {
			thinking["clear_thinking"] = false
			return
		}
		if _, exists := extraBody["thinking"]; !exists {
			extraBody["thinking"] = map[string]any{"clear_thinking": false}
			return
		}
		extraBody["clear_thinking"] = false
	case "kimi":
		// Moonshot documents thinking.keep "all" and no top-level form, so a
		// host that sends thinking as a boolean gets nothing.
		thinking, ok := extraBody["thinking"].(map[string]any)
		if !ok {
			if _, exists := extraBody["thinking"]; exists {
				return
			}
			thinking = map[string]any{"type": "enabled"}
			extraBody["thinking"] = thinking
		}
		thinking["keep"] = "all"
	case "qwen":
		extraBody["preserve_thinking"] = true
	}
}

// vendorReasoningNeedsHistory reports whether the family's API requires the
// reasoning content it produced to be sent back with the history.
func vendorReasoningNeedsHistory(modelID string) bool {
	switch vendorFamily(modelID) {
	case "kimi", "mimo", "deepseek":
		return true
	}
	return false
}

// requestReasons reports whether the request as built asks the model to
// reason: its thinking control is on, or an effort other than "none" is
// sent. The user's own extra_body can turn either off.
func requestReasons(extraBody, userExtra map[string]any, effort any) bool {
	control, userSet := userExtra["thinking"]
	if !userSet {
		control = extraBody["thinking"]
	}
	on, off := thinkingState(control)
	if on {
		return true
	}
	if userSet && off {
		return false
	}
	if value, ok := userExtra["reasoning_effort"]; ok {
		effort = value
	}
	return effortSent(effort)
}

// thinkingState reports whether a thinking control asks for reasoning and
// whether it turns reasoning off.
func thinkingState(control any) (on, off bool) {
	switch value := control.(type) {
	case bool:
		return value, !value
	case map[string]any:
		return value["type"] != "disabled", value["type"] == "disabled"
	}
	return false, false
}

// effortSent reports whether a reasoning effort is sent and is not "none".
func effortSent(effort any) bool {
	value, ok := effort.(string)
	return ok && value != "" && value != "none"
}
