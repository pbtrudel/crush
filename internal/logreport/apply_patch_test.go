package logreport

import (
	"encoding/json"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func applyPatchCallPart(t *testing.T, id, patch string) message.ToolCall {
	t.Helper()
	input, err := json.Marshal(map[string]any{"patch": patch})
	require.NoError(t, err)
	return message.ToolCall{ID: id, Name: "apply_patch", Input: string(input)}
}

const applyPatchA = `*** Begin Patch
*** Update File: a.go
@@
 a
-b
+c
*** End Patch
`

const applyPatchA2 = `*** Begin Patch
*** Update File: a.go
@@
 a
-b
+B
*** End Patch
`

const applyPatchB = `*** Begin Patch
*** Update File: b.go
@@
 a
-b
+c
*** End Patch
`

// TestEditsCountApplyPatch pins that a patch counts in the same section as an
// edit: without it a session on a GPT model reports no edit failures at all,
// which is exactly the comparison the option exists to make.
func TestEditsCountApplyPatch(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.step("s1", newBuild, applyPatchCallPart(t, "p1", applyPatchA))
	log.result("s1", editResult("p1", "apply_patch", `{"error_kind":"not_read"}`, true))
	// The same file with a different patch is a retry with different text.
	log.step("s1", newBuild, applyPatchCallPart(t, "p2", applyPatchA2))
	log.result("s1", editResult("p2", "apply_patch", "", false))
	// The same patch after a view is a retry with the same text.
	log.step("s1", newBuild, applyPatchCallPart(t, "p3", applyPatchB))
	log.result("s1", editResult("p3", "apply_patch", `{"error_kind":"changed_since_read"}`, true))
	log.step("s1", newBuild, applyPatchCallPart(t, "p4", applyPatchB))
	log.result("s1", editResult("p4", "apply_patch", "", false))

	edits := log.report(Options{}).Edits
	require.Equal(t, 4, edits.Calls)
	require.Equal(t, 2, edits.Failed)
	require.InDelta(t, 0.5, edits.Rate, 1e-9)
	require.Equal(t, map[string]int{"changed_since_read": 1, "not_read": 1}, edits.Kinds)
	require.Equal(t, AfterRefusal{Refusals: 2, RetrySameText: 1, RetryNewText: 1}, edits.AfterRefusal)
}
