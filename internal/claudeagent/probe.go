package claudeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// minimumVersion is the oldest CLI this provider supports. Older binaries
// silently ignore `client_composed`, which would let Claude Code interpret
// Crush's text as slash commands.
const minimumVersion = "2.1.248"

// catalogFile is where the model list is cached between runs.
const catalogFile = "models.json"

// CatalogModel is one model the user's login can run.
type CatalogModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Resolved      string   `json:"resolved,omitempty"`
	ContextWindow int64    `json:"context_window,omitempty"`
	Efforts       []string `json:"efforts,omitempty"`
}

// Catalog is what the CLI reported about the login and its models, saved so
// Crush can list models at startup without starting a process.
type Catalog struct {
	Version   string         `json:"version"`
	Account   accountInfo    `json:"account"`
	Models    []CatalogModel `json:"models"`
	Refreshed time.Time      `json:"refreshed"`
}

// Probe asks the CLI what it can run. It never sends a prompt, so no tokens are
// used and no model call is made.
func Probe(ctx context.Context, executable, dir string, conn Connection) (*Catalog, error) {
	cfg := launchConfig{
		Executable: executable,
		Dir:        dir,
		SessionID:  newUUID(),
		Persist:    false,
		Connection: conn,
	}
	proc, init, err := startProcess(ctx, cfg, nil)
	if err != nil {
		return nil, err
	}
	defer proc.stop(context.WithoutCancel(ctx))

	if err := checkVersion(init.ClaudeCodeVersion); err != nil {
		return nil, err
	}
	return catalogFrom(init), nil
}

// catalogFrom turns a process's startup report into a catalog.
func catalogFrom(init *initializeResult) *Catalog {
	if init == nil {
		return nil
	}
	catalog := &Catalog{
		Version:   init.ClaudeCodeVersion,
		Account:   init.Account,
		Refreshed: time.Now(),
	}
	for _, model := range init.Models {
		// "default" follows Claude Code's own default, which can change under a
		// running session, so it is not offered as a Crush model.
		if model.Value == "" || model.Value == "default" {
			continue
		}
		catalog.Models = append(catalog.Models, CatalogModel{
			ID:       model.Value,
			Name:     model.DisplayName,
			Resolved: model.ResolvedModel,
			Efforts:  model.SupportedEffortLevels,
		})
	}
	if len(catalog.Models) == 0 {
		return nil
	}
	return catalog
}

// checkVersion rejects a CLI too old to honor the flags this provider relies on.
func checkVersion(version string) error {
	if version == "" {
		return fmt.Errorf("the installed Claude Code did not report its version; update it with `claude update`")
	}
	if atLeast(version, minimumVersion) {
		return nil
	}
	return fmt.Errorf("the installed Claude Code (%s) is too old; %s or later is required. Run `claude update`", version, minimumVersion)
}

// atLeast compares release numbers like 2.1.248. A pre-release or build suffix
// is ignored.
func atLeast(version, minimum string) bool {
	parse := func(value string) []int {
		if i := strings.IndexAny(value, "-+"); i >= 0 {
			value = value[:i]
		}
		parts := strings.Split(value, ".")
		out := make([]int, len(parts))
		for i, part := range parts {
			out[i], _ = strconv.Atoi(part)
		}
		return out
	}
	current := parse(version)
	required := parse(minimum)
	for i, want := range required {
		have := 0
		if i < len(current) {
			have = current[i]
		}
		if have != want {
			return have > want
		}
	}
	return true
}

// LoadCatalog reads the saved catalog, or returns nil when there is none.
func LoadCatalog(dir string) *Catalog {
	body, err := os.ReadFile(filepath.Join(dir, catalogFile))
	if err != nil {
		return nil
	}
	var catalog Catalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil
	}
	if len(catalog.Models) == 0 {
		return nil
	}
	return &catalog
}

// SaveCatalog caches the catalog.
func SaveCatalog(dir string, catalog *Catalog) error {
	if catalog == nil {
		return nil
	}
	body, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, catalogFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// IsSubscription reports whether the login is a Claude plan rather than a
// metered API key, which is what makes usage free at the point of use. A plan
// login reports no API key source; the token source is often omitted.
func (c *Catalog) IsSubscription() bool {
	if c == nil {
		return false
	}
	return c.Account.APIKeySource == "" && c.Account.TokenSource != "none"
}
