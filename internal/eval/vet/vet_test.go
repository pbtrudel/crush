package vet_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/charmbracelet/crush/internal/eval/vet"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("passes the rules", func(t *testing.T) {
		t.Parallel()
		repo, blob := vetRepo(t, assertAnswer2)
		tk := taskAt(blob, "TestAnswer")

		got, err := vet.Run(context.Background(), tk, repo, t.TempDir(), 2, goEnv(t))
		require.NoError(t, err)
		require.Equal(t, "2/2", got.BaseFails)
		require.Equal(t, "2/2", got.MergePasses)
		require.Empty(t, got.Flaky)
		require.GreaterOrEqual(t, got.GradeSeconds.Touched, 0)
		require.GreaterOrEqual(t, got.GradeSeconds.Packages, 0)
	})

	t.Run("touched test passes at base", func(t *testing.T) {
		t.Parallel()
		repo, blob := vetRepo(t, alwaysPassing)
		tk := taskAt(blob, "TestAlways")

		_, err := vet.Run(context.Background(), tk, repo, t.TempDir(), 2, goEnv(t))
		require.ErrorContains(t, err, "touched tests passed at base")
	})
}

func TestWriteVetted(t *testing.T) {
	t.Parallel()

	before := "limits:\n  max_steps: 120\n  timeout: 25m\n"
	after := "risks: []\n"
	without := strings.Replace(taskFile, "vetted: {}\n", "", 1)

	want := task.Vetted{
		Date:        "2026-11-25",
		Go:          "go1.27.2",
		BaseFails:   "5/5",
		MergePasses: "5/5",
		GradeSeconds: task.GradeSeconds{
			Touched:  1,
			Packages: 2,
		},
	}

	t.Run("appends a missing block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		require.NoError(t, os.WriteFile(path, []byte(without), 0o644))

		v := want
		require.NoError(t, vet.WriteVetted(path, v))
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		t.Log(string(got))
		require.True(t, strings.HasPrefix(string(got), without))
		require.Equal(t, 1, strings.Count(string(got), "vetted:"))

		tk, err := task.Load(path)
		require.NoError(t, err)
		require.Equal(t, v, tk.Vetted)
	})

	t.Run("keeps a column-0 comment after the block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		text := "schema: 1\nid: pr-0007\nvetted: {}\n\n# Why the risks are what they are.\nrisks: []\n"
		require.NoError(t, os.WriteFile(path, []byte(text), 0o644))

		require.NoError(t, vet.WriteVetted(path, want))
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(got), "schema: 1\nid: pr-0007\nvetted:\n"))
		require.True(t, strings.HasSuffix(string(got), "\n\n# Why the risks are what they are.\nrisks: []\n"),
			"the blank line and the column-0 comment stay after the block")

		tk, err := task.Load(path)
		require.NoError(t, err)
		require.Equal(t, want, tk.Vetted)
	})

	t.Run("replaces a block in place", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0007.yaml")
		require.NoError(t, os.WriteFile(path, []byte(taskFile), 0o644))

		v := want
		require.NoError(t, vet.WriteVetted(path, v))
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		text := string(got)
		require.Equal(t, 1, strings.Count(text, "vetted:"))
		require.True(t, strings.HasPrefix(text, taskFile[:strings.Index(taskFile, "vetted:")]))
		require.True(t, strings.HasSuffix(text, after))
		require.Contains(t, text, before)

		tk, err := task.Load(path)
		require.NoError(t, err)
		require.Equal(t, v, tk.Vetted)

		v.Date = "2026-11-26"
		require.NoError(t, vet.WriteVetted(path, v))
		again, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(string(again), "vetted:"))
		require.Contains(t, string(again), "2026-11-26")
		require.True(t, strings.HasSuffix(string(again), after))
	})
}

func TestOverused(t *testing.T) {
	t.Parallel()

	mk := func(id string, packages ...string) *task.Task {
		return &task.Task{ID: id, Grade: task.Grade{Packages: packages}}
	}
	tasks := []*task.Task{
		mk("pr-0001", "./internal/agent"),
		mk("pr-0002", "./internal/agent", "./internal/ui"),
		mk("pr-0003", "./internal/agent"),
		mk("pr-0004", "./internal/agent"),
		mk("pr-0005", "./internal/agent"),
		mk("pr-0006", "./internal/agent"),
		mk("pr-0007", "./internal/agent"),
		mk("pr-0008", "./internal/ui"),
	}

	got := vet.Overused(tasks)
	require.Len(t, got, 1)
	require.Equal(t, "./internal/agent", got[0].Package)
	require.Len(t, got[0].Tasks, 7)
}

const (
	goMod         = "module example.com/vet\n\ngo 1.27.0\n"
	answer1       = "package p\n\nfunc Answer() int { return 1 }\n"
	answer2       = "package p\n\nfunc Answer() int { return 2 }\n"
	assertAnswer2 = "package p\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 2 {\n\t\tt.Fatalf(\"Answer() = %d\", Answer())\n\t}\n}\n"
	alwaysPassing = "package p\n\nimport \"testing\"\n\nfunc TestAlways(t *testing.T) {}\n"
)

const taskFile = `schema: 1
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
  packages: [./p]
  tests: [TestAnswer]
  exclude: []
  hidden:
    - {path: p/p_test.go, blob: 3333333333333333333333333333333333333333}
  deleted: []
  timeout: 10m
limits:
  max_steps: 120
  timeout: 25m
vetted: {}
risks: []
`

// vetRepo builds a throwaway repository whose base returns 1 and whose merge
// returns 2, stores hidden as a blob and returns the repository and blob.
func vetRepo(t *testing.T, hidden string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	write(t, filepath.Join(repo, "go.mod"), goMod)
	write(t, filepath.Join(repo, "p", "p.go"), answer1)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "base")
	blob := gitStdin(t, repo, hidden, "hash-object", "-w", "--stdin")
	write(t, filepath.Join(repo, "p", "p.go"), answer2)
	write(t, filepath.Join(repo, "p", "p_test.go"), hidden)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "merge")
	return repo, blob
}

// taskAt is the task whose base is the first commit of repo and whose merge
// is the second.
func taskAt(blob, test string) *task.Task {
	return &task.Task{
		ID: "pr-0007",
		Source: task.Source{
			Repo:  "pbtrudel/crush",
			PR:    7,
			Merge: "HEAD",
			Base:  "HEAD^1",
		},
		Kind:   task.KindPlain,
		Prompt: "Make the answer 2.",
		Grade: task.Grade{
			Packages: []string{"./p"},
			Tests:    []string{test},
			Hidden:   []task.Hidden{{Path: "p/p_test.go", Blob: blob}},
			Timeout:  task.Duration(time.Minute),
		},
	}
}

// goEnv is the minimal environment go runs with in the tests.
func goEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GOCACHE=" + goEnvValue(t, "GOCACHE"),
		"GOMODCACHE=" + goEnvValue(t, "GOMODCACHE"),
		"GOFLAGS=-buildvcs=false",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
	}
}

func goEnvValue(t *testing.T, name string) string {
	t.Helper()
	if value := os.Getenv(name); value != "" {
		return value
	}
	out, err := exec.CommandContext(context.Background(), "go", "env", name).Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitStdin(t, dir, "", args...)
}

func gitStdin(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	cmd.Stdin = bytes.NewReader([]byte(stdin))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}
