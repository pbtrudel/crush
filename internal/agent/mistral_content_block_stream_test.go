package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/stretchr/testify/require"
)

// Fixture captured from a live api.mistral.ai/v1/chat/completions stream for
// mistral-large-4-0 with reasoning enabled: every delta carries its content
// as a JSON array of content blocks (thinking/text) until the answer text
// arrives as a plain string.
const mistralContentBlockSSE = `{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"mistral-large-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null,"logprobs":null}]}
{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"mistral-large-4","choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"The user is asking"}],"closed":true}]},"finish_reason":null,"logprobs":null}]}
{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"mistral-large-4","choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":" a very simple math"}],"closed":true}]},"finish_reason":null,"logprobs":null}]}
{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"mistral-large-4","choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":" question."}]}]},"finish_reason":null,"logprobs":null}]}
{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"mistral-large-4","choices":[{"index":0,"delta":{"content":[{"type":"text","text":"2 and "},{"type":"text","text":"orientation"}],"closed":true},"finish_reason":null,"logprobs":null}]}
{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"mistral-large-4","choices":[{"index":0,"delta":{"content":"2"},"finish_reason":null}]}
{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"mistral-large-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"mistral-large-4","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12}}`

func serveMistralSSE(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		rc := http.NewResponseController(w)
		for event := range strings.Lines(payload) {
			event = strings.TrimSpace(event)
			if event == "" {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", event)
			_ = rc.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		_ = rc.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestMistralContentBlockStream provesthe SSE transport turns Mistral's
// content-block deltas into reasoning_content + plain content, so the
// openai-compat layer sees the same shapes any thinking-capable provider
// emits instead of concatenated JSON fragments.
func TestMistralContentBlockStream(t *testing.T) {
	t.Parallel()

	srv := serveMistralSSE(t, mistralContentBlockSSE)
	provider, err := openaicompat.New(
		openaicompat.WithBaseURL(srv.URL+"/v1"),
		openaicompat.WithAPIKey("x"),
		openaicompat.WithHTTPClient(&http.Client{Transport: newMistralContentBlockTransport(http.DefaultTransport)}),
	)
	require.NoError(t, err)
	lm, err := provider.LanguageModel(context.Background(), "mistral-large-4")
	require.NoError(t, err)

	var parts []fantasy.StreamPart
	stream, err := lm.Stream(context.Background(), fantasy.Call{Prompt: fantasy.Prompt{
		{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "1+1=?"}}},
	}})
	require.NoError(t, err)
	for part := range stream {
		parts = append(parts, part)
	}

	require.Equal(t, 1, countMistralType(parts, fantasy.StreamPartTypeReasoningStart), "types: %#v", partMistralTypes(parts))
	require.Equal(t, 1, countMistralType(parts, fantasy.StreamPartTypeReasoningEnd), "types: %#v", partMistralTypes(parts))

	reasoning := ""
	var text strings.Builder
	for _, p := range parts {
		switch p.Type {
		case fantasy.StreamPartTypeReasoningDelta:
			require.NotEmpty(t, p.Delta)
			reasoning += p.Delta
		case fantasy.StreamPartTypeTextDelta:
			text.WriteString(p.Delta)
		}
	}
	require.Equal(t, "The user is asking a very simple math question.", reasoning)
	require.Equal(t, "2 and orientation2", text.String())
	require.NotContains(t, text.String(), `{"type":`)
}

func partMistralTypes(parts []fantasy.StreamPart) []fantasy.StreamPartType {
	types := make([]fantasy.StreamPartType, 0, len(parts))
	for _, p := range parts {
		types = append(types, p.Type)
	}
	return types
}

func countMistralType(parts []fantasy.StreamPart, typ fantasy.StreamPartType) int {
	n := 0
	for _, p := range parts {
		if p.Type == typ {
			n++
		}
	}
	return n
}

// TestRewriteMistralContentBlockChunk covers the chunk-level rewrite rules.
func TestRewriteMistralContentBlockChunk(t *testing.T) {
	t.Parallel()

	mk := func(delta string, closed bool) string {
		blocks := fmt.Sprintf(`[{"type":"thinking","thinking":[{"type":"text","text":%q}],%s}]`,
			delta, map[bool]string{true: `"closed":true`, false: `"closed":false`}[closed])
		return fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":%s},"finish_reason":null,"logprobs":null}]}`, blocks)
	}

	t.Run("mixed thinking and text blocks in one delta", func(t *testing.T) {
		in := `{"choices":[{"index":0,"delta":{"content":[{"type":"text","text":"tail"},{"type":"thinking","thinking":[{"type":"text","text":"head"}]}]},"finish_reason":null}]}`
		out := rewriteMistralContentBlockChunk([]byte(in))
		require.NotEqual(t, string(out), in)
		require.Contains(t, string(out), `"reasoning_content":"head"`)
		require.Contains(t, string(out), `"content":"tail"`)
	})

	t.Run("preserve unrelated fields", func(t *testing.T) {
		in := mk("frag", true)
		out := string(rewriteMistralContentBlockChunk([]byte(in)))
		require.Contains(t, out, `"finish_reason":null`)
		require.Contains(t, out, `"logprobs":null`)
		require.Contains(t, out, `"reasoning_content":"frag"`)
		require.NotContains(t, out, `{"type":"thinking"`)
	})

	t.Run("string content passes through untouched", func(t *testing.T) {
		in := `{"choices":[{"index":0,"delta":{"content":"2"},"finish_reason":null}],"usage":{"prompt_tokens":7}}`
		require.Equal(t, in, string(rewriteMistralContentBlockChunk([]byte(in))))
	})

	t.Run("numbeer fields survive round trip", func(t *testing.T) {
		in := `{"choices":[{"index":0,"delta":{"content":[{"type":"text","text":"a"}],"settings":{}},"usage":{"tokens":123456}}]}`
		out := string(rewriteMistralContentBlockChunk([]byte(in)))
		require.Contains(t, out, `"tokens":123456`)
		require.Contains(t, out, `"content":"a"`)
	})

	t.Run("unknown block types are passed through", func(t *testing.T) {
		in := `{"choices":[{"index":0,"delta":{"content":[{"type":"tool_call","name":"f","args":{}}],"x":1}}]}`
		require.Equal(t, in, string(rewriteMistralContentBlockChunk([]byte(in))))
	})

	t.Run("empty content array passes through", func(t *testing.T) {
		in := `{"choices":[{"index":0,"delta":{"content":[],"x":1}}]}`
		require.Equal(t, in, string(rewriteMistralContentBlockChunk([]byte(in))))
	})

	t.Run("malformed payloads pass through", func(t *testing.T) {
		for _, in := range []string{
			"",
			"not json",
			`{"choices":[{"index":0,"delta":{"content":[{"type":"thinking","broken":`,
			`{"usage":{"n":5}}]}`,
		} {
			require.Equal(t, in, string(rewriteMistralContentBlockChunk([]byte(in))))
		}
	})
}

// TestIsMistralBaseURL checks when the transport applies.
func TestIsMistralBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://api.mistral.ai/v1", true},
		{"https://api.mistral.ai", true},
		{"https://MISTRAL.AI/v1", true},
		{"https://mistral-api.example.com/v1", false},
		{"https://evil.example.com/api.mistral.ai", false},
		{"", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.expected, isMistralBaseURL(tt.url), tt.url)
	}
}

// TestRewriteMistralReasoningHistory covers the outbound history rewrite.
func TestRewriteMistralReasoningHistory(t *testing.T) {
	t.Parallel()

	t.Run("string content becomes thinking plus text blocks", func(t *testing.T) {
		t.Parallel()
		in := []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"Hello!","reasoning_content":"thinking about the user"},{"role":"user","content":"ok"}]}`)
		out, changed := rewriteMistralReasoningHistory(in)
		require.True(t, changed)
		var payload struct {
			Messages []map[string]any `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(out, &payload))
		require.NotContains(t, string(out), "reasoning_content")
		asst := payload.Messages[1]
		blocks, ok := asst["content"].([]any)
		require.True(t, ok, "content should be a block array: %#v", asst["content"])
		require.Len(t, blocks, 2)
		thinking, ok := blocks[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "thinking", thinking["type"])
		parts, ok := thinking["thinking"].([]any)
		require.True(t, ok)
		part, ok := parts[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "thinking about the user", part["text"])
		text, ok := blocks[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "Hello!", text["text"])
	})

	t.Run("empty and null content keep only the thinking block", func(t *testing.T) {
		t.Parallel()
		for name, content := range map[string]any{"empty string": "", "null": nil} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				raw, err := json.Marshal(map[string]any{"messages": []map[string]any{
					{"role": "assistant", "content": content, "reasoning_content": "why"},
				}})
				require.NoError(t, err)
				out, changed := rewriteMistralReasoningHistory(raw)
				require.True(t, changed)
				require.NotContains(t, string(out), "reasoning_content")
				var payload struct {
					Messages []map[string]any `json:"messages"`
				}
				require.NoError(t, json.Unmarshal(out, &payload))
				blocks, ok := payload.Messages[0]["content"].([]any)
				require.True(t, ok, "content should be a block array: %#v", payload.Messages[0]["content"])
				require.Len(t, blocks, 1)
				only, _ := blocks[0].(map[string]any)
				require.Equal(t, "thinking", only["type"])
			})
		}
	})

	t.Run("thinking block is prepended to an existing content array", func(t *testing.T) {
		t.Parallel()
		in := []byte(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"answer"}],"reasoning_content":"why"}]}`)
		out, changed := rewriteMistralReasoningHistory(in)
		require.True(t, changed)
		var payload struct {
			Messages []map[string]any `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(out, &payload))
		require.NotContains(t, string(out), "reasoning_content")
		blocks, ok := payload.Messages[0]["content"].([]any)
		require.True(t, ok, "content should be a block array: %#v", payload.Messages[0]["content"])
		require.Len(t, blocks, 2)
		thinking, _ := blocks[0].(map[string]any)
		require.Equal(t, "thinking", thinking["type"])
		text, _ := blocks[1].(map[string]any)
		require.Equal(t, "text", text["type"])
		require.Equal(t, "answer", text["text"])
	})

	t.Run("tool calls pass through untouched", func(t *testing.T) {
		t.Parallel()
		in := []byte(`{"messages":[{"role":"assistant","tool_calls":[{"id":"t1","type":"function","function":{"name":"f","arguments":"{}"}}],"reasoning_content":"need tool"}]}`)
		out, changed := rewriteMistralReasoningHistory(in)
		require.True(t, changed)
		require.Contains(t, string(out), `"tool_calls"`)
		require.Contains(t, string(out), `"type":"thinking"`)
	})

	t.Run("empty reasoning_content is dropped without a block", func(t *testing.T) {
		t.Parallel()
		in := []byte(`{"messages":[{"role":"assistant","content":"Hey","reasoning_content":""}]}`)
		out, changed := rewriteMistralReasoningHistory(in)
		require.True(t, changed)
		require.NotContains(t, string(out), "reasoning_content")
		require.NotContains(t, string(out), `"type":"thinking"`)
		require.Contains(t, string(out), `"content":"Hey"`)
	})

	t.Run("no reasoning_content means body unchanged", func(t *testing.T) {
		t.Parallel()
		in := `{"a":1,"messages":[{"role":"user","content":"hi"}]}`
		out, changed := rewriteMistralReasoningHistory([]byte(in))
		require.False(t, changed)
		require.Nil(t, out)
	})

	t.Run("unparsable bodies pass through", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{"", "not json", `[1,2,3]`, `{"messages":{}}`, `{"messages":[]}`} {
			out, changed := rewriteMistralReasoningHistory([]byte(in))
			require.False(t, changed, in)
			require.Nil(t, out, in)
		}
	})
}

// TestMistralTransportRewritesRequestBody covers the RoundTrip request path.
func TestMistralTransportRewritesRequestBody(t *testing.T) {
	t.Parallel()

	var captured []byte
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: newMistralContentBlockTransport(http.DefaultTransport)}

	t.Run("reasoning_content is converted in flight", func(t *testing.T) {
		body := `{"model":"m","messages":[{"role":"assistant","content":"Hi","reasoning_content":"why"}]}`
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
			strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		require.Equal(t, "/v1/chat/completions", capturedPath)
		require.NotContains(t, string(captured), "reasoning_content")
		require.Contains(t, string(captured), `"type":"thinking"`)
		require.Contains(t, string(captured), `"text":"Hi"`)
	})

	t.Run("non-json and other endpoints pass through", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/models",
			strings.NewReader(`{"reasoning_content":"x"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		require.Equal(t, `{"reasoning_content":"x"}`, string(captured))
	})
}
