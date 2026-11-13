package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchModelLimits(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   map[string]ModelLimits
		fail   bool
	}{
		{"limits", 200, `{"data":[{"id":"model","capabilities":{"limits":{"max_prompt_tokens":100,"max_context_window_tokens":200,"max_output_tokens":30}}}]}`, map[string]ModelLimits{"model": {MaxPromptTokens: 100, MaxContextWindowTokens: 200, MaxOutputTokens: 30}}, false},
		{"missing", 200, `{"data":[{"id":"empty","capabilities":{"limits":{"max_prompt_tokens":0}}}]}`, map[string]ModelLimits{"empty": {}}, false},
		{"malformed", 200, `{`, nil, true},
		{"status", 401, `{}`, nil, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				assert.Equal(t, "/models", request.URL.Path)
				assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))
				assert.Equal(t, "2026-08-01", request.Header.Get("X-Github-Api-Version"))
				assert.Equal(t, "application/json", request.Header.Get("Accept"))
				assert.Equal(t, "value", request.Header.Get("X-Extra"))
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			got, err := FetchModelLimits(context.Background(), server.URL, "secret", map[string]string{"X-Extra": "value"})
			if test.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}
