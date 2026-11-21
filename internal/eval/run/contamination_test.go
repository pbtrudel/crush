package run

import (
	"encoding/json"
	"strings"
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

	merge := task.Source{Merge: "334c5d791d23", Repo: "pbtrudel/crush"}
	markers := contaminationMarkers(&task.Task{Source: merge}, Paths{})

	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "inside a word", input: `grep -n "through prefix" pkg.go`, want: 0},
		{name: "a longer command", input: "gh print", want: 0},
		{name: "a hyphenated command", input: "gh-pr", want: 0},
		{name: "after a separator", input: "cd x && gh pr list -R pbtrudel/crush", want: 1},
		{name: "in backticks", input: "`gh api repos/pbtrudel/crush`", want: 1},
		{name: "git's object reader", input: "git cat-file -p HEAD", want: 1},
		{name: "at the start", input: "gh pr view 51 -R pbtrudel/crush", want: 1},
		{name: "in a JSON command", input: `{"command":"gh pr view 51 -R pbtrudel/crush"}`, want: 1},
		{name: "ends at the closing quote", input: `{"command":"git cat-file"}`, want: 1},
		{name: "after an escaped newline", input: `{"command":"cd x\ngh pr view 93 -R pbtrudel/crush"}`, want: 1},
		{name: "before a newline", input: "gh pr -R pbtrudel/crush\nview", want: 1},
		{name: "before a separator", input: "gh pr -R pbtrudel/crush;ls", want: 1},
		{name: "after a path", input: "/usr/bin/gh pr view -R pbtrudel/crush", want: 1},
		{name: "with a double space", input: "gh  pr view -R pbtrudel/crush", want: 1},
		{name: "under sudo", input: "sudo gh pr view -R pbtrudel/crush", want: 1},
		{name: "in a substitution", input: "$(gh pr view -R pbtrudel/crush)", want: 1},
		{name: "in a nested field", input: `{"args":{"command":"gh pr view 51 -R pbtrudel/crush"}}`, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := len(contaminatedCalls(call(t, test.input), markers))
			require.Equal(t, test.want, got, test.input)
		})
	}
}

// TestContaminatedCallsSubstringMarkers checks that the sha, URL and path
// markers still match anywhere, since they name no command.
func TestContaminatedCallsSubstringMarkers(t *testing.T) {
	t.Parallel()

	markers := contaminationMarkers(
		&task.Task{Source: task.Source{Merge: "334c5d791d23", Repo: "pbtrudel/crush"}},
		Paths{Suite: "/suite/evals", Repo: "/repo", Checkout: "/repo"},
	)

	for _, input := range []string{
		"git show 334c5d791d23",
		"open xpbtrudel/crush/pully",
		"ls /suite/evals/tasks",
		`{"command":"git show 334c5d791d23"}`,
	} {
		require.Equal(t, 1, len(contaminatedCalls(call(t, input), markers)), input)
	}
}

// TestContaminatedCallsCountsEachCall checks that every contaminated tool
// call of a message counts, as design 3.7 asks.
func TestContaminatedCallsCountsEachCall(t *testing.T) {
	t.Parallel()

	markers := contaminationMarkers(
		&task.Task{Source: task.Source{Repo: "pbtrudel/crush"}},
		Paths{Suite: "evals", Repo: "."},
	)
	pr := `{"type":"tool_call","data":{"name":"bash","input":"gh pr view 1 -R pbtrudel/crush"}}`
	text := `{"type":"text","data":{"text":"gh pr view 1"}}`
	cat := `{"type":"tool_call","data":{"name":"bash","input":"git cat-file"}}`
	parts := "[" + pr + "," + text + "," + cat + "]"
	require.Equal(t, 2, len(contaminatedCalls(parts, markers)),
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
		require.Equal(t, 1, len(contaminatedCalls(call(t, input), markers)), input)
	}

	// The run's own directory sits under the batches marker and stays out
	// of the scan, since the child names its own checkout all the time.
	for _, input := range []string{
		"ls /state/crush-eval/batches/b1/runs/pr-0010-r1/src",
		`{"command":"cd /state/crush-eval/batches/b1/runs/pr-0010-r1/src && go test ./pkg"}`,
		"go build ./...",
	} {
		require.Zero(t, len(contaminatedCalls(call(t, input), markers)), input)
	}
}

// TestContaminatedCallsGhNeedsRepository checks that a gh command counts
// only when the same input names this repository: the checkout ships "gh pr
// create" in its own bash tool description, so a plain publish attempt is
// no contact with the answer.
func TestContaminatedCallsGhNeedsRepository(t *testing.T) {
	t.Parallel()

	markers := contaminationMarkers(
		&task.Task{Source: task.Source{Repo: "pbtrudel/crush"}},
		Paths{Remotes: []string{"someone/crush"}},
	)

	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "the checkout's own recipe", input: "gh pr create --fill", want: 0},
		{name: "an unrelated repository", input: "gh pr list -R someone-else/crush", want: 0},
		{name: "the user's own account", input: "gh api user", want: 0},
		{name: "this repository named by -R", input: "gh pr view 123 -R pbtrudel/crush", want: 1},
		{name: "this repository named in the path", input: "gh api repos/pbtrudel/crush/pulls/5", want: 1},
		{name: "the upstream repository", input: "gh pr view 1234 -R charmbracelet/crush", want: 1},
		{name: "the upstream repository in the path", input: "gh api repos/charmbracelet/crush/pulls/9", want: 1},
		{name: "a remote of the checkout", input: "gh pr view 7 -R someone/crush", want: 1},
		{name: "git's object reader", input: "git cat-file -p HEAD", want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := len(contaminatedCalls(call(t, test.input), markers))
			require.Equal(t, test.want, got, test.input)
		})
	}
}

// TestContaminatedCallsKeepTheMarkers checks that a contaminated call records
// which markers it named and a short excerpt of its input, so a flagged run
// can be diagnosed after its session database is gone.
func TestContaminatedCallsKeepTheMarkers(t *testing.T) {
	t.Parallel()

	markers := contaminationMarkers(
		&task.Task{Source: task.Source{Repo: "pbtrudel/crush"}},
		Paths{Checkout: "/host/crush"},
	)
	found := contaminatedCalls(
		call(t, `{"command":"git cat-file -p /host/crush/internal/agent/x.go"}`),
		markers,
	)
	require.Len(t, found, 1)
	require.Equal(t, []string{"git cat-file", "/host/crush"}, found[0].Markers)
	require.Equal(t, `{"command":"git cat-file -p /host/crush/internal/agent/x.go"}`, found[0].Excerpt)
}

// TestExcerpt checks the excerpt a contaminated call keeps: it is short, it
// is centred on the match, and a credential never enters it.
func TestExcerpt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "a short command", input: "git cat-file -p HEAD", want: "git cat-file -p HEAD"},
		{
			name:  "a bearer token",
			input: `curl -H 'Authorization: Bearer abcdefghijklmnop'`,
			want:  `curl -H 'Authorization: Bearer [redacted]'`,
		},
		{
			name:  "a basic authorization",
			input: `curl -H 'Authorization: Basic dXNlcjpwYXNzd29yZA==' https://api.example.com`,
			want:  `curl -H 'Authorization: Basic [redacted]' https://api.example.com`,
		},
		{
			name:  "an ordinary word after basic",
			input: "basic usage of the tool",
			want:  "basic usage of the tool",
		},
		{
			name:  "an api key field",
			input: `{"api_key":"sk-abcdefghijklmnopqrst"}`,
			want:  `{"api_key":"[redacted]"}`,
		},
		{
			name:  "a github token in a url",
			input: "git clone https://ghp_abcdefghijklmnopqrstuvwxyz0123456789@github.com/x",
			want:  "git clone https://[redacted]@github.com/x",
		},
		{
			name:  "a user and password in a url",
			input: "git clone https://user:s3cret@github.com/x/y.git",
			want:  "git clone https://[redacted]@github.com/x/y.git",
		},
		{
			name:  "curl user and password",
			input: "curl -u user:pass https://x.example/pulls",
			want:  "curl -u [redacted] https://x.example/pulls",
		},
		{
			name:  "a google api key",
			input: "echo AIzaSyA1234567890abcdefghijklmnopqrstuv",
			want:  "echo [redacted]",
		},
		{
			name:  "a gitlab token",
			input: "echo glpat-abcdefghij1234567890",
			want:  "echo [redacted]",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, excerpt(test.input, nil))
		})
	}

	t.Run("a long input", func(t *testing.T) {
		t.Parallel()
		got := excerpt(strings.Repeat("a", 3*maxExcerpt), nil)
		require.Len(t, got, maxExcerpt)
	})

	t.Run("a match at the end", func(t *testing.T) {
		t.Parallel()
		got := excerpt(strings.Repeat("y", 300)+" gh pr", []string{"gh pr"})
		require.Len(t, got, maxExcerpt)
		require.Contains(t, got, "gh pr")
	})

	t.Run("a long input centred on the match", func(t *testing.T) {
		t.Parallel()
		input := strings.Repeat("x", 300) + " gh pr view 5 -R pbtrudel/crush " + strings.Repeat("y", 300)
		got := excerpt(input, []string{"gh pr"})
		require.Len(t, got, maxExcerpt)
		require.Equal(t, maxExcerpt/2, strings.Index(got, "gh pr"),
			"the match sits in the middle of the excerpt")
		require.Contains(t, got, "gh pr view 5")
	})
}

// TestContaminatedCallsCutOnlyTheRunsOwnPath checks that the run's own
// directory is cut out of the input only where it ends: a sibling run whose
// name starts the same way, and a path that climbs back out of the run with
// "..", both count.
func TestContaminatedCallsCutOnlyTheRunsOwnPath(t *testing.T) {
	t.Parallel()

	paths := Paths{
		Batches: "/state/crush-eval/batches",
		Own:     "/state/crush-eval/batches/b1/runs/pr-0010-r1",
	}
	markers := contaminationMarkers(&task.Task{}, paths)

	for _, input := range []string{
		"ls /state/crush-eval/batches/b1/runs/pr-0010-r10/src",
		"cat /state/crush-eval/batches/b1/runs/pr-0010-r1/../../b2/x",
		`{"command":"cd /state/crush-eval/batches/b1/runs/pr-0010-r1/../pr-0010-r2 && go test"}`,
	} {
		require.Equal(t, 1, len(contaminatedCalls(call(t, input), markers)), input)
	}

	for _, input := range []string{
		"ls /state/crush-eval/batches/b1/runs/pr-0010-r1/src",
		"cd /state/crush-eval/batches/b1/runs/pr-0010-r1",
	} {
		require.Zero(t, len(contaminatedCalls(call(t, input), markers)), input)
	}
}

// TestRepoName checks the owner/repo a git remote URL names, the way a
// checkout of this repository writes origin and upstream.
func TestRepoName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		remote string
		want   string
	}{
		{name: "https", remote: "https://github.com/charmbracelet/crush.git", want: "charmbracelet/crush"},
		{name: "https without the suffix", remote: "https://github.com/pbtrudel/crush", want: "pbtrudel/crush"},
		{name: "scp-like", remote: "git@github.com:pbtrudel/crush.git", want: "pbtrudel/crush"},
		{name: "ssh", remote: "ssh://git@github.com/charmbracelet/crush.git", want: "charmbracelet/crush"},
		{name: "a trailing slash", remote: "https://github.com/pbtrudel/crush/", want: "pbtrudel/crush"},
		{name: "a path of its own", remote: "/home/someone/crush.git", want: ""},
		{name: "a subgroup", remote: "https://gitlab.com/group/sub/crush.git", want: ""},
		{name: "nothing", remote: "", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, repoName(test.remote))
		})
	}
}
