// Package checkout writes a task's base commit into a fresh repository with a
// single commit, so a run starts from a clean tree and no later history.
package checkout

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/gitx"
)

// The fixed identity and date of the "task base" commit. No user identity
// reaches a run, and the same base always yields the same commit.
const (
	authorName  = "crush-eval"
	authorEmail = "crush-eval@example.com"
	authorDate  = "2000-01-01T00:00:00 +0000"
)

// Prepare writes the tree of base from the repository at repoDir into
// dest/src and makes it a new repository with one commit, "task base". It
// returns the blob hash of the tracked root crush.json, or "" when the tree
// has none.
func Prepare(ctx context.Context, repoDir, base, dest string) (string, error) {
	src := filepath.Join(dest, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	if err := extract(ctx, repoDir, base, src); err != nil {
		return "", err
	}
	env := gitEnv()
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		// -f keeps a tracked file that .gitignore names, which plain
		// "add -A" in a fresh repository would drop.
		{"add", "-A", "-f"},
		{"commit", "-q", "-m", "task base"},
	}
	for _, args := range steps {
		if _, err := gitx.OutputWith(ctx, src, env, args...); err != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	return crushJSONBlob(ctx, src, env)
}

// Head is the commit Prepare wrote into src, which is the base a grade of
// that checkout starts from.
func Head(ctx context.Context, src string) (string, error) {
	return gitx.Output(ctx, src, "rev-parse", "HEAD")
}

// extract streams the tar archive of base from git into dest. It never runs a
// tar program.
func extract(ctx context.Context, repoDir, base, dest string) error {
	cmd := gitx.Command(ctx, repoDir, "archive", "--format=tar", base)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	extractErr := Extract(stdout, dest)
	if extractErr != nil {
		// Keep git from blocking on a full pipe while the error returns.
		_, _ = io.Copy(io.Discard, stdout)
	}
	if err := cmd.Wait(); err != nil && extractErr == nil {
		return fmt.Errorf("git archive %s: %w: %s", base, err, strings.TrimSpace(stderr.String()))
	}
	return extractErr
}

// Extract writes the files of a git archive stream into dest with their
// modes and symlinks. It is the one extractor of the runner: the task
// checkout and the variant build both read `git archive` with it, and it
// refuses any entry that would land outside dest.
func Extract(r io.Reader, dest string) error {
	root, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	type link struct{ name, target string }
	var links []link
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(hdr.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("archive entry %q escapes the checkout", hdr.Name)
		}
		path := filepath.Join(root, name)
		mode := hdr.FileInfo().Mode().Perm()

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			if err := os.Chmod(path, mode); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := writeFile(path, tr, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Links are made after every file is written, so each target
			// can be resolved and no entry is written through a link.
			links = append(links, link{name: name, target: filepath.FromSlash(hdr.Linkname)})
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			// git archive carries the commit id in a pax header.
		default:
			return fmt.Errorf("archive entry %q has unsupported type %q", hdr.Name, hdr.Typeflag)
		}
	}
	for _, l := range links {
		dir := filepath.Join(root, filepath.Dir(l.name))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		// A link that resolves outside the checkout would let the model
		// write through it to the host.
		if filepath.IsAbs(l.target) || !within(root, dir) || !within(root, filepath.Join(dir, l.target)) {
			return fmt.Errorf("archive symlink %q points outside the checkout", filepath.ToSlash(l.name))
		}
		if err := os.Symlink(l.target, filepath.Join(dir, filepath.Base(l.name))); err != nil {
			return err
		}
	}
	return nil
}

// within reports whether path, with every symlink in it resolved, lies inside
// root. A path that does not exist is not within root.
func within(root, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// writeFile writes one regular file and sets its mode past the umask.
func writeFile(path string, r io.Reader, mode fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	if err := f.Close(); err != nil {
		return err
	}
	if copyErr != nil {
		return copyErr
	}
	return os.Chmod(path, mode)
}

// crushJSONBlob returns the blob hash of the root crush.json at HEAD.
func crushJSONBlob(ctx context.Context, src string, env []string) (string, error) {
	if _, err := os.Stat(filepath.Join(src, "crush.json")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return gitx.OutputWith(ctx, src, env, "rev-parse", "HEAD:crush.json")
}

// gitEnv is the environment of every git command Prepare runs: the runner's
// hardened environment plus a fixed identity, so the same base always yields
// the same commit.
func gitEnv() []string {
	return append(gitx.Env(),
		"GIT_AUTHOR_NAME="+authorName,
		"GIT_AUTHOR_EMAIL="+authorEmail,
		"GIT_AUTHOR_DATE="+authorDate,
		"GIT_COMMITTER_NAME="+authorName,
		"GIT_COMMITTER_EMAIL="+authorEmail,
		"GIT_COMMITTER_DATE="+authorDate,
	)
}
