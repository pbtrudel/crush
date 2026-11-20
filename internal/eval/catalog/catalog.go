// Package catalog pins the model catalogs a run reads from the eval home.
//
// A run's child has provider auto-update off, so Crush serves whatever
// provider catalog its data dir holds. The eval home's data dir must
// therefore hold the same providers.json and hyper.json the user's Crush
// downloaded, or a route's model is missing from the catalog and its
// requests fail.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/charmbracelet/crush/internal/eval/fsx"
	"github.com/charmbracelet/crush/internal/eval/home"
)

const (
	// ProvidersFile is the provider catalog Crush downloads: models, their
	// endpoints and their limits.
	ProvidersFile = "providers.json"
	// HyperFile is the Hyper catalog, which Crush keeps beside it.
	HyperFile = "hyper.json"
	// appName is Crush's directory name under the user's data dir.
	appName = "crush"
)

// Fingerprint names the catalogs a run used, by sha256, so two batches are
// known to have been served the same models.
type Fingerprint struct {
	Providers string `json:"providers"`
	Hyper     string `json:"hyper,omitempty"`
}

// Freeze copies the user's cached catalogs from their Crush data dir into the
// eval home's data dir, where every run's child reads them, and returns the
// fingerprint of what the eval home now serves. A catalog that is not valid
// JSON is refused: Crush would fall back to its embedded catalog, which is
// the failure a freeze exists to prevent.
func Freeze(h *home.Home, from string) (Fingerprint, error) {
	providers, err := os.ReadFile(filepath.Join(from, ProvidersFile))
	if err != nil {
		return Fingerprint{}, fmt.Errorf("read %s: %w", filepath.Join(from, ProvidersFile), err)
	}
	if err := validJSON(providers, ProvidersFile); err != nil {
		return Fingerprint{}, err
	}
	// The Hyper catalog is read before anything is written, so a freeze lands
	// both catalogs or changes nothing.
	hyper, err := os.ReadFile(filepath.Join(from, HyperFile))
	switch {
	case err == nil:
		if err := validJSON(hyper, HyperFile); err != nil {
			return Fingerprint{}, err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return Fingerprint{}, fmt.Errorf("read %s: %w", filepath.Join(from, HyperFile), err)
	}
	if err := fsx.WriteFileAtomic(filepath.Join(h.DataDir(), ProvidersFile), providers, 0o600); err != nil {
		return Fingerprint{}, err
	}
	target := filepath.Join(h.DataDir(), HyperFile)
	switch {
	case hyper != nil:
		if err := fsx.WriteFileAtomic(target, hyper, 0o600); err != nil {
			return Fingerprint{}, err
		}
	default:
		// The user has no Hyper catalog, so an earlier freeze's copy is stale
		// and would be recorded as the catalog the runs read.
		if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Fingerprint{}, fmt.Errorf("remove the stale %s: %w", HyperFile, err)
		}
	}
	return Read(h)
}

// validJSON refuses a catalog Crush would not read.
func validJSON(data []byte, name string) error {
	if !json.Valid(data) {
		return fmt.Errorf("%s is not valid JSON; let Crush rewrite it before freezing", name)
	}
	return nil
}

// Read fingerprints the catalogs the eval home serves, and refuses a home
// with no provider catalog: a run there would use Crush's embedded catalog,
// which lacks the models the routes name.
func Read(h *home.Home) (Fingerprint, error) {
	providers, err := sum(filepath.Join(h.DataDir(), ProvidersFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Fingerprint{}, fmt.Errorf("the eval home has no %s; run `crush-eval catalog freeze` first", ProvidersFile)
		}
		return Fingerprint{}, fmt.Errorf("read %s: %w", ProvidersFile, err)
	}
	hyper, err := sum(filepath.Join(h.DataDir(), HyperFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Fingerprint{}, fmt.Errorf("read %s: %w", HyperFile, err)
	}
	return Fingerprint{Providers: providers, Hyper: hyper}, nil
}

// UserDataDir is the directory holding the user's Crush data config and its
// catalog caches, resolved the way Crush resolves it: $CRUSH_GLOBAL_DATA,
// then $XDG_DATA_HOME, then LOCALAPPDATA on Windows and the home directory.
func UserDataDir() (string, error) {
	if dir := os.Getenv("CRUSH_GLOBAL_DATA"); dir != "" {
		return dir, nil
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, appName), nil
	}
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(local, appName), nil
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate the Crush data directory: %w", err)
	}
	return filepath.Join(dir, ".local", "share", appName), nil
}

// sum is the hex sha256 of one file.
func sum(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
