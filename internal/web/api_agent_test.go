package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// shellFrame is one shell_output payload, final frame included.
type shellFrame struct {
	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	Chunk     string `json:"chunk"`
	Done      bool   `json:"done"`
	ExitCode  int    `json:"exit_code"`
	Error     string `json:"error"`
}

// readShellOutput reads frames until the run's final frame, concatenating
// its chunks. Frames from other runs and other frame types are skipped.
func readShellOutput(t *testing.T, c *websocket.Conn, runID string) (string, shellFrame) {
	t.Helper()
	var chunks strings.Builder
	for {
		f := readFrame(t, c)
		if f.Type != frameShellOutput {
			continue
		}
		var p shellFrame
		require.NoError(t, json.Unmarshal(f.Payload, &p))
		require.Equal(t, runID, p.RunID)
		require.Equal(t, "s1", p.SessionID)
		if p.Done {
			return chunks.String(), p
		}
		chunks.WriteString(p.Chunk)
	}
}

// startShell posts a shell command and returns its run id.
func startShell(t *testing.T, c *http.Client, g *Gateway, command string) string {
	t.Helper()
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/sessions/s1/shell",
		`{"command":"`+command+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
	var out struct {
		RunID string `json:"run_id"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.NotEmpty(t, out.RunID)
	return out.RunID
}

func TestSummarizeRunsDetached(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost,
		g.URL()+"/api/sessions/s1/summarize", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "AgentSummarize:s1")
	}, time.Second, 10*time.Millisecond)
}

func TestSummarizeBusyConflicts(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.SetSessionBusy("s1", true)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost,
		g.URL()+"/api/sessions/s1/summarize", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "agent_busy")
	require.NotContains(t, fake.CallLog(), "AgentSummarize:s1")
}

// TestSummarizeFailurePublishesWebError pins the fixed message for a
// detached compaction that failed: the tab must never see the workspace
// error text.
func TestSummarizeFailurePublishesWebError(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.OnSummarize = func(context.Context, string) error {
		return errors.New("dial unix /tmp/crush.sock: refused")
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost,
		g.URL()+"/api/sessions/s1/summarize", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode, string(body))
	f := readFrame(t, c)
	require.Equal(t, frameError, f.Type)
	var p map[string]string
	require.NoError(t, json.Unmarshal(f.Payload, &p))
	require.Equal(t, map[string]string{
		"session_id": "s1",
		"code":       "summarize_failed",
		"error":      "The session could not be compacted.",
	}, p)
}

func TestAskAnswers(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AskAnswer = "It uses a trie"
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/sessions/s1/ask",
		`{"question":"how does it look up names?"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.JSONEq(t, `{"answer":"It uses a trie"}`, string(body))
	require.Contains(t, fake.CallLog(), "AgentAsk:s1:how does it look up names?")

	tooLarge := strings.Repeat("x", 16<<10+1)
	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/sessions/s1/ask",
		`{"question":"`+tooLarge+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "question_too_large")
}

func TestAgentStartingOnAgentRoutes(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.ReadyErr = workspace.ErrAgentNotInitialized
	c := authedClient(t, g)
	for _, tc := range []struct{ target, body string }{
		{"/api/sessions/s1/summarize", ""},
		{"/api/sessions/s1/ask", `{"question":"why?"}`},
		{"/api/sessions/s1/shell", `{"command":"ls"}`},
		{"/api/sessions/s1/notes", `{"text":"prefer tests first"}`},
	} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+tc.target, tc.body)
		rsp.Body.Close()
		require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, tc.target)
		require.Contains(t, string(body), "agent_starting", tc.target)
	}
	require.Empty(t, fake.CallLog())
}

// TestAgentRoutesUnknownSession pins the shared existence check: an
// action on a session the workspace does not have is a 404, not a
// workspace failure or an action.
func TestAgentRoutesUnknownSession(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	for _, tc := range []struct{ target, body string }{
		{"/api/sessions/gone/summarize", ""},
		{"/api/sessions/gone/ask", `{"question":"why?"}`},
		{"/api/sessions/gone/shell", `{"command":"ls"}`},
		{"/api/sessions/gone/notes", `{"text":"prefer tests first"}`},
	} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+tc.target, tc.body)
		rsp.Body.Close()
		require.Equal(t, http.StatusNotFound, rsp.StatusCode, tc.target)
		require.Contains(t, string(body), "unknown_session", tc.target)
	}
	require.Empty(t, fake.CallLog())
}

func TestShellStreamsAndFinishes(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.OnShell = func(_ context.Context, _, _ string, onProgress func(string), _ bool) (proto.ShellCommandResponse, error) {
		onProgress("line 1\n")
		onProgress("line 2\n")
		return proto.ShellCommandResponse{Output: "line 1\nline 2\n", ExitCode: 0}, nil
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	runID := startShell(t, authedClient(t, g), g, "printf lines")
	output, done := readShellOutput(t, c, runID)
	require.Equal(t, "line 1\nline 2\n", output)
	require.True(t, done.Done)
	require.Equal(t, 0, done.ExitCode)
	require.Empty(t, done.Error)
}

// TestShellFirstMessageFlag pins the TUI's meaning: a command is the
// first message only while the session has none, which lets the workspace
// generate the session title from it.
func TestShellFirstMessageFlag(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	client := authedClient(t, g)
	startShell(t, client, g, "ls")
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "AgentRunShellCommand:s1:ls:true")
	}, time.Second, 10*time.Millisecond)

	fake.AddMessage(message.Message{ID: "m1", SessionID: "s1", Role: message.User})
	startShell(t, client, g, "pwd")
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "AgentRunShellCommand:s1:pwd:false")
	}, time.Second, 10*time.Millisecond)
}

// TestShellChunksCutOnRuneBoundary feeds a chunk that straddles the 16 KiB
// boundary with a multi-byte rune and checks every published chunk stays
// under the cap and splits on a rune start.
func TestShellChunksCutOnRuneBoundary(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	output := strings.Repeat("a", 16<<10-1) + "é" + strings.Repeat("b", 16<<10)
	fake.OnShell = func(_ context.Context, _, _ string, onProgress func(string), _ bool) (proto.ShellCommandResponse, error) {
		onProgress(output)
		return proto.ShellCommandResponse{Output: output}, nil
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	runID := startShell(t, authedClient(t, g), g, "generate")
	var chunks []string
	for {
		f := readFrame(t, c)
		if f.Type != frameShellOutput {
			continue
		}
		var p shellFrame
		require.NoError(t, json.Unmarshal(f.Payload, &p))
		require.Equal(t, runID, p.RunID)
		if p.Done {
			break
		}
		require.LessOrEqual(t, len(p.Chunk), 16<<10)
		require.True(t, utf8.ValidString(p.Chunk), "a chunk never splits a rune")
		chunks = append(chunks, p.Chunk)
	}
	require.Greater(t, len(chunks), 1)
	require.Equal(t, output, strings.Join(chunks, ""))
}

func TestShellCancel(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	returned := make(chan error, 1)
	fake.OnShell = func(ctx context.Context, _, _ string, _ func(string), _ bool) (proto.ShellCommandResponse, error) {
		<-ctx.Done()
		err := ctx.Err()
		returned <- err
		return proto.ShellCommandResponse{}, err
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	client := authedClient(t, g)
	runID := startShell(t, client, g, "sleep 10")
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "AgentRunShellCommand:s1:sleep 10:true")
	}, time.Second, 10*time.Millisecond)

	rsp, body := doJSON(t, client, http.MethodDelete, g.URL()+"/api/shell/"+runID, "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))

	rsp, body = doJSON(t, client, http.MethodDelete, g.URL()+"/api/shell/"+runID, "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "unknown_run")

	select {
	case err := <-returned:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled command did not return")
	}
	_, done := readShellOutput(t, c, runID)
	require.Equal(t, 130, done.ExitCode)
	require.Empty(t, done.Error)
}

// TestShellFailureReportsFixedError covers a command that fails for a
// reason other than cancellation: the tab shows the fixed message and the
// log keeps the detail, as the TUI does.
func TestShellFailureReportsFixedError(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.OnShell = func(_ context.Context, _, _ string, onProgress func(string), _ bool) (proto.ShellCommandResponse, error) {
		onProgress("partial\n")
		return proto.ShellCommandResponse{Output: "partial\n", ExitCode: 2}, errors.New("exec: no such file")
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	runID := startShell(t, authedClient(t, g), g, "nope")
	output, done := readShellOutput(t, c, runID)
	require.Equal(t, "partial\n", output)
	require.Equal(t, 2, done.ExitCode)
	require.Equal(t, "The command could not run. The terminal log has the details.", done.Error)
}

func TestShellCancelledOnShutdown(t *testing.T) {
	g, fake := startTest(t)
	returned := make(chan error, 1)
	fake.OnShell = func(ctx context.Context, _, _ string, _ func(string), _ bool) (proto.ShellCommandResponse, error) {
		<-ctx.Done()
		err := ctx.Err()
		returned <- err
		return proto.ShellCommandResponse{}, err
	}
	client := authedClient(t, g)
	startShell(t, client, g, "sleep 10")
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "AgentRunShellCommand:s1:sleep 10:true")
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, g.Shutdown(context.Background()))
	select {
	case err := <-returned:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel the shell command")
	}
}

func TestNotesAddNote(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/sessions/s1/notes",
		`{"text":"prefer tests first"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "AgentAddNote:s1:prefer tests first")
	}, time.Second, 10*time.Millisecond)

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/sessions/s1/notes", `{"text":"   "}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "empty_note")
	require.Len(t, fake.CallLog(), 1)

	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/sessions/s1/notes",
		`{"text":"`+strings.Repeat("x", 64<<10+1)+`"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "note_too_large")
	require.Len(t, fake.CallLog(), 1)
}
