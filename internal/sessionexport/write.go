package sessionexport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// ExportSource is what WriteFile reads a session from. Both the terminal's
// workspace and the gateway's satisfy it.
type ExportSource interface {
	GetSession(ctx context.Context, id string) (session.Session, error)
	ListMessages(ctx context.Context, id string) ([]message.Message, error)
	WorkingDir() string
}

// WriteFile writes the session's Markdown export into the working directory
// and returns the path. The terminal's "Export Session as Markdown" command
// and the web gateway's export route share it, so both write the same file.
// The base name keeps an id from escaping the working directory.
func WriteFile(ctx context.Context, src ExportSource, sessionID string) (string, error) {
	sess, err := src.GetSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	msgs, err := src.ListMessages(ctx, sessionID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(src.WorkingDir(), "crush-session-"+filepath.Base(sessionID)+".md")
	if err := os.WriteFile(path, []byte(Markdown(sess, msgs)), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}
