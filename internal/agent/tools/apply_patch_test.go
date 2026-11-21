package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// applyPatchFixture is one working directory and the file tracker the tool
// records its reads in, so a test never repeats the wiring.
type applyPatchFixture struct {
	dir     string
	tracker *mockEditFileTracker
	tool    fantasy.AgentTool
}

func newApplyPatchFixture(t *testing.T) applyPatchFixture {
	t.Helper()
	dir := t.TempDir()
	tracker := &mockEditFileTracker{lastRead: time.Now().Add(time.Second)}
	return applyPatchFixture{
		dir:     dir,
		tracker: tracker,
		tool:    NewApplyPatchTool(nil, &mockPermissionService{}, &mockHistoryService{}, tracker, dir),
	}
}

func (f applyPatchFixture) path(name string) string {
	return filepath.Join(f.dir, name)
}

func (f applyPatchFixture) write(t *testing.T, name, content string) string {
	t.Helper()
	path := f.path(name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func (f applyPatchFixture) read(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(f.path(name))
	require.NoError(t, err)
	return string(content)
}

func (f applyPatchFixture) run(t *testing.T, patch string) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(ApplyPatchParams{Patch: patch})
	require.NoError(t, err)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session")
	resp, err := f.tool.Run(ctx, fantasy.ToolCall{ID: "call", Name: ApplyPatchToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

func TestApplyPatchAddsAFile(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	resp := f.run(t, `*** Begin Patch
*** Add File: added.txt
+alpha
+beta
*** End Patch
`)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "alpha\nbeta\n", f.read(t, "added.txt"))
	require.Equal(t, []string{f.path("added.txt")}, f.tracker.reads)
}

func TestApplyPatchUpdatesWithContext(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	f.write(t, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")

	resp := f.run(t, `*** Begin Patch
*** Update File: main.go
@@ func main() {
 func main() {
-	println("hello")
+	println("world")
 }
*** End Patch
`)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "package main\n\nfunc main() {\n\tprintln(\"world\")\n}\n", f.read(t, "main.go"))
}

func TestApplyPatchUpdatesSeveralHunks(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	f.write(t, "notes.txt", "one\ntwo\nthree\nfour\nfive\n")

	resp := f.run(t, `*** Begin Patch
*** Update File: notes.txt
@@
 one
-two
+TWO
 three
@@
 four
-five
+FIVE
*** End Patch
`)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "one\nTWO\nthree\nfour\nFIVE\n", f.read(t, "notes.txt"))
}

// A model often separates two hunks with a blank line. It is a separator, not
// a context line, or the patch would demand a blank line the file lacks.
func TestApplyPatchIgnoresABlankLineBetweenHunks(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	f.write(t, "notes.txt", "one\ntwo\nthree\nfour\nfive\n")

	resp := f.run(t, `*** Begin Patch
*** Update File: notes.txt
@@
 one
-two
+TWO
 three

@@
 four
-five
+FIVE
*** End Patch
`)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "one\nTWO\nthree\nfour\nFIVE\n", f.read(t, "notes.txt"))
}

func TestApplyPatchDeletesAFile(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	f.write(t, "gone.txt", "alpha\n")

	resp := f.run(t, `*** Begin Patch
*** Delete File: gone.txt
*** End Patch
`)
	require.False(t, resp.IsError, resp.Content)

	_, err := os.Stat(f.path("gone.txt"))
	require.True(t, os.IsNotExist(err), "the deleted file must be gone")
}

func TestApplyPatchMovesAFile(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	f.write(t, "from.txt", "alpha\nbeta\n")

	resp := f.run(t, `*** Begin Patch
*** Update File: from.txt
*** Move to: to.txt
@@
 alpha
-beta
+BETA
*** End Patch
`)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "alpha\nBETA\n", f.read(t, "to.txt"))

	_, err := os.Stat(f.path("from.txt"))
	require.True(t, os.IsNotExist(err), "the source of a move must be gone")
}

func TestApplyPatchContextMismatchLeavesTheFileAlone(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	f.write(t, "kept.txt", "alpha\n")

	resp := f.run(t, `*** Begin Patch
*** Update File: kept.txt
@@
-gamma
+delta
*** End Patch
`)
	require.True(t, resp.IsError)
	require.Equal(t, "alpha\n", f.read(t, "kept.txt"))
	require.Equal(t, ErrorKindTextNotFound, MetadataErrorKind(resp.Metadata))
}

func TestApplyPatchAppliesNothingWhenOneFileFails(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	f.write(t, "kept.txt", "alpha\n")

	resp := f.run(t, `*** Begin Patch
*** Add File: added.txt
+alpha
*** Update File: kept.txt
@@
-gamma
+delta
*** End Patch
`)
	require.True(t, resp.IsError)

	_, err := os.Stat(f.path("added.txt"))
	require.True(t, os.IsNotExist(err), "a patch that fails anywhere must not create its files")
	require.Equal(t, "alpha\n", f.read(t, "kept.txt"))
}

func TestApplyPatchKeepsCRLF(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	f.write(t, "crlf.txt", "alpha\r\nbeta\r\n")

	resp := f.run(t, `*** Begin Patch
*** Update File: crlf.txt
@@
 alpha
-beta
+BETA
*** End Patch
`)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "alpha\r\nBETA\r\n", f.read(t, "crlf.txt"))
}

func TestApplyPatchRefusesAFileThatWasNotRead(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	f.tracker.lastRead = time.Time{}
	f.write(t, "unread.txt", "alpha\n")

	resp := f.run(t, `*** Begin Patch
*** Update File: unread.txt
@@
-alpha
+beta
*** End Patch
`)
	require.True(t, resp.IsError)
	require.Equal(t, ErrorKindNotRead, MetadataErrorKind(resp.Metadata))
	require.Equal(t, "alpha\n", f.read(t, "unread.txt"))
}

func TestApplyPatchRejectsAnUnterminatedPatch(t *testing.T) {
	t.Parallel()

	f := newApplyPatchFixture(t)
	resp := f.run(t, `*** Begin Patch
*** Add File: added.txt
+alpha
`)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "*** End Patch")

	_, err := os.Stat(f.path("added.txt"))
	require.True(t, os.IsNotExist(err))
}

func TestApplyPatchParsesSections(t *testing.T) {
	t.Parallel()

	ops, err := parseApplyPatch(`*** Begin Patch
*** Add File: added.txt
+alpha
*** Update File: updated.txt
*** Move to: moved.txt
@@ heading
 ctx
-old
+new
*** Delete File: deleted.txt
*** End Patch
`)
	require.NoError(t, err)
	require.Len(t, ops, 3)

	require.Equal(t, patchOpAdd, ops[0].kind)
	require.Equal(t, "added.txt", ops[0].path)
	require.Equal(t, []string{"alpha"}, ops[0].content)

	require.Equal(t, patchOpUpdate, ops[1].kind)
	require.Equal(t, "updated.txt", ops[1].path)
	require.Equal(t, "moved.txt", ops[1].moveTo)
	require.Len(t, ops[1].hunks, 1)
	require.Equal(t, []patchHunk{{
		{kind: ' ', text: "ctx"},
		{kind: '-', text: "old"},
		{kind: '+', text: "new"},
	}}, ops[1].hunks)

	require.Equal(t, patchOpDelete, ops[2].kind)
	require.Equal(t, "deleted.txt", ops[2].path)
}
