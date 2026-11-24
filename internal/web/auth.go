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

// cookieNamePrefix prefixes the per-gateway cookie. Cookies ignore ports,
// so a fixed name would let a second gateway overwrite the first tab's
// cookie and log it out.
const cookieNamePrefix = "crush_web_"

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
// the WebSocket upgrade: the Host check and SameSite=Strict already cover
// a cross-site reader that cannot set the header.
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

// requireAuth protects API routes: same-origin and a valid cookie.
func (g *Gateway) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.originAllowed(r) {
			writeError(w, http.StatusForbidden, "forbidden_origin", "cross-origin request rejected")
			return
		}
		c, err := r.Cookie(g.cookieName)
		if err != nil || !g.tokenMatches(c.Value) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid session cookie")
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
	http.SetCookie(w, &http.Cookie{
		Name:     g.cookieName,
		Value:    g.secret,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
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
