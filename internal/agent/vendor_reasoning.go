package agent

import "strings"

// vendorReasoningRule is what a vendor's API requires before it keeps the
// reasoning the model produced in earlier turns.
type vendorReasoningRule struct {
	// flag is the request field that asks for the reasoning to be kept.
	// Nested flags live inside the "thinking" object; the others are
	// top-level request fields.
	flag   string
	nested bool
	value  any
	// topLevelFallback sets the same field at the top level when the host
	// already sends "thinking" as something other than an object.
	topLevelFallback bool
	// needsHistory is set when the API requires the reasoning content back
	// in the history, so slimming must not drop it.
	needsHistory bool
}

// vendorReasoningRuleFor returns the rule of a model family, matched
// case-insensitively on a fragment of the model id.
//
// Z.AI documents thinking.clear_thinking ("Controls whether to clear
// reasoning_content from previous conversation turns"), true by default.
// Moonshot documents thinking.keep, null by default (earlier turns are
// ignored) and "all" to keep previous turns' reasoning_content. Alibaba
// documents a top-level preserve_thinking, which makes the model read
// historical assistant reasoning_content. MiMo documents no flag: it
// requires the reasoning_content field back in the history, as do Kimi and
// DeepSeek, and Crush already sends it for every reasoning part.
func vendorReasoningRuleFor(modelID string) vendorReasoningRule {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "glm-"):
		return vendorReasoningRule{
			flag: "clear_thinking", nested: true, value: false,
			topLevelFallback: true,
		}
	case strings.Contains(id, "kimi"):
		return vendorReasoningRule{flag: "keep", nested: true, value: "all", needsHistory: true}
	case strings.Contains(id, "qwen"):
		return vendorReasoningRule{flag: "preserve_thinking", value: true}
	case strings.Contains(id, "mimo"):
		return vendorReasoningRule{needsHistory: true}
	case strings.Contains(id, "deepseek"):
		return vendorReasoningRule{needsHistory: true}
	}
	return vendorReasoningRule{}
}

// addVendorReasoningFlag adds the family's keep-earlier-reasoning field to
// an OpenAI-compatible request. A nested flag is skipped when the provider
// already set "thinking" to something other than an object: Hyper sends a
// boolean, and rewriting that value would guess what the host accepts. GLM's
// field is set at the top level there instead, for hosts that forward fields
// they do not know; Kimi's has no top-level form, so it is left out.
func addVendorReasoningFlag(extraBody map[string]any, modelID string) {
	rule := vendorReasoningRuleFor(modelID)
	if rule.flag == "" {
		return
	}
	if !rule.nested {
		extraBody[rule.flag] = rule.value
		return
	}
	if current, exists := extraBody["thinking"]; exists {
		thinking, isObject := current.(map[string]any)
		if !isObject {
			if rule.topLevelFallback {
				extraBody[rule.flag] = rule.value
			}
			return
		}
		thinking[rule.flag] = rule.value
		return
	}
	extraBody["thinking"] = map[string]any{rule.flag: rule.value}
}

// vendorReasoningNeedsHistory reports whether the family's API requires the
// reasoning content it produced to be sent back with the history.
func vendorReasoningNeedsHistory(modelID string) bool {
	return vendorReasoningRuleFor(modelID).needsHistory
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
