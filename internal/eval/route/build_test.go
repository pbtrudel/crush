package route

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/stretchr/testify/require"
)

func TestBuild(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the suite does not support Windows")
	}

	repo := newHelloRepo(t)
	h := home.At(filepath.Join(t.TempDir(), "crush-eval"))

	bin, err := Build(t.Context(), h, repo, "HEAD")
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`bin/[0-9a-f]{40}/crush$`), bin)
	require.Equal(t, "hello\n", run(t, bin))
	requireNoBuildDirs(t, h)

	// Change the source, then build the first commit again. The cached
	// binary is returned and still prints the old message.
	writeFile(t, repo, "main.go", helloMain("changed"))
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "change")
	again, err := Build(t.Context(), h, repo, filepath.Base(filepath.Dir(bin)))
	require.NoError(t, err)
	require.Equal(t, bin, again)
	require.Equal(t, "hello\n", run(t, again))
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the suite does not support Windows")
	}

	t.Run("unknown ref", func(t *testing.T) {
		t.Parallel()
		h := home.At(filepath.Join(t.TempDir(), "crush-eval"))
		_, err := Build(t.Context(), h, newHelloRepo(t), "no-such-ref")
		require.ErrorContains(t, err, "resolve")
	})

	t.Run("build fails", func(t *testing.T) {
		t.Parallel()
		repo := newHelloRepo(t)
		writeFile(t, repo, "main.go", "package main\n\nfunc main() { undefined() }\n")
		git(t, repo, "add", "-A")
		git(t, repo, "commit", "-q", "-m", "broken")

		h := home.At(filepath.Join(t.TempDir(), "crush-eval"))
		_, err := Build(t.Context(), h, repo, "HEAD")
		require.ErrorContains(t, err, "build")

		entries, err := os.ReadDir(filepath.Join(h.Dir(), "bin"))
		require.NoError(t, err)
		require.Empty(t, entries)
		requireNoBuildDirs(t, h)
	})
}

const helloMod = "module example.com/hello\n\ngo 1.21\n"

func helloMain(message string) string {
	return "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"" + message + "\") }\n"
}

// newHelloRepo builds a throwaway repository with a hello-world main
// package, so the tests never depend on this repository.
func newHelloRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", helloMod)
	writeFile(t, dir, "main.go", helloMain("hello"))
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "eval@example.com")
	git(t, dir, "config", "user.name", "eval")
	git(t, dir, "config", "commit.gpgsign", "false")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "hello")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// requireNoBuildDirs checks that Build removed its temporary tree.
func requireNoBuildDirs(t *testing.T, h *home.Home) {
	t.Helper()
	entries, err := os.ReadDir(h.Dir())
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), "build-")
	}
}

func run(t *testing.T, bin string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), bin).Output()
	require.NoError(t, err)
	return string(out)
}
