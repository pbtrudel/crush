package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
)

// promptCacheKeyField is the request field an endpoint may reject.
const promptCacheKeyField = "prompt_cache_key"

// promptCacheKeyScope is the endpoint and model a rejected prompt cache key
// belongs to. A gateway in front of several models can reject the field for
// one of them and accept it for another, so the model is part of the scope.
type promptCacheKeyScope struct {
	endpoint string
	model    string
}

// promptCacheKeyRejections records the scopes that rejected the field for the
// life of the process. Providers are rebuilt often, so the record cannot live
// on one transport.
var promptCacheKeyRejections = struct {
	mu     sync.Mutex
	scopes map[promptCacheKeyScope]bool
}{scopes: map[promptCacheKeyScope]bool{}}

// newPromptCacheKeyTransport wraps base with a transport that retries a
// request once without its prompt cache key when the endpoint rejects the
// field, then stops sending it to that endpoint and model. Dropping the key
// only costs cache routing, so a rejection is never worth failing a turn.
func newPromptCacheKeyTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &promptCacheKeyTransport{base: base}
}

type promptCacheKeyTransport struct {
	base http.RoundTripper
}

func (t *promptCacheKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.Body == nil || req.Body == http.NoBody {
		return t.base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading prompt cache key request body: %w", err)
	}
	if !bytes.Contains(body, []byte(promptCacheKeyField)) {
		// The body was consumed to inspect it, so every request is rebuilt
		// from the bytes held here.
		return t.base.RoundTrip(requestWithBody(req, body))
	}
	scope := promptCacheKeyScope{endpoint: responsesEndpoint(req), model: responsesModel(body)}
	if promptCacheKeyRejected(scope) {
		return t.base.RoundTrip(requestWithBody(req, stripPromptCacheKey(body)))
	}

	resp, err := t.base.RoundTrip(requestWithBody(req, body))
	if err != nil {
		return nil, err
	}
	// Only a 400 is inspected, and only its first 64KiB: a streaming body
	// is never read here.
	if resp.StatusCode != http.StatusBadRequest || resp.Body == nil {
		return resp, nil
	}
	original := resp.Body
	rejection, err := io.ReadAll(io.LimitReader(original, 64<<10))
	if err != nil || !bytes.Contains(bytes.ToLower(rejection), []byte(promptCacheKeyField)) {
		// Hand the rejection on with the bytes already read, and let the
		// caller's Close reach the original body through this one.
		resp.Body = &promptCacheKeyBody{
			Reader:   io.MultiReader(bytes.NewReader(rejection), original),
			original: original,
		}
		return resp, nil
	}
	// The endpoint rejected the field. Drain and close the response so its
	// connection is reused, then retry once without the field.
	_, _ = io.Copy(io.Discard, original)
	_ = original.Close()
	if rejectPromptCacheKey(scope) {
		slog.Info(
			"Endpoint rejected prompt_cache_key, retrying without it",
			"url", req.URL.Redacted(),
			"model", scope.model,
			"body", logBody(rejection),
		)
	}
	return t.base.RoundTrip(requestWithBody(req, stripPromptCacheKey(body)))
}

// promptCacheKeyBody replays a buffered response body and closes the original
// body when the caller is done with it.
type promptCacheKeyBody struct {
	io.Reader
	original io.ReadCloser
}

func (b *promptCacheKeyBody) Close() error {
	return b.original.Close()
}

// stripPromptCacheKey removes the prompt cache key from a request body.
// Bodies that do not decode as a JSON object are returned unchanged.
func stripPromptCacheKey(body []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body
	}
	if _, ok := fields[promptCacheKeyField]; !ok {
		return body
	}
	delete(fields, promptCacheKeyField)
	stripped, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return stripped
}

// promptCacheKeyRejected reports whether an endpoint and model already
// rejected the field.
func promptCacheKeyRejected(scope promptCacheKeyScope) bool {
	promptCacheKeyRejections.mu.Lock()
	defer promptCacheKeyRejections.mu.Unlock()
	return promptCacheKeyRejections.scopes[scope]
}

// rejectPromptCacheKey records a rejection and reports whether it is new, so
// the fallback is logged once per endpoint and model.
func rejectPromptCacheKey(scope promptCacheKeyScope) bool {
	promptCacheKeyRejections.mu.Lock()
	defer promptCacheKeyRejections.mu.Unlock()
	if promptCacheKeyRejections.scopes[scope] {
		return false
	}
	promptCacheKeyRejections.scopes[scope] = true
	return true
}

// withPromptCacheKeyRetry installs the fallback on a provider client. A
// provider that had no client of its own gets one, so the transport has
// somewhere to sit.
func withPromptCacheKeyRetry(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	client.Transport = newPromptCacheKeyTransport(client.Transport)
	return client
}
