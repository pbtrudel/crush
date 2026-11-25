package sessionexport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// exportSrc is an ExportSource with one fixed session.
type exportSrc struct{ dir string }

func (e exportSrc) GetSession(context.Context, string) (session.Session, error) {
	return session.Session{ID: "s1", Title: "Parser"}, nil
}

func (e exportSrc) ListMessages(context.Context, string) ([]message.Message, error) {
	return []message.Message{{ID: "m1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}}}, nil
}

func (e exportSrc) WorkingDir() string { return e.dir }

func TestWriteFileWritesTheMarkdown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, err := WriteFile(t.Context(), exportSrc{dir: dir}, "../s1")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "crush-session-s1.md"), path, "the id cannot escape the working directory")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), "Parser")
}
