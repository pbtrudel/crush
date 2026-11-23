package agent

// fantasy compresses Hyper request bodies outside the fork's transports, so
// the prompt_cache_key retry and the Responses reasoning replay are handed
// gzip or brotli bytes. They must still see, and edit, the JSON inside.

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/charmbracelet/crush/internal/agent/hyper"
	brrr "github.com/molecule-man/go-brrr"
	"github.com/stretchr/testify/require"
)

func gzipBytes(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(body)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func brotliBytes(t *testing.T, body []byte) []byte {
	t.Helper()
	out, err := brrr.Compress(body, 1)
	require.NoError(t, err)
	return out
}

// inflateWire undoes the Content-Encoding a test server received.
func inflateWire(t *testing.T, encoding string, raw []byte) string {
	t.Helper()
	switch encoding {
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		require.NoError(t, err)
		inflated, err := io.ReadAll(zr)
		require.NoError(t, err)
		return string(inflated)
	case "br":
		inflated, err := io.ReadAll(brrr.NewReader(bytes.NewReader(raw)))
		require.NoError(t, err)
		return string(inflated)
	}
	return string(raw)
}

// keyRejectingServer answers 400 to any body carrying prompt_cache_key, the
// way an endpoint that does not know the field refuses it. With
// rejectBrotli it also refuses brotli bodies, with an error that does not
// mention the key.
type keyRejectingServer struct {
	mu        sync.Mutex
	encodings []string
	bodies    []string
	server    *httptest.Server
}

func newKeyRejectingServer(t *testing.T, rejectBrotli bool) *keyRejectingServer {
	t.Helper()
	s := &keyRejectingServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		encoding := r.Header.Get("Content-Encoding")
		body := inflateWire(t, encoding, raw)

		s.mu.Lock()
		s.encodings = append(s.encodings, encoding)
		s.bodies = append(s.bodies, body)
		s.mu.Unlock()

		if rejectBrotli && encoding == "br" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unsupported content encoding","type":"invalid_request_error"}}`))
			return
		}
		if strings.Contains(body, promptCacheKeyField) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: prompt_cache_key","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"z","created":1,"model":"test-model","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`))
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *keyRejectingServer) received() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.encodings...), append([]string(nil), s.bodies...)
}

func generateWithCacheKey(t *testing.T, baseURL string) error {
	t.Helper()
	provider, err := testCoordinator(t).buildOpenaiCompatProvider(baseURL, "test-key", nil, nil, hyper.Name, false, catwalk.TypeCompletions)
	require.NoError(t, err)
	lm, err := provider.LanguageModel(t.Context(), "test-model")
	require.NoError(t, err)
	_, err = lm.Generate(t.Context(), fantasy.Call{
		Prompt: longPrompt(),
		ProviderOptions: fantasy.ProviderOptions{
			openaicompat.Name: &openaicompat.ProviderOptions{
				ExtraBody: map[string]any{promptCacheKeyField: "cache-key"},
			},
		},
	})
	return err
}

func TestPromptCacheKeyRetryUnderHyperCompression(t *testing.T) {
	t.Parallel()

	t.Run("a rejected key is retried without it, still compressed", func(t *testing.T) {
		t.Parallel()
		server := newKeyRejectingServer(t, false)

		require.NoError(t, generateWithCacheKey(t, server.server.URL+"/v1"))

		encodings, bodies := server.received()
		require.Equal(t, []string{"br", "br"}, encodings)
		require.Contains(t, bodies[0], promptCacheKeyField)
		require.NotContains(t, bodies[1], promptCacheKeyField, "the retry drops the key")
		require.Contains(t, bodies[1], `"model":"test-model"`, "the retry inflates to the same request")
	})

	// fantasy's own fallback replays a refused brotli body as gzip through
	// these transports. That refusal is about the encoding, not the key, so
	// the key must survive it.
	t.Run("a brotli refusal is not taken for a key rejection", func(t *testing.T) {
		t.Parallel()
		server := newKeyRejectingServer(t, true)

		require.NoError(t, generateWithCacheKey(t, server.server.URL+"/v1"))

		encodings, bodies := server.received()
		require.Equal(t, []string{"br", "gzip", "gzip"}, encodings)
		require.Contains(t, bodies[0], promptCacheKeyField)
		require.Contains(t, bodies[1], promptCacheKeyField, "the encoding fallback keeps the key")
		require.NotContains(t, bodies[2], promptCacheKeyField, "only the key rejection strips it")
	})
}

// compressingTestTransport encodes each request body the way fantasy's
// request compression does, outside the transport it wraps.
type compressingTestTransport struct {
	t        *testing.T
	encoding string
	base     http.RoundTripper
}

func (c compressingTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	require.NoError(c.t, err)
	var encoded []byte
	switch c.encoding {
	case "gzip":
		encoded = gzipBytes(c.t, body)
	case "br":
		encoded = brotliBytes(c.t, body)
	default:
		encoded = body
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("Content-Encoding", c.encoding)
	clone.Body = io.NopCloser(bytes.NewReader(encoded))
	clone.ContentLength = int64(len(encoded))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(encoded)), nil
	}
	return c.base.RoundTrip(clone)
}

func TestResponsesReplayUnderRequestCompression(t *testing.T) {
	t.Parallel()

	for _, encoding := range []string{"br", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			t.Parallel()
			server := newReplayTestServer(t, reasoningResponse(), completedResponse("resp_2"))
			client := &http.Client{Transport: compressingTestTransport{
				t:        t,
				encoding: encoding,
				base:     newResponsesReasoningReplayTransport(http.DefaultTransport, true),
			}}

			server.post(t, client, replayRequest(t, "gpt-test", `{"role":"user","content":"hi"}`))
			server.post(t, client, replayRequest(t, "gpt-test",
				`{"role":"user","content":"hi"}`,
				functionCall("call_a", "view", `{"path":"a.txt"}`),
				functionCallOutput("call_a", "ok"),
			))

			bodies := server.seenBodies()
			require.Len(t, bodies, 2)
			second := []byte(inflateWire(t, encoding, bodies[1]))
			require.Equal(t,
				[]string{"user", "reasoning", "function_call", "function_call_output"},
				replayInputTypes(t, second),
				"the replayed body is re-encoded with the encoding it arrived in",
			)
		})
	}
}

func TestReadRequestBody(t *testing.T) {
	t.Parallel()

	plain := []byte(`{"model":"m","prompt_cache_key":"k"}`)
	newRequest := func(t *testing.T, encoding string, body []byte) *http.Request {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.test/v1/chat/completions", bytes.NewReader(body))
		if encoding != "" {
			req.Header.Set("Content-Encoding", encoding)
		}
		return req
	}

	for name, tc := range map[string]struct {
		header   string
		wire     []byte
		wantBody []byte
		wantEnc  string
	}{
		"plain":        {header: "", wire: plain, wantBody: plain, wantEnc: ""},
		"gzip":         {header: "gzip", wire: gzipBytes(t, plain), wantBody: plain, wantEnc: "gzip"},
		"brotli":       {header: "br", wire: brotliBytes(t, plain), wantBody: plain, wantEnc: "br"},
		"header case":  {header: " GZip ", wire: gzipBytes(t, plain), wantBody: plain, wantEnc: "gzip"},
		"unknown":      {header: "zstd", wire: []byte("opaque"), wantBody: []byte("opaque"), wantEnc: ""},
		"stacked":      {header: "gzip, br", wire: []byte("opaque"), wantBody: []byte("opaque"), wantEnc: ""},
		"corrupt gzip": {header: "gzip", wire: []byte("not gzip"), wantBody: []byte("not gzip"), wantEnc: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body, encoding, err := readRequestBody(newRequest(t, tc.header, tc.wire))
			require.NoError(t, err)
			require.Equal(t, tc.wantBody, body)
			require.Equal(t, tc.wantEnc, encoding)
		})
	}

	t.Run("re-encodes with the same encoding", func(t *testing.T) {
		t.Parallel()
		for _, encoding := range []string{"gzip", "br"} {
			req := newRequest(t, encoding, nil)
			sent := requestWithBody(req, plain, encoding)
			require.Equal(t, encoding, sent.Header.Get("Content-Encoding"))
			wire, err := io.ReadAll(sent.Body)
			require.NoError(t, err)
			require.Equal(t, int64(len(wire)), sent.ContentLength)
			require.Equal(t, string(plain), inflateWire(t, encoding, wire))
			again, err := sent.GetBody()
			require.NoError(t, err)
			replayed, err := io.ReadAll(again)
			require.NoError(t, err)
			require.Equal(t, wire, replayed, "GetBody replays the same wire bytes")
		}
	})

	t.Run("an unknown encoding is sent untouched", func(t *testing.T) {
		t.Parallel()
		req := newRequest(t, "zstd", nil)
		sent := requestWithBody(req, []byte("opaque"), "")
		require.Equal(t, "zstd", sent.Header.Get("Content-Encoding"))
		wire, err := io.ReadAll(sent.Body)
		require.NoError(t, err)
		require.Equal(t, "opaque", string(wire))
	})
}
