package multiturn

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/eval/route"
)

func neuralwattRoute() *route.Route {
	return &route.Route{
		Name:       "deepseek-flash-nw-ctx",
		Provider:   "neuralwatt",
		Model:      "deepseek-v4.1-flash",
		SmallModel: "deepseek-v4.1-flash",
		ProviderConfig: map[string]any{
			"type":     "openai-compat",
			"base_url": "https://api.neuralwatt.com/v1",
			"api_key":  "$NEURALWATT_API_KEY",
		},
	}
}

// runConfigDir writes the config a run gets, the same way the driver does:
// the route's provider first, then the models the server needs.
func runConfigDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "config", "crush")
	path := filepath.Join(dir, "crush.json")
	if err := route.WriteGlobalConfig(path, neuralwattRoute(), nil); err != nil {
		t.Fatalf("write the run config: %v", err)
	}
	if err := writeServerModels(path, neuralwattRoute()); err != nil {
		t.Fatalf("write the run models: %v", err)
	}
	return dir
}

// sharedDataDir is an eval home's data directory whose config names another
// model, which is what a shared home held after a session selected one.
func sharedDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	doc := map[string]any{
		"models": map[string]any{"main": map[string]any{"provider": "hyper", "model": "qwen3.8-max"}},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode the shared config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "crush.json"), raw, 0o600); err != nil {
		t.Fatalf("write the shared config: %v", err)
	}
	return dir
}

// loadMainModel resolves the config the way an agent does, so the test reads
// the precedence Crush applies rather than the precedence this file assumes.
func loadMainModel(t *testing.T, configDir, workDir string) (provider, model string) {
	t.Helper()
	store, err := config.Load(context.Background(), workDir, filepath.Join(t.TempDir(), "data"), false)
	if err != nil {
		t.Fatalf("load the config: %v", err)
	}
	main := store.Config().Models[config.SelectedModelTypeMain]
	return main.Provider, main.Model
}

// TestPinnedDataDirKeepsTheRouteModel is the regression test for eight runs of
// one batch answering on another model: the data directory's config outranks
// the run's own, so a shared one has to be replaced, not merged.
func TestPinnedDataDirKeepsTheRouteModel(t *testing.T) {
	configDir := runConfigDir(t)
	shared := sharedDataDir(t)
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".git"), 0o700); err != nil {
		t.Fatalf("create the checkout: %v", err)
	}
	t.Setenv("CRUSH_GLOBAL_CONFIG", configDir)
	t.Setenv("CRUSH_GLOBAL_DATA", shared)
	t.Setenv("HOME", t.TempDir())

	if provider, model := loadMainModel(t, configDir, workDir); provider != "hyper" || model != "qwen3.8-max" {
		t.Fatalf("the shared config should win before pinning, got %s/%s", provider, model)
	}

	env, err := pinDataDir([]string{"HOME=/tmp/x", "CRUSH_GLOBAL_DATA=" + shared}, workDir)
	if err != nil {
		t.Fatalf("pin the data directory: %v", err)
	}
	var pinned string
	for _, kv := range env {
		if strings.HasPrefix(kv, "CRUSH_GLOBAL_DATA=") {
			if pinned != "" {
				t.Fatalf("the pinned environment carries CRUSH_GLOBAL_DATA twice: %v", env)
			}
			pinned = strings.TrimPrefix(kv, "CRUSH_GLOBAL_DATA=")
		}
	}
	if pinned == shared || pinned == "" {
		t.Fatalf("the shared data directory is still in the environment: %q", pinned)
	}
	if !slices.Contains(env, "HOME=/tmp/x") {
		t.Fatalf("pinning dropped the rest of the environment: %v", env)
	}
	if _, err := os.Stat(pinned); err != nil {
		t.Fatalf("the pinned data directory does not exist: %v", err)
	}
	t.Setenv("CRUSH_GLOBAL_DATA", pinned)
	if provider, model := loadMainModel(t, configDir, workDir); provider != "neuralwatt" || model != "deepseek-v4.1-flash" {
		t.Fatalf("the route model should win after pinning, got %s/%s", provider, model)
	}
}
