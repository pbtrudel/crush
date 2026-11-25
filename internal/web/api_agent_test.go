package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
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
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.JSONEq(t, `{"answer":"It uses a trie"}`, string(body))
	require.Contains(t, fake.CallLog(), "AgentAsk:s1:how does it look up names?")

	tooLarge := strings.Repeat("x", 16<<10+1)
	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/sessions/s1/ask",
		`{"question":"`+tooLarge+`"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "question_too_large")
}

// TestShellRejectsOversizedCommand pins the shell body cap, like the side
// question's: an oversized command never reaches the workspace.
func TestShellRejectsOversizedCommand(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost,
		g.URL()+"/api/sessions/s1/shell",
		`{"command":"`+strings.Repeat("x", 16<<10+1)+`"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "command_too_large")
	require.Empty(t, fake.CallLog())
}

func TestAgentStartingOnAgentRoutes(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.SetReadyErr(workspace.ErrAgentNotInitialized)
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

// TestReconnectingOnAgentRoutes drives the two lost-link states through a
// route: both answer 503 with their own code and the fixed sentence, and
// the action never reaches the workspace.
func TestReconnectingOnAgentRoutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"workspace gone", workspace.ErrWorkspaceGone, "workspace_gone"},
		{"server unreachable", fmt.Errorf("get agent info: %w", workspace.ErrServerUnreachable), "server_unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, fake := startTest(t)
			fake.SetReadyErr(tc.err)
			rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/summarize", "")
			rsp.Body.Close()
			require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
			require.Contains(t, string(body), tc.code)
			require.Contains(t, string(body), "Crush is reconnecting to its server. Try again in a moment.")
			require.Empty(t, fake.CallLog(), "a lost link answers before the action runs")
		})
	}
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

// TestShellCancelReleasedOnFinish pins that a shell run which finishes on
// its own releases its cancel func: the context is done and the run is no
// longer tracked, so a long session of commands does not leak one context
// each.
func TestShellCancelReleasedOnFinish(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	runCtx := make(chan context.Context, 1)
	fake.OnShell = func(ctx context.Context, _, _ string, _ func(string), _ bool) (proto.ShellCommandResponse, error) {
		runCtx <- ctx
		return proto.ShellCommandResponse{ExitCode: 0}, nil
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	runID := startShell(t, authedClient(t, g), g, "true")
	_, done := readShellOutput(t, c, runID)
	require.True(t, done.Done)

	ctx := <-runCtx
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a finished shell run kept its context alive")
	}

	g.shellMu.Lock()
	_, tracked := g.shells[runID]
	g.shellMu.Unlock()
	require.False(t, tracked, "a finished run must not stay tracked")
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

// TestShellCancelAfterContextDone mirrors the real workspace, whose
// streaming path turns a cancelled run into a nil error and a non-zero
// exit code: the done frame still reads as the SIGINT convention.
func TestShellCancelAfterContextDone(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.OnShell = func(ctx context.Context, _, _ string, _ func(string), _ bool) (proto.ShellCommandResponse, error) {
		<-ctx.Done()
		return proto.ShellCommandResponse{ExitCode: 1}, nil
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	client := authedClient(t, g)
	runID := startShell(t, client, g, "sleep 10")
	rsp, body := doJSON(t, client, http.MethodDelete, g.URL()+"/api/shell/"+runID, "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode, string(body))

	_, done := readShellOutput(t, c, runID)
	require.Equal(t, 130, done.ExitCode)
	require.Empty(t, done.Error)
}

// TestShellCoalescesProgress holds the command open and publishes one
// flush tick by hand, so the three progress writes must arrive as one
// frame, not one frame per write, whatever the machine's timing.
func TestShellCoalescesProgress(t *testing.T) {
	t.Parallel()
	ticks := make(chan time.Time)
	g, fake := startWith(t, Options{shellTick: ticks})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	written := make(chan struct{})
	fake.OnShell = func(_ context.Context, _, _ string, onProgress func(string), _ bool) (proto.ShellCommandResponse, error) {
		onProgress("one\n")
		onProgress("two\n")
		onProgress("three\n")
		close(written)
		<-release
		return proto.ShellCommandResponse{Output: "one\ntwo\nthree\n"}, nil
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	startShell(t, authedClient(t, g), g, "printf three")
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("the command never wrote its chunks")
	}
	ticks <- time.Now()
	f := readFrame(t, c)
	require.Equal(t, frameShellOutput, f.Type)
	var chunk shellFrame
	require.NoError(t, json.Unmarshal(f.Payload, &chunk))
	require.False(t, chunk.Done)
	require.Equal(t, "one\ntwo\nthree\n", chunk.Chunk)

	unblock()
	for {
		f = readFrame(t, c)
		if f.Type == frameShellOutput {
			break
		}
	}
	var final shellFrame
	require.NoError(t, json.Unmarshal(f.Payload, &final))
	require.True(t, final.Done)
	require.Empty(t, final.Chunk)
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
	t.Parallel()
	g, fake := startTest(t)
	returned := make(chan error, 1)
	fake.OnShell = func(ctx context.Context, _, _ string, _ func(string), _ bool) (proto.ShellCommandResponse, error) {
		<-ctx.Done()
		err := ctx.Err()
		returned <- err
		// The real streaming path reports a nil error and a non-zero
		// exit code for a cancelled run.
		return proto.ShellCommandResponse{ExitCode: 1}, nil
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	runID := startShell(t, authedClient(t, g), g, "sleep 10")
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "AgentRunShellCommand:s1:sleep 10:true")
	}, time.Second, 10*time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- g.Shutdown(context.Background()) }()
	var final shellFrame
	for {
		f := readFrame(t, c)
		require.NotEqual(t, frameClosing, f.Type, "the cancelled run settles before the gateway closes")
		if f.Type != frameShellOutput {
			continue
		}
		require.NoError(t, json.Unmarshal(f.Payload, &final))
		require.Equal(t, runID, final.RunID)
		if final.Done {
			break
		}
	}
	require.Equal(t, 130, final.ExitCode)
	require.Empty(t, final.Error)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not return")
	}
	select {
	case err := <-returned:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel the shell command")
	}
}

// TestShutdownBoundsTheShellWait covers a command that ignores its
// context and a request the server cannot drain: Shutdown must give up at
// the caller's deadline instead of hanging on either. The held request is
// what makes the wait real: with an idle connection http.Server.Shutdown
// returns at once and the deadline would prove nothing.
func TestShutdownBoundsTheShellWait(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	fake.OnShell = func(context.Context, string, string, func(string), bool) (proto.ShellCommandResponse, error) {
		<-release
		return proto.ShellCommandResponse{ExitCode: 1}, nil
	}
	c := authedClient(t, g)
	startShell(t, c, g, "stuck")
	require.Eventually(t, func() bool {
		return slices.Contains(fake.CallLog(), "AgentRunShellCommand:s1:stuck:true")
	}, time.Second, 10*time.Millisecond)

	// A request whose body never completes holds a handler open, so the
	// server has an active connection it cannot drain before the deadline.
	bodyR, bodyW := io.Pipe()
	t.Cleanup(func() { _ = bodyW.CloseWithError(io.ErrUnexpectedEOF) })
	seen := make(chan struct{})
	held, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		g.URL()+"/api/sessions/s1/notes", &firstRead{r: bodyR, seen: seen})
	require.NoError(t, err)
	held.Header.Set("Content-Type", "application/json")
	held.Header.Set("Origin", g.URL())
	heldDone := make(chan struct{})
	go func() {
		defer close(heldDone)
		rsp, err := c.Do(held)
		if err == nil {
			rsp.Body.Close()
		}
	}()
	// The transport reads the body only after the headers are on the wire.
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("the held request never reached the gateway")
	}
	_, err = bodyW.Write([]byte(`{"text":"`))
	require.NoError(t, err)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(shutdownCtx) }()
	select {
	case err := <-done:
		// The held request outlasts the deadline, so the server's own
		// drain reports it; the point is that Shutdown returned anyway.
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Less(t, time.Since(start), time.Second,
			"Shutdown waited on work that ignores cancellation")
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown waited on work that ignores cancellation")
	}
	unblock()
	_ = bodyW.CloseWithError(io.ErrUnexpectedEOF)
	select {
	case <-heldDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the held request did not return")
	}
}

// firstRead reports the first read of a request body, which the client
// transport performs only after the request headers are written.
type firstRead struct {
	r    io.Reader
	once sync.Once
	seen chan struct{}
}

func (h *firstRead) Read(p []byte) (int, error) {
	h.once.Do(func() { close(h.seen) })
	return h.r.Read(p)
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
