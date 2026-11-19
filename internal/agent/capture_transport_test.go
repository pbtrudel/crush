package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureFiles returns the capture files written to dir.
func captureFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files
}

// readCapture decodes the only capture file in dir.
func readCapture(t *testing.T, dir string) captureExchange {
	t.Helper()
	files := captureFiles(t, dir)
	require.Len(t, files, 1)
	data, err := os.ReadFile(files[0])
	require.NoError(t, err)
	var exchange captureExchange
	require.NoError(t, json.Unmarshal(data, &exchange))
	return exchange
}

func TestCaptureTransportOffByDefault(t *testing.T) {
	t.Setenv(captureDirEnv, "")

	base := http.DefaultTransport
	require.Same(t, base, captureTransportIfEnabled(base))
	require.Nil(t, captureTransportIfEnabled(nil))
	require.Nil(t, captureClient(nil))

	client := &http.Client{Transport: base}
	require.Same(t, client, captureClient(client))
	require.Same(t, base, client.Transport)
}

func TestCaptureTransportRecordsExchange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(captureDirEnv, dir)

	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req_1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_1","model":"deepseek-v4.1-flash"}`))
	}))
	defer server.Close()

	client := captureClient(nil)
	require.NotNil(t, client)
	body := `{"model":"deepseek-v4.1-flash","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/chat/completions", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-shouldnotappear000000")
	req.Header.Set("X-Api-Key", "sk-shouldnotappear111111")

	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	// The request still reached the server intact.
	require.Equal(t, body, gotBody)

	exchange := readCapture(t, dir)
	require.Equal(t, http.MethodPost, exchange.Method)
	require.Equal(t, http.StatusOK, exchange.Status)
	require.Contains(t, exchange.URL, "/chat/completions")
	require.Equal(t, body, exchange.RequestBody)
	require.Contains(t, exchange.ResponseBody, `"resp_1"`)
	require.Equal(t, "application/json", exchange.RequestHeaders["content-type"])
	require.NotContains(t, exchange.RequestHeaders, "authorization")
	require.NotContains(t, exchange.RequestHeaders, "x-api-key")

	raw, err := os.ReadFile(captureFiles(t, dir)[0])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "shouldnotappear")
	require.Contains(t, string(raw), "deepseek-v4.1-flash")

	info, err := os.Stat(captureFiles(t, dir)[0])
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestCaptureTransportRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(captureDirEnv, dir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := captureClient(nil)
	require.NotNil(t, client)
	reqBody := `{"model":"glm-5.3","api_key":"sk-abcdefghijklmnop","input":"x","metadata":{"authorization":"Bearer abcdefghijklmnopqrst"}}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/chat/completions?key=supersecretvalue&model=glm-5.3", strings.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	exchange := readCapture(t, dir)
	require.True(t, exchange.Stream)
	require.NotContains(t, exchange.URL, "supersecretvalue")
	require.Contains(t, exchange.URL, "model=glm-5.3")
	require.NotContains(t, exchange.RequestBody, "sk-abcdefghijklmnop")
	require.NotContains(t, exchange.RequestBody, "abcdefghijklmnopqrst")
	require.Contains(t, exchange.RequestBody, `"model":"glm-5.3"`)
	// The event stream is kept as the raw stream, one event per line.
	require.Contains(t, exchange.ResponseBody, `"delta":"hello"`)
	require.Contains(t, exchange.ResponseBody, "data: [DONE]")
}

func TestCaptureTransportTruncatesStreams(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(captureDirEnv, dir)

	event := `data: {"type":"response.output_item.added","item":{"id":"rs_1","type":"reasoning"}}` + "\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for range captureBodyLimit/len(event) + 1 {
			_, _ = w.Write([]byte(event))
		}
	}))
	defer server.Close()

	client := captureClient(nil)
	require.NotNil(t, client)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol","stream":true}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	received, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Greater(t, len(received), captureBodyLimit)

	exchange := readCapture(t, dir)
	require.True(t, exchange.Stream)
	require.True(t, exchange.ResponseTruncated)
	require.Len(t, exchange.ResponseBody, captureBodyLimit)
}

func TestCaptureTransportRecordsErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(captureDirEnv, dir)

	client := captureClient(nil)
	require.NotNil(t, client)
	// Port 1 on the loopback interface refuses connections.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://127.0.0.1:1/v1/messages", strings.NewReader(`{"model":"claude-sonnet-5"}`))
	require.NoError(t, err)

	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)

	exchange := readCapture(t, dir)
	require.NotEmpty(t, exchange.Error)
	require.Zero(t, exchange.Status)
	require.Contains(t, exchange.URL, "/v1/messages")
}

func TestCaptureHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("User-Agent", "crush/1")
	header.Set("Openai-Beta", "responses=experimental")
	header.Set("Authorization", "Bearer secret")
	header.Set("X-Model-Id", "gpt-5.6-sol")
	header.Set("X-Session-Affinity", "sess_1")

	got := captureHeaders(header)
	require.Equal(t, map[string]string{
		"content-type": "application/json",
		"user-agent":   "crush/1",
		"x-model-id":   "gpt-5.6-sol",
	}, got)
}

func TestRedactURL(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "API key query parameter",
			in:   "https://api.example.com/v1/chat/completions?api-key=abc123secret&model=glm-5.3",
			want: "api-key=REDACTED",
		},
		{
			name: "key query parameter",
			in:   "https://generativelanguage.googleapis.com/v1beta/models/gemini:streamGenerateContent?key=abc123secret",
			want: "key=REDACTED",
		},
		{
			name: "basic auth userinfo",
			in:   "https://user:pass@api.example.com/v1/models",
			want: "api.example.com/v1/models",
		},
		{
			name: "bearer token in query",
			in:   "https://api.example.com/v1/models?token=abc123secret",
			want: "token=REDACTED",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, test.in, nil)
			require.NoError(t, err)
			got := redactURL(req.URL)
			require.Contains(t, got, test.want)
			require.NotContains(t, got, "abc123secret")
			require.NotContains(t, got, "pass")
		})
	}
}

func TestRedactBody(t *testing.T) {
	t.Parallel()

	body := map[string]any{
		"model":            "kimi-k3",
		"api_key":          "plain-secret",
		"prompt_cache_key": "workspace-hash",
		"messages": []any{
			map[string]any{"role": "user", "content": "my key is sk-abcdefghijklmnop"},
			map[string]any{"role": "assistant", "reasoning_content": "thinking"},
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	got := redactBody(raw)
	require.NotContains(t, got, "plain-secret")
	require.NotContains(t, got, "sk-abcdefghijklmnop")
	require.Contains(t, got, `"model":"kimi-k3"`)
	require.Contains(t, got, `"prompt_cache_key":"workspace-hash"`)
	require.Contains(t, got, `"reasoning_content":"thinking"`)
}

// oversizedBody returns a JSON document whose api_key field comes first,
// followed by enough padding to push it well past the capture size cap.
func oversizedBody() string {
	return `{"api_key":"plain-secret","model":"glm-5.3","pad":"` + strings.Repeat("x", captureBodyLimit) + `"}`
}

// TestCaptureTransportRedactsOversizedRequest pins that a body larger than
// the size cap is redacted before it is cut. A truncated body does not parse
// as JSON, so key-based redaction would otherwise be skipped and a plain
// api_key would reach the disk.
func TestCaptureTransportRedactsOversizedRequest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(captureDirEnv, dir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := captureClient(nil)
	require.NotNil(t, client)
	reqBody := oversizedBody()
	require.Greater(t, len(reqBody), captureBodyLimit)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	raw, err := os.ReadFile(captureFiles(t, dir)[0])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "plain-secret")
	require.True(t, readCapture(t, dir).RequestTruncated)
}

// TestCaptureTransportRedactsOversizedResponse pins the same rule for a
// response: the copy kept is cut at the cap, so it cannot be decoded, and
// the keyed fallback still has to hide the secret.
func TestCaptureTransportRedactsOversizedResponse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(captureDirEnv, dir)

	response := oversizedBody()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	client := captureClient(nil)
	require.NotNil(t, client)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"glm-5.3"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	raw, err := os.ReadFile(captureFiles(t, dir)[0])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "plain-secret")
	require.True(t, readCapture(t, dir).ResponseTruncated)
}

// TestRedactTextCredentialShapes pins that the credential shapes without a
// distinctive prefix are still caught inside a plain string.
func TestRedactTextCredentialShapes(t *testing.T) {
	t.Parallel()

	for _, secret := range []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ASIAIOSFODNN7EXAMPLE",
		"aws_access_key_id=AKIAIOSFODNN7EXAMPLE",
	} {
		got := redactText(secret)
		require.Contains(t, got, "REDACTED")
		require.NotContains(t, got, "AKIAIOSFODNN7EXAMPLE")
	}
}
