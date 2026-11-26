//go:build unix

package roles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDiscoverRefusesSpecialAndOversizeFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, unix.Mkfifo(filepath.Join(dir, "fifo.md"), 0o644))
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(dir, "zero.md")))
	writeRole(t, dir, "huge", "---\nname: huge\ndescription: d\n---\n"+strings.Repeat("a", MaxBodyBytes+8<<10))
	writeRole(t, dir, "ok", "---\nname: ok\ndescription: d\n---\nbody\n")

	type result struct {
		found map[string]Role
		diags []Diagnostic
	}
	done := make(chan result, 1)
	go func() {
		found, diags := Discover([]Dir{{Path: dir, Source: SourceProject}}, nil)
		done <- result{found, diags}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Discover hung on a special file")
	}
	require.Contains(t, got.found, "ok", "the readable role still loads")
	messages := map[string]string{}
	for _, d := range got.diags {
		messages[filepath.Base(d.Path)] = d.Message
	}
	require.Contains(t, messages["fifo.md"], "not a regular file")
	require.Contains(t, messages["zero.md"], "not a regular file")
	require.Contains(t, messages["huge.md"], "file too large")
}
