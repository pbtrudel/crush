package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/pinentry"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/sshaskpass"
	"github.com/stretchr/testify/require"
)

const testSecret = "correct horse battery staple"

func pinentryReq(id string) pinentry.PromptRequest {
	return pinentry.PromptRequest{
		ID: id, Prompt: "Unlock the signing key", Kind: pinentry.KindPassphrase,
		Key: pinentry.Key{KeyID: "ABCDEF0123456789", Holder: "Dev <dev@example.com>"},
	}
}

func TestSecurePromptFrameAndSnapshot(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	c := dialStream(t, g, "")
	_ = readFrame(t, c) // Snapshot.
	f.AddPinentryPrompt(pinentryReq("p1"))
	fr := nextFrameOfType(t.Context(), t, c, frameSecurePrompt)
	require.JSONEq(t, `{"prompts":[{"source":"pinentry","id":"p1","kind":"passphrase","title":"GPG passphrase",
		"message":"Unlock the signing key","details":["Key: ABCDEF0123456789","Holder: Dev <dev@example.com>"]}]}`, string(fr.Payload))
	snap := g.buildSnapshot(t.Context(), "")
	require.Len(t, snap.SecurePrompts, 1)
}

func TestSnapshotRestoresSecurePrompt(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSSHPrompt(sshaskpass.PromptRequest{ID: "s1", Prompt: "git@github.com's password: "})
	require.Eventually(t, func() bool { return len(g.pending.securePrompts()) == 1 }, time.Second, 10*time.Millisecond)
	c := dialStream(t, g, "")
	snap := readFrame(t, c)
	require.Equal(t, frameSnapshot, snap.Type)
	require.Contains(t, string(snap.Payload), `"secure_prompts":[{"source":"ssh","id":"s1","kind":"password"`)
}

func TestSecurePromptAnswerDelivers(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddPinentryPrompt(pinentryReq("p1"))
	require.Eventually(t, func() bool { return len(g.pending.securePrompts()) == 1 }, time.Second, 10*time.Millisecond)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/pinentry/answer", `{"id":"p1","secret":"`+testSecret+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.Empty(t, body)
	require.Equal(t, testSecret, f.LastSecret())
	require.Contains(t, f.CallLog(), "PinentryRespond:p1")
	for _, call := range f.CallLog() {
		require.NotContains(t, call, testSecret, "the call log never holds the secret")
	}
}

func TestSecurePromptAnsweredElsewhere409(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	c := dialStream(t, g, "")
	_ = readFrame(t, c)
	f.AddPinentryPrompt(pinentryReq("p1"))
	_ = nextFrameOfType(t.Context(), t, c, frameSecurePrompt)
	f.ResolveFromTerminal("pinentry", "p1")
	fr := nextFrameOfType(t.Context(), t, c, frameSecurePrompt)
	require.JSONEq(t, `{"prompts":[]}`, string(fr.Payload), "the tab's dialog closes")
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/pinentry/answer", `{"id":"p1","secret":"late"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode)
	require.Contains(t, string(body), "not_pending")
	require.Empty(t, f.LastSecret(), "a late answer reaches no resolver")
}

func TestSSHConfirmAnswersYes(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSSHPrompt(sshaskpass.PromptRequest{ID: "s2", Kind: sshaskpass.KindConfirm, Prompt: "Are you sure you want to continue connecting (yes/no)?"})
	require.Eventually(t, func() bool { return len(g.pending.securePrompts()) == 1 }, time.Second, 10*time.Millisecond)
	rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/ssh/answer", `{"id":"s2","secret":"whatever"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.Equal(t, "yes", f.LastSecret())
}

func TestSSHTouchIsNotAnswerable(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSSHPrompt(sshaskpass.PromptRequest{ID: "s3", Kind: sshaskpass.KindTouch, Prompt: "Confirm user presence for key ED25519-SK"})
	require.Eventually(t, func() bool { return len(g.pending.securePrompts()) == 1 }, time.Second, 10*time.Millisecond)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/ssh/answer", `{"id":"s3"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode)
	require.Contains(t, string(body), "touch_not_answerable")
}

func TestSecretInQueryIsRefused(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddPinentryPrompt(pinentryReq("p1"))
	require.Eventually(t, func() bool { return len(g.pending.securePrompts()) == 1 }, time.Second, 10*time.Millisecond)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/pinentry/answer?secret="+url.QueryEscape(testSecret), `{"id":"p1"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "secret_in_url")
	require.NotContains(t, string(body), testSecret)
	require.Empty(t, f.LastSecret())
}

func TestSecretInHeaderIsNeverRead(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddPinentryPrompt(pinentryReq("p1"))
	require.Eventually(t, func() bool { return len(g.pending.securePrompts()) == 1 }, time.Second, 10*time.Millisecond)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/pinentry/answer", strings.NewReader(`{"id":"p1"}`))
	require.NoError(t, err)
	req.Header.Set("Origin", g.URL())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Secret", testSecret)
	rsp := doReq(t, authedClient(t, g), req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	body, _ := io.ReadAll(rsp.Body)
	require.Contains(t, string(body), "missing_secret")
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "PinentryRespond", "no resolver ran")
	require.Empty(t, f.LastSecret())
}

func TestSecurePromptFramesEndOnTheCurrentSet(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	c := dialStream(t, g, "")
	_ = readFrame(t, c)
	f.AddPinentryPrompt(pinentryReq("p1"))
	_ = nextFrameOfType(t.Context(), t, c, frameSecurePrompt)
	// The answer forgets p1 while a new SSH prompt arrives through the pump.
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.AddSSHPrompt(sshaskpass.PromptRequest{ID: "s9", Prompt: "git@github.com's password: "})
	}()
	rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/pinentry/answer", `{"id":"p1","secret":"x"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	<-done
	require.Eventually(t, func() bool { return len(g.pending.securePrompts()) == 1 }, time.Second, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var last testFrame
	for {
		last = nextFrameOfType(ctx, t, c, frameSecurePrompt)
		if strings.Contains(string(last.Payload), `"id":"s9"`) && !strings.Contains(string(last.Payload), `"id":"p1"`) {
			break
		}
	}
	// No later frame may undo it: publish a marker and read up to it.
	g.hub.publishJSON("test_marker", map[string]string{})
	for {
		fr := nextFrameOfType(ctx, t, c, "")
		if fr.Type == "test_marker" {
			break
		}
		if fr.Type == frameSecurePrompt {
			require.Contains(t, string(fr.Payload), `"id":"s9"`, "a stale set was published after the current one")
		}
	}
}

func TestSecretTooLongIsRefused(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddPinentryPrompt(pinentryReq("p1"))
	require.Eventually(t, func() bool { return len(g.pending.securePrompts()) == 1 }, time.Second, 10*time.Millisecond)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/pinentry/answer", `{"id":"p1","secret":"`+strings.Repeat("x", 4097)+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "secret_too_long")
}

// TestSecretNeverEchoedOrLogged is not parallel: captureLogs replaces the
// default logger.
func TestSecretNeverEchoedOrLogged(t *testing.T) {
	logs := captureLogs(t)
	g, f := startTest(t)
	c := dialStream(t, g, "")
	_ = readFrame(t, c)
	f.AddPinentryPrompt(pinentryReq("p1"))
	_ = nextFrameOfType(t.Context(), t, c, frameSecurePrompt)
	client := authedClient(t, g)
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"id":"p1","secret":"` + testSecret + `"}`, http.StatusNoContent},  // Success.
		{`{"id":"p1","secret":"` + testSecret + `"}`, http.StatusConflict},   // Already answered.
		{`{"id":"nope","secret":"` + testSecret + `"}`, http.StatusConflict}, // Unknown id.
		{`{"id":"p1","secret":"` + testSecret + `"`, http.StatusBadRequest},  // Bad JSON.
	} {
		rsp, rspBody := doJSON(t, client, http.MethodPost, g.URL()+"/api/pinentry/answer", tc.body)
		rsp.Body.Close()
		require.Equal(t, tc.want, rsp.StatusCode, tc.body)
		require.NotContains(t, string(rspBody), testSecret)
	}
	require.Equal(t, testSecret, f.LastSecret(), "only the first answer was delivered")
	for _, fr := range g.hub.frames() {
		require.NotContains(t, string(fr.Payload), testSecret)
	}
	require.NotContains(t, logs.String(), testSecret)
}

func TestSecurePromptCancelDismisses(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	c := dialStream(t, g, "")
	_ = readFrame(t, c)
	f.AddSSHPrompt(sshaskpass.PromptRequest{ID: "s4", Prompt: "git@github.com's password: "})
	_ = nextFrameOfType(t.Context(), t, c, frameSecurePrompt)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/ssh/cancel", `{"id":"s4"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.Empty(t, body)
	require.Contains(t, f.CallLog(), "SSHCancel:s4")
	require.Empty(t, g.pending.securePrompts())
	fr := nextFrameOfType(t.Context(), t, c, frameSecurePrompt)
	require.JSONEq(t, `{"prompts":[]}`, string(fr.Payload))

	rsp, body = doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/ssh/cancel", `{"id":"s4"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode)
	require.Contains(t, string(body), "not_pending")
}

func TestSnapshotCarriesAWSSSO(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.Emit(pubsub.Event[notify.Notification]{Type: pubsub.CreatedEvent, Payload: notify.Notification{
		Type: notify.TypeAWSSSOAuth, AWSSOCommand: "aws sso login --profile dev",
	}})
	require.Eventually(t, func() bool { return g.pending.awsSSO() != nil }, time.Second, 10*time.Millisecond)
	c := dialStream(t, g, "")
	snap := readFrame(t, c)
	require.Equal(t, frameSnapshot, snap.Type)
	require.Contains(t, string(snap.Payload), `"aws_sso":{"command":"aws sso login --profile dev"}`)
}

func TestSnapshotDropsFailedAWSSSO(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.Emit(pubsub.Event[notify.Notification]{Type: pubsub.CreatedEvent, Payload: notify.Notification{
		Type: notify.TypeAWSSSOAuth, AWSSOCommand: "aws sso login --profile dev",
	}})
	require.Eventually(t, func() bool { return g.pending.awsSSO() != nil }, time.Second, 10*time.Millisecond)
	f.Emit(pubsub.Event[notify.Notification]{Type: pubsub.CreatedEvent, Payload: notify.Notification{
		Type: notify.TypeAWSSSOAuthResult, Message: "the SSO session expired",
	}})
	require.Eventually(t, func() bool { return g.pending.awsSSO() == nil }, time.Second, 10*time.Millisecond)
	c := dialStream(t, g, "")
	snap := readFrame(t, c)
	require.Equal(t, frameSnapshot, snap.Type)
	require.NotContains(t, string(snap.Payload), "aws_sso", "a failed refresh leaves no command in the snapshot")
	require.NotContains(t, string(snap.Payload), "aws sso login --profile dev")
}
