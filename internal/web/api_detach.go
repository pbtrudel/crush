package web

import "net/http"

// detachReason is the closing reason a browser detach reports to every
// tab, so the page can tell "web mode stopped" from "Crush exited".
const detachReason = "detached"

// handleDetach asks the terminal to stop web mode. It answers first and
// calls the callback from a goroutine: the callback shuts the gateway
// down, and the server's Shutdown waits for this handler to return, so
// the 202 is flushed before every stream closes.
func (g *Gateway) handleDetach(w http.ResponseWriter, _ *http.Request) {
	fn := g.onDetach
	if fn == nil {
		writeError(w, http.StatusServiceUnavailable, "no_terminal", "no terminal is attached to web mode")
		return
	}
	g.closingReason.Store(detachReason)
	w.WriteHeader(http.StatusAccepted)
	go fn()
}
