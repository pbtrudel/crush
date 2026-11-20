package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// maxResponsesReplayItems bounds the reasoning items one transport keeps.
// A turn rarely runs more than a handful of steps, and each item carries an
// encrypted blob of a few kilobytes, so a small bound is enough and keeps
// the oldest items from sitting in memory for the life of the session.
const maxResponsesReplayItems = 32

// maxResponsesReplayLineBytes bounds one buffered SSE line. A reasoning item
// with its encrypted content fits well under it; anything larger is dropped
// rather than buffered without end.
const maxResponsesReplayLineBytes = 1 << 20

// responsesReasoningReplayEnabled reports whether the transport is installed.
// The config option is the normal switch; the environment variable lets a
// harness turn it on for one run without touching the config file.
func responsesReasoningReplayEnabled(option bool) bool {
	if option {
		return true
	}
	enabled, _ := strconv.ParseBool(os.Getenv("CRUSH_RESPONSES_REASONING_REPLAY"))
	return enabled
}

// newResponsesReasoningReplayTransport wraps base with a transport that
// replays the encrypted reasoning items a Responses endpoint returned earlier
// in the turn. A disabled transport is not installed at all: the caller's
// transport comes back untouched, so requests and responses are byte for byte
// what they were before the feature existed.
func newResponsesReasoningReplayTransport(base http.RoundTripper, enabled bool) http.RoundTripper {
	if !enabled {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &responsesReasoningReplayTransport{base: base, paused: map[replayScope]bool{}}
}

// replayedReasoning is one closed reasoning item and the output item that
// followed it. The follower anchors the item in a later request: it is
// matched by call_id for a function call and by text for an assistant
// message. The prefix pins the conversation that produced it, so a record is
// never handed to a request from another session.
type replayedReasoning struct {
	scope    replayScope
	prefix   conversationPrefix
	item     json.RawMessage
	follower replayFollower
}

// replayScope is the endpoint and model a record belongs to.
type replayScope struct {
	endpoint string
	model    string
}

type replayFollower struct {
	callID string
	text   string
}

// conversationPrefix identifies the request input that produced a record.
// The input only ever grows at the end within a conversation, so a later
// request must start with exactly the same items.
type conversationPrefix struct {
	hash  [sha256.Size]byte
	count int
}

// prefixHashes hashes every prefix of canonical input items, including the
// empty prefix at index 0, so a record's prefix check is a single compare.
func prefixHashes(items [][]byte) [][sha256.Size]byte {
	hashes := make([][sha256.Size]byte, len(items)+1)
	h := sha256.New()
	for i, item := range items {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(item)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(item)
		copy(hashes[i+1][:], h.Sum(nil))
	}
	return hashes
}

// canonicalItems renders each input item with sorted keys and no extra
// whitespace, so equal items compare equal even when the encoder differs.
func canonicalItems(input []json.RawMessage) [][]byte {
	items := make([][]byte, len(input))
	for i, raw := range input {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			items[i] = bytes.Clone(raw)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			items[i] = bytes.Clone(raw)
			continue
		}
		items[i] = encoded
	}
	return items
}

// responsesReasoningReplayTransport records reasoning items from streamed
// Responses responses and inserts them into later requests to the same
// endpoint and model.
type responsesReasoningReplayTransport struct {
	base http.RoundTripper

	mu     sync.Mutex
	items  []replayedReasoning
	paused map[replayScope]bool
}

func (t *responsesReasoningReplayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isResponsesRequest(req) {
		return t.base.RoundTrip(req)
	}

	original, err := readResponsesBody(req)
	if err != nil {
		return nil, err
	}

	input, ok := parseResponsesInput(original)
	if !ok {
		return t.base.RoundTrip(requestWithBody(req, original))
	}
	scope := replayScope{endpoint: responsesEndpoint(req), model: responsesModel(original)}
	if t.pausedFor(scope) {
		return t.base.RoundTrip(requestWithBody(req, original))
	}
	prefix := conversationPrefix{hash: prefixHashes(canonicalItems(input))[len(input)], count: len(input)}

	body, inserted := t.withRecordedItems(original, input, scope)
	// The original body was consumed to inspect it; every request is
	// rebuilt from the bytes we hold.
	sent := requestWithBody(req, body)

	resp, err := t.base.RoundTrip(sent)
	if err != nil {
		return nil, err
	}

	if inserted > 0 {
		if rejected, rejectedBody := replayRejection(resp); rejected {
			t.pauseFor(scope)
			_ = resp.Body.Close()
			slog.Warn("Provider rejected replayed reasoning items, retrying without them",
				"url", req.URL.Redacted(), "status", resp.StatusCode, "body", logBody(rejectedBody))
			// The retry is not wrapped: the scope is paused, so nothing
			// from it would be inserted again.
			return t.base.RoundTrip(requestWithBody(req, original))
		}
	}

	if !t.pausedFor(scope) && isEventStream(resp) {
		resp.Body = &replayRecordingBody{
			body:   resp.Body,
			stream: newReplayStream(t, scope, prefix),
		}
	}
	return resp, nil
}

func (t *responsesReasoningReplayTransport) pausedFor(scope replayScope) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.paused[scope]
}

func (t *responsesReasoningReplayTransport) pauseFor(scope replayScope) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paused[scope] = true
}

// remember stores one item, dropping the oldest ones past the bound.
func (t *responsesReasoningReplayTransport) remember(item replayedReasoning) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.items = append(t.items, item)
	if over := len(t.items) - maxResponsesReplayItems; over > 0 {
		t.items = t.items[over:]
	}
}

func (t *responsesReasoningReplayTransport) recordedItems() []replayedReasoning {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]replayedReasoning(nil), t.items...)
}

// withRecordedItems returns the request body carrying the recorded items in
// front of the item each one followed. Bodies that already hold a reasoning
// item, or that match no recorded item, are returned unchanged.
func (t *responsesReasoningReplayTransport) withRecordedItems(body []byte, input []json.RawMessage, scope replayScope) ([]byte, int) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body, 0
	}

	items := parseReplayInput(input)
	for _, item := range items {
		if item.Type == "reasoning" {
			// A library that replays its own items already sent them.
			return body, 0
		}
	}

	plan := planReasoningInsertions(items, prefixHashes(canonicalItems(input)), t.recordedItems(), scope)
	if len(plan) == 0 {
		return body, 0
	}

	out := make([]json.RawMessage, 0, len(input)+len(plan))
	for i, item := range input {
		for _, insertion := range plan {
			if insertion.index == i {
				out = append(out, insertion.item)
			}
		}
		out = append(out, item)
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return body, 0
	}
	fields["input"] = encoded
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return body, 0
	}
	return rewritten, len(plan)
}

// replayInputItem is the part of an input item the transport matches on.
type replayInputItem struct {
	Type    string          `json:"type"`
	CallID  string          `json:"call_id"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func parseReplayInput(input []json.RawMessage) []replayInputItem {
	items := make([]replayInputItem, len(input))
	for i, raw := range input {
		_ = json.Unmarshal(raw, &items[i])
	}
	return items
}

type reasoningInsertion struct {
	index int
	item  json.RawMessage
}

// planReasoningInsertions decides where each recorded item goes. A record is
// only used by a request from the same conversation, that is one whose input
// starts with the items that produced the record. The plan is in recording
// order and each follower is used once, so two items never land on the same
// anchor.
func planReasoningInsertions(input []replayInputItem, prefixes [][sha256.Size]byte, records []replayedReasoning, scope replayScope) []reasoningInsertion {
	var plan []reasoningInsertion
	used := make(map[int]bool)
	for _, record := range records {
		if record.scope != scope {
			continue
		}
		start := record.prefix.count
		if start >= len(prefixes) || prefixes[start] != record.prefix.hash {
			continue
		}
		index := followerIndex(input, record.follower, used, start)
		if index < 0 {
			continue
		}
		used[index] = true
		plan = append(plan, reasoningInsertion{index: index, item: record.item})
	}
	return plan
}

// followerIndex looks for the anchor after the conversation prefix, so an
// equal assistant text in an earlier turn can never be mistaken for it.
func followerIndex(input []replayInputItem, follower replayFollower, used map[int]bool, start int) int {
	for i := start; i < len(input); i++ {
		item := input[i]
		if used[i] {
			continue
		}
		if follower.callID != "" && item.Type == "function_call" && item.CallID == follower.callID {
			return i
		}
		if follower.text != "" && item.Role == "assistant" && strings.TrimSpace(inputItemText(item.Content)) == follower.text {
			return i
		}
	}
	return -1
}

// inputItemText reads the text of an assistant message input item, whether it
// was written as a plain string or as a list of content parts.
func inputItemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	for _, part := range parts {
		if part.Type == "output_text" || part.Type == "text" {
			return part.Text
		}
	}
	return ""
}

// replayStream reads one streamed Responses response and records its closed
// reasoning items once the response is complete. It accepts both the plain
// SSE events and the envelope GitHub Copilot wraps response-level events in.
type replayStream struct {
	transport *responsesReasoningReplayTransport
	scope     replayScope
	prefix    conversationPrefix

	items    map[int]*replayOutputItem
	buf      []byte
	finished bool
}

type replayOutputItem struct {
	typ       string
	callID    string
	text      string
	encrypted string
	item      json.RawMessage
	closed    bool
}

func newReplayStream(transport *responsesReasoningReplayTransport, scope replayScope, prefix conversationPrefix) *replayStream {
	return &replayStream{
		transport: transport,
		scope:     scope,
		prefix:    prefix,
		items:     map[int]*replayOutputItem{},
	}
}

type replayEvent struct {
	Type        string          `json:"type"`
	OutputIndex *int            `json:"output_index"`
	Item        json.RawMessage `json:"item"`
	Response    *struct {
		Output []json.RawMessage `json:"output"`
	} `json:"response"`
}

func (s *replayStream) write(p []byte) {
	if s.finished {
		return
	}
	s.buf = append(s.buf, p...)
	for {
		index := bytes.IndexByte(s.buf, '\n')
		if index < 0 {
			break
		}
		line := s.buf[:index]
		s.buf = s.buf[index+1:]
		s.observeLine(line)
	}
	if len(s.buf) > maxResponsesReplayLineBytes {
		// One unterminated line this large is unusable; stop buffering.
		s.buf = nil
		s.finished = true
	}
}

func (s *replayStream) observeLine(line []byte) {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(line[len("data:"):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}

	var event replayEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}

	// Copilot wraps response-level events in {"response": {...}}; the
	// completed response repeats the full output.
	if event.Response != nil && len(event.Response.Output) > 0 {
		for i, item := range event.Response.Output {
			s.setItem(i, item, true)
		}
	}
	if len(event.Item) > 0 && event.OutputIndex != nil {
		s.setItem(*event.OutputIndex, event.Item, event.Type == "response.output_item.done")
	}

	switch event.Type {
	case "response.completed", "response.incomplete", "response.failed":
		s.finish()
	}
}

func (s *replayStream) setItem(index int, raw json.RawMessage, closed bool) {
	var item struct {
		Type             string `json:"type"`
		CallID           string `json:"call_id"`
		EncryptedContent string `json:"encrypted_content"`
		Content          []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return
	}

	out, ok := s.items[index]
	if !ok {
		out = &replayOutputItem{}
		s.items[index] = out
	}
	if item.Type != "" {
		out.typ = item.Type
	}
	if item.CallID != "" {
		out.callID = item.CallID
	}
	for _, part := range item.Content {
		if (part.Type == "output_text" || part.Type == "text") && part.Text != "" {
			out.text = part.Text
			break
		}
	}
	if out.typ == "reasoning" {
		out.item = append(json.RawMessage(nil), raw...)
	}
	if closed {
		out.closed = true
		// Only the closing event's encrypted content is kept: the
		// opening event's copy is shorter and unused.
		if item.EncryptedContent != "" {
			out.encrypted = item.EncryptedContent
		}
	}
}

// finish records the closed reasoning items of the finished response, each
// with the first anchorable item that followed it.
func (s *replayStream) finish() {
	if s.finished {
		return
	}
	s.finished = true

	indices := make([]int, 0, len(s.items))
	for index := range s.items {
		indices = append(indices, index)
	}
	sort.Ints(indices)

	for position, index := range indices {
		out := s.items[index]
		if !out.closed || out.typ != "reasoning" || out.encrypted == "" {
			continue
		}
		follower, ok := followingFollower(indices[position+1:], s.items)
		if !ok {
			// Nothing to anchor it to: reasoning behind a final answer
			// is only replayed once the message store carries it.
			continue
		}
		s.transport.remember(replayedReasoning{
			scope:    s.scope,
			prefix:   s.prefix,
			item:     reasoningItemJSON(out.item, out.encrypted),
			follower: follower,
		})
	}
}

// followingFollower walks the output items after a reasoning item and returns
// the first one a request can be anchored on.
func followingFollower(rest []int, items map[int]*replayOutputItem) (replayFollower, bool) {
	for _, index := range rest {
		out := items[index]
		switch {
		case out.typ == "function_call" && out.callID != "":
			return replayFollower{callID: out.callID}, true
		case out.typ == "message" && strings.TrimSpace(out.text) != "":
			return replayFollower{text: strings.TrimSpace(out.text)}, true
		}
	}
	return replayFollower{}, false
}

// reasoningItemJSON trims a closed reasoning item to the fields the input
// array accepts and pins the closing encrypted content. The output item's
// content is dropped: it is not an input field, and the summary is always
// present because the API expects it.
func reasoningItemJSON(raw json.RawMessage, encrypted string) json.RawMessage {
	var original struct {
		ID      string            `json:"id"`
		Summary []json.RawMessage `json:"summary"`
	}
	_ = json.Unmarshal(raw, &original)

	if original.Summary == nil {
		original.Summary = []json.RawMessage{}
	}
	trimmed := map[string]any{
		"type":              "reasoning",
		"encrypted_content": encrypted,
		"summary":           original.Summary,
	}
	if original.ID != "" {
		trimmed["id"] = original.ID
	}
	encoded, err := json.Marshal(trimmed)
	if err != nil {
		return raw
	}
	return encoded
}

// replayRecordingBody records the streamed response as it is read. Close
// finishes the stream too, in case the consumer stops before EOF.
type replayRecordingBody struct {
	body   io.ReadCloser
	stream *replayStream
}

func (b *replayRecordingBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.stream.write(p[:n])
	}
	if err == io.EOF {
		b.stream.finish()
	}
	return n, err
}

func (b *replayRecordingBody) Close() error {
	b.stream.finish()
	return b.body.Close()
}

// isResponsesRequest reports whether the request targets a Responses
// endpoint. Chat-completions traffic from the same provider passes through.
func isResponsesRequest(req *http.Request) bool {
	if req == nil || req.Method != http.MethodPost || req.Body == nil || req.Body == http.NoBody {
		return false
	}
	return strings.HasSuffix(strings.TrimSuffix(req.URL.Path, "/"), "/responses")
}

func responsesEndpoint(req *http.Request) string {
	return req.URL.Scheme + "://" + req.URL.Host + req.URL.Path
}

// parseResponsesInput returns the request's input items. A body without a
// usable input array is not touched.
func parseResponsesInput(body []byte) ([]json.RawMessage, bool) {
	var request struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, false
	}
	var input []json.RawMessage
	if err := json.Unmarshal(request.Input, &input); err != nil {
		return nil, false
	}
	return input, true
}

func responsesModel(body []byte) string {
	var request struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &request)
	return request.Model
}

func isEventStream(resp *http.Response) bool {
	return resp.StatusCode == http.StatusOK &&
		strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
}

// replayRejection reads a 400 body to see whether the provider rejected the
// replayed items specifically. The body is restored for the caller. Only a
// 400 qualifies: 429 and 5xx are transient and say nothing about replay.
func replayRejection(resp *http.Response) (bool, []byte) {
	if resp.StatusCode != http.StatusBadRequest || resp.Body == nil {
		return false, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return false, nil
	}
	resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), resp.Body))
	return namesReplayFailure(body), body
}

// namesReplayFailure recognises the wording of a rejected replay: an item id,
// a reasoning item, a missing required follower, encrypted content, or an
// error param pointing into the input array. A bare "reasoning" or "item" is
// not enough: invalid reasoning effort and tools errors mention both.
func namesReplayFailure(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, marker := range []string{"rs_", "reasoning item", "required following item", "encrypted_content"} {
		if strings.Contains(text, marker) {
			return true
		}
	}

	var payload struct {
		Error struct {
			Param string `json:"param"`
		} `json:"error"`
		Param string `json:"param"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	param := payload.Error.Param
	if param == "" {
		param = payload.Param
	}
	return strings.HasPrefix(strings.ToLower(param), "input")
}

// logBody trims an error body to the 300 bytes a log line should carry.
func logBody(body []byte) string {
	const limit = 300
	if len(body) > limit {
		return strings.TrimSpace(string(body[:limit])) + "..."
	}
	return strings.TrimSpace(string(body))
}

func readResponsesBody(req *http.Request) ([]byte, error) {
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading responses request body: %w", err)
	}
	return body, nil
}

func requestWithBody(req *http.Request, body []byte) *http.Request {
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	clone.TransferEncoding = nil
	return clone
}

// withResponsesReasoningReplay installs the replay transport on a provider
// client when the option or its environment override asks for it. A provider
// that had no client of its own gets one, so the transport has somewhere to
// sit.
func (c *coordinator) withResponsesReasoningReplay(client *http.Client) *http.Client {
	if !responsesReasoningReplayEnabled(c.cfg.Config().Options.ResponsesReasoningReplay) {
		return client
	}
	if client == nil {
		client = &http.Client{}
	}
	client.Transport = newResponsesReasoningReplayTransport(client.Transport, true)
	return client
}
