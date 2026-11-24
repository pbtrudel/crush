package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
)

// foreignReasoningProviders are the provider option keys that carry replay
// data only the model that issued it accepts.
var foreignReasoningProviders = []string{anthropic.Name, openai.Name, google.Name}

// foreignReasoning reports whether a stored assistant message was written by
// a different model or provider than the one a request targets.
//
// Provider-specific reasoning data — an Anthropic thinking signature, the
// encrypted OpenAI Responses reasoning, a Google thought signature — is bound
// to the model that produced it. Replaying it after a model or provider
// switch is rejected outright or bills reasoning the new model cannot use.
//
// A message or a request with no recorded identity is left alone: rows
// written before the fields existed carry nothing to compare, and dropping
// their signatures would cost every resumed session its cached prefix.
func foreignReasoning(storedProvider, storedModel, requestProvider, requestModel string) bool {
	if requestProvider == "" && requestModel == "" {
		return false
	}
	if storedProvider == "" && storedModel == "" {
		return false
	}
	if storedProvider != "" && storedProvider != requestProvider {
		return true
	}
	return storedModel != "" && storedModel != requestModel
}

// stripReasoningReplayData removes the provider-specific replay data from
// every reasoning part in msgs and reports how many parts it changed. The
// reasoning text stays: it is still the model's own thinking, and a provider
// that needs a signature to replay a thinking block drops the block itself.
func stripReasoningReplayData(msgs []fantasy.Message) int {
	stripped := 0
	for i := range msgs {
		for j, part := range msgs[i].Content {
			reasoning, ok := fantasy.AsMessagePart[fantasy.ReasoningPart](part)
			if !ok || len(reasoning.ProviderOptions) == 0 {
				continue
			}
			removed := false
			for _, provider := range foreignReasoningProviders {
				if _, set := reasoning.ProviderOptions[provider]; !set {
					continue
				}
				delete(reasoning.ProviderOptions, provider)
				removed = true
			}
			if !removed {
				continue
			}
			msgs[i].Content[j] = reasoning
			stripped++
		}
	}
	return stripped
}

// signatureField is the JSON field a signed thinking block carries.
var signatureField = []byte(`"signature"`)

// withThinkingSignatureRetry wraps client with a transport that survives a
// replayed thinking block whose signature the provider will not accept.
// Anthropic answers that with a 400 and the whole turn dies; the signed
// blocks cost only their text, so the same body is retried once without
// them. A provider that had no client of its own gets one, so the transport
// has somewhere to sit.
func withThinkingSignatureRetry(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &thinkingSignatureTransport{base: base}
	return client
}

type thinkingSignatureTransport struct {
	base http.RoundTripper
}

func (t *thinkingSignatureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.Body == nil || req.Body == http.NoBody {
		return t.base.RoundTrip(req)
	}
	body, encoding, err := readRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("reading thinking signature request body: %w", err)
	}
	// Only a body that replays a signature can draw the rejection. Reading
	// it consumed the body, so every request is rebuilt from these bytes.
	if !bytes.Contains(body, signatureField) {
		return t.base.RoundTrip(requestWithBody(req, body, encoding))
	}

	resp, err := t.base.RoundTrip(requestWithBody(req, body, encoding))
	if err != nil {
		return nil, err
	}
	// Only a 400 is inspected, and only its first 64KiB: a streaming body is
	// never read here.
	if resp.StatusCode != http.StatusBadRequest || resp.Body == nil {
		return resp, nil
	}
	original := resp.Body
	rejection, err := io.ReadAll(io.LimitReader(original, 64<<10))
	if err != nil || !rejectsThinkingSignature(rejection) {
		resp.Body = &bufferedBody{
			Reader:   io.MultiReader(bytes.NewReader(rejection), original),
			original: original,
		}
		return resp, nil
	}
	stripped, changed := stripThinkingSignatures(body)
	if !changed {
		resp.Body = &bufferedBody{
			Reader:   io.MultiReader(bytes.NewReader(rejection), original),
			original: original,
		}
		return resp, nil
	}
	// The provider refused the replayed signatures. Drain and close the
	// response so its connection is reused, then retry once without them.
	_, _ = io.Copy(io.Discard, original)
	_ = original.Close()
	slog.Warn(
		"Provider rejected a replayed thinking signature, retrying without signed reasoning",
		"url", req.URL.Redacted(),
		"body", logBody(rejection),
	)
	return t.base.RoundTrip(requestWithBody(req, stripped, encoding))
}

// rejectsThinkingSignature reports whether a rejection body complains about a
// thinking signature. Anthropic names both words in prose, in either order:
// "messages.0.content.0.thinking.signature: Invalid `signature` in `thinking`
// block".
func rejectsThinkingSignature(body []byte) bool {
	lowered := bytes.ToLower(body)
	return bytes.Contains(lowered, []byte("signature")) && bytes.Contains(lowered, []byte("thinking"))
}

// stripThinkingSignatures removes every thinking block that carries a
// signature, and every redacted_thinking block, from the messages of a
// Messages request body. The thinking text, tool calls and top-level
// thinking configuration stay, so the retry still asks for new reasoning.
func stripThinkingSignatures(body []byte) ([]byte, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false
	}
	rawMessages, ok := payload["messages"]
	if !ok {
		return body, false
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return body, false
	}

	changed := false
	kept := make([]json.RawMessage, 0, len(messages))
	for _, rawMessage := range messages {
		rewritten, messageChanged, empty := stripMessageThinkingSignatures(rawMessage)
		switch {
		case empty:
			// Nothing model-visible is left, so the message itself goes.
			changed = true
		case messageChanged:
			kept = append(kept, rewritten)
			changed = true
		default:
			kept = append(kept, rawMessage)
		}
	}
	if !changed {
		return body, false
	}
	encodedMessages, err := json.Marshal(kept)
	if err != nil {
		return body, false
	}
	payload["messages"] = encodedMessages
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return encoded, true
}

// stripMessageThinkingSignatures rewrites one message without its signed
// reasoning blocks. It reports whether the message changed and whether its
// content is now empty.
func stripMessageThinkingSignatures(raw json.RawMessage) (json.RawMessage, bool, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return raw, false, false
	}
	rawContent, ok := fields["content"]
	if !ok || len(rawContent) == 0 || rawContent[0] != '[' {
		// A string content carries no blocks.
		return raw, false, false
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(rawContent, &blocks); err != nil {
		return raw, false, false
	}
	kept := make([]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		if carriesThinkingSignature(block) {
			continue
		}
		kept = append(kept, block)
	}
	if len(kept) == len(blocks) {
		return raw, false, false
	}
	if len(kept) == 0 {
		return raw, true, true
	}
	encodedContent, err := json.Marshal(kept)
	if err != nil {
		return raw, false, false
	}
	fields["content"] = encodedContent
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return raw, false, false
	}
	return rewritten, true, false
}

// carriesThinkingSignature reports whether a content block is reasoning the
// provider will only accept from the request that earned its signature.
func carriesThinkingSignature(block json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(block, &fields); err != nil {
		return false
	}
	var blockType string
	if err := json.Unmarshal(fields["type"], &blockType); err != nil {
		return false
	}
	switch blockType {
	case "redacted_thinking":
		return true
	case "thinking":
		_, signed := fields["signature"]
		return signed
	default:
		return false
	}
}
