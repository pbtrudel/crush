package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/charmbracelet/crush/internal/proto"
)

// Typed outcomes callers need to distinguish from ordinary transport
// failures. Match them with errors.Is.
var (
	// ErrNotFound reports that the server answered 404. For a
	// workspace-scoped call this means the server no longer knows the
	// workspace — it was torn down, or the server was replaced under the
	// client — so the right response is to re-register rather than retry
	// the same ID, which can never start succeeding again.
	ErrNotFound = errors.New("not found")

	// ErrSessionNotFound reports that the server answered 404 with the
	// session_not_found code: the workspace exists, the session does
	// not. It is deliberately not ErrNotFound, which means the server
	// no longer knows the workspace and triggers recovery.
	ErrSessionNotFound = errors.New("session not found")

	// ErrServerBusy reports that the server declined to shut down
	// because it is still hosting workspaces or is midway through
	// creating one. A client asking a version-mismatched server to stand
	// down must keep using it instead of assuming it is going away.
	ErrServerBusy = errors.New("server busy")

	// ErrServerShuttingDown reports that the server refused the request
	// because it has already committed to exiting. The work is not lost:
	// a replacement server can be started and the request retried
	// against it.
	ErrServerShuttingDown = errors.New("server is shutting down")

	// ErrUnsupported reports that the running server does not understand
	// the request because it predates the feature. Callers must decide
	// what is safe to do with an older server rather than treating the
	// failure as transient.
	ErrUnsupported = errors.New("unsupported by the running server")

	// ErrAgentNotInitialized reports that the workspace's agent has not
	// been configured yet. The call can succeed once it is, so callers
	// wait and retry rather than treating it as a failure.
	ErrAgentNotInitialized = errors.New("agent not initialized")

	// ErrConfigReloadBusy reports that the server refused a config reload
	// because an agent run is in flight. Reloading swaps models and tools
	// under the live request, so the reload succeeds once the run ends.
	ErrConfigReloadBusy = errors.New("cannot reload the configuration while an agent is running")
)

// configReloadBusyCode is the error code the server's reload route answers
// for a busy agent (internal/server).
const configReloadBusyCode = "config_reload_busy"

// checkStatus returns nil when rsp's status code is one of ok
// (http.StatusOK when none are given). Otherwise it returns an error
// carrying the status code and, when the body decodes as a proto.Error,
// the server-provided message. Statuses that callers act on are wrapped
// in the matching sentinel. checkStatus may consume the response body.
func checkStatus(rsp *http.Response, ok ...int) error {
	if len(ok) == 0 {
		ok = []int{http.StatusOK}
	}
	if slices.Contains(ok, rsp.StatusCode) {
		return nil
	}
	var err error
	msg, code := decodeError(rsp.Body)
	if msg != "" {
		err = fmt.Errorf("status code %d: %s", rsp.StatusCode, msg)
	} else {
		err = fmt.Errorf("status code %d", rsp.StatusCode)
	}
	if code == proto.ErrorCodeAgentNotInitialized {
		return fmt.Errorf("%w: %w", ErrAgentNotInitialized, err)
	}
	if code == proto.ErrorCodeSessionNotFound {
		return fmt.Errorf("%w: %w", ErrSessionNotFound, err)
	}
	switch rsp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case http.StatusServiceUnavailable:
		return fmt.Errorf("%w: %w", ErrServerShuttingDown, err)
	}
	return err
}

// decodeError decodes an error body into its message and machine code. It
// returns empty strings when the body is empty or is not a proto.Error.
func decodeError(body io.Reader) (message, code string) {
	var e proto.Error
	if err := json.NewDecoder(body).Decode(&e); err != nil {
		return "", ""
	}
	return e.Message, e.Code
}

// TeamError is a team action failure as the server sent it: the HTTP
// status plus the machine code and message from the error body. Callers
// that need the original error type match it with errors.As.
type TeamError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *TeamError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("status code %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("status code %d", e.StatusCode)
}

// checkTeamError returns a TeamError when rsp's error body carries a
// machine code. Otherwise it restores the body and falls back to the
// shared status mapping.
func checkTeamError(rsp *http.Response) error {
	data, err := io.ReadAll(rsp.Body)
	if err != nil {
		return checkStatus(rsp)
	}
	var e proto.Error
	if json.Unmarshal(data, &e) == nil && e.Code != "" {
		return &TeamError{StatusCode: rsp.StatusCode, Code: e.Code, Message: e.Message}
	}
	rsp.Body = io.NopCloser(bytes.NewReader(data))
	return checkStatus(rsp)
}
