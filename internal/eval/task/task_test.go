package task_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

const plainFile = `schema: 1
id: pr-0007
source:
  repo: pbtrudel/crush
  pr: 7
  merge: 1111111111111111111111111111111111111111
  base: 2222222222222222222222222222222222222222
kind: plain
why: |
  Every launch shows an update.
prompt: |
  Every launch shows an update.
grade:
  packages: [./internal/update]
  tests: [TestCheckForUpdate_Fork]
  exclude: []
  hidden:
    - {path: internal/update/update_test.go, blob: 3333333333333333333333333333333333333333}
  deleted: []
  timeout: 10m
limits:
  max_steps: 120
  timeout: 25m
vetted: {}
risks: []
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// git runs one git command with the user's config kept out of it.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{
		"-C", dir,
		"-c", "user.name=Test",
		"-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func TestLoad(t *testing.T) {
	t.Parallel()

	t.Run("reads every field", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		writeFile(t, path, plainFile)

		tk, err := task.Load(path)
		require.NoError(t, err)
		require.Equal(t, 1, tk.Schema)
		require.Equal(t, "pr-0007", tk.ID)
		require.Equal(t, "pbtrudel/crush", tk.Source.Repo)
		require.Equal(t, 7, tk.Source.PR)
		require.Equal(t, strings.Repeat("1", 40), tk.Source.Merge)
		require.Equal(t, strings.Repeat("2", 40), tk.Source.Base)
		require.Equal(t, "plain", tk.Kind)
		require.Equal(t, "Every launch shows an update.\n", tk.Why)
		require.Equal(t, "Every launch shows an update.\n", tk.Prompt)
		require.Equal(t, []string{"./internal/update"}, tk.Grade.Packages)
		require.Equal(t, []string{"TestCheckForUpdate_Fork"}, tk.Grade.Tests)
		require.Equal(t, "internal/update/update_test.go", tk.Grade.Hidden[0].Path)
		require.Equal(t, strings.Repeat("3", 40), tk.Grade.Hidden[0].Blob)
		require.Equal(t, 10*time.Minute, time.Duration(tk.Grade.Timeout))
		require.Equal(t, 120, tk.Limits.MaxSteps)
		require.Equal(t, 25*time.Minute, time.Duration(tk.Limits.Timeout))
	})

	t.Run("fills the default limits", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		writeFile(t, path, strings.Replace(plainFile, "limits:\n  max_steps: 120\n  timeout: 25m\n", "", 1))

		tk, err := task.Load(path)
		require.NoError(t, err)
		require.Equal(t, 120, tk.Limits.MaxSteps)
		require.Equal(t, 25*time.Minute, time.Duration(tk.Limits.Timeout))
	})

	t.Run("rejects an unknown field", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		writeFile(t, path, plainFile+"veted: {}\n")

		_, err := task.Load(path)
		require.ErrorContains(t, err, "field veted not found")
	})

	t.Run("rejects a malformed duration", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		writeFile(t, path, strings.Replace(plainFile, "timeout: 10m", "timeout: 10 minutes", 1))

		_, err := task.Load(path)
		require.ErrorContains(t, err, `in duration "10 minutes"`)
	})
}

func TestLoadDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ten := strings.NewReplacer("pr-0007", "pr-0010", "pr: 7", "pr: 10").Replace(plainFile)
	two := strings.NewReplacer("pr-0007", "pr-0002", "pr: 7", "pr: 2").Replace(plainFile)
	writeFile(t, filepath.Join(dir, "pr-0010.yaml"), ten)
	writeFile(t, filepath.Join(dir, "pr-0002.yaml"), two)

	tasks, err := task.LoadDir(dir)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, "pr-0002", tasks[0].ID)
	require.Equal(t, "pr-0010", tasks[1].ID)
}

const brokenFile = `schema: 2
id: pr-7
source:
  repo: pbtrudel/crush
  pr: 0
  merge: ""
  base: ""
kind: other
why: ""
prompt: ""
interface: "The tests call X."
grade:
  packages: []
  tests: []
  hidden: []
  timeout: 0s
limits: {}
`

func TestValidateReportsEveryProblem(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pr-0007.yaml")
	writeFile(t, path, brokenFile)

	tk, err := task.Load(path)
	require.NoError(t, err)

	err = tk.Validate()
	require.Error(t, err)
	for _, want := range []string{
		"schema is 2, want 1",
		`id "pr-7" does not match pr-NNNN`,
		`id "pr-7" does not match file name "pr-0007.yaml"`,
		"source.pr is 0",
		`kind "other"`,
		"prompt is empty",
		"grade.packages is empty",
		"grade.tests is empty",
		"grade.hidden is empty",
		"grade.timeout must be positive",
	} {
		require.ErrorContains(t, err, want)
	}
}

func TestValidateKind(t *testing.T) {
	t.Parallel()

	t.Run("plain task with an interface paragraph", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		writeFile(t, path, strings.Replace(plainFile, "kind: plain\n", "kind: plain\ninterface: |\n  The tests call X.\n", 1))

		tk, err := task.Load(path)
		require.NoError(t, err)
		require.ErrorContains(t, tk.Validate(), "plain task has an interface paragraph")
	})

	t.Run("interface task without an interface paragraph", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		writeFile(t, path, strings.Replace(plainFile, "kind: plain\n", "kind: interface\n", 1))

		tk, err := task.Load(path)
		require.NoError(t, err)
		require.ErrorContains(t, tk.Validate(), "interface task needs an interface paragraph")
	})
}

func TestValidateHidden(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pr-0007.yaml")
	entry := "{path: internal/update/update_test.go, blob: " + strings.Repeat("3", 40) + "}"
	writeFile(t, path, strings.Replace(plainFile, entry, "{path: /etc/passwd, blob: abc}", 1))

	tk, err := task.Load(path)
	require.NoError(t, err)

	err = tk.Validate()
	require.ErrorContains(t, err, "grade.hidden[0].path")
	require.ErrorContains(t, err, "grade.hidden[0].blob")
}

// TestValidateTestNames checks the names the grade selects: they are joined
// into one -run pattern, so a name that is not a plain Test function would
// change which tests run.
func TestValidateTestNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		from    string
		to      string
		wantErr string
	}{
		{
			name:    "pattern in tests",
			from:    "tests: [TestCheckForUpdate_Fork]",
			to:      "tests: ['TestCheck.*']",
			wantErr: "is not a test name",
		},
		{
			name:    "lowercase test name",
			from:    "tests: [TestCheckForUpdate_Fork]",
			to:      "tests: [testCheck]",
			wantErr: "is not a test name",
		},
		{
			name:    "empty name",
			from:    "tests: [TestCheckForUpdate_Fork]",
			to:      "tests: ['']",
			wantErr: "is not a test name",
		},
		{
			name:    "pattern in exclude",
			from:    "exclude: []",
			to:      "exclude: ['TestSkip|TestOther']",
			wantErr: "grade.exclude[0]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "pr-0007.yaml")
			writeFile(t, path, strings.Replace(plainFile, tt.from, tt.to, 1))

			tk, err := task.Load(path)
			require.NoError(t, err)
			require.ErrorContains(t, tk.Validate(), tt.wantErr)
		})
	}
}

// TestValidateDeleted checks that the files the grade deletes are inside the
// checkout: the grade removes them from a tree the model wrote.
func TestValidateDeleted(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pr-0007.yaml")
	writeFile(t, path, strings.Replace(plainFile, "  deleted: []", "  deleted: [../../etc/passwd, internal/update/old_test.go]", 1))

	tk, err := task.Load(path)
	require.NoError(t, err)
	err = tk.Validate()
	require.ErrorContains(t, err, "grade.deleted[0]")
	require.NotContains(t, err.Error(), "grade.deleted[1]")
}

func TestValidateRepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "README.md"), "base\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, filepath.Join(dir, "internal", "shell", "background_test.go"), "package shell\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "merge")

	merge := git(t, dir, "rev-parse", "HEAD")
	base := git(t, dir, "rev-parse", "HEAD^1")
	blob := git(t, dir, "rev-parse", "HEAD:internal/shell/background_test.go")

	newTask := func() *task.Task {
		return &task.Task{
			ID:     "pr-0051",
			Source: task.Source{Repo: "pbtrudel/crush", PR: 51, Merge: merge, Base: base},
			Kind:   "interface",
			Prompt: "Fix it.",
			Grade:  task.Grade{Hidden: []task.Hidden{{Path: "internal/shell/background_test.go", Blob: blob}}},
		}
	}

	t.Run("passes", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, newTask().ValidateRepo(dir))
	})

	t.Run("wrong blob", func(t *testing.T) {
		t.Parallel()
		tk := newTask()
		tk.Grade.Hidden[0].Blob = strings.Repeat("0", 40)
		require.ErrorContains(t, tk.ValidateRepo(dir), "merge has "+blob)
	})

	t.Run("base is not merge^1", func(t *testing.T) {
		t.Parallel()
		tk := newTask()
		tk.Source.Base = merge
		require.ErrorContains(t, tk.ValidateRepo(dir), "is not merge^1")
	})

	t.Run("merge does not resolve", func(t *testing.T) {
		t.Parallel()
		tk := newTask()
		tk.Source.Merge = strings.Repeat("0", 40)
		require.ErrorContains(t, tk.ValidateRepo(dir), "does not resolve")
	})

	t.Run("hidden path is missing at the merge", func(t *testing.T) {
		t.Parallel()
		tk := newTask()
		tk.Grade.Hidden[0].Path = "internal/shell/missing_test.go"
		require.ErrorContains(t, tk.ValidateRepo(dir), "does not exist at merge")
	})
}

func TestPrompt(t *testing.T) {
	t.Parallel()

	t.Run("plain task", func(t *testing.T) {
		t.Parallel()
		tk := &task.Task{Prompt: "Every launch shows an update.\n", Why: "Why text that is never sent."}
		require.Equal(t, "Every launch shows an update.\n\nMake this change.", task.Prompt(tk, "Make this change.\n"))
	})

	t.Run("interface task", func(t *testing.T) {
		t.Parallel()
		tk := &task.Task{
			Prompt:    "Every launch shows an update.\n",
			Interface: "Interface: the tests call X.\n",
			Why:       "Why text that is never sent.",
		}
		got := task.Prompt(tk, "Make this change.\n")
		require.Equal(t, "Every launch shows an update.\n\nInterface: the tests call X.\n\nMake this change.", got)
		require.NotContains(t, got, "never sent")
	})
}

func TestValidateVetted(t *testing.T) {
	t.Parallel()

	filled := strings.Replace(plainFile, "vetted: {}\n", `vetted:
  date: 2026-11-25
  go: go1.27.2
  base_fails: 5/5
  merge_passes: 5/5
  grade_seconds: {touched: 1, packages: 2}
`, 1)

	t.Run("accepts a filled block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		writeFile(t, path, filled)

		tk, err := task.Load(path)
		require.NoError(t, err)
		require.NoError(t, tk.Validate())
	})

	t.Run("rejects a malformed block", func(t *testing.T) {
		t.Parallel()
		broken := strings.NewReplacer("2026-11-25", "25/11/2026", "base_fails: 5/5", "base_fails: five", "touched: 1", "touched: -1").Replace(filled)
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		writeFile(t, path, broken)

		tk, err := task.Load(path)
		require.NoError(t, err)
		err = tk.Validate()
		require.ErrorContains(t, err, "vetted.date")
		require.ErrorContains(t, err, "vetted.base_fails")
		require.ErrorContains(t, err, "vetted.grade_seconds.touched")
	})
}
