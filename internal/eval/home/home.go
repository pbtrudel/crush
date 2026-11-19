// Package home locates and creates the eval state directory of the task
// suite runner (design section 3.3).
package home

import (
	"fmt"
	"os"
	"path/filepath"
)

// Name is the directory name of the eval home.
const Name = "crush-eval"

// Home is the eval state directory. Runs read their configuration, data,
// skills and binaries from it, never from the user's own Crush directories.
type Home struct {
	dir string
}

// Default returns the eval home from $XDG_STATE_HOME, defaulting to
// ~/.local/state/crush-eval. A relative $XDG_STATE_HOME is ignored, as the
// XDG base directory spec requires.
func Default() (*Home, error) {
	if state := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(state) {
		return At(filepath.Join(state, Name)), nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home directory: %w", err)
	}
	return At(filepath.Join(userHome, ".local", "state", Name)), nil
}

// At returns the eval home rooted at dir. It does not touch the disk.
func At(dir string) *Home {
	return &Home{dir: dir}
}

// Dir is the eval home itself.
func (h *Home) Dir() string { return h.dir }

// RoutesDir holds one YAML file per route.
func (h *Home) RoutesDir() string { return filepath.Join(h.dir, "routes") }

// DataDir is CRUSH_GLOBAL_DATA: the tokens written by `crush-eval login`
// and the sessions of the runs.
func (h *Home) DataDir() string { return filepath.Join(h.dir, "data") }

// ConfigDir is CRUSH_GLOBAL_CONFIG. Crush reads crush.json from it.
func (h *Home) ConfigDir() string { return filepath.Join(h.dir, "config", "crush") }

// ConfigFile is the generated global config of one batch.
func (h *Home) ConfigFile() string { return filepath.Join(h.ConfigDir(), "crush.json") }

// SkillsDir is CRUSH_SKILLS_DIR, kept empty so the user's skills never
// reach a run.
func (h *Home) SkillsDir() string { return filepath.Join(h.dir, "skills") }

// GHDir is GH_CONFIG_DIR, kept empty so the user's gh credentials never
// reach a run.
func (h *Home) GHDir() string { return filepath.Join(h.dir, "gh") }

// ClaudeDir is CLAUDE_CONFIG_DIR for routes that drive the claude CLI, so
// the user's ~/.claude settings stay out of a run.
func (h *Home) ClaudeDir() string { return filepath.Join(h.dir, "claude") }

// BinPath is the built binary of one variant, identified by its commit.
func (h *Home) BinPath(sha string) string {
	return filepath.Join(h.dir, "bin", sha, "crush")
}

// BatchDir holds the results and runs of one batch.
func (h *Home) BatchDir(id string) string {
	return filepath.Join(h.dir, "batches", id)
}

// Ensure creates the layout with mode 0o700 and tightens directories that
// exist with a looser mode. It is safe to call more than once.
func (h *Home) Ensure() error {
	dirs := []string{
		h.dir,
		h.RoutesDir(),
		h.DataDir(),
		h.ConfigDir(),
		h.SkillsDir(),
		h.GHDir(),
		h.ClaudeDir(),
		filepath.Join(h.dir, "bin"),
		filepath.Join(h.dir, "batches"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("tighten %s: %w", dir, err)
		}
	}
	return nil
}
