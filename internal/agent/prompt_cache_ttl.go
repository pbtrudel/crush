package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/log"
)

// promptCacheTTLBeta enables the extended prompt cache lifetime on the
// Anthropic API.
const promptCacheTTLBeta = "extended-cache-ttl-2025-04-11"

// promptCacheTTLExtendedTTL is the cache_control ttl that asks for the
// one-hour lifetime.
const promptCacheTTLExtendedTTL = "1h"

// promptCacheTTLHeader is the request header that carries Anthropic beta
// feature tokens.
const promptCacheTTLHeader = "anthropic-beta"

// promptCacheRetentionField is the OpenAI request field that selects how long
// a cached prompt prefix is retained.
const promptCacheRetentionField = "prompt_cache_retention"

// promptCacheRetentionValue asks OpenAI for its longest prompt cache
// retention window.
const promptCacheRetentionValue = "24h"

// promptCacheTTLRejections holds the "endpoint\x00model" scopes that rejected
// the extended cache ttl, for the life of the process. Providers are rebuilt
// often, so the record cannot live on one transport. A gateway in front of
// several models can reject the extension for one of them and accept it for
// another, so the model is part of the scope.
var promptCacheTTLRejections sync.Map

// newPromptCacheTTLTransport wraps base with a transport that asks the
// Anthropic API for the extended prompt-cache lifetime: every ephemeral
// cache_control block gains "ttl":"1h" and the request carries the beta
// header that enables the longer lifetime. An endpoint that rejects the
// extension is retried once with the original request and then left alone.
func newPromptCacheTTLTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &promptCacheTTLTransport{base: base}
}

type promptCacheTTLTransport struct {
	base http.RoundTripper
}

func (t *promptCacheTTLTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.Body == nil || req.Body == http.NoBody {
		return t.base.RoundTrip(req)
	}
	body, encoding, err := readRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("reading prompt cache ttl request body: %w", err)
	}
	extended, changed := withExtendedCacheTTL(body)
	if !changed {
		// Nothing to rewrite, so the request keeps its original bytes and
		// headers; there is no reason to advertise the beta either.
		return t.base.RoundTrip(requestWithBody(req, body, encoding))
	}
	model := responsesModel(body)
	scope := responsesEndpoint(req) + "\x00" + model
	if _, rejected := promptCacheTTLRejections.Load(scope); rejected {
		return t.base.RoundTrip(requestWithBody(req, body, encoding))
	}
	clone := requestWithBody(req, extended, encoding)
	addPromptCacheTTLBeta(clone.Header)

	resp, err := t.base.RoundTrip(clone)
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
	if err != nil || !bytes.Contains(bytes.ToLower(rejection), []byte("ttl")) {
		// Hand the rejection on with the bytes already read, and let the
		// caller's Close reach the original body through this one.
		resp.Body = &bufferedBody{
			Reader:   io.MultiReader(bytes.NewReader(rejection), original),
			original: original,
		}
		return resp, nil
	}
	// The endpoint rejected the extension. Drain and close the response so
	// its connection is reused, then retry once with the original body and
	// headers, without the rewrite.
	_, _ = io.Copy(io.Discard, original)
	_ = original.Close()
	if _, loaded := promptCacheTTLRejections.LoadOrStore(scope, struct{}{}); !loaded {
		slog.Info(
			"Endpoint rejected the extended prompt cache ttl, retrying without it",
			"url", req.URL.Redacted(),
			"model", model,
			"body", logBody(rejection),
		)
	}
	return t.base.RoundTrip(requestWithBody(req, body, encoding))
}

// withExtendedCacheTTL adds the extended ttl to every ephemeral cache_control
// block in an Anthropic request body. It reports false and returns the
// original bytes when the body carries no such block, so a request with
// nothing to change reaches the wire byte for byte.
func withExtendedCacheTTL(body []byte) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return body, false
	}
	object, ok := root.(map[string]any)
	if !ok {
		return body, false
	}
	if _, ok := object["messages"]; !ok {
		if _, ok := object["system"]; !ok {
			return body, false
		}
	}
	if !addExtendedTTL(root) {
		return body, false
	}
	rewritten, err := json.Marshal(root)
	if err != nil {
		return body, false
	}
	return rewritten, true
}

// addExtendedTTL walks a decoded request body and adds the extended ttl to
// every ephemeral cache_control block that does not set one. It reports
// whether anything changed.
func addExtendedTTL(value any) bool {
	changed := false
	switch node := value.(type) {
	case map[string]any:
		if control, ok := node["cache_control"].(map[string]any); ok && addExtendedCacheControlTTL(control) {
			changed = true
		}
		for _, child := range node {
			if addExtendedTTL(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range node {
			if addExtendedTTL(child) {
				changed = true
			}
		}
	}
	return changed
}

// addExtendedCacheControlTTL adds the extended ttl to one cache_control
// object when it is ephemeral and sets none. Other cache_control types and
// blocks that already chose a ttl are left alone. It reports whether the
// block changed.
func addExtendedCacheControlTTL(control map[string]any) bool {
	if _, ok := control["ttl"]; ok {
		return false
	}
	if kind, ok := control["type"].(string); !ok || kind != "ephemeral" {
		return false
	}
	control["ttl"] = promptCacheTTLExtendedTTL
	return true
}

// addPromptCacheTTLBeta appends the extended cache TTL beta token to the
// anthropic-beta header unless the request already asks for it.
func addPromptCacheTTLBeta(header http.Header) {
	values := header.Values(promptCacheTTLHeader)
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), promptCacheTTLBeta) {
				return
			}
		}
	}
	existing := strings.Join(values, ",")
	if existing == "" {
		header.Set(promptCacheTTLHeader, promptCacheTTLBeta)
		return
	}
	header.Set(promptCacheTTLHeader, existing+","+promptCacheTTLBeta)
}

// promptCacheRetentionRejections holds the "endpoint\x00model" scopes that
// rejected prompt_cache_retention, for the life of the process. Providers are
// rebuilt often, so the record cannot live on one transport. A gateway in
// front of several models can reject the field for one of them and accept it
// for another, so the model is part of the scope.
var promptCacheRetentionRejections sync.Map

// newPromptCacheRetentionTransport wraps base with a transport that adds
// OpenAI's longest prompt cache retention window to every prompt request and
// retries once without it when the endpoint rejects the field.
func newPromptCacheRetentionTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &promptCacheRetentionTransport{base: base}
}

type promptCacheRetentionTransport struct {
	base http.RoundTripper
}

func (t *promptCacheRetentionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.Body == nil || req.Body == http.NoBody {
		return t.base.RoundTrip(req)
	}
	body, encoding, err := readRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("reading prompt cache retention request body: %w", err)
	}
	model := responsesModel(body)
	scope := responsesEndpoint(req) + "\x00" + model
	if _, rejected := promptCacheRetentionRejections.Load(scope); rejected {
		return t.base.RoundTrip(requestWithBody(req, body, encoding))
	}
	body = addPromptCacheRetention(body)

	resp, err := t.base.RoundTrip(requestWithBody(req, body, encoding))
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
	if err != nil || !bytes.Contains(bytes.ToLower(rejection), []byte(promptCacheRetentionField)) {
		// Hand the rejection on with the bytes already read, and let the
		// caller's Close reach the original body through this one.
		resp.Body = &bufferedBody{
			Reader:   io.MultiReader(bytes.NewReader(rejection), original),
			original: original,
		}
		return resp, nil
	}
	// The endpoint rejected the field. Drain and close the response so its
	// connection is reused, then retry once without the field.
	_, _ = io.Copy(io.Discard, original)
	_ = original.Close()
	if _, loaded := promptCacheRetentionRejections.LoadOrStore(scope, struct{}{}); !loaded {
		slog.Info(
			"Endpoint rejected prompt_cache_retention, retrying without it",
			"url", req.URL.Redacted(),
			"model", model,
			"body", logBody(rejection),
		)
	}
	return t.base.RoundTrip(requestWithBody(req, stripPromptCacheRetention(body), encoding))
}

// addPromptCacheRetention adds the retention hint to a request body that
// carries a prompt and does not set one already. Bodies that do not decode as
// a JSON object, or that carry neither messages nor input, are returned
// unchanged.
func addPromptCacheRetention(body []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body
	}
	if _, ok := fields[promptCacheRetentionField]; ok {
		return body
	}
	if _, ok := fields["messages"]; !ok {
		if _, ok := fields["input"]; !ok {
			return body
		}
	}
	fields[promptCacheRetentionField] = json.RawMessage(strconv.Quote(promptCacheRetentionValue))
	updated, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return updated
}

// stripPromptCacheRetention removes the retention hint from a request body.
// Bodies that do not decode as a JSON object are returned unchanged.
func stripPromptCacheRetention(body []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body
	}
	if _, ok := fields[promptCacheRetentionField]; !ok {
		return body
	}
	delete(fields, promptCacheRetentionField)
	stripped, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return stripped
}

// withPromptCacheRetention wraps client with the retention transport. A
// provider that had no client of its own gets one, so the transport has
// somewhere to sit.
func withPromptCacheRetention(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = newPromptCacheRetentionTransport(base)
	return client
}

// anthropicHTTPClient builds the HTTP client the Anthropic provider sends
// requests with, or nil when the SDK's default client is enough. Debug
// logging needs a client of its own, and so does the extended prompt cache
// lifetime, but only for Anthropic itself: every other Messages-wire provider
// may reject the ttl field and the beta header it needs.
func (c *coordinator) anthropicHTTPClient(providerID, baseURL string) *http.Client {
	var client *http.Client
	if c.cfg.Config().Options.Debug {
		client = log.NewHTTPClient()
	}
	if !isAnthropicEndpoint(providerID, baseURL) {
		return client
	}
	return c.withPromptCacheTTL(client)
}

// isAnthropicEndpoint reports whether a Messages-wire provider talks to
// Anthropic itself, the only API known to accept the extended cache ttl and
// its beta header. An empty base URL means the SDK's default Anthropic
// endpoint.
func isAnthropicEndpoint(providerID, baseURL string) bool {
	if providerID == string(catwalk.InferenceProviderAnthropic) {
		return true
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return true
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "api.anthropic.com")
}

// withPromptCacheTTL wraps client with the extended cache TTL transport when
// the prompt_cache_ttl option asks for the one-hour lifetime. The client
// comes back untouched otherwise, so the default lifetime sends exactly the
// bytes it always has.
func (c *coordinator) withPromptCacheTTL(client *http.Client) *http.Client {
	if c.cfg.Config().Options.GetPromptCacheTTL() != config.PromptCacheTTLExtended {
		return client
	}
	if client == nil {
		client = &http.Client{}
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = newPromptCacheTTLTransport(base)
	return client
}

// withOpenAIPromptCacheRetention installs the retention hint on OpenAI's own
// provider only, and only for the extended lifetime. Gateways serve Responses
// models too but may reject fields they do not know, so they keep the
// default retention.
func (c *coordinator) withOpenAIPromptCacheRetention(client *http.Client, providerID string) *http.Client {
	if providerID != string(catwalk.InferenceProviderOpenAI) {
		return client
	}
	if c.cfg.Config().Options.GetPromptCacheTTL() != config.PromptCacheTTLExtended {
		return client
	}
	return withPromptCacheRetention(client)
}
