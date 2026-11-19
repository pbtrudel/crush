package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// captureDirEnv names the environment variable that turns the provider
// request recorder on. When it is unset no transport is installed, so a
// normal run pays nothing for it.
const captureDirEnv = "CRUSH_CAPTURE_DIR"

// captureBodyLimit caps the bytes stored per request or response body. A
// streamed answer can run past this; the capture keeps its head and marks
// itself truncated.
const captureBodyLimit = 200 * 1024

// captureClient returns client with the recorder installed under its
// transport when CRUSH_CAPTURE_DIR is set, and returns client untouched
// otherwise. A nil client stays nil while capturing is off, so a provider
// that builds its own client keeps doing so; while capturing is on the
// returned client only carries the recorder, which is enough to capture a
// provider that would otherwise use its own transport.
func captureClient(client *http.Client) *http.Client {
	dir := captureDir()
	if dir == "" {
		return client
	}
	if client == nil {
		client = &http.Client{}
	}
	client.Transport = captureTransportIfEnabled(client.Transport)
	return client
}

// captureDir returns the capture directory, or "" when capturing is off.
func captureDir() string {
	return strings.TrimSpace(os.Getenv(captureDirEnv))
}

// captureTransportIfEnabled wraps base in a recorder when CRUSH_CAPTURE_DIR
// names a usable directory, and returns base unchanged otherwise.
func captureTransportIfEnabled(base http.RoundTripper) http.RoundTripper {
	dir := captureDir()
	if dir == "" {
		return base
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("Provider request capture is disabled", "error", err)
		return base
	}
	return &captureTransport{base: base, dir: dir}
}

// captureTransport records every exchange it forwards and writes one JSON
// file per exchange into dir. It never changes the request or the response.
type captureTransport struct {
	base http.RoundTripper
	dir  string
	seq  atomic.Uint64
}

// captureExchange is one recorded request and its response.
type captureExchange struct {
	StartedAt         string            `json:"started_at"`
	DurationMS        int64             `json:"duration_ms"`
	Method            string            `json:"method"`
	URL               string            `json:"url"`
	RequestHeaders    map[string]string `json:"request_headers,omitempty"`
	RequestBody       string            `json:"request_body,omitempty"`
	RequestTruncated  bool              `json:"request_truncated,omitempty"`
	Status            int               `json:"status,omitempty"`
	ResponseHeaders   map[string]string `json:"response_headers,omitempty"`
	ResponseBody      string            `json:"response_body,omitempty"`
	ResponseTruncated bool              `json:"response_truncated,omitempty"`
	Stream            bool              `json:"stream,omitempty"`
	Error             string            `json:"error,omitempty"`
}

// RoundTrip implements http.RoundTripper.
func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	start := time.Now()
	rawBody := captureRequestBody(req)
	requestBody, truncated := recordBody(rawBody)
	exchange := captureExchange{
		StartedAt:        start.UTC().Format(time.RFC3339Nano),
		Method:           req.Method,
		URL:              redactURL(req.URL),
		RequestHeaders:   captureHeaders(req.Header),
		RequestBody:      requestBody,
		RequestTruncated: truncated,
		Stream:           requestStreams(rawBody),
	}

	resp, err := base.RoundTrip(req)
	exchange.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		exchange.Error = redactText(err.Error())
		t.write(exchange)
		return resp, err
	}

	exchange.Status = resp.StatusCode
	exchange.ResponseHeaders = captureHeaders(resp.Header)
	exchange.Stream = exchange.Stream || strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "event-stream")
	if resp.Body == nil || resp.Body == http.NoBody {
		t.write(exchange)
		return resp, nil
	}
	resp.Body = &captureBody{
		ReadCloser: resp.Body,
		done: func(body []byte, cutAtCap bool) {
			exchange.ResponseBody, exchange.ResponseTruncated = recordBody(body)
			exchange.ResponseTruncated = exchange.ResponseTruncated || cutAtCap
			t.write(exchange)
		},
	}
	return resp, nil
}

// write stores one exchange as a JSON file, named so that a directory
// listing reads in the order the exchanges happened.
func (t *captureTransport) write(exchange captureExchange) {
	data, err := json.MarshalIndent(exchange, "", "  ")
	if err != nil {
		slog.Debug("Could not encode a provider request capture", "error", err)
		return
	}
	name := fmt.Sprintf(
		"%s-%03d-%s.json",
		time.Now().UTC().Format("20060102T150405.000000Z"),
		t.seq.Add(1),
		captureURLSlug(exchange.URL),
	)
	if err := os.WriteFile(filepath.Join(t.dir, name), append(data, '\n'), 0o600); err != nil {
		slog.Debug("Could not write a provider request capture", "error", err)
	}
}

// captureURLSlug turns a redacted URL into a short, filename-safe hint at
// the endpoint that was called.
func captureURLSlug(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" {
		return "request"
	}
	slug := strings.Trim(u.Path, "/")
	slug = strings.NewReplacer("/", "_", ".", "-").Replace(slug)
	if len(slug) > 48 {
		slug = slug[len(slug)-48:]
	}
	if slug == "" {
		return "request"
	}
	return slug
}

// captureBody tees a response body into a buffer while the caller reads it,
// and hands the buffer to the recorder once the body is closed or drained.
type captureBody struct {
	io.ReadCloser
	buf       bytes.Buffer
	truncated bool
	once      sync.Once
	done      func([]byte, bool)
}

// Read implements io.Reader.
func (b *captureBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		if b.buf.Len() < captureBodyLimit {
			room := captureBodyLimit - b.buf.Len()
			b.buf.Write(p[:min(n, room)])
			b.truncated = b.truncated || n > room
		} else {
			b.truncated = true
		}
	}
	if err != nil {
		b.finish()
	}
	return n, err
}

// Close implements io.Closer. The capture is written even when the caller
// stops reading early, so a cancelled stream still leaves evidence.
func (b *captureBody) Close() error {
	err := b.ReadCloser.Close()
	b.finish()
	return err
}

func (b *captureBody) finish() {
	b.once.Do(func() {
		if b.done != nil {
			b.done(b.buf.Bytes(), b.truncated)
		}
	})
}

// captureRequestBody returns the whole request body for recording and restores
// it for the real request. The caller caps the stored copy, and it caps it
// after redaction rather than here: a body cut in this function no longer
// parses as JSON, which would skip the field-by-field redaction.
func captureRequestBody(req *http.Request) []byte {
	if req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if req.GetBody != nil {
		if rc, err := req.GetBody(); err == nil {
			data, _ := io.ReadAll(rc)
			_ = rc.Close()
			return data
		}
	}
	data, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.ContentLength = int64(len(data))
	if err != nil {
		return nil
	}
	return data
}

// recordBody redacts a body and caps the stored copy at captureBodyLimit,
// reporting whether the copy was cut. Redaction runs over the whole body
// first, so a body past the cap still loses its secret fields instead of
// being stored raw because the cut copy no longer decodes.
func recordBody(body []byte) (string, bool) {
	if len(body) == 0 {
		return "", false
	}
	redacted := redactBody(body)
	cut := cutBodyAtLimit(redacted)
	return cut, len(cut) != len(redacted)
}

// cutBodyAtLimit cuts a body at the size cap without splitting a UTF-8
// sequence, so the stored string survives JSON re-encoding.
func cutBodyAtLimit(body string) string {
	if len(body) <= captureBodyLimit {
		return body
	}
	cut := body[:captureBodyLimit]
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRuneInString(cut); r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut
}

// captureHeaders keeps only the headers that identify the content type, the
// client and the model. Everything else, credentials above all, is dropped.
func captureHeaders(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string]string, len(header))
	for name, values := range header {
		lower := strings.ToLower(name)
		if lower != "content-type" && lower != "user-agent" && !strings.Contains(lower, "model") {
			continue
		}
		out[lower] = redactText(strings.Join(values, ", "))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// requestStreams reports whether a request body asks for a streamed answer.
func requestStreams(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var payload struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Stream
}

// captureSecretKeys holds the normalized JSON keys whose values are always
// redacted. Matching is exact on the normalized name, so a field such as
// prompt_cache_key keeps its value while an api_key does not.
var captureSecretKeys = map[string]bool{
	"api_key":         true,
	"api-key":         true,
	"apikey":          true,
	"access_token":    true,
	"refresh_token":   true,
	"id_token":        true,
	"session_token":   true,
	"auth":            true,
	"authorization":   true,
	"key":             true,
	"password":        true,
	"passwd":          true,
	"secret":          true,
	"client_secret":   true,
	"credential":      true,
	"credentials":     true,
	"cookie":          true,
	"set-cookie":      true,
	"signature":       true,
	"api_key_id":      true,
	"subscription_id": true,
}

// captureSecretPatterns match the credential shapes that can appear inside
// an ordinary value, a URL or a query string.
var captureSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}`),
	regexp.MustCompile(`\bghp_[A-Za-z0-9]{12,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{12,}`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{12,}`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{12,}`),
	regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`\baws_secret_access_key\b\s*[=:]\s*[A-Za-z0-9/+=]{20,}`),
}

// captureSecretFieldValue matches a secret field in text that does not decode
// as JSON, which is what a body cut at the size cap looks like. It hides the
// whole string value so a truncated body cannot store a secret whose key
// would have hidden it.
var captureSecretFieldValue = regexp.MustCompile(
	`(?i)("(?:` + captureSecretKeyAlternation() + `)"\s*:\s*)"(?:[^"\\]|\\.)*"`,
)

// captureSecretKeyAlternation renders captureSecretKeys as a regexp
// alternation, longest key first so a short key cannot shadow a longer one.
func captureSecretKeyAlternation() string {
	keys := make([]string, 0, len(captureSecretKeys))
	for key := range captureSecretKeys {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	for i, key := range keys {
		keys[i] = regexp.QuoteMeta(key)
	}
	return strings.Join(keys, "|")
}

// captureSecretQuery lists the query parameters whose values are redacted.
var captureSecretQuery = map[string]bool{
	"key":           true,
	"api-key":       true,
	"api_key":       true,
	"apikey":        true,
	"token":         true,
	"access_token":  true,
	"auth":          true,
	"authorization": true,
	"sig":           true,
	"signature":     true,
	"secret":        true,
	"password":      true,
}

// redactURL renders a URL with its credentials and secret query values
// removed.
func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clone := *u
	clone.User = nil
	if clone.RawQuery != "" {
		values, err := url.ParseQuery(clone.RawQuery)
		if err == nil {
			for name := range values {
				if captureSecretQuery[strings.ToLower(name)] {
					values[name] = []string{"REDACTED"}
				}
			}
			clone.RawQuery = values.Encode()
		} else {
			clone.RawQuery = "REDACTED"
		}
	}
	if clone.Fragment != "" {
		clone.Fragment = ""
	}
	return redactText(clone.String())
}

// redactBody returns a body safe to store: JSON bodies are redacted field by
// field, streamed bodies line by line, and every text then loses any value
// that looks like a credential.
func redactBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	return redactText(redactJSONBytes(body))
}

// redactJSONBytes redacts a JSON document, and each JSON payload of a
// server-sent event stream. Whatever is left undecoded, a body cut at the
// size cap above all, then gets a keyed pass so its secret fields still lose
// their values. Input that holds nothing to redact is returned byte for byte,
// so a capture shows the request or event exactly as sent.
func redactJSONBytes(body []byte) string {
	if redacted, ok := redactJSONDocument(body); ok {
		return redacted
	}
	lines := strings.Split(string(body), "\n")
	changed := false
	for i, line := range lines {
		payload, found := strings.CutPrefix(line, "data:")
		if !found {
			continue
		}
		trimmed := strings.TrimSpace(payload)
		if trimmed == "" || trimmed == "[DONE]" {
			continue
		}
		redacted, ok := redactJSONDocument([]byte(trimmed))
		if !ok || redacted == trimmed {
			continue
		}
		lines[i] = "data: " + redacted
		changed = true
	}
	text := string(body)
	if changed {
		text = strings.Join(lines, "\n")
	}
	return captureSecretFieldValue.ReplaceAllString(text, `${1}"REDACTED"`)
}

// redactJSONDocument redacts a JSON document and reports false when the input
// is not a JSON document. A document with nothing to redact keeps its exact
// original bytes; one that needed redaction is re-encoded compactly, which
// keeps values but loses the original key order.
func redactJSONDocument(body []byte) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var payload any
	if err := dec.Decode(&payload); err != nil {
		return "", false
	}
	if dec.More() {
		return "", false
	}
	redacted, changed := redactValue("", payload)
	if !changed {
		return string(body), true
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// redactValue redacts one decoded JSON value, keyed by the field name that
// holds it, and reports whether it changed anything.
func redactValue(key string, value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		changed := false
		for name, inner := range typed {
			redacted, innerChanged := redactValue(name, inner)
			out[name] = redacted
			changed = changed || innerChanged
		}
		return out, changed
	case []any:
		out := make([]any, len(typed))
		changed := false
		for i, inner := range typed {
			redacted, innerChanged := redactValue(key, inner)
			out[i] = redacted
			changed = changed || innerChanged
		}
		return out, changed
	case string:
		if captureSecretKeys[strings.ToLower(key)] {
			return "REDACTED", true
		}
		redacted := redactText(typed)
		return redacted, redacted != typed
	default:
		return value, false
	}
}

// redactText replaces anything that looks like a credential inside a plain
// string.
func redactText(text string) string {
	for _, pattern := range captureSecretPatterns {
		text = pattern.ReplaceAllString(text, "REDACTED")
	}
	return text
}
