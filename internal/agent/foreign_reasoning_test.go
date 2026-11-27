package agent

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// reasoningResponsesData is the OpenAI Responses payload a stored
// assistant message carries when it was produced on that route.
var reasoningResponsesData = &openai.ResponsesReasoningMetadata{ItemID: "rs_1"}

// foreignAssistantMessage is an assistant message whose reasoning carries
// every provider's replay payload at once, so a strip that misses one is
// visible.
func foreignAssistantMessage(provider, model string) message.Message {
	return message.Message{
		ID:       "msg-foreign",
		Role:     message.Assistant,
		Provider: provider,
		Model:    model,
		Parts: []message.ContentPart{
			message.ReasoningContent{
				Thinking:         "plan",
				Signature:        "anthropic-signature",
				RedactedData:     "anthropic-redacted",
				ThoughtSignature: "google-signature",
				ToolID:           "call_1",
				ResponsesData:    reasoningResponsesData,
			},
			message.TextContent{Text: "answer"},
		},
	}
}

// prepareHistoryFor replays one stored message for a request targeting the
// given provider and model.
func prepareHistoryFor(t *testing.T, msgs []message.Message, requestProvider, requestModel string) *sessionAgent {
	t.Helper()
	env := testEnv(t)
	sa := testSessionAgent(env, nil, nil, "test prompt").(*sessionAgent)
	_, _ = sa.preparePromptFor(msgs, requestProvider, requestModel, false, false)
	return sa
}

// reasoningInHistory returns the reasoning part of the replayed history.
func reasoningInHistory(t *testing.T, msgs []message.Message, requestProvider, requestModel string) fantasy.ReasoningPart {
	t.Helper()
	env := testEnv(t)
	sa := testSessionAgent(env, nil, nil, "test prompt").(*sessionAgent)
	history, _ := sa.preparePromptFor(msgs, requestProvider, requestModel, false, false)
	for _, replayed := range history {
		for _, part := range replayed.Content {
			if reasoning, ok := fantasy.AsMessagePart[fantasy.ReasoningPart](part); ok {
				return reasoning
			}
		}
	}
	t.Fatal("replayed history carries no reasoning part")
	return fantasy.ReasoningPart{}
}

// TestReplayedReasoning_KeepsSignaturesForTheSameModel pins the normal path:
// a request answered by the model that wrote the message replays every
// signature unchanged, so the provider's prefix cache still matches.
func TestReplayedReasoning_KeepsSignaturesForTheSameModel(t *testing.T) {
	t.Parallel()

	reasoning := reasoningInHistory(t, []message.Message{
		foreignAssistantMessage("anthropic", "claude-sonnet-4"),
	}, "anthropic", "claude-sonnet-4")

	require.Equal(t, "plan", reasoning.Text)
	require.Equal(t, &anthropic.ReasoningOptionMetadata{
		Signature:    "anthropic-signature",
		RedactedData: "anthropic-redacted",
	}, reasoning.ProviderOptions[anthropic.Name])
	require.Equal(t, reasoningResponsesData, reasoning.ProviderOptions[openai.Name])
	require.Equal(t, &google.ReasoningMetadata{
		Signature: "google-signature",
		ToolID:    "call_1",
	}, reasoning.ProviderOptions[google.Name])
}

// TestReplayedReasoning_StripsSignaturesFromAnotherModel pins the model
// switch: the reasoning text stays, but the payloads that belong to the
// model that wrote them are gone.
func TestReplayedReasoning_StripsSignaturesFromAnotherModel(t *testing.T) {
	t.Parallel()

	reasoning := reasoningInHistory(t, []message.Message{
		foreignAssistantMessage("anthropic", "claude-sonnet-4"),
	}, "anthropic", "claude-opus-4")

	require.Equal(t, "plan", reasoning.Text)
	require.Empty(t, reasoning.ProviderOptions[anthropic.Name])
	require.Empty(t, reasoning.ProviderOptions[openai.Name])
	require.Empty(t, reasoning.ProviderOptions[google.Name])
	require.Empty(t, reasoning.ProviderOptions, "every provider payload must go")
}

// TestReplayedReasoning_StripsSignaturesFromAnotherProvider pins the route
// switch: the same model ID through a different provider signs differently.
func TestReplayedReasoning_StripsSignaturesFromAnotherProvider(t *testing.T) {
	t.Parallel()

	reasoning := reasoningInHistory(t, []message.Message{
		foreignAssistantMessage("anthropic", "claude-sonnet-4"),
	}, "openrouter", "claude-sonnet-4")

	require.Equal(t, "plan", reasoning.Text)
	require.Empty(t, reasoning.ProviderOptions)
}

// TestReplayedReasoning_KeepsSignaturesWithoutStoredIdentity pins the old
// rows: a message written before the provider and model were stored keeps
// its payloads, because there is nothing to compare and dropping them
// would cost every resumed session its cached prefix.
func TestReplayedReasoning_KeepsSignaturesWithoutStoredIdentity(t *testing.T) {
	t.Parallel()

	reasoning := reasoningInHistory(t, []message.Message{
		foreignAssistantMessage("", ""),
	}, "anthropic", "claude-sonnet-4")

	require.NotEmpty(t, reasoning.ProviderOptions[anthropic.Name])
	require.NotEmpty(t, reasoning.ProviderOptions[openai.Name])
	require.NotEmpty(t, reasoning.ProviderOptions[google.Name])
}

// TestReplayedReasoning_LeavesTheStoredMessageAlone pins that stripping
// happens on the way to the request only: the message in the session keeps
// its signatures, so switching back to the original model replays them.
func TestReplayedReasoning_LeavesTheStoredMessageAlone(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{foreignAssistantMessage("anthropic", "claude-sonnet-4")}
	prepareHistoryFor(t, msgs, "openai", "gpt-5")

	require.Equal(t, "anthropic-signature", msgs[0].ReasoningContent().Signature)
	require.Equal(t, "google-signature", msgs[0].ReasoningContent().ThoughtSignature)
	require.NotNil(t, msgs[0].ReasoningContent().ResponsesData)
}

func TestForeignReasoning(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                          string
		storedProvider, storedModel   string
		requestProvider, requestModel string
		wantForeign                   bool
	}{
		{"same provider and model", "anthropic", "claude-sonnet-4", "anthropic", "claude-sonnet-4", false},
		{"another model", "anthropic", "claude-sonnet-4", "anthropic", "claude-opus-4", true},
		{"another provider", "anthropic", "claude-sonnet-4", "openrouter", "claude-sonnet-4", true},
		{"no stored identity", "", "", "anthropic", "claude-sonnet-4", false},
		{"no request identity", "anthropic", "claude-sonnet-4", "", "", false},
		{"stored model only", "", "claude-sonnet-4", "anthropic", "claude-opus-4", true},
		{"stored provider only", "anthropic", "", "openrouter", "claude-sonnet-4", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.wantForeign, foreignReasoning(
				tc.storedProvider, tc.storedModel, tc.requestProvider, tc.requestModel,
			))
		})
	}
}

// anthropicThinkingRequest is an Anthropic Messages body whose signed
// reasoning sits in history, the shape a model switch leaves behind.
const anthropicThinkingRequest = `{"model":"claude-sonnet-4","max_tokens":1024,` +
	`"thinking":{"type":"enabled","budget_tokens":2048},` +
	`"messages":[` +
	`{"role":"assistant","content":[` +
	`{"type":"thinking","thinking":"let me think","signature":"sig-abc"},` +
	`{"type":"redacted_thinking","data":"enc-xyz"},` +
	`{"type":"tool_use","id":"toolu_1","name":"bash","input":{}}]},` +
	`{"role":"user","content":"go"}]}`

// anthropicSignatureRejection is the body Anthropic sends when a thinking
// block's signature was issued for another request prefix.
const anthropicSignatureRejection = `{"type":"error","error":{"type":"invalid_request_error",` +
	`"message":"messages.0.content.0.thinking.signature: Invalid ` + "`signature`" + ` in ` + "`thinking`" + ` block"}}`

// postAnthropicMessages sends one Messages request through client.
func postAnthropicMessages(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url+"/v1/messages", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// TestThinkingSignatureRetryDropsStaleSignatures pins the safety net for a
// history we could not classify: a 400 naming the signature is retried once
// with the signed blocks gone, keeping the tool call and the reply.
func TestThinkingSignatureRetryDropsStaleSignatures(t *testing.T) {
	t.Parallel()

	srv := newRecordingServer(t, func(w http.ResponseWriter, _ string, count int) {
		if count == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, anthropicSignatureRejection)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message"}`)
	})

	client := withThinkingSignatureRetry(&http.Client{})
	resp := postAnthropicMessages(t, client, srv.server.URL, anthropicThinkingRequest)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	bodies := srv.seen()
	require.Len(t, bodies, 2, "one rejected request and one retry without the signed blocks")
	require.Contains(t, bodies[0], "sig-abc")
	require.Contains(t, bodies[0], "redacted_thinking")
	require.NotContains(t, bodies[1], "sig-abc", "the stale signature must not be resent")
	require.NotContains(t, bodies[1], "redacted_thinking")
	require.NotContains(t, bodies[1], `"signature"`)
	require.NotContains(t, bodies[1], "let me think", "a signed thinking block goes whole: an unsigned one is rejected too")
	require.Contains(t, bodies[1], "toolu_1", "the tool call stays")
	require.Contains(t, bodies[1], `"thinking":{"type":"enabled"`, "the reply may still think")
}

// TestThinkingSignatureRetryLeavesOtherRejectionsAlone pins that only a 400
// naming both a signature and thinking is retried: anything else reaches the
// caller untouched and keeps being sent.
func TestThinkingSignatureRetryLeavesOtherRejectionsAlone(t *testing.T) {
	t.Parallel()

	srv := newRecordingServer(t, func(w http.ResponseWriter, _ string, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: too large"}}`)
	})

	client := withThinkingSignatureRetry(&http.Client{})
	resp := postAnthropicMessages(t, client, srv.server.URL, anthropicThinkingRequest)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	_ = resp.Body.Close()

	resp = postAnthropicMessages(t, client, srv.server.URL, anthropicThinkingRequest)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	_ = resp.Body.Close()

	bodies := srv.seen()
	require.Len(t, bodies, 2, "no retry for an unrelated 400")
	for _, body := range bodies {
		require.Contains(t, body, "sig-abc")
	}
}

// TestThinkingSignatureRetryRetriesOnlyOnce pins the loop bound: a second
// rejection with the same complaint reaches the caller.
func TestThinkingSignatureRetryRetriesOnlyOnce(t *testing.T) {
	t.Parallel()

	srv := newRecordingServer(t, func(w http.ResponseWriter, _ string, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, anthropicSignatureRejection)
	})

	client := withThinkingSignatureRetry(&http.Client{})
	resp := postAnthropicMessages(t, client, srv.server.URL, anthropicThinkingRequest)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	_ = resp.Body.Close()

	require.Len(t, srv.seen(), 2, "exactly one retry")
}
