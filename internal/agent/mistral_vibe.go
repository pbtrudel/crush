package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// mistralVibeUserAgent is the exact User-Agent the Vibe CLI sends to
// api.mistral.ai (see mistralai/mistral-vibe, vibe/utils/http.py). Keep it
// plausible and current: any server-side version checks are unknown, so a
// stale version is a risk.
const mistralVibeUserAgent = "mistral-client-python/Mistral-Vibe/2.26.1"

// newMistralVibeTransport wraps a transport for api.mistral.ai so its
// requests carry the client signature the Mistral Vibe CLI sends, letting a
// subscription-linked ("vibe"-scoped) key draw on the Vibe usage allowance
// instead of looking like generic API traffic.
func newMistralVibeTransport(base http.RoundTripper) http.RoundTripper {
	return mistralVibeTransport{base: base}
}

type mistralVibeTransport struct {
	base http.RoundTripper
}

func (t mistralVibeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = t.signRequest(req)
	return t.base.RoundTrip(req)
}

// signRequest clones the request and applies the Vibe client signature. The
// User-Agent is forced for every request; x-affinity mirrors Crush's own
// x-session-affinity header; the metadata body field is only touched on
// chat-completions requests, matching where the Vibe CLI sends it.
func (t mistralVibeTransport) signRequest(req *http.Request) *http.Request {
	out := req.Clone(req.Context())
	out.Header.Set("User-Agent", mistralVibeUserAgent)
	affinity := req.Header.Get("x-session-affinity")
	if affinity != "" {
		out.Header.Set("x-affinity", affinity)
	}
	if req.Body == nil ||
		!strings.Contains(req.Header.Get("Content-Type"), "application/json") ||
		!strings.Contains(req.URL.Path, "/chat/completions") {
		return out
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return out
	}
	rewritten, changed := addVibeMetadata(body, affinity)
	if !changed {
		rewritten = body
	}
	out.Body = io.NopCloser(bytes.NewReader(rewritten))
	out.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rewritten)), nil
	}
	out.ContentLength = int64(len(rewritten))
	out.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	return out
}

// addVibeMetadata merges the fields the Vibe CLI puts in every completion's
// metadata object. Existing metadata keys are preserved; bodies that do not
// decode as JSON objects are returned unchanged.
func addVibeMetadata(body []byte, affinity string) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var payload map[string]any
	if err := dec.Decode(&payload); err != nil {
		return nil, false
	}
	meta, _ := payload["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["call_source"] = "vibe_code"
	meta["host_kind"] = "local"
	meta["client_name"] = "vibe_cli"
	meta["agent_entrypoint"] = "cli"
	meta["call_type"] = "main_call"
	if affinity != "" {
		meta["session_id"] = affinity
	}
	payload["metadata"] = meta
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	return out, true
}
