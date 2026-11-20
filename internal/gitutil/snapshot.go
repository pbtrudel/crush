package gitutil

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// snapshotTimeout bounds one Snapshot call, so a wedged git cannot hold a
// turn's end.
const snapshotTimeout = 5 * time.Second

// WorktreeState is the git state of a working tree at one moment: the HEAD
// sha and the porcelain v2 entry of every path git reports as changed.
type WorktreeState struct {
	Head string
	// Entries maps each changed path to its porcelain entry, so two states
	// compare entry by entry.
	Entries map[string]string
}

// Snapshot reads a working tree's git state in one call. The branch header
// carries the HEAD sha, so the entries and the sha come from the same
// snapshot. --no-optional-locks keeps the call from taking the index lock
// that the model's own git commands need, and a submodule's dirty work tree
// is not a change of this tree.
func Snapshot(ctx context.Context, dir string) (WorktreeState, error) {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git",
		"--no-optional-locks", "-C", dir,
		"status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all",
		"--ignore-submodules=dirty",
	)
	out, err := cmd.Output()
	if err != nil {
		return WorktreeState{}, fmt.Errorf("git status: %w", err)
	}
	state := parseWorktreeState(out)
	slog.Debug("Read the working tree",
		"dir", dir, "entries", len(state.Entries), "elapsed", time.Since(start))
	return state, nil
}

// Changed lists the paths that differ between a state and a later one, plus
// the HEAD sha at each end. A path is changed if it was added, removed or its
// entry differs, which covers a file staged, unstaged, untracked or deleted.
// A HEAD that moved is reported by the two shas, because the files it
// committed no longer show in the entries.
func (s WorktreeState) Changed(later WorktreeState) (paths []string, headFrom, headTo string) {
	for path, entry := range later.Entries {
		if before, known := s.Entries[path]; !known || before != entry {
			paths = append(paths, path)
		}
	}
	for path := range s.Entries {
		if _, known := later.Entries[path]; !known {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths, s.Head, later.Head
}

// parseWorktreeState reads the NUL-separated porcelain v2 output. Each record
// is one entry, except that a rename carries its original path as a second
// NUL-terminated token.
func parseWorktreeState(out []byte) WorktreeState {
	state := WorktreeState{Entries: map[string]string{}}
	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens); i++ {
		record := tokens[i]
		switch {
		case record == "":
		case strings.HasPrefix(record, "# branch.oid "):
			state.Head = strings.TrimPrefix(record, "# branch.oid ")
		case strings.HasPrefix(record, "#"):
			// Other headers describe the branch, not a path.
		case strings.HasPrefix(record, "2 "):
			// A rename or copy: the new path, then the original path as
			// its own token.
			path, entry, ok := pathRecord(record, 9)
			if !ok {
				continue
			}
			if i+1 < len(tokens) {
				i++
				entry += " <- " + tokens[i]
			}
			state.Entries[path] = entry
		case strings.HasPrefix(record, "1 "), strings.HasPrefix(record, "u "):
			fields := 8
			if strings.HasPrefix(record, "u ") {
				fields = 10
			}
			path, entry, ok := pathRecord(record, fields)
			if !ok {
				continue
			}
			state.Entries[path] = entry
		case strings.HasPrefix(record, "? "), strings.HasPrefix(record, "! "):
			if path := record[2:]; path != "" {
				state.Entries[path] = record
			}
		}
	}
	return state
}

// pathRecord splits a porcelain record whose first fields are fixed and
// whose last is the path, which may hold spaces.
func pathRecord(record string, fields int) (path, entry string, ok bool) {
	parts := strings.SplitN(record, " ", fields+1)
	if len(parts) != fields+1 || parts[fields] == "" {
		return "", "", false
	}
	return parts[fields], record, true
}
