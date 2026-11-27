package web

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/charmbracelet/crush/internal/config"
)

// DisplaySettings is the display state of the web mode tab: what the
// settings dialog writes, what GET /api/config answers and what the
// display_changed frame carries. The empty Motion follows the browser's
// reduced motion setting.
type DisplaySettings struct {
	Theme         string `json:"theme"`
	Motion        string `json:"motion"`
	LowPower      bool   `json:"low_power"`
	CinematicHint bool   `json:"cinematic_hint"`
}

// displayDefaults is what each setting falls back to when its key is
// absent, and what a remove leaves behind.
var displayDefaults = DisplaySettings{
	Theme:         "holodeck",
	Motion:        "",
	LowPower:      false,
	CinematicHint: true,
}

// displayFromConfig reads the tab's settings from the config, defaulting
// every unset field. A missing config, options block or web_display block
// means the defaults.
func displayFromConfig(cfg *config.Config) DisplaySettings {
	d := displayDefaults
	if cfg == nil || cfg.Options == nil || cfg.Options.WebDisplay == nil {
		return d
	}
	o := cfg.Options.WebDisplay
	if o.Theme != "" {
		d.Theme = o.Theme
	}
	if o.Motion != "" {
		d.Motion = o.Motion
	}
	if o.LowPower != nil {
		d.LowPower = *o.LowPower
	}
	if o.CinematicHint != nil {
		d.CinematicHint = *o.CinematicHint
	}
	return d
}

// enumString checks that raw is one of the allowed strings.
func enumString(allowed ...string) func(json.RawMessage) (any, bool) {
	return func(raw json.RawMessage) (any, bool) {
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return nil, false
		}
		s, ok := v.(string)
		if !ok || !slices.Contains(allowed, s) {
			return nil, false
		}
		return s, true
	}
}

// boolValue checks that raw is a JSON boolean. A null or a number is not
// one, even though encoding/json would fold either into false.
func boolValue(raw json.RawMessage) (any, bool) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	b, ok := v.(bool)
	return b, ok
}

// displayKeys is the only config the browser may write: its own display
// settings, each with its value check.
//
// Both write routes touch the global config only. A web_display block in
// a project-scope config still overrides what a tab writes here, and a
// remove cannot clear the project's value. That limit is accepted:
// display settings describe the person at the browser, not the
// repository, and the web UI never writes project scope, so a project
// that pins web_display does so on purpose and keeps it.
var displayKeys = map[string]func(json.RawMessage) (any, bool){
	"options.web_display.theme":          enumString("holodeck", "solid"),
	"options.web_display.motion":         enumString("full", "reduced", "off"),
	"options.web_display.low_power":      boolValue,
	"options.web_display.cinematic_hint": boolValue,
}

// applyDisplay writes one allowed key, already checked by displayKeys,
// into d. Every key in displayKeys has a case here.
func applyDisplay(d *DisplaySettings, key string, v any) {
	switch key {
	case "options.web_display.theme":
		d.Theme = v.(string)
	case "options.web_display.motion":
		d.Motion = v.(string)
	case "options.web_display.low_power":
		d.LowPower = v.(bool)
	case "options.web_display.cinematic_hint":
		d.CinematicHint = v.(bool)
	}
}

// displayDefault returns the value one allowed key falls back to, which
// is what a remove leaves in its place. Every key in displayKeys has a
// case here.
func displayDefault(key string) any {
	switch key {
	case "options.web_display.theme":
		return displayDefaults.Theme
	case "options.web_display.motion":
		return displayDefaults.Motion
	case "options.web_display.low_power":
		return displayDefaults.LowPower
	case "options.web_display.cinematic_hint":
		return displayDefaults.CinematicHint
	}
	return nil
}

// currentDisplay is the copy the gateway last wrote, seeded from the
// config at start. It is read under displayMu so a concurrent set cannot
// tear it.
func (g *Gateway) currentDisplay() DisplaySettings {
	g.displayMu.Lock()
	defer g.displayMu.Unlock()
	return g.display
}

// handleConfigGet answers the settings the tab renders at start, plus
// whether Agent Teams is on.
func (g *Gateway) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	teams := false
	if cfg := g.ws.Config(); cfg != nil {
		teams = cfg.Options.GetAgentTeams()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"display":     g.currentDisplay(),
		"agent_teams": teams,
	})
}

// handleConfigSet writes one of the tab's display settings and tells every
// tab the new set. It holds displayMu across the write, the apply and the
// publish, so two quick writes can never leave the disk and g.display
// disagreeing nor publish an older set after a newer one.
func (g *Gateway) handleConfigSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	decode, ok := displayKeys[body.Key]
	if !ok {
		writeError(w, http.StatusBadRequest, "key_not_allowed", "only the web display settings may be written")
		return
	}
	v, ok := decode(body.Value)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_value", "the value is not valid for this setting")
		return
	}
	g.displayMu.Lock()
	defer g.displayMu.Unlock()
	if err := g.ws.SetConfigField(config.ScopeGlobal, body.Key, v); err != nil {
		writeInternal(w, "config_write", err)
		return
	}
	applyDisplay(&g.display, body.Key, v)
	g.hub.publishJSON(frameDisplayChanged, g.display)
	writeJSON(w, http.StatusOK, g.display)
}

// handleConfigRemove removes one of the tab's display settings, which
// returns it to its default, and tells every tab the new set. It holds
// displayMu for the same reason handleConfigSet does. The remove only
// touches the global config, so it publishes the default even when a
// project-scope web_display block overrides the global one.
func (g *Gateway) handleConfigRemove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if _, ok := displayKeys[body.Key]; !ok {
		writeError(w, http.StatusBadRequest, "key_not_allowed", "only the web display settings may be written")
		return
	}
	g.displayMu.Lock()
	defer g.displayMu.Unlock()
	if err := g.ws.RemoveConfigField(config.ScopeGlobal, body.Key); err != nil {
		writeInternal(w, "config_write", err)
		return
	}
	applyDisplay(&g.display, body.Key, displayDefault(body.Key))
	g.hub.publishJSON(frameDisplayChanged, g.display)
	writeJSON(w, http.StatusOK, g.display)
}
