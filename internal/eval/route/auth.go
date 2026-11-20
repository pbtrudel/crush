package route

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	hyperp "github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/eval/fsx"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/copilot"
	hyperoauth "github.com/charmbracelet/crush/internal/oauth/hyper"
)

// authMargin is the least time a run's token must outlive the run. A token
// that dies inside a run cannot be renewed there: the jail keeps the Copilot
// token endpoint on api.github.com blocked. Crush itself starts refreshing at
// a tenth of the lifetime before expiry, so the real buffer is the larger of
// this and that window.
const authMargin = 5 * time.Minute

// copilotTokenLifetime is about how long a Copilot token lives, and
// copilotRunLimit is the longest a run can therefore last: the lifetime minus
// the buffer it must leave.
const (
	copilotTokenLifetime = 30 * time.Minute
	copilotRunLimit      = copilotTokenLifetime - authMargin
)

// RefreshTokenFunc exchanges a provider's refresh token for a new token. It
// is Crush's own exchange, so the runner renews exactly what a run would.
type RefreshTokenFunc func(ctx context.Context, provider, refreshToken string) (*oauth.Token, error)

// DefaultRefreshToken is the exchange Crush performs for each provider
// that signs in through OAuth.
func DefaultRefreshToken(ctx context.Context, provider, refreshToken string) (*oauth.Token, error) {
	switch provider {
	case ProviderCopilot:
		return copilot.RefreshToken(ctx, refreshToken)
	case hyperp.Name:
		return hyperoauth.ExchangeToken(ctx, refreshToken)
	}
	return nil, fmt.Errorf("no OAuth refresh for provider %s", provider)
}

// PrepareToken gets the token of a jailed run ready before the child starts.
// Only Copilot needs it: it renews its token at api.github.com, which the
// jail blocks. Hyper renews at its API host, which the jail allows, so a
// Hyper run refreshes itself.
//
// A token is exchanged when Crush would exchange it and also when it cannot
// cover the run plus the buffer Crush keeps before expiry: the run cannot
// renew it later. Only a fresh token that is still too short fails the run.
//
// The token is written to the run's workspace config, which Crush reads last
// and therefore over the eval home's data config, so the child keeps it for
// the whole run.
func (r *Route) PrepareToken(ctx context.Context, h *home.Home, runDataDir string, timeout time.Duration, refresh RefreshTokenFunc) error {
	if r.Provider != ProviderCopilot {
		return nil
	}
	token, err := StoredToken(h, r.Provider)
	if err != nil {
		return fmt.Errorf("route %s: sign in once with `crush-eval login %s`: %w", r.Name, r.Name, err)
	}
	margin := tokenMargin(token)
	if token.IsExpired() || token.ExpiresAt <= time.Now().Add(timeout+margin).Unix() {
		token, err = refresh(ctx, r.Provider, token.RefreshToken)
		if err != nil {
			return fmt.Errorf("route %s: refresh the %s token: %w", r.Name, r.Provider, err)
		}
		if token == nil {
			return fmt.Errorf("route %s: refresh the %s token returned no token", r.Name, r.Provider)
		}
		margin = tokenMargin(token)
		if token.ExpiresAt <= time.Now().Add(timeout+margin).Unix() {
			return fmt.Errorf("route %s: the %s token expires in %s, before the run's timeout plus the %s buffer; a %s run must stay under about %s, since its token lives about %s; sign in again with `crush-eval login %s`",
				r.Name, r.Provider, time.Until(time.Unix(token.ExpiresAt, 0)).Round(time.Minute), margin.Round(time.Second),
				r.Provider, copilotRunLimit, copilotTokenLifetime, r.Name)
		}
	}
	// The pinned copy is the child's for the whole run and cannot renew
	// anything, so the long-lived GitHub token stays out of the run directory,
	// which outlives the run.
	pinned := *token
	pinned.RefreshToken = ""
	return writeTokenFile(filepath.Join(runDataDir, "crush.json"), r.Provider, &pinned)
}

// tokenMargin is how long before a token's expiry the run must be over: Crush
// starts refreshing at a tenth of the lifetime, and the jail blocks that
// refresh, so the run keeps authMargin on top of it.
func tokenMargin(token *oauth.Token) time.Duration {
	return max(authMargin, time.Duration(token.ExpiresIn/10)*time.Second+30*time.Second)
}

// StoredToken reads the OAuth token `crush-eval login` stored for the
// provider in the eval home's data config. The token is returned, never
// printed.
func StoredToken(h *home.Home, provider string) (*oauth.Token, error) {
	path := filepath.Join(h.DataDir(), "crush.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no stored token for %s", provider)
		}
		return nil, fmt.Errorf("read the stored token: %w", err)
	}
	var doc struct {
		Providers map[string]struct {
			OAuth *oauth.Token `json:"oauth"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the stored token: %w", err)
	}
	token := doc.Providers[provider].OAuth
	if token == nil || token.AccessToken == "" {
		return nil, fmt.Errorf("no stored token for %s", provider)
	}
	return token, nil
}

// writeTokenFile sets providers.<provider>.api_key and .oauth in the config
// file at path, creating it when the run has not written one yet.
func writeTokenFile(path, provider string, token *oauth.Token) error {
	doc := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("read %s: %w", path, err)
	}

	providers, _ := doc["providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
		doc["providers"] = providers
	}
	entry, _ := providers[provider].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		providers[provider] = entry
	}
	entry["api_key"] = token.AccessToken
	entry["oauth"] = token

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return fsx.WriteFileAtomic(path, append(raw, '\n'), 0o600)
}
