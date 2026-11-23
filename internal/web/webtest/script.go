package webtest

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
)

// Scripted returns an AgentRun implementation that plays a short, fixed
// conversation. Prompts containing "permission" raise a permission
// request; prompts containing "question" raise a question batch.
func Scripted(f *Fake) func(ctx context.Context, sid, prompt string, att []message.Attachment) error {
	return func(ctx context.Context, sid, prompt string, _ []message.Attachment) error {
		f.drainResolutions()
		now := time.Now().Unix()
		user := message.Message{
			ID: f.nextID("u"), SessionID: sid, Role: message.User, CreatedAt: now, UpdatedAt: now,
			Parts: []message.ContentPart{message.TextContent{Text: prompt}},
		}
		f.AddMessage(user)
		f.Emit(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: user})

		asst := message.Message{ID: f.nextID("a"), SessionID: sid, Role: message.Assistant, CreatedAt: now + 1, UpdatedAt: now + 1}
		emit := func(parts ...message.ContentPart) {
			asst.Parts = parts
			f.Emit(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: asst})
		}
		thinking := message.ReasoningContent{Thinking: "Mapping the request onto the workspace."}
		emit(thinking)
		for _, chunk := range []string{"Signal ", "Signal received. ", "Signal received. " + prompt} {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(60 * time.Millisecond):
			}
			emit(thinking, message.TextContent{Text: chunk})
		}
		text := "Signal received. " + prompt
		switch {
		case strings.Contains(prompt, "permission"):
			call := message.ToolCall{ID: f.nextID("tc"), Name: "bash", Input: `{"command":"go test ./..."}`}
			emit(thinking, message.TextContent{Text: text}, call)
			req := permission.PermissionRequest{
				ID: f.nextID("perm"), SessionID: sid, ToolCallID: call.ID, ToolName: "bash",
				Description: "Run the test suite", Action: "execute", Params: map[string]string{"command": "go test ./..."}, Path: f.WorkingDir(),
			}
			f.Emit(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: req})
			select {
			case <-ctx.Done():
				return ctx.Err()
			case n := <-f.decisions:
				if n.Granted {
					text += "\n\nPermission granted, continuing."
				} else {
					text += "\n\nPermission denied, stopping."
				}
			}
		case strings.Contains(prompt, "question"):
			q := question.Request{ID: f.nextID("q"), SessionID: sid, ToolCallID: f.nextID("tc"), Questions: []question.Question{{
				ID: "flavor", Type: question.TypeSingleChoice, Text: "Which theme should the hologram use?",
				Description: "Pick one; the agent continues with your choice.",
				Choices:     []question.Choice{{ID: "cyan", Label: "Cyan"}, {ID: "magenta", Label: "Magenta"}},
			}}}
			f.Emit(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: q})
			select {
			case <-ctx.Done():
				return ctx.Err()
			case a := <-f.answers:
				f.Emit(pubsub.Event[question.Notification]{Type: pubsub.UpdatedEvent, Payload: question.Notification{BatchID: q.ID, SessionID: sid}})
				choice := ""
				if len(a) > 0 && len(a[0].SelectedIDs) > 0 {
					choice = a[0].SelectedIDs[0]
				}
				text += "\n\nYou chose " + choice + "."
			}
		case strings.Contains(prompt, "delegate"):
			f.delegate()
			text += "\n\nHanded the tokenizer to the subagent."
		}
		final := message.Finish{Reason: message.FinishReasonEndTurn, Time: time.Now().Unix()}
		emit(thinking, message.TextContent{Text: text}, final)
		f.AddMessage(asst)
		sess, err := f.GetSession(ctx, sid)
		if err != nil {
			sess = session.Session{ID: sid, Title: "Holodeck test"}
		}
		sess.PromptTokens, sess.CompletionTokens, sess.Cost = 1200, 340, 0.0123
		f.AddSession(sess)
		f.Emit(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: sess})
		return nil
	}
}

// drainResolutions forgets answers left over from an earlier run, so a
// stale grant cannot resolve this run's request.
func (f *Fake) drainResolutions() {
	for {
		select {
		case <-f.decisions:
		case <-f.answers:
		default:
			return
		}
	}
}
