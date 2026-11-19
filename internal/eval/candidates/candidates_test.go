package candidates_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/candidates"
	"github.com/stretchr/testify/require"
)

func TestTouchedTests(t *testing.T) {
	t.Parallel()
	oldSrc, err := os.ReadFile("testdata/base_test.go")
	require.NoError(t, err)
	newSrc, err := os.ReadFile("testdata/merge_test.go")
	require.NoError(t, err)

	got, err := candidates.TouchedTestsIn(oldSrc, newSrc)
	require.NoError(t, err)
	require.Equal(t, []string{"TestAdded", "TestChanged"}, got)
}

func TestMissingNames(t *testing.T) {
	t.Parallel()
	output, err := os.ReadFile("testdata/compiler.txt")
	require.NoError(t, err)

	got := candidates.MissingNames(string(output))
	require.Equal(t, []string{"Bar", "ErrBackgroundShellNotFound", "New", "Old"}, got)
}

func TestFilter(t *testing.T) {
	t.Parallel()

	source := func(n int) candidates.File {
		return candidates.File{Path: "p/p.go", ChangeType: "MODIFIED", Additions: n}
	}
	tests := []struct {
		name  string
		title string
		files []candidates.File
		want  string
	}{
		{
			name:  "passes",
			title: "fix: answer the question",
			files: []candidates.File{source(10), {Path: "p/p_test.go", ChangeType: "MODIFIED", Additions: 5}},
		},
		{
			name:  "no source file",
			title: "fix: only tests",
			files: []candidates.File{{Path: "p/p_test.go", ChangeType: "MODIFIED"}},
			want:  "no non-test Go file",
		},
		{
			name:  "no test file",
			title: "fix: only source",
			files: []candidates.File{source(10)},
			want:  "no test file",
		},
		{
			name:  "too many source files",
			title: "feat: touch everything",
			files: append(thirteenSources(), candidates.File{Path: "p/p_test.go", ChangeType: "ADDED"}),
			want:  "13 source files",
		},
		{
			name:  "too many added source lines",
			title: "feat: a large change",
			files: []candidates.File{source(601), {Path: "p/p_test.go", ChangeType: "ADDED"}},
			want:  "601 added source lines",
		},
		{name: "ci", title: "ci: build on linux", files: testFiles(), want: "title"},
		{name: "docs", title: "docs: explain the flag", files: testFiles(), want: "title"},
		{name: "deps", title: "chore(deps): bump the all group with 13 updates", files: testFiles(), want: "title"},
		{name: "sync", title: "chore: sync upstream v0.100.0", files: testFiles(), want: "title"},
		{name: "bump", title: "fix: bump andybalholm/brotli to v1.2.7", files: testFiles(), want: "title"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := candidates.Filter(candidates.PullRequest{Title: tt.title, Files: tt.files})
			if tt.want == "" {
				require.Empty(t, got)
				return
			}
			require.Contains(t, got, tt.want)
		})
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		title  string
		base   map[string]string
		merge  map[string]string
		files  []candidates.File
		class  string
		names  []string
		tests  []string
		reason string
	}{
		{
			name:   "plain",
			base:   map[string]string{"go.mod": goMod, "p/p.go": answer1, "p/p_test.go": assertAnswer1},
			merge:  map[string]string{"p/p.go": answer2, "p/p_test.go": assertAnswer2},
			files:  goFiles(),
			class:  candidates.ClassPlain,
			tests:  []string{"TestAnswer"},
			reason: "",
		},
		{
			name:   "interface",
			base:   map[string]string{"go.mod": goMod, "p/p.go": answer1},
			merge:  map[string]string{"p/p.go": answer1 + "\nfunc Want() int { return 2 }\n", "p/p_test.go": wantTest},
			files:  []candidates.File{{Path: "p/p.go", ChangeType: "MODIFIED", Additions: 1}, {Path: "p/p_test.go", ChangeType: "ADDED", Additions: 8}},
			class:  candidates.ClassInterface,
			names:  []string{"Want"},
			tests:  []string{"TestWant"},
			reason: "",
		},
		{
			name:   "too many names",
			base:   map[string]string{"go.mod": goMod, "p/p.go": answer1},
			merge:  map[string]string{"p/p.go": answer1 + manyNames, "p/p_test.go": manyNamesTest},
			files:  []candidates.File{{Path: "p/p.go", ChangeType: "MODIFIED", Additions: 5}, {Path: "p/p_test.go", ChangeType: "ADDED", Additions: 10}},
			class:  candidates.ClassDropped,
			names:  []string{"W1", "W2", "W3", "W4", "W5"},
			tests:  []string{"TestMany"},
			reason: "compile fails on 5 names",
		},
		{
			name:   "merge fails",
			base:   map[string]string{"go.mod": goMod, "p/p.go": answer1},
			merge:  map[string]string{"p/p.go": answer1 + "\nfunc Want() int { return 2 }\n", "p/p_test.go": wantTest + brokenTest},
			files:  []candidates.File{{Path: "p/p.go", ChangeType: "MODIFIED", Additions: 1}, {Path: "p/p_test.go", ChangeType: "ADDED", Additions: 12}},
			class:  candidates.ClassDropped,
			tests:  []string{"TestBroken", "TestWant"},
			reason: "merge fails: TestBroken",
		},
		{
			name:   "passes on parent",
			base:   map[string]string{"go.mod": goMod, "p/p.go": answer1, "p/p_test.go": assertAnswer1},
			merge:  map[string]string{"p/p.go": answer1 + "\nfunc Unused() {}\n", "p/p_test.go": assertAnswer1 + "\nfunc TestSecond(t *testing.T) {}\n"},
			files:  goFiles(),
			class:  candidates.ClassDropped,
			tests:  []string{"TestSecond"},
			reason: "pass on parent",
		},
		{
			name:   "filtered",
			title:  "ci: nothing to grade",
			base:   map[string]string{"go.mod": goMod},
			merge:  map[string]string{"README.md": "docs\n"},
			files:  []candidates.File{{Path: "README.md", ChangeType: "MODIFIED"}},
			class:  candidates.ClassDropped,
			reason: "title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo, _, merge := makeRepo(t, tt.base, tt.merge)
			pr := candidates.PullRequest{Number: 7, Title: "fix: the problem", Files: tt.files}
			if tt.title != "" {
				pr.Title = tt.title
			}
			pr.MergeCommit.OID = merge

			got, err := candidates.Classify(context.Background(), repo, pr, t.TempDir(), goEnv(t))
			require.NoError(t, err)
			require.Equal(t, tt.class, got.Class)
			require.Equal(t, tt.names, got.Names)
			require.Equal(t, tt.tests, got.Tests)
			if tt.reason == "" {
				require.Empty(t, got.Reason)
			} else {
				require.Contains(t, got.Reason, tt.reason)
			}
		})
	}
}

func thirteenSources() []candidates.File {
	files := make([]candidates.File, 0, 13)
	for i := range 13 {
		files = append(files, candidates.File{Path: "p/f" + string(rune('a'+i)) + ".go", ChangeType: "ADDED", Additions: 1})
	}
	return files
}

func testFiles() []candidates.File {
	return []candidates.File{
		{Path: "p/p.go", ChangeType: "MODIFIED", Additions: 1},
		{Path: "p/p_test.go", ChangeType: "MODIFIED", Additions: 1},
	}
}

func goFiles() []candidates.File {
	return []candidates.File{
		{Path: "p/p.go", ChangeType: "MODIFIED", Additions: 1},
		{Path: "p/p_test.go", ChangeType: "MODIFIED", Additions: 1},
	}
}

const (
	goMod = "module example.com/candidate\n\ngo 1.27.0\n"

	answer1 = "package p\n\nfunc Answer() int { return 1 }\n"
	answer2 = "package p\n\nfunc Answer() int { return 2 }\n"

	assertAnswer1 = "package p\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 1 {\n\t\tt.Fatalf(\"Answer() = %d\", Answer())\n\t}\n}\n"
	assertAnswer2 = "package p\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 2 {\n\t\tt.Fatalf(\"Answer() = %d\", Answer())\n\t}\n}\n"

	wantTest = "package p\n\nimport \"testing\"\n\nfunc TestWant(t *testing.T) {\n\tif Want() != 2 {\n\t\tt.Fatalf(\"Want() = %d\", Want())\n\t}\n}\n"

	brokenTest = "\nfunc TestBroken(t *testing.T) { t.Fatal(\"broken\") }\n"

	manyNames     = "\nfunc W1() {}\nfunc W2() {}\nfunc W3() {}\nfunc W4() {}\nfunc W5() {}\n"
	manyNamesTest = "package p\n\nimport \"testing\"\n\nfunc TestMany(t *testing.T) {\n\tW1()\n\tW2()\n\tW3()\n\tW4()\n\tW5()\n}\n"
)

// makeRepo builds a throwaway repository with a base commit and a merge
// commit and returns it with both commit ids.
func makeRepo(t *testing.T, base, merge map[string]string) (dir, baseSHA, mergeSHA string) {
	t.Helper()
	dir = t.TempDir()
	writeFiles(t, dir, base)
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	baseSHA = git(t, dir, "rev-parse", "HEAD")
	writeFiles(t, dir, merge)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "merge")
	mergeSHA = git(t, dir, "rev-parse", "HEAD")
	return dir, baseSHA, mergeSHA
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
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

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	require.NoError(t, cmd.Run(), out.String())
	return strings.TrimSpace(out.String())
}
