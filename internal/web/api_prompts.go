package web

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/charmbracelet/crush/internal/sshaskpass"
)

const (
	maxSecretBody  = 8 << 10
	maxSecretBytes = 4096
)

func (g *Gateway) registerPromptRoutes(api *http.ServeMux) {
	g.route(api, "POST /api/pinentry/answer", g.handleSecureAnswer(sourcePinentry))
	g.route(api, "POST /api/pinentry/cancel", g.handleSecureCancel(sourcePinentry))
	g.route(api, "POST /api/ssh/answer", g.handleSecureAnswer(sourceSSH))
	g.route(api, "POST /api/ssh/cancel", g.handleSecureCancel(sourceSSH))
}

// handleSecureAnswer delivers a secret to the open prompt. The secret is
// read from the body only, is never logged, and no answer echoes it.
func (g *Gateway) handleSecureAnswer(source string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "secret_in_url", "secrets are accepted in the request body only")
			return
		}
		var body struct {
			ID     string  `json:"id"`
			Secret *string `json:"secret"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSecretBody)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		if body.Secret != nil && len(*body.Secret) > maxSecretBytes {
			writeError(w, http.StatusBadRequest, "secret_too_long", "the answer is too long")
			return
		}
		p, ok := g.pending.securePrompt(source, body.ID)
		if !ok {
			writeError(w, http.StatusConflict, "not_pending", "The prompt was already answered or has expired.")
			return
		}
		var secret string
		switch p.Kind {
		case string(sshaskpass.KindTouch):
			writeError(w, http.StatusConflict, "touch_not_answerable", "Touch the security key to continue.")
			return
		case string(sshaskpass.KindConfirm):
			secret = "yes"
		default:
			if body.Secret == nil {
				writeError(w, http.StatusBadRequest, "missing_secret", "the answer belongs in the request body")
				return
			}
			secret = *body.Secret
		}
		var resolved bool
		if source == sourcePinentry {
			resolved = g.ws.PinentryRespond(p.ID, secret)
		} else {
			resolved = g.ws.SSHRespond(p.ID, secret)
		}
		if !resolved {
			writeError(w, http.StatusConflict, "not_pending", "The prompt was already answered or has expired.")
			return
		}
		if g.pending.forgetSecure(source, p.ID) {
			g.publishSecurePrompts()
		}
		slog.Debug("Web secure prompt answered", "source", source, "id", p.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleSecureCancel dismisses an open prompt. The body carries only the
// id, so a malformed body cannot leak an answer into a log or a response.
func (g *Gateway) handleSecureCancel(source string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		p, ok := g.pending.securePrompt(source, body.ID)
		if !ok {
			writeError(w, http.StatusConflict, "not_pending", "The prompt was already answered or has expired.")
			return
		}
		// A touch prompt is answered by the key itself, as the answer
		// route says, so the tab cannot dismiss it either.
		if p.Kind == string(sshaskpass.KindTouch) {
			writeError(w, http.StatusConflict, "touch_not_answerable", "Touch the security key to continue.")
			return
		}
		var cancelled bool
		if source == sourcePinentry {
			cancelled = g.ws.PinentryCancel(p.ID)
		} else {
			cancelled = g.ws.SSHCancel(p.ID)
		}
		if !cancelled {
			writeError(w, http.StatusConflict, "not_pending", "The prompt was already answered or has expired.")
			return
		}
		if g.pending.forgetSecure(source, p.ID) {
			g.publishSecurePrompts()
		}
		slog.Debug("Web secure prompt cancelled", "source", source, "id", p.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}
