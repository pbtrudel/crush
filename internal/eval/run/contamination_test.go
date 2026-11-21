package run

import (
	"encoding/json"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// call returns one message whose parts hold a single tool call with input.
func call(t *testing.T, input string) string {
	t.Helper()
	parts, err := json.Marshal([]map[string]any{{
		"type": "tool_call",
		"data": map[string]any{"name": "bash", "input": input},
	}})
	require.NoError(t, err)
	return string(parts)
}

// TestContaminatedCallsCommandMarkers checks design 3.7's command markers
// against the false positive of the DeepSeek A/A batch, where "gh pr" matched
// inside "through prefix", and against the commands the markers exist for.
func TestContaminatedCallsCommandMarkers(t *testing.T) {
	t.Parallel()

	merge := task.Source{Merge: "334c5d791d23"}
	markers := contaminationMarkers(&task.Task{Source: merge}, Paths{})

	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "inside a word", input: `grep -n "through prefix" pkg.go`, want: 0},
		{name: "a longer command", input: "gh print", want: 0},
		{name: "a hyphenated command", input: "gh-pr", want: 0},
		{name: "after a separator", input: "cd x && gh pr list", want: 1},
		{name: "in backticks", input: "`gh api repos`", want: 1},
		{name: "git's object reader", input: "git cat-file -p HEAD", want: 1},
		{name: "at the start", input: "gh pr view 51", want: 1},
		{name: "in a JSON command", input: `{"command":"gh pr view 51"}`, want: 1},
		{name: "ends at the closing quote", input: `{"command":"gh pr"}`, want: 1},
		{name: "after an escaped newline", input: `{"command":"cd x\ngh pr view 93"}`, want: 1},
		{name: "before a newline", input: "gh pr\nview", want: 1},
		{name: "before a separator", input: "gh pr;ls", want: 1},
		{name: "after a path", input: "/usr/bin/gh pr view", want: 1},
		{name: "with a double space", input: "gh  pr view", want: 1},
		{name: "under sudo", input: "sudo gh pr view", want: 1},
		{name: "in a substitution", input: "$(gh pr view)", want: 1},
		{name: "in a nested field", input: `{"args":{"command":"gh pr view 51"}}`, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := contaminatedCalls(call(t, test.input), markers)
			require.Equal(t, test.want, got, test.input)
		})
	}
}

// TestContaminatedCallsSubstringMarkers checks that the sha, URL and path
// markers still match anywhere, since they name no command.
func TestContaminatedCallsSubstringMarkers(t *testing.T) {
	t.Parallel()

	markers := contaminationMarkers(
		&task.Task{Source: task.Source{Merge: "334c5d791d23"}},
		Paths{Suite: "/suite/evals", Repo: "/repo", Checkout: "/repo"},
	)

	for _, input := range []string{
		"git show 334c5d791d23",
		"open xpbtrudel/crush/pully",
		"ls /suite/evals/tasks",
		`{"command":"git show 334c5d791d23"}`,
	} {
		require.Equal(t, 1, contaminatedCalls(call(t, input), markers), input)
	}
}

// TestContaminatedCallsCountsEachCall checks that every contaminated tool
// call of a message counts, as design 3.7 asks.
func TestContaminatedCallsCountsEachCall(t *testing.T) {
	t.Parallel()

	markers := contaminationMarkers(&task.Task{}, Paths{Suite: "evals", Repo: "."})
	pr := `{"type":"tool_call","data":{"name":"bash","input":"gh pr view 1"}}`
	text := `{"type":"text","data":{"text":"gh pr view 1"}}`
	cat := `{"type":"tool_call","data":{"name":"bash","input":"git cat-file"}}`
	parts := "[" + pr + "," + text + "," + cat + "]"
	require.Equal(t, 2, contaminatedCalls(parts, markers),
		"text parts do not count")
}

// TestContaminatedCallsHostPaths checks the markers of the paths a jailed
// run hides: naming the host checkout, a sibling worktree or another batch
// counts, while naming the run's own directory does not.
func TestContaminatedCallsHostPaths(t *testing.T) {
	t.Parallel()

	paths := Paths{
		Suite:     "/host/crush/evals",
		Repo:      "/host/crush",
		Checkout:  "/host/crush",
		Worktrees: "/host/.crush-worktrees",
		Batches:   "/state/crush-eval/batches",
		Own:       "/state/crush-eval/batches/b1/runs/pr-0010-r1",
	}
	markers := contaminationMarkers(&task.Task{}, paths)

	for _, input := range []string{
		"ls /host/crush/internal/agent",
		"cd /host/.crush-worktrees/crush-other && git log --all",
		"cat /state/crush-eval/batches/b2/results.jsonl",
		`{"command":"cat /state/crush-eval/batches/b2/runs/pr-0010/src/x.go"}`,
	} {
		require.Equal(t, 1, contaminatedCalls(call(t, input), markers), input)
	}

	// The run's own directory sits under the batches marker and stays out
	// of the scan, since the child names its own checkout all the time.
	for _, input := range []string{
		"ls /state/crush-eval/batches/b1/runs/pr-0010-r1/src",
		`{"command":"cd /state/crush-eval/batches/b1/runs/pr-0010-r1/src && go test ./pkg"}`,
		"go build ./...",
	} {
		require.Zero(t, contaminatedCalls(call(t, input), markers), input)
	}
}
