package agent

import (
	"context"
	"encoding/json"

	"charm.land/fantasy"
	"charm.land/fantasy/jsonrepair"

	"github.com/charmbracelet/crush/internal/agent/tools"
)

// toolNameAliases maps a tool name models use for a Crush tool onto the name
// the tool is registered under. It costs nothing until a call misses.
var toolNameAliases = map[string]string{
	"todo": tools.TodosToolName,
}

// repairToolCall renames a call that used an alias and mirrors fantasy's own
// repair for malformed JSON. fantasy runs jsonrepair only when no repair
// function is wired, so wiring this one without repeating that step would
// lose it. A nil return means nothing changed and the original validation
// error stands.
func repairToolCall(_ context.Context, opts fantasy.ToolCallRepairOptions) (*fantasy.ToolCallContent, error) {
	repaired := opts.OriginalToolCall
	changed := false
	if target, ok := toolNameAliases[repaired.ToolName]; ok && toolAvailable(opts.AvailableTools, target) {
		repaired.ToolName = target
		changed = true
	}
	// jsonrepair also reformats valid JSON, so it runs only on input that is
	// not valid, which is what it exists for and what keeps a repaired alias
	// call byte-identical.
	if !json.Valid([]byte(repaired.Input)) {
		if fixed, err := jsonrepair.RepairJSON(repaired.Input); err == nil && fixed != repaired.Input {
			repaired.Input = fixed
			changed = true
		}
	}
	if !changed {
		return nil, nil
	}
	return &repaired, nil
}

// toolAvailable reports whether the step offers a tool under name.
func toolAvailable(available []fantasy.AgentTool, name string) bool {
	for _, tool := range available {
		if tool.Info().Name == name {
			return true
		}
	}
	return false
}
