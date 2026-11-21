package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

func doJSON(t *testing.T, c *http.Client, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rsp := doReq(t, c, req)
	data, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	return rsp, data
}

// runResult is what the fake agent run reports back to the test goroutine.
// OnRun executes on the handler's goroutine, where FailNow is not valid.
type runResult struct {
	att    []message.Attachment
	ctxErr error
}

func TestPromptMultipartRunsAgentDetached(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	got := make(chan runResult, 1)
	fake.OnRun = func(ctx context.Context, sid, prompt string, att []message.Attachment) error {
		got <- runResult{att: att, ctxErr: ctx.Err()}
		return nil
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("prompt", "fix the bug"))
	fw, err := mw.CreateFormFile("attachments", "shot.png")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\nrest"))
	require.NoError(t, mw.Close())

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/sessions/s1/prompt", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rsp := doReq(t, authedClient(t, g), req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)

	select {
	case res := <-got:
		require.NoError(t, res.ctxErr, "the run must be detached from the request")
		require.Len(t, res.att, 1)
		require.Equal(t, "shot.png", res.att[0].FileName)
		require.Equal(t, "image/png", res.att[0].MimeType)
	case <-time.After(2 * time.Second):
		t.Fatal("AgentRun not called")
	}
	require.Contains(t, fake.CallLog(), "AgentRun:s1:fix the bug")
}

// TestPromptSniffsAttachmentTypes verifies attachments are classified the
// way the TUI classifies them: by content, not by extension. A .json file
// must land as text so it is inlined, and image bytes must stay images
// whatever their name says.
func TestPromptSniffsAttachmentTypes(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	got := make(chan runResult, 1)
	fake.OnRun = func(_ context.Context, _, _ string, att []message.Attachment) error {
		got <- runResult{att: att}
		return nil
	}
	for _, tc := range []struct {
		name  string
		body  []byte
		check func(t *testing.T, mime string)
	}{
		{"config.json", []byte(`{"answer": 42}`), func(t *testing.T, mime string) {
			require.True(t, strings.HasPrefix(mime, "text/"), "json is text, got %q", mime)
		}},
		{"actually-a-png.json", []byte("\x89PNG\r\n\x1a\nrest"), func(t *testing.T, mime string) {
			require.Equal(t, "image/png", mime)
		}},
	} {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		require.NoError(t, mw.WriteField("prompt", "look at this"))
		fw, err := mw.CreateFormFile("attachments", tc.name)
		require.NoError(t, err)
		_, _ = fw.Write(tc.body)
		require.NoError(t, mw.Close())
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/sessions/s1/prompt", &buf)
		require.NoError(t, err)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rsp := doReq(t, authedClient(t, g), req)
		rsp.Body.Close()
		require.Equal(t, http.StatusAccepted, rsp.StatusCode)

		select {
		case res := <-got:
			require.Len(t, res.att, 1)
			tc.check(t, res.att[0].MimeType)
		case <-time.After(2 * time.Second):
			t.Fatal("AgentRun not called")
		}
	}
}

func TestPromptEmptyRejected(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("prompt", "   "))
	require.NoError(t, mw.Close())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/sessions/s1/prompt", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rsp := doReq(t, authedClient(t, g), req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
}

func TestGrantPermissionActions(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	for _, tc := range []struct{ id, action, call string }{
		{"p1", "allow", "PermissionGrant:p1"},
		{"p2", "allow_session", "PermissionGrantPersistent:p2"},
		{"p3", "deny", "PermissionDeny:p3"},
	} {
		g.pending.observe(pubsub.Event[permission.PermissionRequest]{Payload: permission.PermissionRequest{ID: tc.id, ToolCallID: "tc-" + tc.id}})
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/permissions/grant", `{"id":"`+tc.id+`","action":"`+tc.action+`"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
		require.JSONEq(t, `{"resolved":true}`, string(body))
		require.Contains(t, fake.CallLog(), tc.call)
	}
}

// TestGrantAlreadyResolved covers a service that no longer holds the
// request: the gateway must answer not_pending, the same as for an unknown
// id, and must forget the stale entry so reloads do not restore the dialog.
func TestGrantAlreadyResolved(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	fake.Resolve = false
	g.pending.observe(pubsub.Event[permission.PermissionRequest]{Payload: permission.PermissionRequest{ID: "p1", ToolCallID: "tc1"}})
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/permissions/grant", `{"id":"p1","action":"allow"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "not_pending")
	_, ok := g.pending.permission("p1")
	require.False(t, ok)

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/permissions/grant", `{"id":"gone","action":"allow"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "not_pending")
}

func TestGrantBadAction(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	g.pending.observe(pubsub.Event[permission.PermissionRequest]{Payload: permission.PermissionRequest{ID: "p1"}})
	rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/permissions/grant", `{"id":"p1","action":"maybe"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
}

func TestPermissionMode(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	rsp, _ := doJSON(t, c, http.MethodPost, g.URL()+"/api/permissions/mode", `{"mode":"yolo"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.Equal(t, permission.PermissionModeYolo, fake.PermissionMode())
	rsp, _ = doJSON(t, c, http.MethodPost, g.URL()+"/api/permissions/mode", `{"mode":"chaos"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
}

func TestQuestionAnswerAndCancel(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/questions/answer",
		`{"answers":[{"question_id":"q1","selected_ids":["a"]}]}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"resolved":true}`, string(body))
	require.Equal(t, "q1", fake.Answers[0][0].QuestionID)

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/questions/cancel", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"resolved":true}`, string(body))
}

func TestCancelAndQueue(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	rsp, _ := doJSON(t, c, http.MethodPost, g.URL()+"/api/sessions/s1/cancel", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/queue", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"queued":[]}`, string(body))
	rsp, _ = doJSON(t, c, http.MethodDelete, g.URL()+"/api/sessions/s1/queue", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.Contains(t, fake.CallLog(), "AgentCancel:s1")
	require.Contains(t, fake.CallLog(), "AgentClearQueue:s1")
}

func TestSessionsAndMessages(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	fake.AddMessage(message.Message{
		ID: "m1", SessionID: "s1", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hi"}},
	})
	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var msgs []map[string]any
	require.NoError(t, json.Unmarshal(body, &msgs))
	require.Len(t, msgs, 1)
	require.Equal(t, "m1", msgs[0]["id"])

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/sessions", `{"title":"New"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusCreated, rsp.StatusCode)
	require.Contains(t, string(body), `"title":"New"`)
}

func TestHealthAndSnapshot(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/health", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"ok":true}`, string(body))

	rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/snapshot", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var snap Snapshot
	require.NoError(t, json.Unmarshal(body, &snap))
	require.Equal(t, "s1", snap.CurrentSessionID)
	require.NotNil(t, snap.Session)
	require.Equal(t, "main", snap.Branch)

	rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/snapshot?session=missing", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var missing Snapshot
	require.NoError(t, json.Unmarshal(body, &missing))
	require.Equal(t, "missing", missing.CurrentSessionID)
	require.Nil(t, missing.Session)

	// The terminal's current session changes the default.
	fake.SetCurrent("other")
	rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/snapshot", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.NoError(t, json.Unmarshal(body, &snap))
	require.Equal(t, "other", snap.CurrentSessionID)
}

func TestListSessions(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/sessions", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(body, &list))
	require.Len(t, list, 1)
	require.Equal(t, "s1", list[0]["id"])
}

func TestCompleteFilesIgnoresGlobSyntax(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	dir := t.TempDir()
	for _, name := range []string{"a[b.go", "abc.go", "other.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
	}
	fake.WorkDir = dir

	// "[b" becomes "b": both names containing "b" match. Unstripped, the
	// pattern "**/*[b*" is malformed and the list would be empty.
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/complete/files?q=%5Bb", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var out struct {
		Files     []string `json:"files"`
		Truncated bool     `json:"truncated"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.ElementsMatch(t, []string{"a[b.go", "abc.go"}, out.Files)
	require.False(t, out.Truncated)
}

// emitPermission publishes a permission request and waits until the
// gateway's pending set has it.
func emitPermission(t *testing.T, g *Gateway, fake *webtest.Fake, req permission.PermissionRequest) {
	t.Helper()
	fake.Emit(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: req})
	require.Eventually(t, func() bool {
		_, ok := g.pending.permission(req.ID)
		return ok
	}, 2*time.Second, 10*time.Millisecond)
}

func TestAnswerNotPendingForgetsQuestion(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.Resolve = false
	fake.Emit(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: question.Request{ID: "b1", SessionID: "s1"}})
	require.Eventually(t, func() bool { return g.pending.question() != nil }, 2*time.Second, 10*time.Millisecond)

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/questions/answer", `{"answers":[]}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.JSONEq(t, `{"error":"question batch is no longer pending","code":"not_pending"}`, string(body))
	require.Nil(t, g.pending.question(), "a batch the service no longer holds must not stay pending")
}

func TestQuestionCancelNotPendingForgetsQuestion(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.Resolve = false
	fake.Emit(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: question.Request{ID: "b1", SessionID: "s1"}})
	require.Eventually(t, func() bool { return g.pending.question() != nil }, 2*time.Second, 10*time.Millisecond)

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/questions/cancel", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.JSONEq(t, `{"error":"question batch is no longer pending","code":"not_pending"}`, string(body))
	require.Nil(t, g.pending.question())
}

func TestRunCompleteClearsPending(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	emitPermission(t, g, fake, permission.PermissionRequest{ID: "p1", SessionID: "s1", ToolCallID: "tc1"})
	emitPermission(t, g, fake, permission.PermissionRequest{ID: "p2", SessionID: "s2", ToolCallID: "tc2"})
	fake.Emit(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: question.Request{ID: "b1", SessionID: "s1"}})
	require.Eventually(t, func() bool { return g.pending.question() != nil }, 2*time.Second, 10*time.Millisecond)

	fake.Emit(pubsub.Event[notify.RunComplete]{Type: pubsub.UpdatedEvent, Payload: notify.RunComplete{SessionID: "s1"}})
	require.Eventually(t, func() bool {
		_, ok := g.pending.permission("p1")
		return !ok && g.pending.question() == nil
	}, 2*time.Second, 10*time.Millisecond)
	_, ok := g.pending.permission("p2")
	require.True(t, ok, "another session's pending request must survive")
}
