package logreport

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// editCallPart builds an edit call, or a multiedit call when olds holds
// more than one text.
func editCallPart(t *testing.T, id, path string, olds ...string) message.ToolCall {
	t.Helper()
	if len(olds) == 1 {
		input, err := json.Marshal(map[string]any{"file_path": path, "old_string": olds[0], "new_string": "x"})
		require.NoError(t, err)
		return message.ToolCall{ID: id, Name: "edit", Input: string(input)}
	}
	edits := []map[string]string{}
	for _, old := range olds {
		edits = append(edits, map[string]string{"old_string": old, "new_string": "x"})
	}
	input, err := json.Marshal(map[string]any{"file_path": path, "edits": edits})
	require.NoError(t, err)
	return message.ToolCall{ID: id, Name: "multiedit", Input: string(input)}
}

func editResult(id, name, metadata string, failed bool) message.ToolResult {
	return message.ToolResult{ToolCallID: id, Name: name, Metadata: metadata, IsError: failed}
}

func TestEditsFailuresKindsAndBatches(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.step("s1", newBuild,
		editCallPart(t, "e1", "a.go", "one"),
		editCallPart(t, "e2", "a.go", "two"),
		editCallPart(t, "m1", "b.go", "one", "two"),
		editCallPart(t, "m2", "b.go", "three", "four"),
	)
	log.result("s1", editResult("e1", "edit", `{"error_kind":"text_not_found"}`, true))
	log.result("s1", editResult("e2", "edit", `{"additions":1}`, false))
	// A batch that applied one edit and failed one: not a failed call.
	log.result("s1", editResult("m1", "multiedit", `{"edits_applied":1,"edits_failed":[{"index":2,"error_kind":"multiple_matches"}]}`, false))
	log.result("s1", editResult("m2", "multiedit", `{"edits_applied":0,"edits_failed":[{"index":1,"error_kind":"text_not_found"},{"index":2}],"error_kind":"text_not_found"}`, true))

	edits := log.report(Options{}).Edits
	require.Equal(t, 4, edits.Calls)
	require.Equal(t, 2, edits.Failed)
	require.InDelta(t, 0.5, edits.Rate, 1e-9)
	require.Equal(t, map[string]int{"text_not_found": 2}, edits.Kinds)
	require.Equal(t, 1, edits.BatchFailures, "only batches that otherwise succeeded")
	require.Equal(t, map[string]int{"multiple_matches": 1}, edits.BatchFailureKinds)
}

func TestEditsAfterReadGuardRefusal(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	refuse := func(id string) {
		log.result("s1", editResult(id, "edit", `{"error_kind":"not_read"}`, true))
	}
	// Retried with the same text after a view.
	log.step("s1", newBuild, editCallPart(t, "r1", "a.go", "alpha"))
	refuse("r1")
	log.step("s1", newBuild, message.ToolCall{ID: "v1", Name: "view", Input: `{"file_path":"a.go"}`})
	log.step("s1", newBuild, editCallPart(t, "r2", "a.go", "alpha"))
	log.result("s1", editResult("r2", "edit", "", false))
	// Retried with a different text, as a multiedit.
	log.step("s1", newBuild, editCallPart(t, "r3", "b.go", "beta"))
	log.result("s1", editResult("r3", "edit", `{"error_kind":"changed_since_read"}`, true))
	log.step("s1", newBuild, editCallPart(t, "r4", "b.go", "beta", "gamma"))
	log.result("s1", editResult("r4", "multiedit", "", false))
	// Moved on: a write to the same file.
	log.step("s1", newBuild, editCallPart(t, "r5", "c.go", "c"))
	refuse("r5")
	log.step("s1", newBuild, message.ToolCall{ID: "w1", Name: "write", Input: `{"file_path":"c.go","content":"x"}`})
	// Moved on: an edit of another file first; a write to another file
	// does not settle it.
	log.step("s1", newBuild, editCallPart(t, "r6", "d.go", "d"))
	refuse("r6")
	log.step("s1", newBuild, message.ToolCall{ID: "w2", Name: "write", Input: `{"file_path":"other.go","content":"x"}`})
	log.step("s1", newBuild, editCallPart(t, "r7", "e.go", "e"))
	log.result("s1", editResult("r7", "edit", "", false))
	// No retry before the session ended.
	log.step("s1", newBuild, editCallPart(t, "r8", "f.go", "f"))
	refuse("r8")
	// Another session's edit of the same file is not a retry.
	log.session("s2", "title")
	log.step("s2", newBuild, editCallPart(t, "x1", "f.go", "f"))
	log.result("s2", editResult("x1", "edit", "", false))

	require.Equal(t, AfterRefusal{Refusals: 5, RetrySameText: 1, RetryNewText: 1, MovedOn: 2, NoRetry: 1}, log.report(Options{}).Edits.AfterRefusal)
}

func TestEditsFailureRuns(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	outcome := func(session, id string, failed bool) {
		log.step(session, oldBuild, editCallPart(t, id, "a.go", "a"))
		log.result(session, editResult(id, "edit", "", failed))
	}
	for i, failed := range []bool{true, true, false, true, false, false} {
		outcome("s1", fmt.Sprintf("a%d", i), failed)
	}
	// A run ends with its session.
	log.session("s2", "title")
	for i := range 6 {
		outcome("s2", fmt.Sprintf("b%d", i), true)
	}
	// Results of other tools do not break a run.
	log.session("s3", "title")
	outcome("s3", "c1", true)
	log.step("s3", oldBuild, message.ToolCall{ID: "c2", Name: "bash"})
	log.result("s3", message.ToolResult{ToolCallID: "c2", Name: "bash"})
	outcome("s3", "c3", true)

	edits := log.report(Options{}).Edits
	require.Equal(t, FailureRuns{Runs: 4, Failed: 11, ByLength: [5]int{1, 2, 0, 0, 1}, Max: 6}, edits.Runs)
	require.Equal(t, map[string]int{"unstamped": 11}, edits.Kinds, "no stamps from the old build")
	require.Zero(t, edits.AfterRefusal.Refusals, "a refusal is only known by its stamp")
}

func TestEditsUntestedPaths(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	// A batch failure without a kind reads "unstamped" from a build that
	// never stamps its results.
	log.session("s1", "title")
	log.step("s1", oldBuild, editCallPart(t, "a1", "a.go", "one", "two"))
	log.result("s1", editResult("a1", "multiedit", `{"edits_applied":1,"edits_failed":[{"index":2}]}`, false))
	// The same shape reads "other" from a build the pass has seen stamp a
	// result in the window.
	log.session("s2", "title")
	log.step("s2", newBuild,
		editCallPart(t, "b1", "b.go", "one"),
		editCallPart(t, "b2", "b.go", "one", "two"),
	)
	log.result("s2", editResult("b1", "edit", `{"error_kind":"text_not_found"}`, true))
	log.result("s2", editResult("b2", "multiedit", `{"edits_applied":1,"edits_failed":[{"index":2}]}`, false))
	// A multiedit that succeeded but failed one edit closes a run.
	log.session("s3", "title")
	log.step("s3", oldBuild, editCallPart(t, "c1", "c.go", "one"))
	log.result("s3", editResult("c1", "edit", "", true))
	log.step("s3", oldBuild, editCallPart(t, "c2", "c.go", "one", "two"))
	log.result("s3", editResult("c2", "multiedit", `{"edits_applied":1,"edits_failed":[{"index":3}]}`, false))
	log.step("s3", oldBuild, editCallPart(t, "c3", "c.go", "one"))
	log.result("s3", editResult("c3", "edit", "", true))
	// A write result does not break a run.
	log.session("s4", "title")
	log.step("s4", oldBuild, editCallPart(t, "d1", "d.go", "one"))
	log.result("s4", editResult("d1", "edit", "", true))
	log.step("s4", oldBuild, message.ToolCall{ID: "d2", Name: "write", Input: `{"file_path":"d.go","content":"x"}`})
	log.result("s4", message.ToolResult{ToolCallID: "d2", Name: "write"})
	log.step("s4", oldBuild, editCallPart(t, "d3", "d.go", "one"))
	log.result("s4", editResult("d3", "edit", "", true))
	// A refused multiedit is a refusal, and the next multiedit of the same
	// file with the same texts retries with the same text.
	log.session("s5", "title")
	log.step("s5", newBuild, editCallPart(t, "e1", "e.go", "one", "two"))
	log.result("s5", editResult("e1", "multiedit", `{"error_kind":"not_read"}`, true))
	log.step("s5", newBuild, editCallPart(t, "e2", "e.go", "one", "two"))
	log.result("s5", editResult("e2", "multiedit", "", false))

	// By hand: 1+2+3+2+2 calls, 1+2+2+1 failures, and the batch failures
	// of s1, s2 and s3.
	edits := log.report(Options{}).Edits
	require.Equal(t, 10, edits.Calls)
	require.Equal(t, 6, edits.Failed)
	require.Equal(t, 3, edits.BatchFailures)
	require.Equal(t, map[string]int{"unstamped": 2, "other": 1}, edits.BatchFailureKinds)
	require.Equal(t, AfterRefusal{Refusals: 1, RetrySameText: 1}, edits.AfterRefusal)
	require.Equal(t, FailureRuns{Runs: 5, Failed: 6, ByLength: [5]int{4, 1, 0, 0, 0}, Max: 2}, edits.Runs)
}
