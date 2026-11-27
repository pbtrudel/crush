package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// TestPageMessagesWalksBackwards pins the page boundaries: the newest page
// first, then older pages ending before the id the client already has.
func TestPageMessagesWalksBackwards(t *testing.T) {
	t.Parallel()
	msgs := messagesWithIDs("m1", "m2", "m3", "m4", "m5")

	page, hasMore, err := pageMessages(msgs, "", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m4", "m5"}, messageIDs(page))
	require.True(t, hasMore)

	page, hasMore, err = pageMessages(msgs, "m4", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m2", "m3"}, messageIDs(page))
	require.True(t, hasMore)

	page, hasMore, err = pageMessages(msgs, "m2", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m1"}, messageIDs(page))
	require.False(t, hasMore)

	page, hasMore, err = pageMessages(msgs, "m1", 2)
	require.NoError(t, err)
	require.Empty(t, page)
	require.False(t, hasMore)
}

// TestPageMessagesUnknownID pins the refusal: an id the session does not
// have is an error, never a silent jump to the newest page.
func TestPageMessagesUnknownID(t *testing.T) {
	t.Parallel()
	_, _, err := pageMessages(messagesWithIDs("m1", "m2"), "nope", 10)
	require.ErrorIs(t, err, errUnknownMessage)
}

// TestMessagesRouteAnswersAPage pins the envelope every client decodes.
func TestMessagesRouteAnswersAPage(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for _, id := range []string{"m1", "m2", "m3"} {
		fake.AddMessage(textMessage(id, "s1", "hello "+id))
	}
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages?limit=2", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var page messagesResponse
	require.NoError(t, json.Unmarshal(body, &page))
	require.Len(t, page.Messages, 2)
	require.True(t, page.HasMore)
	var first struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(page.Messages[0], &first))
	require.Equal(t, "m2", first.ID)

	rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages?before=m2&limit=2", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.NoError(t, json.Unmarshal(body, &page))
	require.Len(t, page.Messages, 1)
	require.False(t, page.HasMore)
	require.NoError(t, json.Unmarshal(page.Messages[0], &first))
	require.Equal(t, "m1", first.ID)
}

// TestMessagesRouteRefusesABadPage pins the two refusals.
func TestMessagesRouteRefusesABadPage(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddMessage(textMessage("m1", "s1", "hi"))
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages?before=nope", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_message")

	for _, query := range []string{"limit=0", "limit=-3", "limit=ten"} {
		rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages?"+query, "")
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, query)
		require.Contains(t, string(body), "bad_limit", query)
	}
}

// TestMessagesRouteClampsThePage pins the cap: an oversized limit is
// clamped, never a refusal, so a client cannot ask for unbounded history.
func TestMessagesRouteClampsThePage(t *testing.T) {
	t.Parallel()
	require.Equal(t, maxMessagePageSize, clampMessageLimit(10_000))
	require.Equal(t, 7, clampMessageLimit(7))
}

func messagesWithIDs(ids ...string) []message.Message {
	out := make([]message.Message, 0, len(ids))
	for _, id := range ids {
		out = append(out, textMessage(id, "s1", id))
	}
	return out
}

func textMessage(id, sessionID, text string) message.Message {
	return message.Message{
		ID: id, SessionID: sessionID, Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}
}

func messageIDs(msgs []message.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}
