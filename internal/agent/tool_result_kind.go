package agent

import (
	"strings"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
)

// sanitizedToolCallResult replaces the result of a call whose arguments
// were not valid JSON.
const sanitizedToolCallResult = "Tool call failed: arguments were not valid JSON. Please check your tool call format and try again."

// libraryErrorKinds maps the start of each error charm.land/fantasy writes
// for a call it could not run to its kind. The wording is the library's,
// so tool_result_kind_test.go produces every one of them with the pinned
// version: a reworded message fails that test instead of the report.
var libraryErrorKinds = []struct {
	prefix string
	kind   tools.ErrorKind
}{
	{prefix: "tool not found: ", kind: tools.ErrorKindUnknownTool},
	{prefix: "invalid JSON input: ", kind: tools.ErrorKindInvalidArguments},
	{prefix: "missing required parameter: ", kind: tools.ErrorKindInvalidArguments},
	{prefix: "invalid parameters: ", kind: tools.ErrorKindInvalidArguments},
}

// stampLibraryErrorKind records the kind of an error the provider library
// wrote for a call it could not run. A result that is not an error, or
// that already carries a kind, is returned unchanged.
func stampLibraryErrorKind(result message.ToolResult) message.ToolResult {
	if !result.IsError || tools.MetadataErrorKind(result.Metadata) != "" {
		return result
	}
	for _, lib := range libraryErrorKinds {
		if strings.HasPrefix(result.Content, lib.prefix) {
			result.Metadata = tools.SetErrorKind(result.Metadata, lib.kind)
			break
		}
	}
	return result
}
