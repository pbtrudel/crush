package agent

import (
	"encoding/json"
	"slices"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/gitutil"
)

// changedFileTools are the tools whose input names the file they change. A
// rename is handled apart: it names a symbol, not a file.
var changedFileTools = []string{
	tools.EditToolName,
	tools.MultiEditToolName,
	tools.WriteToolName,
	tools.ReplaceSymbolToolName,
}

// stepChangedFiles lists the files one step changed, from the file_path
// input of its successful tool calls. It also catches an edit to a file that
// was already modified before the prompt started, which the working-tree
// comparison cannot see. A failed call changed nothing.
//
// A successful lsp_rename names no file: it records the symbol it renamed,
// as "(renamed <symbol>)", so the turn still counts as changed.
func stepChangedFiles(step fantasy.StepResult) []string {
	succeeded := make(map[string]struct{})
	for _, result := range step.Content.ToolResults() {
		if _, failed := result.Result.(fantasy.ToolResultOutputContentError); failed {
			continue
		}
		succeeded[result.ToolCallID] = struct{}{}
	}
	var files []string
	for _, call := range step.Content.ToolCalls() {
		if _, ok := succeeded[call.ToolCallID]; !ok {
			continue
		}
		if call.ToolName == tools.RenameToolName {
			if symbol := toolStringInput(call.Input, "symbol"); symbol != "" {
				files = append(files, "(renamed "+symbol+")")
			}
			continue
		}
		if !slices.Contains(changedFileTools, call.ToolName) {
			continue
		}
		if path := toolStringInput(call.Input, "file_path"); path != "" {
			files = append(files, path)
		}
	}
	return files
}

// toolStringInput reads one string field of a tool call's JSON input.
func toolStringInput(input, field string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(input), &parsed); err != nil {
		return ""
	}
	value, _ := parsed[field].(string)
	return value
}

// turnChangedFiles returns the files a prompt changed: the union of the
// working tree's comparison against the prompt's baseline and the tool calls
// of every run, sorted and deduplicated. The HEAD shas frame the comparison;
// both are empty when there was nothing to compare, as outside a repository
// or when one of the two snapshots failed.
func turnChangedFiles(call SessionAgentCall, end *gitutil.WorktreeState) (paths []string, headFrom, headTo string) {
	paths = slices.Clone(call.gateChangedFiles)
	if call.gateBaseline == nil || end == nil {
		slices.Sort(paths)
		return slices.Compact(paths), "", ""
	}
	gitPaths, headFrom, headTo := call.gateBaseline.Changed(*end)
	paths = append(paths, gitPaths...)
	slices.Sort(paths)
	return slices.Compact(paths), headFrom, headTo
}
