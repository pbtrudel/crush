package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestREST(t *testing.T, handler http.HandlerFunc) *restClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := newRESTClient("test-token")
	c.base = srv.URL
	return c
}

func TestCreateMessage(t *testing.T) {
	t.Parallel()
	var gotAuth, gotPath, gotBody string
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		_, _ = w.Write([]byte(`{"id":"42"}`))
	})

	id, err := c.createMessage(t.Context(), "chan-1", "hello")
	require.NoError(t, err)
	require.Equal(t, "42", id)
	require.Equal(t, "Bot test-token", gotAuth)
	require.Equal(t, "/channels/chan-1/messages", gotPath)
	require.Contains(t, gotBody, `"content":"hello"`)
	require.Contains(t, gotBody, `"allowed_mentions":{"parse":[]}`)
}

func TestCreateThread(t *testing.T) {
	t.Parallel()
	var gotBody string
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/channels/chan-1/threads", r.URL.Path)
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		_, _ = w.Write([]byte(`{"id":"t1","guild_id":"g1"}`))
	})

	thread, err := c.createThread(t.Context(), "chan-1", strings.Repeat("x", 300))
	require.NoError(t, err)
	require.Equal(t, "t1", thread.ID)
	require.Equal(t, "g1", thread.GuildID)

	var body struct {
		Name                string `json:"name"`
		Type                int    `json:"type"`
		AutoArchiveDuration int    `json:"auto_archive_duration"`
	}
	require.NoError(t, json.Unmarshal([]byte(gotBody), &body))
	require.Len(t, []rune(body.Name), 100, "thread names cap at 100 runes")
	require.Equal(t, 11, body.Type)
	require.Equal(t, 1440, body.AutoArchiveDuration)
}

func TestEditAndRename(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var paths []string
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	})

	require.NoError(t, c.editMessage(t.Context(), "c1", "m1", "body"))
	require.NoError(t, c.editChannelName(t.Context(), "c1", "name"))
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{
		"PATCH /channels/c1/messages/m1",
		"PATCH /channels/c1",
	}, paths)
}

func TestDeleteMessage(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	require.NoError(t, c.deleteMessage(t.Context(), "c1", "m1"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/channels/c1/messages/m1", gotPath)
}

func TestRespondInteraction(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var bodies []string
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/interactions/i1/itok/callback", r.URL.Path)
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	require.NoError(t, c.respondInteraction(t.Context(), "i1", "itok", "hi", true))
	require.NoError(t, c.respondInteraction(t.Context(), "i1", "itok", "public", false))

	mu.Lock()
	defer mu.Unlock()
	require.JSONEq(t, `{"type":4,"data":{"content":"hi","flags":64}}`, bodies[0])
	require.JSONEq(t, `{"type":4,"data":{"content":"public"}}`, bodies[1])
}

func TestAddReactionEscapesEmoji(t *testing.T) {
	t.Parallel()
	var gotPath string
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	})

	require.NoError(t, c.addReaction(t.Context(), "c1", "m1", "✅"))
	require.Equal(t, "/channels/c1/messages/m1/reactions/%E2%9C%85/@me", gotPath)
}

func TestGatewayURL(t *testing.T) {
	t.Parallel()
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"wss://gateway.discord.gg"}`))
	})
	url, err := c.gatewayURL(t.Context())
	require.NoError(t, err)
	require.Equal(t, "wss://gateway.discord.gg", url)

	empty := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	_, err = empty.gatewayURL(t.Context())
	require.ErrorContains(t, err, "no url")
}

func TestRateLimitBackoff(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"retry_after":0.05}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"9"}`))
	})

	start := time.Now()
	id, err := c.createMessage(t.Context(), "c1", "hi")
	require.NoError(t, err)
	require.Equal(t, "9", id)
	require.EqualValues(t, 2, attempts.Load())
	require.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond, "must honor retry_after")
}

func TestErrorStatus(t *testing.T) {
	t.Parallel()
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Missing Permissions"}`))
	})
	_, err := c.createMessage(t.Context(), "c1", "hi")
	require.ErrorContains(t, err, "403")
}

func TestSerializedRequests(t *testing.T) {
	t.Parallel()
	var inFlight, maxInFlight atomic.Int32
	c := newTestREST(t, func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	})

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.createMessage(t.Context(), "c1", "hi")
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, maxInFlight.Load(), "requests must be serialized")
}

func TestRetryAfterParsing(t *testing.T) {
	t.Parallel()
	require.Equal(t, time.Duration(0), readRetryAfter([]byte(`garbage`)))
	require.Equal(t, 500*time.Millisecond, readRetryAfter([]byte(`{"retry_after":0.5}`)))
}

func TestTruncateRunes(t *testing.T) {
	t.Parallel()
	require.Equal(t, "héllo", truncateRunes("héllo", 10))
	require.Equal(t, "hé", truncateRunes("héllo", 2), "must not split UTF-8")
}
