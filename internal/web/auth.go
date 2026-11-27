package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	// streamProtocol is the subprotocol the stream speaks. The page offers
	// it beside the token entry, and the gateway selects it.
	streamProtocol = "crush.v2"
	// authProtocolPrefix carries the token on the WebSocket upgrade, where
	// a browser cannot set an Authorization header.
	authProtocolPrefix = "crush.auth."
)

const csp = "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; " +
	"connect-src 'self'; object-src 'none'; frame-ancestors 'none'"

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (g *Gateway) hostAllowed(host string) bool {
	port := strconv.Itoa(g.port)
	return host == "127.0.0.1:"+port || host == "localhost:"+port
}

// originAllowed checks Origin. Browsers send it on every request that can
// change state, so a state-changing request without one did not come from
// the tab and is refused. A missing Origin is allowed on GET, including
// the WebSocket upgrade: the upgrade now needs the token in a header a
// cross-site page cannot set, so a foreign reader cannot reach it anyway.
func (g *Gateway) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Scheme == "http" && g.hostAllowed(u.Host) && u.Host == r.Host
}

func (g *Gateway) tokenMatches(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(g.secret)) == 1
}

// requestToken returns the token a request presents: the bearer header,
// or on a WebSocket upgrade the crush.auth subprotocol entry. A query
// string never carries it, so the token stays out of URLs and logs.
func requestToken(r *http.Request) string {
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return ""
	}
	for _, field := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(field, ",") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(p), authProtocolPrefix); ok {
				return v
			}
		}
	}
	return ""
}

// guard applies the host check and security headers to every request.
func (g *Gateway) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// API answers carry session state and change with every
			// action, so no cache may keep them.
			h.Set("Cache-Control", "no-store")
		}
		if !g.hostAllowed(r.Host) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusForbidden, "forbidden_host", "host not allowed")
			} else {
				http.Error(w, "forbidden host", http.StatusForbidden)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth protects API routes: same-origin and a valid bearer token.
func (g *Gateway) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.originAllowed(r) {
			writeError(w, http.StatusForbidden, "forbidden_origin", "cross-origin request rejected")
			return
		}
		if !g.tokenMatches(requestToken(r)) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Gateway) handleAuth(w http.ResponseWriter, r *http.Request) {
	if !g.originAllowed(r) {
		writeError(w, http.StatusForbidden, "forbidden_origin", "cross-origin request rejected")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || !g.tokenMatches(body.Token) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	// The link has served its purpose once a tab signed in. Removing it
	// before the answer means the page cannot follow it after this
	// point, and the token outlives it only in the tab.
	g.removeLaunchFile()
	w.WriteHeader(http.StatusNoContent)
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: msg, Code: code})
}

// writeJSON answers v as JSON. The encoder leaves <, > and & alone: escaping
// them triples their size, which matters on a diff already near the cap.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
