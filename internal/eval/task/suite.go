package task

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Git tree entry modes this package writes.
const (
	modeFile = "100644"
	modeExec = "100755"
	modeTree = "40000"
)

// SuiteVersion is the git tree hash of a suite's tasks directory and its
// footer file, computed from the files on disk the way git writes them. It
// changes when a task or the footer changes and not when any other file does.
func SuiteVersion(evalsDir string) (string, error) {
	tasks, err := treeHashDir(filepath.Join(evalsDir, "tasks"))
	if err != nil {
		return "", err
	}
	footer, err := hashFile(filepath.Join(evalsDir, "footer.md"))
	if err != nil {
		return "", err
	}
	return treeHash([]treeEntry{
		{mode: footer.mode, name: "footer.md", sha: footer.sha},
		{mode: modeTree, name: "tasks", sha: tasks},
	}), nil
}

// treeEntry is one entry of a git tree.
type treeEntry struct {
	mode string
	name string
	sha  string
}

// sortKey is what git sorts tree entries by: a tree sorts as its name with a
// trailing slash.
func (e treeEntry) sortKey() string {
	if e.mode == modeTree {
		return e.name + "/"
	}
	return e.name
}

// treeHash hashes a directory listing the way git hashes a tree object.
func treeHash(entries []treeEntry) string {
	sort.Slice(entries, func(i, j int) bool { return entries[i].sortKey() < entries[j].sortKey() })
	var buf bytes.Buffer
	for _, entry := range entries {
		buf.WriteString(entry.mode)
		buf.WriteByte(' ')
		buf.WriteString(entry.name)
		buf.WriteByte(0)
		sha, _ := hex.DecodeString(entry.sha)
		buf.Write(sha)
	}
	return objectHash("tree", buf.Bytes())
}

// treeHashDir hashes the files of one directory as a git tree. Subdirectories
// are not supported: the suite keeps its tasks in one flat directory.
func treeHashDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	tree := make([]treeEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return "", fmt.Errorf("%s: subdirectories are not supported", filepath.Join(dir, entry.Name()))
		}
		file, err := hashFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", err
		}
		tree = append(tree, treeEntry{mode: file.mode, name: entry.Name(), sha: file.sha})
	}
	return treeHash(tree), nil
}

// fileEntry is the blob sha and tree mode of one file.
type fileEntry struct {
	mode string
	sha  string
}

// hashFile returns the blob sha of a file's contents and the mode git would
// record for it.
func hashFile(path string) (fileEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileEntry{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileEntry{}, err
	}
	mode := modeFile
	if info.Mode()&0o111 != 0 {
		mode = modeExec
	}
	return fileEntry{mode: mode, sha: objectHash("blob", data)}, nil
}

// objectHash returns the sha1 git gives an object of the given kind and
// contents.
func objectHash(kind string, content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d%c", kind, len(content), 0)
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}
