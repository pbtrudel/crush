package agent

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReplayRejectionClosesTheOriginalBody pins the connection handling of an
// inspected 400: whether or not it names a rejected replay, the body handed
// back still delivers every byte, and closing it closes the original, so the
// transport does not leak the connection.
func TestReplayRejectionClosesTheOriginalBody(t *testing.T) {
	t.Parallel()

	for name, text := range map[string]string{
		"rejected replay": `{"error":{"message":"Item with id 'rs_1' not found."}}`,
		"other 400":       `{"error":{"message":"Invalid value for temperature."}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			original := &closeRecorder{Reader: strings.NewReader(text)}
			resp := &http.Response{StatusCode: http.StatusBadRequest, Body: original, Header: http.Header{}}

			rejected, body := replayRejection(resp)
			require.Equal(t, name == "rejected replay", rejected)
			require.Equal(t, text, string(body))

			got, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, text, string(got), "the caller still reads the whole body")
			require.NoError(t, resp.Body.Close())
			require.EqualValues(t, 1, original.closed.Load(), "the original body is closed")
		})
	}
}
