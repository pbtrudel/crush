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
	rejected, rejectedBody := promptCacheKeyRejection(resp)
	if !rejected {
		return resp, nil
	}
	_ = resp.Body.Close()
	if rejectPromptCacheKey(scope) {
		slog.Info(
			"Endpoint rejected prompt_cache_key, retrying without it",
			"url", req.URL.Redacted(),
			"model", scope.model,
			"body", logBody(rejectedBody),
		)
	}
	return t.base.RoundTrip(requestWithBody(req, stripPromptCacheKey(body)))
}

// promptCacheKeyRejection reports whether a response is a 400 naming the
// prompt cache key field. Other statuses pass through untouched, so a
// streaming response body is never read here. The body is restored for the
// caller.
func promptCacheKeyRejection(resp *http.Response) (bool, []byte) {
	if resp.StatusCode != http.StatusBadRequest || resp.Body == nil {
		return false, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return false, nil
	}
	resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), resp.Body))
	return bytes.Contains(bytes.ToLower(body), []byte(promptCacheKeyField)), body
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
