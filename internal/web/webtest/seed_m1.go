package webtest

import (
	"strconv"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

// SeedM1 gives the fake the sessions the M1 browser specs drive: one
// running, one with a queued prompt, one waiting for the person and one
// idle. Call it before the gateway starts; then call SeedM1Pending once the
// gateway has subscribed, so the open permission reaches its pending store.
func SeedM1(f *Fake, clock time.Time) {
	seeded := []struct {
		id        string
		title     string
		updatedAt int64
		messages  []string
	}{
		{"s-m1-run", "Port the parser", clock.Unix(), []string{"Read the tokenizer", "Port the tokenizer"}},
		{"s-m1-queue", "Write parser tests", clock.Add(-time.Minute).Unix(), []string{"Draft the table tests"}},
		{"s-m1-wait", "Rename the grammar", clock.Add(-2 * time.Minute).Unix(), []string{"Rename Token to Lexeme"}},
		{"s-m1-idle", "Old scratch session", clock.Add(-time.Hour).Unix(), []string{"Hello"}},
	}
	for _, s := range seeded {
		f.AddSession(session.Session{ID: s.id, Title: s.title, UpdatedAt: s.updatedAt})
		for i, text := range s.messages {
			role := message.User
			if i%2 == 1 {
				role = message.Assistant
			}
			at := s.updatedAt + int64(i)
			f.AddMessage(message.Message{
				ID: s.id + "-m" + strconv.Itoa(i), SessionID: s.id, Role: role,
				CreatedAt: at, UpdatedAt: at, Model: "claude-haiku", Provider: "anthropic",
				Parts: []message.ContentPart{message.TextContent{Text: text}},
			})
		}
	}
	f.SetSessionBusy("s-m1-run", true)
	f.SetSessionQueued("s-m1-queue", []string{"and the golden files"})
}

// SeedM1Pending publishes the permission request that leaves a session
// waiting for the person. It must run after the gateway started, or the
// event has no subscriber.
func SeedM1Pending(f *Fake) {
	f.Emit(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
		ID: "p-m1", SessionID: "s-m1-wait", ToolCallID: "tc-m1",
		ToolName: "edit", Description: "Edit internal/parser/parser.go",
		Path: "/tmp/project/internal/parser/parser.go",
	}})
}
