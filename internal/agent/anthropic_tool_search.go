package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
)

// Anthropic's server-side tool search lets a request define tools with
// defer_loading set, so their schemas stay out of the cached prompt prefix
// and load only when the model searches for them. The promise this
// implementation buys is the one Crush cares about: the tools array of a
// session stops changing when a tool is revealed or an MCP server connects,
// because the deferred half sits after the cache breakpoint and every change
// lands there.
const (
	// anthropicToolSearchToolName is the server tool the model calls to
	// search the deferred palette.
	anthropicToolSearchToolName = "tool_search_tool_bm25"
	// anthropicToolSearchToolType is the versioned definition that goes in
	// the request's tools array.
	anthropicToolSearchToolType = "tool_search_tool_bm25_20251119"
	// anthropicToolSearchBeta is the beta flag the server tool needs.
	anthropicToolSearchBeta = "advanced-tool-use-2025-11-20"

	// anthropicToolSearchResultLimit bounds the result blocks one transport
	// holds. A turn rarely searches more than a few times, and each block
	// is small, so the bound only exists to keep a long session's memory
	// flat.
	anthropicToolSearchResultLimit = 128
	// anthropicToolSearchLineLimit bounds one buffered SSE line. A result
	// block is far smaller; anything larger is dropped rather than
	// buffered without end.
	anthropicToolSearchLineLimit = 1 << 20
)

// anthropicToolSearchServerTool is the definition inserted after the
// non-deferred tools. It is written as bytes because fantasy has no field for
// a server tool and the bytes have to be stable across requests.
var anthropicToolSearchServerTool = []byte(
	`{"type":"` + anthropicToolSearchToolType + `","name":"` + anthropicToolSearchToolName + `"}`,
)

// anthropicToolSearchContextKey marks a request whose tools block carries
// deferred tools. The value travels on the request context: fantasy passes
// the step context into the provider, and the provider's HTTP client hands it
// to the transport, which is the only layer that can rewrite the tools array
// fantasy serialized.
type anthropicToolSearchContextKey struct{}

// withAnthropicToolSearch marks the context of a request that should use
// Anthropic's server-side tool search.
func withAnthropicToolSearch(ctx context.Context) context.Context {
	return context.WithValue(ctx, anthropicToolSearchContextKey{}, true)
}

// anthropicToolSearchRequested reports whether a request carries the marker.
func anthropicToolSearchRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(anthropicToolSearchContextKey{}).(bool)
	return requested
}

// anthropicToolSearchActive reports whether the mode may run for one model.
//
// It is opt-in twice: the option has to be on and lazy tools mode has to be
// on, since deferring a palette is the whole point. It is Anthropic-direct
// only: the markers and the beta header are Anthropic's, and a gateway,
// Bedrock, Vertex, or a proxy that speaks the same wire format does not
// necessarily honour them.
func anthropicToolSearchActive(options *config.Options, servingProvider, baseURL string) bool {
	if options == nil || !options.GetAnthropicToolSearch() || !options.GetLazyTools() {
		return false
	}
	if servingProvider != anthropic.Name {
		return false
	}
	if baseURL == "" {
		// The provider's default endpoint is Anthropic's own.
		return true
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), "api.anthropic.com")
}

// anthropicToolSearchForModel resolves the mode for the model a turn runs on,
// reading the provider's base URL the same way the rest of the coordinator
// does.
func (a *sessionAgent) anthropicToolSearchForModel(model Model) bool {
	if a == nil || a.cfg == nil {
		return false
	}
	cfg := a.cfg.Config()
	if cfg == nil || cfg.Providers == nil {
		return false
	}
	providerCfg, ok := cfg.Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return false
	}
	baseURL, err := a.cfg.Resolve(providerCfg.BaseURL)
	if err != nil {
		return false
	}
	return anthropicToolSearchActive(cfg.Options, model.ServingProvider, baseURL)
}

// clientToolCallCount returns how many calls in a message Crush has to run
// itself. A provider-executed call is answered inside the provider's own
// response, so it never has a tool result and must not hold a tail cut open.
func clientToolCallCount(msg message.Message) int {
	count := 0
	for _, call := range msg.ToolCalls() {
		if !call.ProviderExecuted {
			count++
		}
	}
	return count
}

// anthropicToolSearchDeferred reports whether a tool is part of the deferred
// half of the palette. The core palette is the reader's own definition of what
// a turn needs up front.
func anthropicToolSearchDeferred(name string) bool {
	_, core := lazyCoreTools[name]
	return !core
}

// anthropicToolSearchPalette returns the palette a request carries in this
// mode: our own tool_search is gone (the server tool replaces it), the core
// tools keep their order, and the deferred ones follow sorted by name. The
// non-deferred prefix is what the provider caches, so it must not depend on
// which deferred tools exist.
func anthropicToolSearchPalette(list []fantasy.AgentTool) []fantasy.AgentTool {
	core := make([]fantasy.AgentTool, 0, len(list))
	deferred := make([]fantasy.AgentTool, 0, len(list))
	for _, tool := range list {
		name := tool.Info().Name
		if name == tools.ToolSearchToolName {
			continue
		}
		if anthropicToolSearchDeferred(name) {
			deferred = append(deferred, tool)
			continue
		}
		core = append(core, tool)
	}
	slices.SortFunc(deferred, func(a, b fantasy.AgentTool) int {
		return strings.Compare(a.Info().Name, b.Info().Name)
	})
	return append(core, deferred...)
}

// anthropicToolSearchTransport rewrites Anthropic requests that carry the
// marker and records the tool search results the stream returns, so they can
// be replayed.
//
// A transport without a marked request passes it through untouched, which is
// what keeps the feature byte-identical while the option is off.
type anthropicToolSearchTransport struct {
	base http.RoundTripper

	mu      sync.Mutex
	results map[string]json.RawMessage
	order   []string
}

// newAnthropicToolSearchTransport wraps base with the tool search rewrite.
func newAnthropicToolSearchTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &anthropicToolSearchTransport{
		base:    base,
		results: map[string]json.RawMessage{},
	}
}

// RoundTrip rewrites a marked messages request, then watches the response
// stream for tool search results.
func (t *anthropicToolSearchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isAnthropicToolSearchRequest(req) || !anthropicToolSearchRequested(req.Context()) {
		return t.base.RoundTrip(req)
	}

	original, encoding, err := readRequestBody(req)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's own body failed to read
	}
	rewritten := t.rewriteRequest(original)
	sent := requestWithBody(req, rewritten, encoding)
	appendAnthropicToolSearchBeta(sent.Header)

	resp, err := t.base.RoundTrip(sent)
	if err != nil {
		return nil, err //nolint:wrapcheck // transport errors belong to the caller
	}
	if isEventStream(resp) && resp.Body != nil {
		resp.Body = &anthropicToolSearchBody{body: resp.Body, transport: t}
	}
	return resp, nil
}

// isAnthropicToolSearchRequest reports whether a request targets the messages
// endpoint. Anything else on the same provider passes through.
func isAnthropicToolSearchRequest(req *http.Request) bool {
	if req == nil || req.Method != http.MethodPost || req.Body == nil || req.Body == http.NoBody {
		return false
	}
	return strings.HasSuffix(strings.TrimSuffix(req.URL.Path, "/"), "/v1/messages")
}

// appendAnthropicToolSearchBeta adds the server tool's beta flag to the
// request, keeping whatever beta flags the SDK already set.
func appendAnthropicToolSearchBeta(header http.Header) {
	existing := header.Get("anthropic-beta")
	for _, flag := range strings.Split(existing, ",") {
		if strings.TrimSpace(flag) == anthropicToolSearchBeta {
			return
		}
	}
	joined := anthropicToolSearchBeta
	if strings.TrimSpace(existing) != "" {
		joined = existing + "," + anthropicToolSearchBeta
	}
	header.Set("anthropic-beta", joined)
}

// rewriteRequest applies the deferred-tool layout to one request body. A body
// that does not decode, or that carries no tools, comes back unchanged.
func (t *anthropicToolSearchTransport) rewriteRequest(body []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body
	}
	rawTools, ok := fields["tools"]
	if !ok {
		return body
	}
	var toolList []json.RawMessage
	if err := json.Unmarshal(rawTools, &toolList); err != nil || len(toolList) == 0 {
		return body
	}

	messages, orphaned, used := t.rewriteMessages(fields["messages"])
	if messages != nil {
		fields["messages"] = messages
	}
	fields["tools"] = t.rewriteTools(toolList, orphaned, used)

	rewritten, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return rewritten
}

// rewriteTools splits the palette into the cached non-deferred half and the
// deferred tail, inserts the server tool between them, and moves any
// breakpoint off a deferred definition.
func (t *anthropicToolSearchTransport) rewriteTools(list []json.RawMessage, undeferUsed bool, used map[string]struct{}) json.RawMessage {
	core := make([]json.RawMessage, 0, len(list))
	deferred := make([]json.RawMessage, 0, len(list))
	movedBreakpoint := false

	for _, raw := range list {
		var tool struct {
			Name         string          `json:"name"`
			Type         string          `json:"type"`
			CacheControl json.RawMessage `json:"cache_control"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			core = append(core, raw)
			continue
		}
		// A request that already carries the server tool is left alone: a
		// second copy is a provider error.
		if tool.Name == anthropicToolSearchToolName || tool.Type == anthropicToolSearchToolType {
			continue
		}
		_, wasUsed := used[tool.Name]
		switch {
		case !anthropicToolSearchDeferred(tool.Name):
			// A core tool is already loaded; its bytes stay exactly as
			// fantasy wrote them.
			core = append(core, raw)
		case undeferUsed && wasUsed:
			// A tool the history already called has to be loaded, or the
			// provider rejects the request.
			core = append(core, rewriteToolJSON(raw, func(fields map[string]json.RawMessage) {
				delete(fields, "defer_loading")
			}))
		default:
			if len(tool.CacheControl) > 0 {
				movedBreakpoint = true
			}
			deferred = append(deferred, rewriteToolJSON(raw, func(fields map[string]json.RawMessage) {
				fields["defer_loading"] = json.RawMessage(`true`)
				delete(fields, "cache_control")
			}))
		}
	}

	if movedBreakpoint && len(core) > 0 {
		core[len(core)-1] = rewriteToolJSON(core[len(core)-1], func(fields map[string]json.RawMessage) {
			fields["cache_control"] = json.RawMessage(`{"type":"ephemeral"}`)
		})
	}

	out := make([]json.RawMessage, 0, len(core)+1+len(deferred))
	out = append(out, core...)
	out = append(out, anthropicToolSearchServerTool)
	out = append(out, deferred...)
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return encoded
}

// rewriteToolJSON applies one change to a tool definition through a map, so
// every field this code does not know about survives the rewrite.
func rewriteToolJSON(raw json.RawMessage, change func(map[string]json.RawMessage)) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return raw
	}
	change(fields)
	encoded, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return encoded
}

// rewriteMessages re-inserts the recorded tool search results and removes a
// server_tool_use the transport has no result for. It reports whether any
// orphan was removed and which tools the history called.
func (t *anthropicToolSearchTransport) rewriteMessages(raw json.RawMessage) (json.RawMessage, bool, map[string]struct{}) {
	used := map[string]struct{}{}
	if len(raw) == 0 {
		return nil, false, used
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, false, used
	}

	recorded := t.recordedResults()
	orphaned := false
	changed := false
	out := make([]json.RawMessage, 0, len(messages))

	for _, rawMessage := range messages {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			out = append(out, rawMessage)
			continue
		}
		var role string
		_ = json.Unmarshal(message["role"], &role)
		var content []json.RawMessage
		if err := json.Unmarshal(message["content"], &content); err != nil {
			// A plain string content cannot carry a tool block.
			out = append(out, rawMessage)
			continue
		}

		messageOrphaned := false
		var blocks []json.RawMessage
		for _, rawBlock := range content {
			var block struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				ToolUseID string `json:"tool_use_id"`
			}
			_ = json.Unmarshal(rawBlock, &block)
			switch block.Type {
			case "tool_use":
				used[block.Name] = struct{}{}
				blocks = append(blocks, rawBlock)
			case "server_tool_use":
				result, ok := recorded[block.ID]
				alreadyPresent := messageHasResult(content, block.ID)
				if block.Name != anthropicToolSearchToolName || (!ok && !alreadyPresent) {
					if block.Name == anthropicToolSearchToolName {
						// A restart drops the transport's records while the
						// session log keeps the call. An unanswered
						// server_tool_use makes the request invalid.
						messageOrphaned = true
						changed = true
						continue
					}
					blocks = append(blocks, rawBlock)
					continue
				}
				blocks = append(blocks, rawBlock)
				if ok && !alreadyPresent {
					blocks = append(blocks, result)
					changed = true
				}
			default:
				blocks = append(blocks, rawBlock)
			}
		}
		if messageOrphaned {
			orphaned = true
		}
		if len(blocks) == len(content) {
			out = append(out, rawMessage)
			continue
		}
		if len(blocks) == 0 && role == "assistant" {
			// The message held nothing but the orphan.
			continue
		}
		encodedBlocks, err := json.Marshal(blocks)
		if err != nil {
			out = append(out, rawMessage)
			continue
		}
		message["content"] = encodedBlocks
		encodedMessage, err := json.Marshal(message)
		if err != nil {
			out = append(out, rawMessage)
			continue
		}
		out = append(out, encodedMessage)
	}

	if !changed {
		return nil, orphaned, used
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, orphaned, used
	}
	return encoded, orphaned, used
}

// messageHasResult reports whether one assistant message already carries the
// tool search result for a call.
func messageHasResult(content []json.RawMessage, toolUseID string) bool {
	for _, rawBlock := range content {
		var block struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			continue
		}
		if block.Type == "tool_search_tool_result" && block.ToolUseID == toolUseID {
			return true
		}
	}
	return false
}

// recordedResults returns a copy of the captured result blocks.
func (t *anthropicToolSearchTransport) recordedResults() map[string]json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]json.RawMessage, len(t.results))
	for id, block := range t.results {
		out[id] = block
	}
	return out
}

// remember stores one result block, dropping the oldest past the bound.
func (t *anthropicToolSearchTransport) remember(toolUseID string, block json.RawMessage) {
	if toolUseID == "" || len(block) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.results[toolUseID]; !ok {
		t.order = append(t.order, toolUseID)
	}
	t.results[toolUseID] = append(json.RawMessage(nil), block...)
	for len(t.order) > anthropicToolSearchResultLimit {
		delete(t.results, t.order[0])
		t.order = t.order[1:]
	}
}

// anthropicToolSearchBody observes a streamed response as it is read and
// records every tool search result block. The bytes are forwarded unchanged.
type anthropicToolSearchBody struct {
	body      io.ReadCloser
	transport *anthropicToolSearchTransport
	buf       []byte
	skipping  bool
}

func (b *anthropicToolSearchBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.observe(p[:n])
	}
	return n, err //nolint:wrapcheck // the caller reads the provider's stream
}

func (b *anthropicToolSearchBody) Close() error {
	return b.body.Close() //nolint:wrapcheck // closing the provider's stream is its own error
}

// observe records complete SSE lines as they pass through.
func (b *anthropicToolSearchBody) observe(p []byte) {
	b.buf = append(b.buf, p...)
	for {
		index := bytes.IndexByte(b.buf, '\n')
		if index < 0 {
			break
		}
		line := b.buf[:index]
		b.buf = b.buf[index+1:]
		if !b.skipping {
			b.observeLine(line)
		}
		b.skipping = false
	}
	if len(b.buf) > anthropicToolSearchLineLimit {
		b.buf = nil
		b.skipping = true
	}
}

// observeLine records one `content_block_start` carrying a tool search
// result.
func (b *anthropicToolSearchBody) observeLine(line []byte) {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("data:")) || !bytes.Contains(line, []byte("tool_search_tool_result")) {
		return
	}
	payload := bytes.TrimSpace(line[len("data:"):])
	var event struct {
		Type         string          `json:"type"`
		ContentBlock json.RawMessage `json:"content_block"`
	}
	if err := json.Unmarshal(payload, &event); err != nil || event.Type != "content_block_start" {
		return
	}
	var block struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
	}
	if err := json.Unmarshal(event.ContentBlock, &block); err != nil {
		return
	}
	if block.Type != "tool_search_tool_result" || block.ToolUseID == "" {
		return
	}
	b.transport.remember(block.ToolUseID, event.ContentBlock)
}
