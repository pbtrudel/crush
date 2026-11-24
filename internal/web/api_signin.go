package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/workspace"
)

// keySignInTimeout bounds the provider check a key sign-in waits for. The
// request itself is the user waiting, so a provider that never answers
// must not hold the tab forever.
const keySignInTimeout = 30 * time.Second

// registerSignInRoutes installs the sign-in routes: the providers a tab can
// sign in to and the API key form.
func (g *Gateway) registerSignInRoutes(api *http.ServeMux) {
	g.route(api, "GET /api/auth/providers", g.handleSignInProviders)
	g.route(api, "POST /api/auth/provider/key", g.handleAPIKeySignIn)
}

// knownProviders lists the provider catalog the gateway's seam answers for
// cfg.
func (g *Gateway) knownProviders(cfg *config.Config) ([]catwalk.Provider, error) {
	return g.knownProvidersFn(cfg)
}

// defaultKnownProviders is the real catalog loader. A workspace without a
// config yet has no catalog to load, which the browser shows as unavailable
// rather than a crash.
func defaultKnownProviders(cfg *config.Config) ([]catwalk.Provider, error) {
	if cfg == nil {
		return nil, errors.New("the workspace has no configuration")
	}
	return config.Providers(cfg)
}

// handleSignInProviders lists every provider a tab can sign in to, with the
// methods it offers.
func (g *Gateway) handleSignInProviders(w http.ResponseWriter, _ *http.Request) {
	cfg := g.ws.Config()
	known, err := g.knownProviders(cfg)
	if len(known) == 0 {
		if err != nil {
			slog.Warn("Web sign-in providers unavailable", "error", err)
		}
		writeError(w, http.StatusBadGateway, "providers_unavailable", "The provider list is not available yet.")
		return
	}
	targets := workspace.SignInTargets(cfg, known)
	if targets == nil {
		targets = []workspace.SignInTarget{}
	}
	writeJSON(w, http.StatusOK, map[string][]workspace.SignInTarget{"providers": targets})
}

// keySignInResponse is what a key sign-in answers. The key itself never
// appears in it.
type keySignInResponse struct {
	SignedIn     bool   `json:"signed_in"`
	ModelApplied bool   `json:"model_applied"`
	Reason       string `json:"reason,omitempty"`
}

// handleAPIKeySignIn verifies a provider API key and saves it in one
// request. The key is read from the body only and is never echoed or
// logged, including when the provider rejects it.
func (g *Gateway) handleAPIKeySignIn(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "secret_in_url", "secrets are accepted in the request body only")
		return
	}
	var body struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		Model    *struct {
			Type  string `json:"type"`
			Model string `json:"model"`
		} `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSecretBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return
	}

	cfg := g.ws.Config()
	known, _ := g.knownProviders(cfg)
	targets := workspace.SignInTargets(cfg, known)
	idx := slices.IndexFunc(targets, func(t workspace.SignInTarget) bool { return t.ID == body.Provider })
	if idx < 0 {
		writeError(w, http.StatusNotFound, "unknown_provider", "the provider is not one Crush can sign in to")
		return
	}
	target := targets[idx]
	if !slices.Contains(target.Methods, "api_key") {
		writeError(w, http.StatusBadRequest, "api_key_not_supported", "the provider signs in with OAuth only")
		return
	}

	key := strings.TrimSpace(body.APIKey)
	if key == "" || len(key) > maxSecretBytes {
		writeError(w, http.StatusBadRequest, "bad_api_key", "send an API key of at most 4096 bytes")
		return
	}

	var modelType config.SelectedModelType
	if body.Model != nil {
		var ok bool
		modelType, ok = selectedModelType(body.Model.Type)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_type", "type must be main or small")
			return
		}
	}

	var provider catwalk.Provider
	for _, p := range known {
		if string(p.ID) == body.Provider {
			provider = p
			break
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), keySignInTimeout)
	defer cancel()
	if err := g.testAPIKeyFn(ctx, provider, key, g.ws.Resolver()); err != nil {
		// Providers echo part of a rejected key in their error text, so the
		// detail stays out of the log as well as the answer.
		slog.Info("Web sign-in key rejected", "provider", body.Provider)
		writeError(w, http.StatusUnprocessableEntity, "key_rejected", "The provider did not accept this key.")
		return
	}

	if err := g.ws.SetProviderAPIKey(config.ScopeGlobal, body.Provider, key); err != nil {
		writeWorkspaceError(w, "save_api_key", err)
		return
	}

	resp := keySignInResponse{SignedIn: true}
	if body.Model != nil {
		selected := config.SelectedModel{Provider: body.Provider, Model: body.Model.Model}
		if err := workspace.SelectModel(r.Context(), g.ws, modelType, selected); err != nil {
			resp.Reason = signInModelReason(err)
			if resp.Reason == "model_failed" {
				slog.Warn("Web sign-in model not applied", "provider", body.Provider, "error", err)
			}
		} else {
			resp.ModelApplied = true
			g.announceModel()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// signInModelReason maps a failed model switch to the code a tab shows.
func signInModelReason(err error) string {
	switch {
	case errors.Is(err, workspace.ErrAgentBusy):
		return "agent_busy"
	case errors.Is(err, workspace.ErrAgentNotInitialized):
		return "agent_starting"
	case errors.Is(err, workspace.ErrModelUnavailable):
		return "model_unavailable"
	}
	return "model_failed"
}

// testAPIKey is the default provider check: the catalog entry becomes the
// provider config the TUI's key dialog tests, so both clients ask the same
// question.
func testAPIKey(_ context.Context, p catwalk.Provider, key string, r config.VariableResolver) error {
	pc := config.ProviderConfig{
		ID:      string(p.ID),
		Name:    p.Name,
		APIKey:  key,
		Type:    p.Type,
		BaseURL: p.APIEndpoint,
	}
	return pc.TestConnection(r)
}
