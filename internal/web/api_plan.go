package web

import "net/http"

// registerPlanRoutes installs the routes that ask the terminal to act on a
// ready plan.
func (g *Gateway) registerPlanRoutes(api *http.ServeMux) {
	g.route(api, "POST /api/plan/handoff", g.handlePlanHandoff)
}

// handlePlanHandoff asks the terminal to confirm the pending ready plan and
// switch to code mode. The terminal owns the busy check and the switch, and
// it reports the cleared plan back, so the gateway never clears it here.
func (g *Gateway) handlePlanHandoff(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		Action    string `json:"action"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	yolo := body.Action == "code_yolo"
	if body.Action != "code" && body.Action != "code_yolo" {
		writeError(w, http.StatusBadRequest, "bad_action", "action must be code or code_yolo")
		return
	}
	// Only the plan the terminal reported can be confirmed: a stale tab
	// must not switch a plan the user has since dismissed or replaced.
	if body.SessionID == "" || body.SessionID != g.PlanReadySessionID() {
		writeError(w, http.StatusConflict, "plan_not_ready", "no ready plan to hand off")
		return
	}
	if !g.notifyPlanHandoff(body.SessionID, yolo) {
		writeError(w, http.StatusServiceUnavailable, "agent_starting", agentStartingText)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
