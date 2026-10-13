package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Mistral's OpenAI-compatible endpoint streams assistant deltas differently
// from every other chat-completions server: while reasoning is active
// delta.content is a JSON array of content blocks
// ({"type":"thinking",...},{"type":"text",...}); the answer text finally
// arrives as a plain string. The openai-go fork can only decode content as a
// string, so the array lands in that field as raw JSON text and gets
// streamed to the user (and stored as the final message) verbatim.
//
// This transport rewrites each SSE event into the standard shapes the
// openai-compat layer already understands: thinking blocks become
// delta.reasoning_content (mapped to reasoning parts by
// openaicompat.StreamExtraFunc) and text blocks become a string
// delta.content, so reasoning renders as thinking and the answer stays clean.
//
// It also rewrites outgoing request bodies: the openai-compat layer replays
// reasoning parts as a DeepSeek-style `reasoning_content` field on the
// message, which Mistral rejects with 422 extra_forbidden (verified against
// api.mistral.ai for assistant and user roles, with and without tool calls).
// Mistral instead accepts reasoning replayed as a
// {"type":"thinking","thinking":[{"type":"text",...}]} block inside the
// message content array, so the rewriter moves the field there.

// isMistralBaseURL reports whether the provider's base URL points at
// Mistral's own API, the only server known to stream content-block arrays in
// delta.content. Host-based matching avoids surprises for providers that
// merely mention mistral somewhere in their URL.
func isMistralBaseURL(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "mistral.ai", "api.mistral.ai":
		return true
	default:
		return false
	}
}

func newMistralContentBlockTransport(base http.RoundTripper) http.RoundTripper {
	return mistralContentBlockTransport{base: base}
}

type mistralContentBlockTransport struct {
	base http.RoundTripper
}

func (t mistralContentBlockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = t.maybeRewriteRequestBody(req)
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return resp, nil
	}

	pr, pw := io.Pipe()
	go func() {
		rewriteErr := rewriteMistralSSEBody(resp.Body, pw)
		_ = pw.CloseWithError(rewriteErr)
		_ = resp.Body.Close()
	}()

	out := *resp
	out.Body = pr
	out.Header.Del("Content-Length")
	return &out, nil
}

// rewriteMistralSSEBody copies an SSE body through, replacing every "data:"
// event whose delta.content is a content-block array with an equivalent
// delta carrying reasoning_content and/or a string content. Non-event lines
// and unusable payloads pass through untouched.
func rewriteMistralSSEBody(r io.Reader, w io.Writer) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, readErr := br.ReadString('\n')
		if err := rewriteSSELine(line, w); err != nil {
			return err
		}
		switch readErr {
		case nil:
			continue
		case io.EOF:
			return nil
		default:
			return readErr
		}
	}
}

func rewriteSSELine(line string, w io.Writer) error {
	if strings.HasPrefix(line, "data: ") {
		payload := strings.TrimSuffix(line, "\n")[len("data: "):]
		if payload != "" && payload != "[DONE]" {
			payload = string(rewriteMistralContentBlockChunk([]byte(payload)))
			written := "data: " + payload
			if strings.HasSuffix(line, "\n") {
				written += "\n"
			}
			_, err := io.WriteString(w, written)
			return err
		}
	}
	_, err := io.WriteString(w, line)
	return err
}

// maybeRewriteRequestBody rewrites an outgoing chat-completions body that
// carries reasoning_content fields and returns a new request for it; the
// request passes through untouched when no rewrite is needed. GetBody is set
// so redirect/retry machinery can replay the rewritten body.
func (t mistralContentBlockTransport) maybeRewriteRequestBody(req *http.Request) *http.Request {
	if req.Body == nil ||
		!strings.Contains(req.Header.Get("Content-Type"), "application/json") ||
		!strings.Contains(req.URL.Path, "/chat/completions") {
		return req
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return req
	}
	rewritten, changed := rewriteMistralReasoningHistory(body)
	if !changed {
		rewritten = body
	}
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(rewritten))
	out.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rewritten)), nil
	}
	out.ContentLength = int64(len(rewritten))
	out.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	return out
}

// rewriteMistralReasoningHistory converts every `reasoning_content` message
// field into a thinking content block prepended to the message content — the
// shape Mistral requires when reasoning is replayed in message history.
// Bodies without the field are returned unchanged; anything unparsable or
// with an unexpected content shape degrades to removing the field.
func rewriteMistralReasoningHistory(body []byte) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var payload map[string]any
	if err := dec.Decode(&payload); err != nil {
		return nil, false
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, false
	}
	changed := false
	for _, msgRaw := range messages {
		msg, ok := msgRaw.(map[string]any)
		if !ok {
			continue
		}
		reasoningRaw, present := msg["reasoning_content"]
		if !present {
			continue
		}
		delete(msg, "reasoning_content")
		changed = true
		reasoning, _ := reasoningRaw.(string)
		if reasoning == "" {
			continue
		}
		thinking := []any{map[string]any{
			"type":     "thinking",
			"thinking": []any{map[string]any{"type": "text", "text": reasoning}},
			"closed":   true,
		}}
		switch content := msg["content"].(type) {
		case string:
			if content != "" {
				thinking = append(thinking, map[string]any{"type": "text", "text": content})
			}
			msg["content"] = thinking
		case []any:
			msg["content"] = append(thinking, content...)
		default:
			msg["content"] = thinking
		}
	}
	if !changed {
		return nil, false
	}
	b := new(bytes.Buffer)
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, false
	}
	return bytes.TrimRight(b.Bytes(), "\n"), true
}

type mistralContentBlock struct {
	Type     string           `json:"type"`
	Text     string           `json:"text"`
	Thinking []mistralContent `json:"thinking"`
}

type mistralContent struct {
	Type json.RawMessage `json:"type"`
	Text string          `json:"text"`
}

// rewriteMistralContentBlockChunk rewrites choices[].delta.content when it is
// an array of recognized content blocks: "thinking" blocks become a
// reasoning_content string delta field, "text" blocks are concatenated into a
// string content. Anything else — plain string content, empty arrays,
// unrecognized block types, malformed JSON — is returned unchanged, so the
// rewriter degrades to a no-op rather than corrupting a stream it cannot
// parse.
func rewriteMistralContentBlockChunk(raw []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var payload map[string]any
	if err := dec.Decode(&payload); err != nil {
		return raw
	}
	choices, ok := payload["choices"].([]any)
	if !ok {
		return raw
	}

	changed := false
	for _, ch := range choices {
		choice, ok := ch.(map[string]any)
		if !ok {
			continue
		}
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			continue
		}
		content, ok := delta["content"].([]any)
		if !ok || len(content) == 0 {
			continue
		}

		var reasoning, text strings.Builder
		unusable := false
		for _, blockRaw := range content {
			block, ok := blockRaw.(map[string]any)
			if !ok {
				unusable = true
				break
			}
			blockType, _ := block["type"].(string)
			switch blockType {
			case "thinking":
				parts, ok := block["thinking"].([]any)
				if !ok {
					unusable = true
					break
				}
				for _, partRaw := range parts {
					part, ok := partRaw.(map[string]any)
					if !ok {
						unusable = true
						break
					}
					partType, _ := part["type"].(string)
					if partType != "text" {
						unusable = true
						break
					}
					partText, _ := part["text"].(string)
					reasoning.WriteString(partText)
				}
			case "text":
				textText, ok := block["text"].(string)
				if !ok {
					unusable = true
					break
				}
				text.WriteString(textText)
			default:
				unusable = true
			}
		}
		if unusable {
			return raw
		}

		if reasoning.Len() > 0 {
			delta["reasoning_content"] = reasoning.String()
			changed = true
		}
		if text.Len() > 0 {
			delta["content"] = text.String()
			changed = true
		} else {
			delete(delta, "content")
			changed = true
		}
	}

	if !changed {
		return raw
	}
	b := new(bytes.Buffer)
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return raw
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}
