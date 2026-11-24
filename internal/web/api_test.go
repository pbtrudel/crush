package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/client"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// doJSON sends a JSON request. The target is named target, not url, so the
// Origin header can be built from the net/url package.
func doJSON(t *testing.T, c *http.Client, method, target, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, target, bytes.NewBufferString(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	// Browsers send Origin on every state-changing request, and the
	// gateway requires it.
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", (&url.URL{Scheme: req.URL.Scheme, Host: req.URL.Host}).String())
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
	req.Header.Set("Origin", g.URL())
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
		req.Header.Set("Origin", g.URL())
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
	req.Header.Set("Origin", g.URL())
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
	emitQuestion(t, g, fake, question.Request{ID: "b1", SessionID: "s1"})
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/questions/answer",
		`{"batch_id":"b1","answers":[{"question_id":"q1","selected_ids":["a"]}]}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"resolved":true}`, string(body))
	require.Equal(t, "q1", fake.Answers[0][0].QuestionID)
	require.Equal(t, []string{"b1"}, fake.AnswerBatches, "the answer must name the pending batch")

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/questions/cancel", `{"batch_id":"b1"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"resolved":true}`, string(body))
	require.Contains(t, fake.CallLog(), "QuestionCancel:b1")
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

// TestSnapshotExposesEveryPendingBatch checks that a reload restores each
// open question, not just the one that arrived last.
func TestSnapshotExposesEveryPendingBatch(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	emitQuestion(t, g, fake, question.Request{ID: "b1", SessionID: "s2"})
	emitQuestion(t, g, fake, question.Request{ID: "b2", SessionID: "s3"})

	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/snapshot", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var snap struct {
		Questions []struct {
			ID string `json:"id"`
		} `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(body, &snap))
	require.Len(t, snap.Questions, 2)
	require.Equal(t, "b1", snap.Questions[0].ID)
	require.Equal(t, "b2", snap.Questions[1].ID)
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

// emitQuestion publishes a question batch and waits until the gateway's
// pending set has it, so an answer can name its batch.
func emitQuestion(t *testing.T, g *Gateway, fake *webtest.Fake, req question.Request) {
	t.Helper()
	fake.Emit(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: req})
	require.Eventually(t, func() bool {
		_, ok := g.pending.question(req.ID)
		return ok
	}, 2*time.Second, 10*time.Millisecond)
}

func TestAnswerNotPendingForgetsQuestion(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.Resolve = false
	emitQuestion(t, g, fake, question.Request{ID: "b1", SessionID: "s1"})

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/questions/answer", `{"batch_id":"b1","answers":[]}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.JSONEq(t, `{"error":"question batch is no longer pending","code":"not_pending"}`, string(body))
	_, ok := g.pending.question("b1")
	require.False(t, ok, "a batch the service no longer holds must not stay pending")
}

func TestQuestionCancelNotPendingForgetsQuestion(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.Resolve = false
	emitQuestion(t, g, fake, question.Request{ID: "b1", SessionID: "s1"})

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/questions/cancel", `{"batch_id":"b1"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.JSONEq(t, `{"error":"question batch is no longer pending","code":"not_pending"}`, string(body))
	_, ok := g.pending.question("b1")
	require.False(t, ok)
}

// TestQuestionAnswerRoutesByBatchID checks that an answer resolves the
// batch the browser was looking at. A second batch arriving while the
// first is open must not steal the answer.
func TestQuestionAnswerRoutesByBatchID(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	emitQuestion(t, g, fake, question.Request{ID: "b1", SessionID: "s1"})
	emitQuestion(t, g, fake, question.Request{ID: "b2", SessionID: "s2"})

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/questions/answer",
		`{"batch_id":"b1","answers":[{"question_id":"q1","fill_in_text":"notes"}]}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Equal(t, []string{"b1"}, fake.AnswerBatches)
	require.Len(t, fake.Answers, 1)
	require.Equal(t, "notes", fake.Answers[0][0].FillInText)
	_, other := g.pending.question("b2")
	require.True(t, other, "the other batch stays pending")
}

// TestQuestionCancelRoutesByBatchID checks that a cancel resolves the
// batch it names, not the one that arrived last.
func TestQuestionCancelRoutesByBatchID(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	emitQuestion(t, g, fake, question.Request{ID: "b1", SessionID: "s1"})
	emitQuestion(t, g, fake, question.Request{ID: "b2", SessionID: "s2"})

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/questions/cancel", `{"batch_id":"b1"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Contains(t, fake.CallLog(), "QuestionCancel:b1")
	require.NotContains(t, fake.CallLog(), "QuestionCancel:b2")
	_, other := g.pending.question("b2")
	require.True(t, other, "the other batch stays pending")
}

// TestQuestionAnswerUnknownBatchKeepsOthers checks that a stale batch ID
// fails on its own entry without clearing a batch still on screen.
func TestQuestionAnswerUnknownBatchKeepsOthers(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	emitQuestion(t, g, fake, question.Request{ID: "b1", SessionID: "s1"})

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/questions/answer", `{"batch_id":"gone","answers":[]}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.JSONEq(t, `{"error":"question batch is no longer pending","code":"not_pending"}`, string(body))
	_, still := g.pending.question("b1")
	require.True(t, still, "an unknown batch must not clear the pending one")
	require.Empty(t, fake.AnswerBatches)
}

func TestRunCompleteClearsPending(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	emitPermission(t, g, fake, permission.PermissionRequest{ID: "p1", SessionID: "s1", ToolCallID: "tc1"})
	emitPermission(t, g, fake, permission.PermissionRequest{ID: "p2", SessionID: "s2", ToolCallID: "tc2"})
	emitQuestion(t, g, fake, question.Request{ID: "b1", SessionID: "s1"})

	fake.Emit(pubsub.Event[notify.RunComplete]{Type: pubsub.UpdatedEvent, Payload: notify.RunComplete{SessionID: "s1"}})
	require.Eventually(t, func() bool {
		_, permOK := g.pending.permission("p1")
		_, questionOK := g.pending.question("b1")
		return !permOK && !questionOK
	}, 2*time.Second, 10*time.Millisecond)
	_, ok := g.pending.permission("p2")
	require.True(t, ok, "another session's pending request must survive")
}

// TestCreateSessionStartsOneSessionInTerminal checks that the browser can
// start a session and that the gateway tells the terminal to open it.
func TestCreateSessionStartsOneSessionInTerminal(t *testing.T) {
	t.Parallel()
	fake := webtest.NewFake()
	type createdSession struct{ id, current string }
	created := make(chan createdSession, 1)
	g, err := Start(t.Context(), Options{
		Workspace: fake,
		OnSessionCreated: func(id string) {
			created <- createdSession{id: id, current: fake.CurrentSessionID()}
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })

	rsp, data := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions", "{}")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusCreated, rsp.StatusCode)

	var sess struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal(data, &sess))
	require.NotEmpty(t, sess.ID)
	require.Equal(t, "New Session", sess.Title, "the title matches the TUI's first prompt")

	select {
	case got := <-created:
		require.Equal(t, sess.ID, got.id, "the terminal opens the session the browser created")
		require.Equal(t, sess.ID, got.current, "the workspace reports the new session before the terminal is notified")
	case <-time.After(2 * time.Second):
		t.Fatal("OnSessionCreated not called")
	}
	require.Equal(t, sess.ID, fake.CurrentSessionID())

	sessions, err := fake.ListSessions(t.Context())
	require.NoError(t, err)
	require.Len(t, sessions, 2, "exactly one session was created")
}

// TestInputModeEndpointForwardsValidModes checks that a browser toggle
// always reaches the terminal callback; the terminal decides whether the
// request is a no-op.
func TestInputModeEndpointForwardsValidModes(t *testing.T) {
	t.Parallel()
	fake := webtest.NewFake()
	modes := make(chan bool, 3)
	g, err := Start(t.Context(), Options{
		Workspace:            fake,
		OnInputModeRequested: func(plan bool) { modes <- plan },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })

	rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/input-mode", `{"mode":"turbo"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)

	rsp, _ = doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/input-mode", `{"mode":"plan"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	select {
	case plan := <-modes:
		require.True(t, plan)
	case <-time.After(2 * time.Second):
		t.Fatal("OnInputModeRequested not called")
	}

	// The terminal already switched, but the gateway still forwards the
	// request: a same-mode request is the terminal's no-op to make.
	g.SetInputMode(true)
	rsp, _ = doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/input-mode", `{"mode":"plan"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	select {
	case plan := <-modes:
		require.True(t, plan)
	case <-time.After(2 * time.Second):
		t.Fatal("same-mode request was not forwarded")
	}

	rsp, _ = doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/input-mode", `{"mode":"code"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	select {
	case plan := <-modes:
		require.False(t, plan)
	case <-time.After(2 * time.Second):
		t.Fatal("OnInputModeRequested not called for code mode")
	}
}

// TestInputModeEndpointRejectsWrongMethod checks the route only accepts the
// method the browser uses.
func TestInputModeEndpointRejectsWrongMethod(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)

	rsp := do(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/input-mode", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusMethodNotAllowed, rsp.StatusCode)
	require.Equal(t, http.MethodPost, rsp.Header.Get("Allow"))
}

// TestInputModeEndpointRejectsOversizeBody checks the endpoint honors the
// gateway's JSON body limit.
func TestInputModeEndpointRejectsOversizeBody(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)

	body := `{"mode":"` + strings.Repeat("x", maxJSONBytes) + `"}`
	rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/input-mode", body)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
}

// TestInputModeRejectsUnauthenticatedAndForeignOrigin checks the route sits
// behind the same guards as every other state-changing route.
func TestInputModeRejectsUnauthenticatedAndForeignOrigin(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)

	rsp := do(t, nil, http.MethodPost, g.URL()+"/api/input-mode", strings.NewReader(`{"mode":"plan"}`))
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/input-mode", strings.NewReader(`{"mode":"plan"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	rsp = doReq(t, authedClient(t, g), req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusForbidden, rsp.StatusCode)
}

// TestCreateSessionRejectsUnauthenticatedAndForeignOrigin checks that the
// route sits behind the same guards as every other state-changing route.
func TestCreateSessionRejectsUnauthenticatedAndForeignOrigin(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)

	rsp := do(t, nil, http.MethodPost, g.URL()+"/api/sessions", strings.NewReader("{}"))
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/sessions", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	rsp = doReq(t, authedClient(t, g), req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusForbidden, rsp.StatusCode)
}

func TestInternalErrorsDoNotLeakDetails(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeInternal(rec, "list_sessions", errors.New("open /home/user/.crush/crush.db: disk I/O error"))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotContains(t, rec.Body.String(), "/home/user")
	require.JSONEq(t, `{"error":"the workspace could not complete the request","code":"list_sessions"}`, rec.Body.String())
}

// TestIsNotFoundMatchesBothWorkspaceKinds covers the shapes a miss
// arrives in: sql.ErrNoRows from the in-process workspace, and in
// client/server mode client.ErrNotFound for a workspace the server no
// longer knows or client.ErrSessionNotFound for a session it does not
// have. Only the session shapes count as an unknown session.
func TestIsNotFoundMatchesBothWorkspaceKinds(t *testing.T) {
	t.Parallel()
	require.True(t, isNotFound(sql.ErrNoRows))
	require.True(t, isNotFound(fmt.Errorf("get session s1: %w", sql.ErrNoRows)))
	require.True(t, isNotFound(client.ErrNotFound))
	require.True(t, isNotFound(fmt.Errorf("status code 404: %w", client.ErrNotFound)))
	require.True(t, isNotFound(client.ErrSessionNotFound))
	require.False(t, isNotFound(errors.New("open crush.db: disk I/O error")))
	require.False(t, isNotFound(nil))

	require.True(t, isSessionNotFound(sql.ErrNoRows))
	require.True(t, isSessionNotFound(fmt.Errorf("failed to get session: %w", client.ErrSessionNotFound)))
	require.False(t, isSessionNotFound(client.ErrNotFound), "a workspace-gone 404 says nothing about the session")
}

// clientSessionNotFoundWorkspace reports a session miss the way
// ClientWorkspace does in client/server mode, where the server's
// session_not_found 404 arrives as a wrapped client.ErrSessionNotFound.
type clientSessionNotFoundWorkspace struct{ *webtest.Fake }

func (clientSessionNotFoundWorkspace) GetSession(context.Context, string) (session.Session, error) {
	return session.Session{}, fmt.Errorf("failed to get session: %w", client.ErrSessionNotFound)
}

// workspaceGoneWorkspace reports the 404 a client/server workspace gets
// when the server no longer knows the workspace. That says nothing about
// the session, so a session route must not answer it as a session miss.
type workspaceGoneWorkspace struct{ *webtest.Fake }

func (workspaceGoneWorkspace) GetSession(context.Context, string) (session.Session, error) {
	return session.Session{}, fmt.Errorf("failed to get session: %w", client.ErrNotFound)
}

func TestSessionRouteReportsWorkspaceGoneAsFailure(t *testing.T) {
	t.Parallel()
	g, err := Start(t.Context(), Options{Workspace: workspaceGoneWorkspace{webtest.NewFake()}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/sessions/s1", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusInternalServerError, rsp.StatusCode, string(body))
	var got apiError
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "get_session", got.Code, "a workspace-gone 404 must surface as a workspace failure")
	require.NotEqual(t, "unknown_session", got.Code)
}

func TestMessagesForUnknownSessionIs404(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/sessions/nope/messages", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
}

func TestMessagesOverClientWorkspaceNotFoundIs404(t *testing.T) {
	t.Parallel()
	g, err := Start(t.Context(), Options{Workspace: clientSessionNotFoundWorkspace{webtest.NewFake()}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/sessions/nope/messages", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
}

func TestMalformedAndOversizedBodies(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := authedClient(t, g)
	rsp, _ := doJSON(t, c, http.MethodPost, g.URL()+"/api/permissions/mode", `{"mode":`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	rsp, _ = doJSON(t, c, http.MethodPost, g.URL()+"/api/permissions/mode", `{"mode":"`+strings.Repeat("x", maxJSONBytes)+`"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
}

func TestPromptMustBeMultipart(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/prompt", `{"prompt":"hi"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "bad_form")
	require.Empty(t, fake.CallLog())
}

// TestAgentStartingAnswers503 pins the gateway half of the contract: an
// agent that is not configured yet is a retryable state on every route that
// reaches it, not a workspace failure.
func TestAgentStartingAnswers503(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.SetTeamErr(workspace.ErrAgentNotInitialized)
	rsp, data := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/team/members/alice/message", `{"session_id":"s1","text":"hi"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode)
	require.JSONEq(t, `{"error":"Crush is starting the agent","code":"agent_starting"}`, string(data))
}

// TestPromptFailurePublishesSafeError pins that a detached prompt failure
// never carries the workspace error text, which can name files or sockets,
// and that a starting agent keeps its own code.
func TestPromptFailurePublishesSafeError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		err     error
		code    string
		message string
	}{
		{
			"prompt failure",
			fmt.Errorf("open /home/u/.secret: %w", os.ErrPermission),
			"prompt_failed",
			"The prompt could not run. The terminal log has the details.",
		},
		{
			"starting agent",
			fmt.Errorf("run: %w", workspace.ErrAgentNotInitialized),
			"agent_starting",
			"Crush is starting the agent; send the prompt again in a moment",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, fake := startTest(t)
			fake.OnRun = func(context.Context, string, string, []message.Attachment) error {
				return tc.err
			}
			c := dialStream(t, g, "")
			readFrame(t, c) // Snapshot.

			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			require.NoError(t, mw.WriteField("prompt", "fix the bug"))
			require.NoError(t, mw.Close())
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/sessions/s1/prompt", &buf)
			require.NoError(t, err)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			req.Header.Set("Origin", g.URL())
			rsp := doReq(t, authedClient(t, g), req)
			rsp.Body.Close()
			require.Equal(t, http.StatusAccepted, rsp.StatusCode)

			f := readFrame(t, c)
			require.Equal(t, frameError, f.Type)
			var payload struct {
				SessionID string `json:"session_id"`
				Code      string `json:"code"`
				Error     string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(f.Payload, &payload))
			require.Equal(t, "s1", payload.SessionID)
			require.Equal(t, tc.code, payload.Code)
			require.Equal(t, tc.message, payload.Error)
			require.NotContains(t, payload.Error, "/home/u")
			require.NotContains(t, payload.Error, "agent coordinator")
		})
	}
}

// TestSnapshotReportsAgentReady pins that a tab can tell on load whether
// the agent is starting.
func TestSnapshotReportsAgentReady(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		ready error
		want  bool
	}{
		{"ready", nil, true},
		{"starting", workspace.ErrAgentNotInitialized, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, fake := startTest(t)
			fake.SetReadyErr(tc.ready)
			rsp, data := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/snapshot", "")
			rsp.Body.Close()
			require.Equal(t, http.StatusOK, rsp.StatusCode)
			var snap Snapshot
			require.NoError(t, json.Unmarshal(data, &snap))
			require.Equal(t, tc.want, snap.AgentReady)
		})
	}
}
