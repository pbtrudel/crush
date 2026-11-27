package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func TestFormatJSONPreservesNumbers(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]string{
		"large integer":     `{"id":9007199254740993}`,
		"negative integer":  `{"id":-9007199254740993}`,
		"precise decimal":   `{"value":0.1234567890123456789}`,
		"large exponent":    `{"value":1e400}`,
		"nested numbers":    `{"values":[9007199254740993,{"value":0.1234567890123456789}]}`,
		"top-level integer": `9007199254740993`,
		"key order":         `{"z":1,"a":2}`,
		"duplicate keys":    `{"a":1,"a":2}`,
		"html characters":   `{"html":"<b>&</b>"}`,
		"string escapes":    `{"s":"\u00e9"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			formatted := formatJSON(input)
			var compact bytes.Buffer
			require.NoError(t, json.Compact(&compact, []byte(formatted)))
			require.Equal(t, input, compact.String())
		})
	}
}

func TestFormatJSONKeepsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", `{"id":}`, `{"id":1} {"id":2}`, `{"id":1} trailing`} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, input, formatJSON(input))
		})
	}
}

func TestFormatJSONIndentation(t *testing.T) {
	t.Parallel()

	formatted := formatJSON(" \n\t" + `{"values":[true,null,"text",1]}` + "\r\n ")
	require.Equal(t, "{\n  \"values\": [\n    true,\n    null,\n    \"text\",\n    1\n  ]\n}", formatted)
}

func TestWebFetchPreservesJSONNumbers(t *testing.T) {
	t.Parallel()

	const body = `{"id":9007199254740993,"value":0.1234567890123456789}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	tool := NewWebFetchTool(t.TempDir(), server.Client())
	input, err := json.Marshal(WebFetchParams{URL: server.URL})
	require.NoError(t, err)
	response, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "web-fetch-numbers",
		Name:  WebFetchToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, response.IsError)
	require.Contains(t, response.Content, `"id": 9007199254740993`)
	require.Contains(t, response.Content, `"value": 0.1234567890123456789`)
}

// A failed status is the one place the model has no way to know what went
// wrong. Each class of answer has to name the next step.
func TestFetchStatusErrorsNameTheNextStep(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		header string
		want   string
	}{
		{"not found", http.StatusNotFound, "", "does not exist; the address was probably guessed. Open the site's index or search instead of guessing another path"},
		{"gone", http.StatusGone, "", "does not exist; the address was probably guessed. Open the site's index or search instead of guessing another path"},
		{"rate limited", http.StatusTooManyRequests, "120", "rate-limited (retry after 120s); do not retry right away"},
		{"unavailable", http.StatusServiceUnavailable, "", "temporarily unavailable; do not retry right away"},
		{"forbidden", http.StatusForbidden, "", "refused access"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.header != "" {
					w.Header().Set("Retry-After", tc.header)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			_, err := FetchURLAndConvert(t.Context(), server.Client(), server.URL)
			require.Error(t, err)
			var statusErr *FetchStatusError
			require.ErrorAs(t, err, &statusErr)
			require.Equal(t, tc.status, statusErr.Status)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

// DuckDuckGo answers its own result pages with a challenge, so fetching one
// is the same block the search backend reports, and it arms the same wait.
func TestFetchDuckDuckGoChallengeMarksTheSearchBlocked(t *testing.T) {
	t.Cleanup(resetSearchBlock)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	// The server stands in for a DuckDuckGo host, so the check must see it as
	// one without the test touching the network.
	savedHost := ddgHost
	ddgHost = func(string) bool { return true }
	t.Cleanup(func() { ddgHost = savedHost })

	_, err := FetchURLAndConvert(t.Context(), server.Client(), server.URL)
	require.ErrorIs(t, err, errSearchRateLimited)
	require.True(t, SearchBlocked())
}

// Only DuckDuckGo's block answers fold into the search block. A 404 is a URL
// that is not there, and a 5xx is the server's problem; neither means search
// is throttled.
func TestFetchDuckDuckGoBlockOnlyOnBlockAnswers(t *testing.T) {
	savedHost := ddgHost
	ddgHost = func(string) bool { return true }
	t.Cleanup(func() {
		ddgHost = savedHost
		resetSearchBlock()
	})

	for _, tc := range []struct {
		name      string
		status    int
		wantBlock bool
	}{
		{"accepted", http.StatusAccepted, true},
		{"forbidden", http.StatusForbidden, true},
		{"too many requests", http.StatusTooManyRequests, true},
		{"not found", http.StatusNotFound, false},
		{"unavailable", http.StatusServiceUnavailable, false},
		{"internal error", http.StatusInternalServerError, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetSearchBlock()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			_, err := FetchURLAndConvert(t.Context(), server.Client(), server.URL)
			require.Error(t, err)
			if tc.wantBlock {
				require.ErrorIs(t, err, errSearchRateLimited)
				require.True(t, SearchBlocked())
				return
			}
			require.NotErrorIs(t, err, errSearchRateLimited)
			require.False(t, SearchBlocked(), "a %d must not start a search block", tc.status)
			var statusErr *FetchStatusError
			require.ErrorAs(t, err, &statusErr)
			require.Equal(t, tc.status, statusErr.Status)
		})
	}
}
