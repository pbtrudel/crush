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

// promptCacheKeyRejections holds the "endpoint\x00model" scopes that rejected
// prompt_cache_key, for the life of the process. Providers are rebuilt often,
// so the record cannot live on one transport. A gateway in front of several
// models can reject the field for one of them and accept it for another, so
// the model is part of the scope.
var promptCacheKeyRejections sync.Map

// withPromptCacheKeyRetry wraps the client with a transport that retries a
// request once without its prompt cache key when the endpoint rejects the
// field, then stops sending it to that endpoint and model. Dropping the key
// only costs cache routing, so a rejection is never worth failing a turn. A
// provider that had no client of its own gets one, so the transport has
// somewhere to sit.
func withPromptCacheKeyRetry(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &promptCacheKeyTransport{base: base}
	return client
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
	model := responsesModel(body)
	scope := responsesEndpoint(req) + "\x00" + model
	if _, rejected := promptCacheKeyRejections.Load(scope); rejected {
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
	if _, loaded := promptCacheKeyRejections.LoadOrStore(scope, struct{}{}); !loaded {
		slog.Info(
			"Endpoint rejected prompt_cache_key, retrying without it",
			"url", req.URL.Redacted(),
			"model", model,
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
