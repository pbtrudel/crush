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
	// IndexMode and IndexOID are the mode and object git reported for the
	// path's index entry, in the form git update-index takes. Both are
	// empty when the index did not hold the path.
	IndexMode string
	IndexOID  string
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
		// A rename names its original path inside its own record, while a
		// path that merely holds " <- " is only a name.
		if original, ok := renamedFrom(entry); ok {
			paths = append(paths, original)
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)

	snap := FileSnapshot{Head: state.Head, Files: make(map[string]SnapshotFile, len(paths))}
	var saved int64
	for _, path := range paths {
		entry, listed := state.Entries[path]
		file := SnapshotFile{Tracked: !listed || !strings.HasPrefix(entry, "? ")}
		if listed {
			file.IndexMode, file.IndexOID = indexEntry(entry)
		}
		if err := readSnapshotFile(filepath.Join(root, filepath.FromSlash(path)), &file, caps, &saved); err != nil {
			return FileSnapshot{}, fmt.Errorf("snapshot %s: %w", path, err)
		}
		snap.Files[path] = file
	}
	return snap, nil
}

// indexEntry is the index mode and object a porcelain v2 record reports for
// its path, empty when the index did not hold the path.
func indexEntry(entry string) (mode, oid string) {
	if len(entry) < 2 || entry[1] != ' ' || (entry[0] != '1' && entry[0] != '2') {
		return "", ""
	}
	// Both record kinds carry the index mode fourth and its object last
	// before the path.
	fields := strings.SplitN(entry, " ", 9)
	if len(fields) != 9 {
		return "", ""
	}
	mode, oid = fields[4], fields[7]
	if mode == "000000" || strings.Trim(oid, "0") == "" {
		return "", ""
	}
	return mode, oid
}

// renamedFrom is the original path a porcelain v2 rename record names
// after its own path, for a record that is a rename.
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
	// symlink, a special file), paths a restore refused because they leave
	// the work tree, and paths whose restore failed.
	Unrestored []string
}

// restoreRoot is the top of a work tree, opened so that no restore can
// reach outside it.
type restoreRoot struct {
	root string
	dir  *os.Root
}

// RestorePaths puts each path under root back to its state in before. A
// path before did not list was clean: it is restored from before.Head when
// that commit has it, and removed (from the index too) when it is new. A
// listed path is rewritten from its saved content, or removed when it did
// not exist, and its index entry is put back as before had it.
//
// A path to restore can be the destination of a move whose original is not
// among paths, typically a file the caller's scopes allowed the command to
// change. The original is restored first, from before or from before.Head,
// and when the snapshot cannot supply it the moved bytes are renamed back
// to it. Only when neither works is the destination left in place and both
// paths reported in Unrestored: a restore never deletes the last copy.
//
// A path must lie inside root, never inside its .git, and never behind a
// symbolic link that leaves root; a path that does not is reported in
// Unrestored and left untouched.
func RestorePaths(ctx context.Context, root string, before FileSnapshot, paths []string) RestoreResult {
	targets := uniquePaths(paths)
	if len(targets) == 0 {
		return RestoreResult{}
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		slog.Warn("Could not open the worktree to restore paths", "dir", root, "error", err)
		return RestoreResult{Unrestored: targets}
	}
	defer dir.Close()
	w := restoreRoot{root: root, dir: dir}
	moves, ambiguous := moveOriginals(ctx, root, before, targets)
	destOf := make(map[string]string, len(moves))
	for dest, original := range moves {
		if current, ok := destOf[original]; !ok || dest < current {
			destOf[original] = dest
		}
	}
	var result RestoreResult
	settled := make(map[string]bool, len(targets))
	// An original comes first: its destination may hold the only copy.
	for _, original := range slices.Sorted(maps.Keys(destOf)) {
		dest := destOf[original]
		if err := restorePath(ctx, w, before, original); err == nil {
			result.Restored = append(result.Restored, original)
			continue
		}
		// The snapshot cannot supply the original: move the bytes back, so
		// restoring the destination cannot delete the only copy.
		if err := reverseMove(ctx, w, before, dest, original); err != nil {
			result.Unrestored = append(result.Unrestored, original, dest)
			settled[dest] = true
			continue
		}
		// The destination is restored below, now that the move is undone.
		result.Restored = append(result.Restored, original)
	}
	for _, path := range targets {
		if settled[path] {
			continue
		}
		if ambiguous[path] {
			// The bytes may be the only copy of a path the command
			// removed, so the destination stays.
			result.Unrestored = append(result.Unrestored, path)
			continue
		}
		if err := restorePath(ctx, w, before, path); err != nil {
			result.Unrestored = append(result.Unrestored, path)
			continue
		}
		result.Restored = append(result.Restored, path)
	}
	return result
}

// uniquePaths drops empty and repeated paths, keeping their order.
func uniquePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// moveOriginals maps each destination in targets to the original it was
// moved from, for a move git names or a content match exactly one removed
// path supports, and lists the destinations whose content matches several
// removed paths. A copy is not a move, and empty bytes prove nothing.
func moveOriginals(ctx context.Context, root string, before FileSnapshot, targets []string) (moves map[string]string, ambiguous map[string]bool) {
	target := make(map[string]bool, len(targets))
	for _, path := range targets {
		target[path] = true
	}
	moves, ambiguous = map[string]string{}, map[string]bool{}
	state, err := Snapshot(ctx, root)
	if err != nil {
		slog.Debug("Could not read the worktree for the originals of a move", "dir", root, "error", err)
	}
	for _, path := range targets {
		// A rename record names its original, and a copy's original still
		// stands, so a copy is no move.
		if original, ok := renamedFrom(state.Entries[path]); ok && !target[original] && !isPresent(root, original) {
			moves[path] = original
		}
	}
	// The bytes the worktree held before, as before saved or hashed them,
	// and those only before.Head still holds for a clean tracked path.
	bySaved := map[string][]string{}
	for path, file := range before.Files {
		if !file.Exists || !isContentHash(file.Hash) || (file.Saved && len(file.Content) == 0) {
			continue
		}
		bySaved[file.Hash] = append(bySaved[file.Hash], path)
	}
	var byBlob map[string][]string
	if knownHead(before.Head) {
		byBlob = headBlobs(ctx, root, before.Head, deletedPaths(state, before, target))
	}
	for _, path := range targets {
		if _, named := moves[path]; named {
			continue
		}
		hash, err := contentHash(root, path)
		if err != nil || hash == "" {
			continue
		}
		candidates := removedCandidates(root, bySaved[hash], target)
		if len(byBlob) > 0 {
			if oid, err := blobOID(ctx, root, path); err == nil && oid != "" {
				for _, candidate := range removedCandidates(root, byBlob[oid], target) {
					if !slices.Contains(candidates, candidate) {
						candidates = append(candidates, candidate)
					}
				}
			}
		}
		switch len(candidates) {
		case 1:
			moves[path] = candidates[0]
		case 0:
		default:
			ambiguous[path] = true
		}
	}
	return moves, ambiguous
}

// deletedEntry reports whether a porcelain v2 record shows a path gone from
// the worktree.
func deletedEntry(entry string) bool {
	return len(entry) > 3 && strings.HasPrefix(entry, "1 ") && (entry[2] == 'D' || entry[3] == 'D')
}

// deletedPaths lists the tracked paths git reports gone that before did not
// list, whose content therefore lives only in before.Head.
func deletedPaths(state WorktreeState, before FileSnapshot, target map[string]bool) []string {
	var paths []string
	for path, entry := range state.Entries {
		if target[path] || !deletedEntry(entry) {
			continue
		}
		if _, listed := before.Files[path]; listed {
			continue
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

// headBlobs maps each object id in commit to the paths that held it, for
// the paths git reports gone from the worktree. One ls-tree call serves
// them all.
func headBlobs(ctx context.Context, root, commit string, paths []string) map[string][]string {
	if len(paths) == 0 {
		return nil
	}
	out, err := gitLiteral(ctx, root, append([]string{"ls-tree", "-z", "-l", commit, "--"}, paths...)...)
	if err != nil {
		slog.Debug("Could not list the files a command removed", "dir", root, "error", err)
		return nil
	}
	byBlob := map[string][]string{}
	for record := range strings.SplitSeq(out, "\x00") {
		// "<mode> SP <type> SP <object> SP <size> TAB <path>".
		meta, path, ok := strings.Cut(record, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 4 || fields[1] != "blob" || fields[3] == "0" {
			continue
		}
		byBlob[fields[2]] = append(byBlob[fields[2]], path)
	}
	return byBlob
}

// blobOID is the object id git gives the worktree file at path.
func blobOID(ctx context.Context, root, path string) (string, error) {
	out, err := gitOutput(ctx, root, "hash-object", "--", filepath.FromSlash(path))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// removedCandidates narrows candidates to the paths that are gone from the
// worktree and are not being restored themselves.
func removedCandidates(root string, candidates []string, target map[string]bool) []string {
	var out []string
	for _, path := range candidates {
		if target[path] || isPresent(root, path) {
			continue
		}
		out = append(out, path)
	}
	return out
}

// contentHash is the hash of the worktree file at path, or "" when nothing
// regular or anything empty stands there: empty bytes prove no move.
func contentHash(root, path string) (string, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
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

var (
	// errNotSaved marks a path whose content the snapshot did not keep.
	errNotSaved = errors.New("content not saved")
	// errOutsideWorktree marks a path that leaves the work tree.
	errOutsideWorktree = errors.New("path is outside the worktree")
	// errGitDir marks a path inside the work tree's git directory.
	errGitDir = errors.New("path is inside the git directory")
)

// restorePath puts one path back to its state in before, through the root so
// that no write or delete can follow a symbolic link out of the work tree.
func restorePath(ctx context.Context, w restoreRoot, before FileSnapshot, path string) error {
	if err := checkContained(w, path); err != nil {
		return err
	}
	osPath := filepath.FromSlash(path)
	file, listed := before.Files[path]
	if !listed {
		if knownHead(before.Head) && inCommit(ctx, w.root, before.Head, path) {
			_, err := gitLiteral(ctx, w.root, "restore", "--source="+before.Head, "--staged", "--worktree", "--", path)
			return err
		}
		if _, err := gitLiteral(ctx, w.root, "rm", "--cached", "--quiet", "--ignore-unmatch", "--", path); err != nil {
			return err
		}
		return removeIfPresent(w, osPath)
	}
	if !file.Exists {
		if err := removeIfPresent(w, osPath); err != nil {
			return err
		}
		return restoreIndex(ctx, w.root, before, path)
	}
	if !file.Saved {
		return errNotSaved
	}
	if dir := filepath.Dir(osPath); dir != "." {
		if err := w.dir.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	// Whatever now stands in the way (a symlink, a directory) is replaced,
	// never written through.
	if info, err := w.dir.Lstat(osPath); err == nil && !info.Mode().IsRegular() {
		if err := w.dir.RemoveAll(osPath); err != nil {
			return err
		}
	}
	if err := w.dir.WriteFile(osPath, file.Content, file.Mode); err != nil {
		return err
	}
	if err := w.dir.Chmod(osPath, file.Mode); err != nil {
		return err
	}
	return restoreIndex(ctx, w.root, before, path)
}

// restoreIndex puts the index entry for path back as before had it, or
// takes the path out of the index when before did not hold it.
func restoreIndex(ctx context.Context, root string, before FileSnapshot, path string) error {
	if file, listed := before.Files[path]; listed && file.IndexOID != "" {
		_, err := gitLiteral(ctx, root, "update-index", "--add", "--cacheinfo",
			file.IndexMode, file.IndexOID, path)
		return err
	}
	_, err := gitLiteral(ctx, root, "update-index", "--force-remove", "--", path)
	return err
}

// reverseMove puts the bytes a move left at dest back at original, for an
// original the snapshot cannot supply, and puts both index entries back.
func reverseMove(ctx context.Context, w restoreRoot, before FileSnapshot, dest, original string) error {
	if err := checkContained(w, original); err != nil {
		return err
	}
	if err := checkContained(w, dest); err != nil {
		return err
	}
	originalPath := filepath.FromSlash(original)
	if dir := filepath.Dir(originalPath); dir != "." {
		if err := w.dir.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := w.dir.Rename(filepath.FromSlash(dest), originalPath); err != nil {
		return err
	}
	if file, listed := before.Files[original]; listed && file.Mode != 0 {
		if err := w.dir.Chmod(originalPath, file.Mode); err != nil {
			return err
		}
	}
	if err := restoreIndex(ctx, w.root, before, original); err != nil {
		return err
	}
	return restoreIndex(ctx, w.root, before, dest)
}

func removeIfPresent(w restoreRoot, osPath string) error {
	if err := w.dir.Remove(osPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// checkContained rejects a path a restore must never touch, and one whose
// parents resolve outside the work tree.
func checkContained(w restoreRoot, path string) error {
	if err := checkRestorablePath(path); err != nil {
		return err
	}
	if _, err := w.dir.Lstat(filepath.FromSlash(path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// checkRestorablePath rejects an absolute path, one that climbs out of the
// work tree, and one inside the work tree's git directory.
func checkRestorablePath(path string) error {
	if path == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return errOutsideWorktree
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "" || part == "." || part == ".." {
			return errOutsideWorktree
		}
		if strings.EqualFold(part, ".git") {
			return errGitDir
		}
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
