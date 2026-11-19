package route

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/gitx"
	"github.com/charmbracelet/crush/internal/eval/home"
)

// Build returns the binary of the variant at ref, building it into
// bin/<sha>/crush when it is not there yet. The source comes from
// `git archive <sha>`, so the build sees that commit's tree and nothing
// after it, and the temporary tree lives under the eval home.
func Build(ctx context.Context, h *home.Home, repoDir, ref string) (string, error) {
	sha, err := gitx.Output(ctx, repoDir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	bin := h.BinPath(sha)
	if info, err := os.Stat(bin); err == nil && info.Mode().IsRegular() {
		return bin, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("stat %s: %w", bin, err)
	}

	if err := h.Ensure(); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(h.Dir(), "build-")
	if err != nil {
		return "", fmt.Errorf("create build directory: %w", err)
	}
	defer os.RemoveAll(tmp) //nolint:errcheck // Temporary tree.

	if err := archiveTree(ctx, repoDir, sha, tmp); err != nil {
		return "", err
	}
	// Build outside the bin tree, then move the binary in place, so a
	// failed build leaves no half-written variant behind.
	out := filepath.Join(tmp, "out", "crush")
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", fmt.Errorf("create build output directory: %w", err)
	}
	var log bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, ".")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build %s: %w: %s", sha, err, strings.TrimSpace(log.String()))
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		return "", fmt.Errorf("create bin directory: %w", err)
	}
	if err := os.Rename(out, bin); err != nil {
		return "", fmt.Errorf("install %s: %w", sha, err)
	}
	return bin, nil
}

// archiveTree writes the tree of sha from repoDir into dst.
func archiveTree(ctx context.Context, repoDir, sha, dst string) error {
	cmd := gitx.Command(ctx, repoDir, "archive", "--format=tar", sha)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("archive %s: %w", sha, err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("archive %s: %w", sha, err)
	}
	if err := extractTar(stdout, dst); err != nil {
		_ = cmd.Wait()
		return fmt.Errorf("extract %s: %w", sha, err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("archive %s: %w: %s", sha, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// extractTar unpacks a git archive into dst with tight permissions.
func extractTar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(hdr.Name))
		if target != dst && !strings.HasPrefix(target, dst+string(os.PathSeparator)) {
			return fmt.Errorf("refuse path %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if hdr.Mode&0o111 != 0 {
				mode = 0o700
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close() //nolint:errcheck // Copy error wins.
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}
