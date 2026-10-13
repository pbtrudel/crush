package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// discordAPIBase is the Discord REST API root. Overridable in tests.
const discordAPIBase = "https://discord.com/api/v10"

// Discord limits message content to 2000 characters; splitting leaves
// headroom so a re-render never trips the hard limit.
const (
	maxMessageLen  = 2000
	safeMessageLen = 1900
)

// restClient is a minimal Discord REST client. Every request goes
// through a single mutex, so bursts can never interleave and a global
// 429 backoff is honored before the next request is sent. This mirrors
// the hardened commandcode bridge: under load, concurrent sends used to
// trip per-channel rate limits and a single hung request could stall a
// run's status loop.
type restClient struct {
	token  string
	base   string
	client *http.Client

	mu          sync.Mutex
	blockedTill time.Time
}

func newRESTClient(token string) *restClient {
	return &restClient{
		token:  token,
		base:   discordAPIBase,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

// createdThread is the subset of the thread-create response the bridge
// uses to build a link.
type createdThread struct {
	ID      string `json:"id"`
	GuildID string `json:"guild_id"`
}

// discordMessage is the subset of a message response the bridge uses.
type discordMessage struct {
	ID string `json:"id"`
}

// createThread starts a public thread in a channel.
func (c *restClient) createThread(ctx context.Context, channelID, name string) (createdThread, error) {
	body := map[string]any{
		"name":                  truncateRunes(name, 100),
		"type":                  11, // Public thread.
		"auto_archive_duration": 1440,
	}
	data, err := c.do(ctx, http.MethodPost, "/channels/"+channelID+"/threads", body)
	if err != nil {
		return createdThread{}, err
	}
	var out createdThread
	if err := json.Unmarshal(data, &out); err != nil {
		return createdThread{}, fmt.Errorf("decode thread response: %w", err)
	}
	return out, nil
}

// createMessage posts a message and returns its id.
func (c *restClient) createMessage(ctx context.Context, channelID, content string) (string, error) {
	body := map[string]any{
		"content": content,
		// Never let model output or mirrored prompts ping anyone.
		"allowed_mentions": map[string]any{"parse": []string{}},
	}
	data, err := c.do(ctx, http.MethodPost, "/channels/"+channelID+"/messages", body)
	if err != nil {
		return "", err
	}
	var out discordMessage
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode message response: %w", err)
	}
	return out.ID, nil
}

// editMessage replaces a message's content.
func (c *restClient) editMessage(ctx context.Context, channelID, messageID, content string) error {
	body := map[string]any{"content": content}
	_, err := c.do(ctx, http.MethodPatch, "/channels/"+channelID+"/messages/"+messageID, body)
	return err
}

// triggerTyping shows the typing indicator in a channel.
func (c *restClient) triggerTyping(ctx context.Context, channelID string) error {
	_, err := c.do(ctx, http.MethodPost, "/channels/"+channelID+"/typing", nil)
	return err
}

// addReaction adds the bot's reaction to a message.
func (c *restClient) addReaction(ctx context.Context, channelID, messageID, emoji string) error {
	path := "/channels/" + channelID + "/messages/" + messageID + "/reactions/" + url.PathEscape(emoji) + "/@me"
	_, err := c.do(ctx, http.MethodPut, path, nil)
	return err
}

// editChannelName renames a channel (used to track the session title).
func (c *restClient) editChannelName(ctx context.Context, channelID, name string) error {
	body := map[string]any{"name": truncateRunes(name, 100)}
	_, err := c.do(ctx, http.MethodPatch, "/channels/"+channelID, body)
	return err
}

// gatewayURL fetches the gateway websocket URL. Discord rate-limits this
// endpoint aggressively, so callers fetch it once and reuse it.
func (c *restClient) gatewayURL(ctx context.Context) (string, error) {
	data, err := c.do(ctx, http.MethodGet, "/gateway/bot", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode gateway response: %w", err)
	}
	if out.URL == "" {
		return "", errors.New("gateway response contained no url")
	}
	return out.URL, nil
}

// do performs one serialized API request. payload may be nil for GET and
// bodyless requests. A 429 response parks the whole client until the
// retry window passes, then retries once; network errors retry once.
func (c *restClient) do(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal request for %s %s: %w", method, path, err)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		if err := c.waitBackoff(ctx); err != nil {
			return nil, err
		}

		var body io.Reader
		if raw != nil {
			body = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
		if err != nil {
			return nil, fmt.Errorf("build request for %s %s: %w", method, path, err)
		}
		req.Header.Set("Authorization", "Bot "+c.token)
		if raw != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.client.Do(req)
		if err != nil {
			if attempt == 0 {
				continue
			}
			return nil, fmt.Errorf("discord request %s %s: %w", method, path, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			if attempt == 0 {
				continue
			}
			return nil, fmt.Errorf("read response for %s %s: %w", method, path, readErr)
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			c.park(readRetryAfter(data))
			continue
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return data, nil
		default:
			return nil, fmt.Errorf("discord %s %s: status %d: %s", method, path, resp.StatusCode, truncateRunes(string(data), 200))
		}
	}
	return nil, fmt.Errorf("discord %s %s: exhausted retries", method, path)
}

// waitBackoff sleeps until a previously seen 429 window has passed.
// Callers must hold c.mu.
func (c *restClient) waitBackoff(ctx context.Context) error {
	wait := time.Until(c.blockedTill)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		c.blockedTill = time.Time{}
		return nil
	}
}

// park extends the global backoff window after a 429. Callers must hold
// c.mu.
func (c *restClient) park(retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	until := time.Now().Add(retryAfter + 500*time.Millisecond)
	if until.After(c.blockedTill) {
		c.blockedTill = until
	}
}

// readRetryAfter extracts retry_after (seconds) from a 429 body.
func readRetryAfter(data []byte) time.Duration {
	var body struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if err := json.Unmarshal(data, &body); err != nil || body.RetryAfter <= 0 {
		return 0
	}
	return time.Duration(body.RetryAfter * float64(time.Second))
}

// truncateRunes caps a string at n runes without splitting UTF-8.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// splitMessage splits text into Discord-sized chunks at newline
// boundaries. Empty input yields no chunks, so callers can post
// unconditionally.
func splitMessage(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if len(text) <= maxMessageLen {
		return []string{text}
	}
	var out []string
	rest := text
	for len(rest) > safeMessageLen {
		cut := strings.LastIndex(rest[:safeMessageLen], "\n")
		if cut <= 0 {
			cut = safeMessageLen
		}
		out = append(out, rest[:cut])
		rest = strings.TrimPrefix(rest[cut:], "\n")
	}
	if rest != "" {
		out = append(out, rest)
	}
	return out
}
