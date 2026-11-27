package gitutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// SnapshotCaps bound how much content one FileSnapshot saves. A file over
// PerFile, or one that would take the snapshot past Total, is hashed but
// not saved: a change to it can be seen and reported, not undone. Zero caps
// save nothing but empty files.
type SnapshotCaps struct {
	PerFile int64
	Total   int64
}

// SnapshotFile is one path git status listed.
type SnapshotFile struct {
	// Exists is false for a path listed as deleted.
	Exists bool
	// Tracked is false for an untracked path.
	Tracked bool
	// Hash identifies the content: a sha256 of the bytes, the link target
	// of a symlink, or "absent".
	Hash string
	// Saved reports whether Content holds the bytes. A deleted path needs
	// none and counts as saved; a symlink or special file never is.
	Saved   bool
	Content []byte
	Mode    fs.FileMode
}

// FileSnapshot is the changed-file state of a work tree: its HEAD and every
// path git status lists there. Ignored files are not listed.
type FileSnapshot struct {
	Head  string
	Files map[string]SnapshotFile
}

// SnapshotFiles records root's status and each listed path. root must be
// the top of a work tree, since the paths are relative to it.
func SnapshotFiles(ctx context.Context, root string, caps SnapshotCaps) (FileSnapshot, error) {
	state, err := Snapshot(ctx, root)
	if err != nil {
		return FileSnapshot{}, err
	}
	paths := make([]string, 0, len(state.Entries))
	for path, entry := range state.Entries {
		paths = append(paths, path)
		// A rename names its original path only inside its entry.
		if _, orig, ok := strings.Cut(entry, " <- "); ok && orig != "" {
			paths = append(paths, orig)
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)

	snap := FileSnapshot{Head: state.Head, Files: make(map[string]SnapshotFile, len(paths))}
	var saved int64
	for _, path := range paths {
		entry, listed := state.Entries[path]
		file := SnapshotFile{Tracked: !listed || !strings.HasPrefix(entry, "? ")}
		if err := readSnapshotFile(filepath.Join(root, filepath.FromSlash(path)), &file, caps, &saved); err != nil {
			return FileSnapshot{}, fmt.Errorf("snapshot %s: %w", path, err)
		}
		snap.Files[path] = file
	}
	return snap, nil
}

// readSnapshotFile fills file from the path on disk, saving its bytes while
// the caps allow.
func readSnapshotFile(full string, file *SnapshotFile, caps SnapshotCaps, saved *int64) error {
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		file.Hash, file.Saved = "absent", true
		return nil
	}
	if err != nil {
		return err
	}
	file.Exists = true
	file.Mode = info.Mode().Perm()
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(full)
		if err != nil {
			return err
		}
		file.Hash = "link:" + target
		return nil
	case !info.Mode().IsRegular():
		file.Hash = "special:" + info.Mode().Type().String()
		return nil
	}
	if info.Size() <= caps.PerFile && *saved+info.Size() <= caps.Total {
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		file.Hash = hex.EncodeToString(sum[:])
		file.Content, file.Saved = data, true
		*saved += int64(len(data))
		return nil
	}
	hash, err := hashFile(full)
	if err != nil {
		return err
	}
	file.Hash = hash
	return nil
}

// hashFile is the sha256 of a file too large to keep, read as a stream.
func hashFile(full string) (string, error) {
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// ChangedPaths lists, sorted, the paths whose state differs between two
// snapshots of root: listed in one and not the other, or listed in both
// with other content. When HEAD moved, every path the new commits touched
// counts too, since a committed file leaves the status list.
func ChangedPaths(ctx context.Context, root string, before, after FileSnapshot) ([]string, error) {
	set := map[string]bool{}
	for path, file := range after.Files {
		if prior, ok := before.Files[path]; !ok || prior.Hash != file.Hash {
			set[path] = true
		}
	}
	for path := range before.Files {
		if _, ok := after.Files[path]; !ok {
			set[path] = true
		}
	}
	if knownHead(before.Head) && knownHead(after.Head) && before.Head != after.Head {
		out, err := gitLiteral(ctx, root, "diff", "--name-only", "-z", before.Head, after.Head)
		if err != nil {
			return nil, fmt.Errorf("git diff --name-only: %w", err)
		}
		for path := range strings.SplitSeq(out, "\x00") {
			if path != "" {
				set[path] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set)), nil
}

// knownHead reports whether head names a commit; an unborn branch reports
// "(initial)".
func knownHead(head string) bool {
	return head != "" && head != "(initial)"
}

// RestoreResult reports what RestorePaths did with each path.
type RestoreResult struct {
	Restored []string
	// Unrestored lists paths whose content was not saved (over the caps, a
	// symlink, a special file) and paths whose restore failed.
	Unrestored []string
}

// RestorePaths puts each path under root back to its state in before. A
// path before did not list was clean: it is restored from before.Head when
// that commit has it, and removed (from the index too) when it is new. A
// listed path is rewritten from its saved content, or removed when it did
// not exist.
//
// A path to restore can be the destination of a move whose original is not
// among paths, typically a file the caller's scopes allowed the command to
// change. The original is restored too, from before or from before.Head,
// so undoing an out-of-scope write does not take an in-scope file with it.
// An original the snapshot cannot supply is reported in Unrestored.
func RestorePaths(ctx context.Context, root string, before FileSnapshot, paths []string) RestoreResult {
	var result RestoreResult
	for _, path := range restoreTargets(ctx, root, before, paths) {
		if err := restorePath(ctx, root, before, path); err != nil {
			result.Unrestored = append(result.Unrestored, path)
			continue
		}
		result.Restored = append(result.Restored, path)
	}
	return result
}

// restoreTargets adds to paths the originals of the moves into them. It
// reads the worktree before anything is restored, since a move leaves no
// trace once its destination is gone.
func restoreTargets(ctx context.Context, root string, before FileSnapshot, paths []string) []string {
	targets := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		targets = append(targets, path)
	}
	if len(targets) == 0 {
		return targets
	}
	for _, original := range moveOriginals(ctx, root, before, targets) {
		if !seen[original] {
			seen[original] = true
			targets = append(targets, original)
		}
	}
	return targets
}

// moveOriginals lists, sorted, the originals of the moves into targets: the
// original a rename record names, and a path whose content a destination
// now holds that the command emptied. A copy is not a move, since its
// original still stands.
func moveOriginals(ctx context.Context, root string, before FileSnapshot, targets []string) []string {
	target := make(map[string]bool, len(targets))
	for _, path := range targets {
		target[path] = true
	}
	// By content hash: the paths before saw, plus the clean tracked paths
	// whose content lives in before.Head.
	byContent := map[string][]string{}
	for path, file := range before.Files {
		if file.Exists && isContentHash(file.Hash) {
			byContent[file.Hash] = append(byContent[file.Hash], path)
		}
	}
	state, err := Snapshot(ctx, root)
	if err != nil {
		slog.Debug("Could not read the worktree for the originals of a move", "dir", root, "error", err)
	}
	found := map[string]bool{}
	for _, path := range targets {
		if original, ok := renamedFrom(state.Entries[path]); ok {
			found[original] = true
		}
	}
	hashes := make(map[string]string, len(targets))
	for _, path := range targets {
		hash, err := contentHash(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || hash == "" {
			continue
		}
		hashes[path] = hash
	}
	// The content a clean tracked path had before lives only in HEAD, so
	// those paths are hashed only when a destination needs them.
	if knownHead(before.Head) && needsHead(hashes, byContent) {
		for path, entry := range state.Entries {
			if target[path] || !deletedEntry(entry) {
				continue
			}
			if _, listed := before.Files[path]; listed {
				continue
			}
			if hash, ok := headBlobHash(ctx, root, before.Head, path); ok {
				byContent[hash] = append(byContent[hash], path)
			}
		}
	}
	for _, hash := range hashes {
		for _, original := range slices.Sorted(slices.Values(byContent[hash])) {
			if target[original] || isPresent(root, original) {
				continue
			}
			found[original] = true
			break
		}
	}
	return slices.DeleteFunc(slices.Sorted(maps.Keys(found)), func(path string) bool {
		return target[path] || isPresent(root, path)
	})
}

// renamedFrom is the original path a porcelain v2 rename record names after
// its own path, for a record that is a rename.
func renamedFrom(entry string) (string, bool) {
	if len(entry) < 4 || !strings.HasPrefix(entry, "2 ") {
		return "", false
	}
	if entry[2] != 'R' && entry[3] != 'R' {
		return "", false
	}
	_, original, ok := strings.Cut(entry, " <- ")
	return original, ok && original != ""
}

// deletedEntry reports whether a porcelain v2 record shows a path gone from
// the worktree.
func deletedEntry(entry string) bool {
	return len(entry) > 3 && strings.HasPrefix(entry, "1 ") && (entry[2] == 'D' || entry[3] == 'D')
}

// needsHead reports whether a destination holds content that nothing before
// saw, so a clean tracked path may be its original.
func needsHead(hashes map[string]string, byContent map[string][]string) bool {
	for _, hash := range hashes {
		if len(byContent[hash]) == 0 {
			return true
		}
	}
	return false
}

// headBlobHash is the content hash of path in commit, for a path before did
// not list because it was clean.
func headBlobHash(ctx context.Context, root, commit, path string) (string, bool) {
	blob, err := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", commit+":"+path).Output()
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:]), true
}

// contentHash is the hash of the regular file at full, or "" when nothing
// regular stands there.
func contentHash(full string) (string, error) {
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", nil
	}
	return hashFile(full)
}

// isPresent reports whether anything stands at the worktree path.
func isPresent(root, path string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil
}

// isContentHash reports whether hash is the sha256 of bytes, not the target
// of a symlink, a special file or an absent path.
func isContentHash(hash string) bool {
	if len(hash) != sha256.Size*2 {
		return false
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// errNotSaved marks a path whose content the snapshot did not keep.
var errNotSaved = errors.New("content not saved")

func restorePath(ctx context.Context, root string, before FileSnapshot, path string) error {
	full := filepath.Join(root, filepath.FromSlash(path))
	file, listed := before.Files[path]
	if !listed {
		if knownHead(before.Head) && inCommit(ctx, root, before.Head, path) {
			_, err := gitLiteral(ctx, root, "restore", "--source="+before.Head, "--staged", "--worktree", "--", path)
			return err
		}
		if _, err := gitLiteral(ctx, root, "rm", "--cached", "--quiet", "--ignore-unmatch", "--", path); err != nil {
			return err
		}
		return removeIfPresent(full)
	}
	if !file.Exists {
		return removeIfPresent(full)
	}
	if !file.Saved {
		return errNotSaved
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	// Whatever now stands in the way (a symlink, a directory) is replaced,
	// never written through.
	if info, err := os.Lstat(full); err == nil && !info.Mode().IsRegular() {
		if err := os.RemoveAll(full); err != nil {
			return err
		}
	}
	if err := os.WriteFile(full, file.Content, file.Mode); err != nil {
		return err
	}
	return os.Chmod(full, file.Mode)
}

func removeIfPresent(full string) error {
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// inCommit reports whether commit has a file at path.
func inCommit(ctx context.Context, root, commit, path string) bool {
	_, err := gitOutput(ctx, root, "cat-file", "-e", commit+":"+path)
	return err == nil
}

// gitLiteral runs git in dir with pathspec magic off, so a path read from a
// status listing only ever means that path.
func gitLiteral(ctx context.Context, dir string, args ...string) (string, error) {
	return gitOutput(ctx, dir, append([]string{"--literal-pathspecs"}, args...)...)
}
