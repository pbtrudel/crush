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
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/copilot"
	hyperoauth "github.com/charmbracelet/crush/internal/oauth/hyper"
)

// authMargin is how long past a run's timeout its token must stay valid. A
// token that dies inside a run cannot be renewed there: the jail keeps the
// Copilot token endpoint on api.github.com blocked.
const authMargin = 5 * time.Minute

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
// A token Crush would still accept is never exchanged. A token that would
// expire before the run's timeout plus authMargin fails the run instead of
// starting it, because the child could not renew it in time.
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
	if token.IsExpired() {
		token, err = refresh(ctx, r.Provider, token.RefreshToken)
		if err != nil {
			return fmt.Errorf("route %s: refresh the %s token: %w", r.Name, r.Provider, err)
		}
	}
	if deadline := time.Now().Add(timeout + authMargin); token.ExpiresAt <= deadline.Unix() {
		return fmt.Errorf("route %s: %s token expires in %s, before the run's timeout plus %s; sign in again with `crush-eval login %s`",
			r.Name, r.Provider, time.Until(time.Unix(token.ExpiresAt, 0)).Round(time.Minute), authMargin, r.Name)
	}
	return writeTokenFile(filepath.Join(runDataDir, "crush.json"), r.Provider, token)
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

// StoreToken writes a refreshed token into the eval home's data config, the
// way Crush persists one, and keeps every other field of the file.
func StoreToken(h *home.Home, provider string, token *oauth.Token) error {
	return writeTokenFile(filepath.Join(h.DataDir(), "crush.json"), provider, token)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	// Write beside the target and rename, so a crash cannot leave half a
	// config behind for the child to read.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".crush-*.json")
	if err != nil {
		return fmt.Errorf("create the token file: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // Only there when the rename failed.
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tighten %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}
