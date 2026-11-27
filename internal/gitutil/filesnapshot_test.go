package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var testCaps = SnapshotCaps{PerFile: 1 << 20, Total: 32 << 20}

func writeRepoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}

func snapshot(t *testing.T, root string, caps SnapshotCaps) FileSnapshot {
	t.Helper()
	snap, err := SnapshotFiles(context.Background(), root, caps)
	require.NoError(t, err)
	return snap
}

func changed(t *testing.T, root string, before, after FileSnapshot) []string {
	t.Helper()
	paths, err := ChangedPaths(context.Background(), root, before, after)
	require.NoError(t, err)
	return paths
}

func TestSnapshotFilesListsChangedPaths(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, ".gitignore", "build/\n")
	writeRepoFile(t, repo, "gone.txt", "bye\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")

	writeRepoFile(t, repo, "README.md", "dirty\n")
	writeRepoFile(t, repo, "docs/new.md", "new\n")
	writeRepoFile(t, repo, "build/out.bin", "ignored\n")
	require.NoError(t, os.Remove(filepath.Join(repo, "gone.txt")))

	snap := snapshot(t, repo, testCaps)
	require.Equal(t, gitText(t, repo, "rev-parse", "HEAD"), snap.Head)
	require.ElementsMatch(t, []string{"README.md", "docs/new.md", "gone.txt"}, keys(snap.Files), "ignored files are not listed")
	require.True(t, snap.Files["README.md"].Tracked)
	require.True(t, snap.Files["README.md"].Saved)
	require.Equal(t, "dirty\n", string(snap.Files["README.md"].Content))
	require.False(t, snap.Files["docs/new.md"].Tracked)
	require.False(t, snap.Files["gone.txt"].Exists)
}

func keys(files map[string]SnapshotFile) []string {
	out := make([]string, 0, len(files))
	for path := range files {
		out = append(out, path)
	}
	return out
}

func TestChangedPathsSeesEveryKindOfChange(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "clean.txt", "clean\n")
	writeRepoFile(t, repo, "reverted.txt", "base\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	writeRepoFile(t, repo, "README.md", "dirty\n")
	writeRepoFile(t, repo, "reverted.txt", "dirty\n")
	before := snapshot(t, repo, testCaps)

	writeRepoFile(t, repo, "clean.txt", "edited\n")  // Clean before, now listed.
	writeRepoFile(t, repo, "README.md", "dirtier\n") // Listed both times, new hash.
	writeRepoFile(t, repo, "reverted.txt", "base\n") // Listed before, left the list.
	writeRepoFile(t, repo, "fresh.txt", "fresh\n")   // New and untracked.
	after := snapshot(t, repo, SnapshotCaps{})

	require.Equal(t, []string{"README.md", "clean.txt", "fresh.txt", "reverted.txt"}, changed(t, repo, before, after))
	require.Empty(t, changed(t, repo, after, snapshot(t, repo, SnapshotCaps{})), "nothing changed since")
}

func TestChangedPathsFollowsAMovedHead(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	before := snapshot(t, repo, testCaps)
	writeRepoFile(t, repo, "README.md", "committed\n")
	runGit(t, repo, "commit", "-am", "inside the command")
	after := snapshot(t, repo, SnapshotCaps{})
	require.Empty(t, after.Files, "a committed file leaves the status list")
	require.Equal(t, []string{"README.md"}, changed(t, repo, before, after))

	result := RestorePaths(context.Background(), repo, before, []string{"README.md"})
	require.Equal(t, []string{"README.md"}, result.Restored)
	require.Equal(t, "hello\n", readRepoFile(t, repo, "README.md"), "restored from the HEAD taken before")
}

func TestRestorePathsUndoesEachKind(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "dirty.txt", "base\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	writeRepoFile(t, repo, "dirty.txt", "mine\n")
	writeRepoFile(t, repo, "untracked.txt", "kept\n")
	writeRepoFile(t, repo, "big.bin", strings.Repeat("x", 2<<20))
	before := snapshot(t, repo, testCaps)
	require.False(t, before.Files["big.bin"].Saved, "a file over the per-file cap is hashed, not saved")

	writeRepoFile(t, repo, "README.md", "theirs\n")     // Clean tracked.
	writeRepoFile(t, repo, "dirty.txt", "theirs\n")     // Dirty tracked.
	writeRepoFile(t, repo, "untracked.txt", "theirs\n") // Untracked that existed.
	writeRepoFile(t, repo, "new/added.txt", "new\n")    // New untracked.
	runGit(t, repo, "add", "new/added.txt")             // Even when staged.
	writeRepoFile(t, repo, "big.bin", "small now\n")    // Over the cap.

	paths := changed(t, repo, before, snapshot(t, repo, SnapshotCaps{}))
	result := RestorePaths(context.Background(), repo, before, paths)
	require.ElementsMatch(t, []string{"README.md", "dirty.txt", "untracked.txt", "new/added.txt"}, result.Restored)
	require.Equal(t, []string{"big.bin"}, result.Unrestored)

	require.Equal(t, "hello\n", readRepoFile(t, repo, "README.md"))
	require.Equal(t, "mine\n", readRepoFile(t, repo, "dirty.txt"), "a dirty file gets its saved content back, not HEAD's")
	require.Equal(t, "kept\n", readRepoFile(t, repo, "untracked.txt"))
	require.NoFileExists(t, filepath.Join(repo, "new", "added.txt"))
	require.Equal(t, "small now\n", readRepoFile(t, repo, "big.bin"), "a path over the cap is reported, not restored")
	require.NotContains(t, gitText(t, repo, "status", "--porcelain"), "new/added.txt", "the staged new file left the index")
}

func TestRestorePathsTreatsPathspecMagicLiterally(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows file names cannot hold *")
	}
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "*.md", "star\n")
	writeRepoFile(t, repo, "other.md", "other\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	before := snapshot(t, repo, testCaps)
	writeRepoFile(t, repo, "*.md", "changed\n")
	writeRepoFile(t, repo, "other.md", "changed\n")

	result := RestorePaths(context.Background(), repo, before, []string{"*.md"})
	require.Equal(t, []string{"*.md"}, result.Restored)
	require.Equal(t, "star\n", readRepoFile(t, repo, "*.md"))
	require.Equal(t, "changed\n", readRepoFile(t, repo, "other.md"), "the glob matched only its own name")
}

// TestRestorePathsRestoresTheSourceOfAMove is controller ruling W7: a
// command that moves an in-scope file onto an out-of-scope path leaves the
// in-scope original missing when only the out-of-scope path is restored.
func TestRestorePathsRestoresTheSourceOfAMove(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "dirty.txt", "base\n")
	runGit(t, repo, "add", "dirty.txt")
	runGit(t, repo, "commit", "-m", "setup")
	writeRepoFile(t, repo, "dirty.txt", "mine\n")
	writeRepoFile(t, repo, "untracked.txt", "kept\n")
	before := snapshot(t, repo, testCaps)

	require.NoError(t, os.Rename(filepath.Join(repo, "dirty.txt"), filepath.Join(repo, "away-dirty.txt")))
	require.NoError(t, os.Rename(filepath.Join(repo, "untracked.txt"), filepath.Join(repo, "away-new.txt")))

	result := RestorePaths(context.Background(), repo, before, []string{"away-dirty.txt", "away-new.txt"})
	require.ElementsMatch(t, []string{"away-dirty.txt", "away-new.txt", "dirty.txt", "untracked.txt"}, result.Restored)
	require.Empty(t, result.Unrestored)

	require.NoFileExists(t, filepath.Join(repo, "away-dirty.txt"))
	require.NoFileExists(t, filepath.Join(repo, "away-new.txt"))
	require.Equal(t, "mine\n", readRepoFile(t, repo, "dirty.txt"), "the dirty original came back from the snapshot")
	require.Equal(t, "kept\n", readRepoFile(t, repo, "untracked.txt"), "the untracked original came back from the snapshot")
	require.Equal(t, "M dirty.txt\n?? untracked.txt", gitText(t, repo, "status", "--porcelain"))
}

// TestRestorePathsRestoresACleanFileMovedAway is W7 too: a clean tracked
// file the command moved out of scope has no saved content, so it comes
// back from the HEAD taken before.
func TestRestorePathsRestoresACleanFileMovedAway(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	before := snapshot(t, repo, testCaps)
	require.NoError(t, os.Rename(filepath.Join(repo, "README.md"), filepath.Join(repo, "away.md")))

	result := RestorePaths(context.Background(), repo, before, []string{"away.md"})
	require.ElementsMatch(t, []string{"away.md", "README.md"}, result.Restored)
	require.Empty(t, result.Unrestored)
	require.NoFileExists(t, filepath.Join(repo, "away.md"))
	require.Equal(t, "hello\n", readRepoFile(t, repo, "README.md"))
	require.Empty(t, gitText(t, repo, "status", "--porcelain"))
}

// TestRestorePathsReversesAMoveItCannotRestore is W10: an over-cap source
// cannot be rewritten from the snapshot, so the moved bytes are renamed
// back instead of being deleted with the destination.
func TestRestorePathsReversesAMoveItCannotRestore(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	big := strings.Repeat("x", 2<<20)
	writeRepoFile(t, repo, "big.bin", big)
	before := snapshot(t, repo, testCaps)
	require.False(t, before.Files["big.bin"].Saved, "hashed, not saved")
	require.NoError(t, os.Rename(filepath.Join(repo, "big.bin"), filepath.Join(repo, "away.bin")))

	result := RestorePaths(context.Background(), repo, before, []string{"away.bin"})
	require.ElementsMatch(t, []string{"away.bin", "big.bin"}, result.Restored, "the move was reversed")
	require.Empty(t, result.Unrestored)
	require.NoFileExists(t, filepath.Join(repo, "away.bin"))
	require.Equal(t, big, readRepoFile(t, repo, "big.bin"), "the only copy of the bytes moved back")
}

// TestRestorePathsKeepsTheDirtyContentOfAMovedFile is W10: a dirty tracked
// file over the cap must keep its dirty bytes, not HEAD's.
func TestRestorePathsKeepsTheDirtyContentOfAMovedFile(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	base := strings.Repeat("a", 2<<20)
	dirty := strings.Repeat("b", 2<<20)
	writeRepoFile(t, repo, "big.bin", base)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	writeRepoFile(t, repo, "big.bin", dirty)
	before := snapshot(t, repo, testCaps)
	require.False(t, before.Files["big.bin"].Saved)
	require.NoError(t, os.Rename(filepath.Join(repo, "big.bin"), filepath.Join(repo, "away.bin")))

	result := RestorePaths(context.Background(), repo, before, []string{"away.bin"})
	require.ElementsMatch(t, []string{"away.bin", "big.bin"}, result.Restored)
	require.Empty(t, result.Unrestored)
	require.NoFileExists(t, filepath.Join(repo, "away.bin"))
	require.Equal(t, dirty, readRepoFile(t, repo, "big.bin"), "the dirty content is kept, not HEAD's")
	require.Equal(t, "M big.bin", gitText(t, repo, "status", "--porcelain"))
}

// TestRestorePathsKeepsAMoveItCannotReverse is W10's fallback: when the
// bytes cannot be put back where they came from, the destination stays and
// both paths are reported rather than deleted.
func TestRestorePathsKeepsAMoveItCannotReverse(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	big := strings.Repeat("x", 2<<20)
	writeRepoFile(t, repo, "sub/big.bin", big)
	before := snapshot(t, repo, testCaps)
	require.NoError(t, os.Rename(filepath.Join(repo, "sub", "big.bin"), filepath.Join(repo, "away.bin")))
	require.NoError(t, os.Remove(filepath.Join(repo, "sub")))
	writeRepoFile(t, repo, "sub", "not a directory\n")

	result := RestorePaths(context.Background(), repo, before, []string{"away.bin"})
	require.ElementsMatch(t, []string{"away.bin", "sub/big.bin"}, result.Unrestored)
	require.Empty(t, result.Restored)
	require.Equal(t, big, readRepoFile(t, repo, "away.bin"), "the only copy of the bytes stays")
}

// TestRestorePathsStaysInsideTheWorktree is W10: a parent replaced by a
// symbolic link out of the worktree is never written through.
func TestRestorePathsStaysInsideTheWorktree(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "sub/notes.txt", "mine\n")
	before := snapshot(t, repo, testCaps)
	outside := t.TempDir()
	writeRepoFile(t, outside, "notes.txt", "outside\n")
	require.NoError(t, os.RemoveAll(filepath.Join(repo, "sub")))
	require.NoError(t, os.Symlink(outside, filepath.Join(repo, "sub")))

	result := RestorePaths(context.Background(), repo, before, []string{"sub/notes.txt"})
	require.Equal(t, []string{"sub/notes.txt"}, result.Unrestored)
	require.Equal(t, "outside\n", readRepoFile(t, outside, "notes.txt"), "the link out of the worktree is untouched")
}

// TestRestorePathsDoesNotDeleteThroughASymlinkedParent is W10: a delete
// never follows a parent symbolic link out of the worktree.
func TestRestorePathsDoesNotDeleteThroughASymlinkedParent(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "sub/notes.txt", "mine\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	require.NoError(t, os.Remove(filepath.Join(repo, "sub", "notes.txt")))
	before := snapshot(t, repo, testCaps)
	require.False(t, before.Files["sub/notes.txt"].Exists)
	outside := t.TempDir()
	writeRepoFile(t, outside, "notes.txt", "outside\n")
	require.NoError(t, os.RemoveAll(filepath.Join(repo, "sub")))
	require.NoError(t, os.Symlink(outside, filepath.Join(repo, "sub")))

	result := RestorePaths(context.Background(), repo, before, []string{"sub/notes.txt"})
	require.Equal(t, []string{"sub/notes.txt"}, result.Unrestored)
	require.Equal(t, "outside\n", readRepoFile(t, outside, "notes.txt"), "the link out of the worktree is untouched")
}

// TestRestorePathsRefusesPathsOutsideTheWorktree is W10: an absolute path,
// a path that climbs out and a path in .git are all left alone.
func TestRestorePathsRefusesPathsOutsideTheWorktree(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	before := snapshot(t, repo, testCaps)
	config := readRepoFile(t, repo, ".git/config")
	paths := []string{"../evil.txt", filepath.ToSlash(filepath.Join(repo, "abs.txt")), ".git/config"}

	result := RestorePaths(context.Background(), repo, before, paths)
	require.ElementsMatch(t, paths, result.Unrestored)
	require.Empty(t, result.Restored)
	require.NoFileExists(t, filepath.Join(filepath.Dir(repo), "evil.txt"))
	require.Equal(t, config, readRepoFile(t, repo, ".git/config"), "the git directory is untouched")
}

// TestRestorePathsKeepsAnInScopeDeletion is W10: an empty file proves no
// move, so a deletion the scopes allowed stays.
func TestRestorePathsKeepsAnInScopeDeletion(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "scope/__init__.py", "")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	before := snapshot(t, repo, testCaps)

	require.NoError(t, os.Remove(filepath.Join(repo, "scope", "__init__.py")))
	writeRepoFile(t, repo, "other/x.log", "")
	after := snapshot(t, repo, SnapshotCaps{})
	require.ElementsMatch(t, []string{"other/x.log", "scope/__init__.py"}, changed(t, repo, before, after))

	result := RestorePaths(context.Background(), repo, before, []string{"other/x.log"})
	require.Equal(t, []string{"other/x.log"}, result.Restored)
	require.Empty(t, result.Unrestored)
	require.NoFileExists(t, filepath.Join(repo, "other", "x.log"))
	require.NoFileExists(t, filepath.Join(repo, "scope", "__init__.py"), "an empty deletion is no source of a move")
}

// TestRestorePathsLeavesAnAmbiguousMove is W10: when several removed paths
// hold the same bytes, the destination stays, since it may be the last copy.
func TestRestorePathsLeavesAnAmbiguousMove(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "a.txt", "same\n")
	writeRepoFile(t, repo, "b.txt", "same\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	before := snapshot(t, repo, testCaps)
	require.NoError(t, os.Remove(filepath.Join(repo, "a.txt")))
	require.NoError(t, os.Remove(filepath.Join(repo, "b.txt")))
	writeRepoFile(t, repo, "away.txt", "same\n")

	result := RestorePaths(context.Background(), repo, before, []string{"away.txt"})
	require.Equal(t, []string{"away.txt"}, result.Unrestored)
	require.Empty(t, result.Restored)
	require.Equal(t, "same\n", readRepoFile(t, repo, "away.txt"), "the bytes are kept rather than deleted")
}

// TestRestorePathsRestoresTheStagedIndex is W10: a staged index entry the
// command changed comes back as it was, and an untracked path the command
// staged leaves the index.
func TestRestorePathsRestoresTheStagedIndex(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "README.md", "staged\n")
	runGit(t, repo, "add", "README.md")
	writeRepoFile(t, repo, "README.md", "worktree\n")
	writeRepoFile(t, repo, "notes.txt", "kept\n")
	before := snapshot(t, repo, testCaps)

	writeRepoFile(t, repo, "README.md", "theirs\n")
	runGit(t, repo, "add", "README.md")
	writeRepoFile(t, repo, "notes.txt", "theirs\n")
	runGit(t, repo, "add", "notes.txt")

	result := RestorePaths(context.Background(), repo, before, []string{"README.md", "notes.txt"})
	require.ElementsMatch(t, []string{"README.md", "notes.txt"}, result.Restored)
	require.Empty(t, result.Unrestored)
	require.Equal(t, "worktree\n", readRepoFile(t, repo, "README.md"))
	require.Equal(t, "staged", gitText(t, repo, "show", ":README.md"), "the index entry from before is back")
	require.Equal(t, "kept\n", readRepoFile(t, repo, "notes.txt"))
	err := exec.CommandContext(context.Background(), "git", "-C", repo, "ls-files", "--error-unmatch", "notes.txt").Run()
	require.Error(t, err, "the untracked path left the index")
}

// TestSnapshotFilesKeepsBracketNames is W10: only a rename record names an
// original, so a file called "x <- y.txt" never yields a phantom "y.txt".
func TestSnapshotFilesKeepsBracketNames(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "y.txt", "keep\n")
	writeRepoFile(t, repo, "x <- y.txt", "star\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "setup")
	before := snapshot(t, repo, testCaps)
	writeRepoFile(t, repo, "x <- y.txt", "changed\n")
	after := snapshot(t, repo, testCaps)
	require.ElementsMatch(t, []string{"x <- y.txt"}, keys(after.Files), "a name with <- is not a rename record")

	paths := changed(t, repo, before, after)
	require.Equal(t, []string{"x <- y.txt"}, paths)
	result := RestorePaths(context.Background(), repo, before, paths)
	require.Equal(t, []string{"x <- y.txt"}, result.Restored)
	require.Equal(t, "star\n", readRepoFile(t, repo, "x <- y.txt"))
	require.Equal(t, "keep\n", readRepoFile(t, repo, "y.txt"), "the path named in the file is untouched")
}

// TestRestorePathsFollowsAStagedRename is W7 for a rename git reports: the
// original named by the rename record is restored too.
func TestRestorePathsFollowsAStagedRename(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	before := snapshot(t, repo, testCaps)
	runGit(t, repo, "mv", "README.md", "away.md")
	after := snapshot(t, repo, testCaps)
	require.ElementsMatch(t, []string{"README.md", "away.md"}, keys(after.Files), "a rename lists both paths")
	require.False(t, after.Files["README.md"].Exists)

	result := RestorePaths(context.Background(), repo, before, []string{"away.md"})
	require.ElementsMatch(t, []string{"away.md", "README.md"}, result.Restored)
	require.Empty(t, result.Unrestored)
	require.NoFileExists(t, filepath.Join(repo, "away.md"))
	require.Equal(t, "hello\n", readRepoFile(t, repo, "README.md"))
	require.Empty(t, gitText(t, repo, "status", "--porcelain"))
}

// TestRestorePathsKeepsACopiedFile is W7's other side: a copy leaves its
// original in place, so only the new path is restored.
func TestRestorePathsKeepsACopiedFile(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "notes.txt", "mine\n")
	before := snapshot(t, repo, testCaps)
	writeRepoFile(t, repo, "away.txt", "mine\n")

	result := RestorePaths(context.Background(), repo, before, []string{"away.txt"})
	require.Equal(t, []string{"away.txt"}, result.Restored)
	require.NoFileExists(t, filepath.Join(repo, "away.txt"))
	require.Equal(t, "mine\n", readRepoFile(t, repo, "notes.txt"), "a copied original stays as it was")
}

// TestRestorePathsReversesAMoveOntoAnExistingPath is W10 too: the
// destination gets its own saved content back while the over-cap original
// comes back by reversing the move onto it.
func TestRestorePathsReversesAMoveOntoAnExistingPath(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	big := strings.Repeat("x", 2<<20)
	writeRepoFile(t, repo, "big.bin", big)
	writeRepoFile(t, repo, "away.txt", "theirs\n")
	before := snapshot(t, repo, testCaps)
	require.True(t, before.Files["away.txt"].Saved)
	require.False(t, before.Files["big.bin"].Saved)
	require.NoError(t, os.Rename(filepath.Join(repo, "big.bin"), filepath.Join(repo, "away.txt")))

	result := RestorePaths(context.Background(), repo, before, []string{"away.txt"})
	require.ElementsMatch(t, []string{"away.txt", "big.bin"}, result.Restored)
	require.Empty(t, result.Unrestored)
	require.Equal(t, big, readRepoFile(t, repo, "big.bin"), "the moved bytes came back")
	require.Equal(t, "theirs\n", readRepoFile(t, repo, "away.txt"), "the destination's own content is back")
}

// TestRestorePathsKeepsConflictedStages is fix round 2: a restore in the
// middle of a merge puts the unmerged stages back instead of clearing them.
func TestRestorePathsKeepsConflictedStages(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	runGit(t, repo, "checkout", "-b", "other")
	writeRepoFile(t, repo, "README.md", "theirs\n")
	runGit(t, repo, "commit", "-am", "theirs")
	runGit(t, repo, "checkout", "main")
	writeRepoFile(t, repo, "README.md", "mine\n")
	runGit(t, repo, "commit", "-am", "mine")
	conflict := exec.CommandContext(context.Background(), "git", "-C", repo, "merge", "--no-ff", "-m", "merge", "other")
	require.Error(t, conflict.Run(), "the merge conflicts")
	require.Equal(t, "UU README.md", gitText(t, repo, "status", "--porcelain"))
	stages := gitText(t, repo, "ls-files", "-s", "README.md")
	before := snapshot(t, repo, testCaps)
	require.True(t, before.Files["README.md"].Unmerged)
	require.Len(t, before.Files["README.md"].IndexStages, 3)

	// The command resolves the conflict and stages its own content.
	writeRepoFile(t, repo, "README.md", "resolved\n")
	runGit(t, repo, "add", "README.md")

	result := RestorePaths(context.Background(), repo, before, []string{"README.md"})
	require.Equal(t, []string{"README.md"}, result.Restored)
	require.Empty(t, result.Unrestored)
	require.Contains(t, readRepoFile(t, repo, "README.md"), "<<<<<<<", "the conflicted content is back")
	require.Equal(t, stages, gitText(t, repo, "ls-files", "-s", "README.md"), "the same stages are back")
	require.Equal(t, "UU README.md", gitText(t, repo, "status", "--porcelain"))
	require.True(t, mergeInProgressForTest(repo), "the merge is still in progress")
}

// TestRestorePathsKeepsAnIntentToAddEntry is fix round 2: a path git add -N
// put in the index stays an intent to add after a restore.
func TestRestorePathsKeepsAnIntentToAddEntry(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "ita.txt", "planned\n")
	runGit(t, repo, "add", "-N", "ita.txt")
	before := snapshot(t, repo, testCaps)
	require.True(t, before.Files["ita.txt"].IntentToAdd)
	require.Empty(t, before.Files["ita.txt"].IndexOID, "an intent to add holds no object")

	writeRepoFile(t, repo, "ita.txt", "changed\n")
	result := RestorePaths(context.Background(), repo, before, []string{"ita.txt"})
	require.Equal(t, []string{"ita.txt"}, result.Restored)
	require.Empty(t, result.Unrestored)
	require.Equal(t, "planned\n", readRepoFile(t, repo, "ita.txt"))
	status := gitText(t, repo, "status", "--porcelain=v2")
	require.True(t, strings.HasPrefix(status, "1 .A "), "still an intent to add: %s", status)
}

// TestRestorePathsDoesNotReadThroughASymlinkedParent is fix round 2: move
// detection reads the worktree through the root, so a file behind a
// symbolic link out of it is never taken for a moved in-scope file.
func TestRestorePathsDoesNotReadThroughASymlinkedParent(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	writeRepoFile(t, repo, "scope/log.txt", "mine\n")
	before := snapshot(t, repo, testCaps)
	outside := t.TempDir()
	writeRepoFile(t, outside, "leak.txt", "mine\n")
	require.NoError(t, os.RemoveAll(filepath.Join(repo, "scope")))
	require.NoError(t, os.Symlink(outside, filepath.Join(repo, "scope")))

	result := RestorePaths(context.Background(), repo, before, []string{"scope/leak.txt"})
	require.Equal(t, []string{"scope/leak.txt"}, result.Unrestored)
	require.Empty(t, result.Restored, "the outside file is no moved in-scope file")
	require.Equal(t, "mine\n", readRepoFile(t, outside, "leak.txt"), "the outside file is untouched")
	require.NoFileExists(t, filepath.Join(outside, "log.txt"))
}
