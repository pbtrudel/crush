package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// replayTestResponse is one canned HTTP response the test server serves, in
// order, for the requests it receives.
type replayTestResponse struct {
	status      int
	contentType string
	body        string
}

// replayTestServer records every request body and serves canned responses in
// order. The mutex keeps the handler goroutine and the test goroutine
// race-free under -race.
type replayTestServer struct {
	mu        sync.Mutex
	bodies    [][]byte
	responses []replayTestResponse
	server    *httptest.Server
}

func newReplayTestServer(t *testing.T, responses ...replayTestResponse) *replayTestServer {
	t.Helper()

	s := &replayTestServer{responses: responses}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		index := len(s.bodies)
		s.bodies = append(s.bodies, body)
		s.mu.Unlock()

		if index >= len(s.responses) {
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		response := s.responses[index]
		if response.contentType != "" {
			w.Header().Set("Content-Type", response.contentType)
		}
		w.WriteHeader(response.status)
		_, _ = io.WriteString(w, response.body)
	}))
	t.Cleanup(s.server.Close)
	return s
}

// seenBodies returns a copy of every request body the server received.
func (s *replayTestServer) seenBodies() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.bodies))
	for i, body := range s.bodies {
		out[i] = append([]byte(nil), body...)
	}
	return out
}

// post sends one request through client and drains the response.
func (s *replayTestServer) post(t *testing.T, client *http.Client, body string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.server.URL+"/responses", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

func sseBody(events ...string) replayTestResponse {
	var b strings.Builder
	for _, event := range events {
		b.WriteString("data: ")
		b.WriteString(event)
		b.WriteString("\n\n")
	}
	return replayTestResponse{status: http.StatusOK, contentType: "text/event-stream", body: b.String()}
}

func replayTestClient(t *testing.T, enabled bool) *http.Client {
	t.Helper()
	return &http.Client{Transport: newResponsesReasoningReplayTransport(http.DefaultTransport, enabled)}
}

// replayItem builds one JSON output/input item from its fields. Values are
// fake; nothing here is a real encrypted blob.
func replayItem(fields map[string]any) string {
	raw, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func replayRequest(t *testing.T, model string, input ...string) string {
	t.Helper()

	items := make([]json.RawMessage, len(input))
	for i, item := range input {
		items[i] = json.RawMessage(item)
	}
	body, err := json.Marshal(map[string]any{
		"model": model,
		"input": items,
		"store": false,
	})
	require.NoError(t, err)
	return string(body)
}

// replayInputItems decodes the input array of a request body the server saw.
func replayInputItems(t *testing.T, body []byte) []map[string]any {
	t.Helper()

	var request struct {
		Input []map[string]any `json:"input"`
	}
	require.NoError(t, json.Unmarshal(body, &request))
	return request.Input
}

// replayInputTypes names each input item: its type, or its role for a
// message item.
func replayInputTypes(t *testing.T, body []byte) []string {
	t.Helper()

	var types []string
	for _, item := range replayInputItems(t, body) {
		if itemType, ok := item["type"].(string); ok && itemType != "" {
			types = append(types, itemType)
			continue
		}
		role, _ := item["role"].(string)
		types = append(types, role)
	}
	return types
}

// replayReasoningItems returns the encrypted content of every reasoning item
// in a request body, in order.
func replayReasoningItems(t *testing.T, body []byte) []string {
	t.Helper()

	var encrypted []string
	for _, item := range replayInputItems(t, body) {
		if item["type"] != "reasoning" {
			continue
		}
		content, _ := item["encrypted_content"].(string)
		encrypted = append(encrypted, content)
	}
	return encrypted
}

// reasoningStream builds a streamed response holding one reasoning item whose
// closing event carries closeCipher, followed by one function_call.
func reasoningStream() string {
	return sseBody(
		`{"type":"response.created","response":{"id":"resp_1","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":`+replayItem(map[string]any{
			"id": "rsn_1", "type": "reasoning", "encrypted_content": "open-cipher-1", "summary": []any{},
		})+`}`,
		`{"type":"response.reasoning_summary_text.delta","item_id":"rsn_1","delta":"Thinking"}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+replayItem(map[string]any{
			"id": "rsn_1", "type": "reasoning", "encrypted_content": "close-cipher-1",
			"summary": []any{map[string]any{"type": "summary_text", "text": "Thinking"}},
		})+`}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_a","name":"view","arguments":""}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_a","name":"view","arguments":"{\"path\":\"a.txt\"}"}}`,
		// Copilot wraps the response-level events; the completed event
		// repeats the whole output, including the closing reasoning item.
		`{"type":"response.completed","response":{"id":"resp_1","output":[`+
			replayItem(map[string]any{
				"id": "rsn_1", "type": "reasoning", "encrypted_content": "close-cipher-1",
				"summary": []any{map[string]any{"type": "summary_text", "text": "Thinking"}},
			})+`,`+
			replayItem(map[string]any{
				"id": "fc_1", "type": "function_call", "call_id": "call_a", "name": "view", "arguments": `{"path":"a.txt"}`,
			})+`]}}`,
	).body
}

// reasoningTextStream builds a streamed response holding one reasoning item
// followed by an assistant message carrying text.
func reasoningTextStream(id, encrypted, text string) string {
	return sseBody(
		`{"type":"response.output_item.done","output_index":0,"item":`+replayItem(map[string]any{
			"id": id, "type": "reasoning", "encrypted_content": encrypted, "summary": []any{},
		})+`}`,
		`{"type":"response.output_item.done","output_index":1,"item":`+replayItem(map[string]any{
			"id": "msg_" + id, "type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text}},
		})+`}`,
		`{"type":"response.completed","response":{"id":"resp_text","output":[]}}`,
	).body
}

// reasoningResponse is reasoningStream as a canned server response.
func reasoningResponse() replayTestResponse {
	return replayTestResponse{status: http.StatusOK, contentType: "text/event-stream", body: reasoningStream()}
}

// completedResponse is a minimal terminal response with no output items.
func completedResponse(id string) replayTestResponse {
	return sseBody(`{"type":"response.completed","response":{"id":"` + id + `","output":[]}}`)
}

func functionCall(callID, name, arguments string) string {
	return replayItem(map[string]any{"type": "function_call", "call_id": callID, "name": name, "arguments": arguments})
}

func functionCallOutput(callID, output string) string {
	return replayItem(map[string]any{"type": "function_call_output", "call_id": callID, "output": output})
}

// TestResponsesReplayInsertsReasoningBeforeFunctionCall covers the basic
// shape: the response's reasoning item is recorded with its closing
// encrypted content and put back into the next request right before the
// function call that followed it.
func TestResponsesReplayInsertsReasoningBeforeFunctionCall(t *testing.T) {
	t.Parallel()

	server := newReplayTestServer(t, reasoningResponse())
	client := replayTestClient(t, true)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))

	bodies := server.seenBodies()
	require.Len(t, bodies, 1)
	require.Empty(t, replayReasoningItems(t, bodies[0]), "the first request carries no reasoning")

	second := replayRequest(t, "gpt-test",
		`{"role":"user","content":"hi"}`,
		functionCall("call_a", "view", `{"path":"a.txt"}`),
		functionCallOutput("call_a", "ok"),
	)
	server.post(t, client, second)

	bodies = server.seenBodies()
	require.Len(t, bodies, 2)
	require.Equal(t,
		[]string{"user", "reasoning", "function_call", "function_call_output"},
		replayInputTypes(t, bodies[1]),
	)
	require.Equal(t, []string{"close-cipher-1"}, replayReasoningItems(t, bodies[1]),
		"the closing encrypted content is replayed, not the opening one")

	items := replayInputItems(t, bodies[1])
	require.Equal(t, "rsn_1", items[1]["id"], "the item id travels with the reasoning item")
}

// TestResponsesReplayInsertsReasoningBeforeAssistantText covers a response
// that reasons, writes text and then calls a tool: the reasoning item is
// anchored by the assistant message that followed it.
func TestResponsesReplayInsertsReasoningBeforeAssistantText(t *testing.T) {
	t.Parallel()

	stream := sseBody(
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"rsn_text","type":"reasoning","encrypted_content":"open-cipher-text","summary":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"rsn_text","type":"reasoning","encrypted_content":"close-cipher-text","summary":[]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","delta":"Let me look."}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Let me look."}]}}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"id":"fc_text","type":"function_call","call_id":"call_text","name":"view","arguments":""}}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"id":"fc_text","type":"function_call","call_id":"call_text","name":"view","arguments":"{}"}}`,
	).body
	server := newReplayTestServer(t, replayTestResponse{status: http.StatusOK, contentType: "text/event-stream", body: stream})
	client := replayTestClient(t, true)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))
	server.post(t, client, replayRequest(t, "gpt-test",
		`{"role":"user","content":"hi"}`,
		`{"role":"assistant","content":"Let me look."}`,
		functionCall("call_text", "view", "{}"),
		functionCallOutput("call_text", "ok"),
	))

	bodies := server.seenBodies()
	require.Len(t, bodies, 2)
	require.Equal(t,
		[]string{"user", "reasoning", "assistant", "function_call", "function_call_output"},
		replayInputTypes(t, bodies[1]),
	)
	require.Equal(t, []string{"close-cipher-text"}, replayReasoningItems(t, bodies[1]))
}

// TestResponsesReplayInsertsOneReasoningItemBeforeSeveralCalls covers a
// response whose reasoning item is followed by several parallel tool calls:
// the item is replayed once, before the first call.
func TestResponsesReplayInsertsOneReasoningItemBeforeSeveralCalls(t *testing.T) {
	t.Parallel()

	stream := sseBody(
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"rsn_many","type":"reasoning","encrypted_content":"close-cipher-many","summary":[]}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_a","type":"function_call","call_id":"call_a","name":"view","arguments":"{}"}}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"id":"fc_b","type":"function_call","call_id":"call_b","name":"bash","arguments":"{}"}}`,
		`{"type":"response.output_item.done","output_index":3,"item":{"id":"fc_c","type":"function_call","call_id":"call_c","name":"grep","arguments":"{}"}}`,
		`{"type":"response.completed","response":{"id":"resp_many","output":[]}}`,
	).body
	server := newReplayTestServer(t, replayTestResponse{status: http.StatusOK, contentType: "text/event-stream", body: stream})
	client := replayTestClient(t, true)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))
	server.post(t, client, replayRequest(t, "gpt-test",
		`{"role":"user","content":"hi"}`,
		functionCall("call_a", "view", "{}"), functionCallOutput("call_a", "a"),
		functionCall("call_b", "bash", "{}"), functionCallOutput("call_b", "b"),
		functionCall("call_c", "grep", "{}"), functionCallOutput("call_c", "c"),
	))

	bodies := server.seenBodies()
	require.Len(t, bodies, 2)
	require.Equal(t, []string{"close-cipher-many"}, replayReasoningItems(t, bodies[1]))
	require.Equal(t,
		[]string{"user", "reasoning", "function_call", "function_call_output", "function_call", "function_call_output", "function_call", "function_call_output"},
		replayInputTypes(t, bodies[1]),
	)
}

// TestResponsesReplayStandsDownWhenRequestHasReasoning covers the compatibility
// rule: a body that already holds a reasoning item is left exactly as sent, so
// a provider library that replays its own items does not get duplicates.
func TestResponsesReplayStandsDownWhenRequestHasReasoning(t *testing.T) {
	t.Parallel()

	server := newReplayTestServer(t, reasoningResponse())
	client := replayTestClient(t, true)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))

	second := replayRequest(t, "gpt-test",
		`{"role":"user","content":"hi"}`,
		replayItem(map[string]any{"type": "reasoning", "id": "rsn_manual", "encrypted_content": "existing-cipher"}),
		functionCall("call_a", "view", `{"path":"a.txt"}`),
		functionCallOutput("call_a", "ok"),
	)
	server.post(t, client, second)

	bodies := server.seenBodies()
	require.Len(t, bodies, 2)
	require.Equal(t, second, string(bodies[1]), "the body passes through byte for byte")
	require.Equal(t, []string{"existing-cipher"}, replayReasoningItems(t, bodies[1]))
}

// TestResponsesReplayRetriesWithoutItemsOnRejection covers a provider that
// refuses the inserted items: the transport retries once without them and
// stops inserting for its remaining lifetime.
func TestResponsesReplayRetriesWithoutItemsOnRejection(t *testing.T) {
	t.Parallel()

	server := newReplayTestServer(t,
		reasoningResponse(),
		replayTestResponse{
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body:        `{"error":{"message":"Item 'rsn_1' of type 'reasoning' was not found."}}`,
		},
		completedResponse("resp_2"),
		completedResponse("resp_3"),
	)
	client := replayTestClient(t, true)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))

	second := replayRequest(t, "gpt-test",
		`{"role":"user","content":"hi"}`,
		functionCall("call_a", "view", `{"path":"a.txt"}`),
		functionCallOutput("call_a", "ok"),
	)
	server.post(t, client, second)

	bodies := server.seenBodies()
	require.Len(t, bodies, 3, "the rejected request is retried once")
	require.Equal(t, []string{"close-cipher-1"}, replayReasoningItems(t, bodies[1]), "the first attempt carries the items")
	require.Equal(t, second, string(bodies[2]), "the retry is the original body")

	server.post(t, client, second)
	bodies = server.seenBodies()
	require.Len(t, bodies, 4)
	require.Equal(t, second, string(bodies[3]), "no later request inserts items again")
}

// TestResponsesReplayScopesToModel covers the endpoint rule: an item is given
// back only to the model that produced it.
func TestResponsesReplayScopesToModel(t *testing.T) {
	t.Parallel()

	server := newReplayTestServer(t, reasoningResponse())
	client := replayTestClient(t, true)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))

	other := replayRequest(t, "gpt-other",
		`{"role":"user","content":"hi"}`,
		functionCall("call_a", "view", `{"path":"a.txt"}`),
		functionCallOutput("call_a", "ok"),
	)
	server.post(t, client, other)

	bodies := server.seenBodies()
	require.Len(t, bodies, 2)
	require.Equal(t, other, string(bodies[1]), "a different model gets the body untouched")
}

// TestResponsesReplayDisabledPassesThrough covers the off switch: the option
// off returns the wrapped transport itself, so requests and responses are
// untouched byte for byte.
func TestResponsesReplayDisabledPassesThrough(t *testing.T) {
	t.Parallel()

	base := http.DefaultTransport
	require.Equal(t, base, newResponsesReasoningReplayTransport(base, false))

	server := newReplayTestServer(t, reasoningResponse())
	client := replayTestClient(t, false)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))
	second := replayRequest(t, "gpt-test",
		`{"role":"user","content":"hi"}`,
		functionCall("call_a", "view", `{"path":"a.txt"}`),
		functionCallOutput("call_a", "ok"),
	)
	server.post(t, client, second)

	bodies := server.seenBodies()
	require.Len(t, bodies, 2)
	require.Equal(t, second, string(bodies[1]), "nothing is recorded or inserted when the option is off")
}

// TestResponsesReplayDropsOldestItems covers the memory bound: the transport
// keeps the newest items and drops the oldest first.
func TestResponsesReplayDropsOldestItems(t *testing.T) {
	t.Parallel()

	transport := newResponsesReasoningReplayTransport(nil, true).(*responsesReasoningReplayTransport)
	for i := range maxResponsesReplayItems + 5 {
		transport.remember(replayedReasoning{
			scope: replayScope{endpoint: "https://example.test/responses", model: "gpt-test"},
			item: json.RawMessage(replayItem(map[string]any{
				"type": "reasoning", "id": fmt.Sprintf("rsn_%d", i), "encrypted_content": "cipher",
			})),
			follower: replayFollower{callID: "call"},
		})
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	require.Len(t, transport.items, maxResponsesReplayItems)
	var oldest struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(transport.items[0].item, &oldest))
	require.Equal(t, "rsn_5", oldest.ID, "the oldest items are dropped first")
}

// TestResponsesReplayReadsEnvelopeOnlyStream covers GitHub Copilot's
// envelope: when the only record of the output is the wrapped completed
// response, its output list still yields the reasoning item and its follower.
func TestResponsesReplayReadsEnvelopeOnlyStream(t *testing.T) {
	t.Parallel()

	stream := sseBody(
		`{"type":"response.created","response":{"id":"resp_env","output":[]}}`,
		`{"type":"response.in_progress","response":{"id":"resp_env","output":[]}}`,
		`{"type":"response.completed","response":{"id":"resp_env","output":[`+
			replayItem(map[string]any{
				"id": "rsn_env", "type": "reasoning", "encrypted_content": "close-cipher-env",
				"summary": []any{map[string]any{"type": "summary_text", "text": "Looking"}},
			})+`,`+
			replayItem(map[string]any{
				"type": "function_call", "call_id": "call_env", "name": "view", "arguments": "{}",
			})+`]}}`,
	)
	server := newReplayTestServer(t, replayTestResponse{status: http.StatusOK, contentType: "text/event-stream", body: stream.body})
	client := replayTestClient(t, true)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))
	server.post(t, client, replayRequest(t, "gpt-test",
		`{"role":"user","content":"hi"}`,
		functionCall("call_env", "view", "{}"),
		functionCallOutput("call_env", "ok"),
	))

	bodies := server.seenBodies()
	require.Len(t, bodies, 2)
	require.Equal(t, []string{"close-cipher-env"}, replayReasoningItems(t, bodies[1]))
	require.Equal(t,
		[]string{"user", "reasoning", "function_call", "function_call_output"},
		replayInputTypes(t, bodies[1]),
	)
}

// TestResponsesReasoningReplayEnabled covers the two gates: the config option
// and the environment override the eval runner sets per variant.
func TestResponsesReasoningReplayEnabled(t *testing.T) {
	t.Run("option off and no override", func(t *testing.T) {
		t.Setenv("CRUSH_RESPONSES_REASONING_REPLAY", "")
		require.False(t, responsesReasoningReplayEnabled(false))
	})
	t.Run("option on", func(t *testing.T) {
		t.Setenv("CRUSH_RESPONSES_REASONING_REPLAY", "")
		require.True(t, responsesReasoningReplayEnabled(true))
	})
	t.Run("override one", func(t *testing.T) {
		t.Setenv("CRUSH_RESPONSES_REASONING_REPLAY", "1")
		require.True(t, responsesReasoningReplayEnabled(false))
	})
	t.Run("override true", func(t *testing.T) {
		t.Setenv("CRUSH_RESPONSES_REASONING_REPLAY", "true")
		require.True(t, responsesReasoningReplayEnabled(false))
	})
	t.Run("override off", func(t *testing.T) {
		t.Setenv("CRUSH_RESPONSES_REASONING_REPLAY", "0")
		require.False(t, responsesReasoningReplayEnabled(false))
	})
}

// TestResponsesReplayIgnoresOpeningEncryptedContent pins the closing value:
// an item whose closing event carries no encrypted content is not replayed
// from the opening event's copy.
func TestResponsesReplayIgnoresOpeningEncryptedContent(t *testing.T) {
	t.Parallel()

	stream := sseBody(
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"rsn_open","type":"reasoning","encrypted_content":"open-cipher-only","summary":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"rsn_open","type":"reasoning","summary":[]}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_open","type":"function_call","call_id":"call_open","name":"view","arguments":"{}"}}`,
	).body
	server := newReplayTestServer(t, replayTestResponse{status: http.StatusOK, contentType: "text/event-stream", body: stream})
	client := replayTestClient(t, true)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))
	second := replayRequest(t, "gpt-test",
		`{"role":"user","content":"hi"}`,
		functionCall("call_open", "view", "{}"),
		functionCallOutput("call_open", "ok"),
	)
	server.post(t, client, second)

	bodies := server.seenBodies()
	require.Len(t, bodies, 2)
	require.Equal(t, second, string(bodies[1]), "the opening event's encrypted content is never replayed")
}

// TestResponsesReplayScopesToConversation covers two sessions sharing one
// transport, both ending an assistant message with the same text: the record
// from one conversation is never inserted into the other.
func TestResponsesReplayScopesToConversation(t *testing.T) {
	t.Parallel()

	server := newReplayTestServer(t,
		replayTestResponse{status: http.StatusOK, contentType: "text/event-stream", body: reasoningTextStream("rsn_a", "close-cipher-a", "Done.")},
		completedResponse("resp_b1"),
		completedResponse("resp_a2"),
		completedResponse("resp_b2"),
	)
	client := replayTestClient(t, true)

	// Session A reasons, writes "Done." and stops.
	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"session A"}`))
	// Session B starts later with the same closing words ahead of it.
	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"session B"}`))

	server.post(t, client, replayRequest(t, "gpt-test",
		`{"role":"user","content":"session A"}`,
		`{"role":"assistant","content":"Done."}`,
	))
	server.post(t, client, replayRequest(t, "gpt-test",
		`{"role":"user","content":"session B"}`,
		`{"role":"assistant","content":"Done."}`,
	))

	bodies := server.seenBodies()
	require.Len(t, bodies, 4)
	require.Equal(t, []string{"close-cipher-a"}, replayReasoningItems(t, bodies[2]),
		"the conversation that produced the item gets it")
	require.Empty(t, replayReasoningItems(t, bodies[3]),
		"the other conversation, with the same assistant text, does not")
}

// TestResponsesReplayTrimsTextAnchors covers whitespace around an assistant
// message: the response and the replayed request may differ by surrounding
// whitespace and must still match.
func TestResponsesReplayTrimsTextAnchors(t *testing.T) {
	t.Parallel()

	server := newReplayTestServer(t,
		replayTestResponse{status: http.StatusOK, contentType: "text/event-stream", body: reasoningTextStream("rsn_trim", "close-cipher-trim", "  Done.\n")},
	)
	client := replayTestClient(t, true)

	server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))
	server.post(t, client, replayRequest(t, "gpt-test",
		`{"role":"user","content":"hi"}`,
		`{"role":"assistant","content":"Done."}`,
	))

	bodies := server.seenBodies()
	require.Len(t, bodies, 2)
	require.Equal(t, []string{"close-cipher-trim"}, replayReasoningItems(t, bodies[1]))
	require.Equal(t, []string{"user", "reasoning", "assistant"}, replayInputTypes(t, bodies[1]))
}
