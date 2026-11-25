package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/stretchr/testify/require"
)

// TestHandleErrorMapsConfigReloadBusy pins the wire answer for a reload
// refused while a run is in flight: a client restores its own busy error
// from the code instead of guessing from the status.
func TestHandleErrorMapsConfigReloadBusy(t *testing.T) {
	t.Parallel()

	c := &controllerV1{server: &Server{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)

	c.handleError(rec, req, fmt.Errorf("workspace ws-1: %w", app.ErrConfigReloadBusy))

	require.Equal(t, http.StatusConflict, rec.Code)
	var e proto.Error
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
	require.Equal(t, "config_reload_busy", e.Code)
}
