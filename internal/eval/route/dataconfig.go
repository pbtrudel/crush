package route

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/charmbracelet/crush/internal/eval/home"
)

// signInFields are the data config fields an interactive `crush login` leaves
// behind: a model selection, its recent models and UI options. A run names its
// model on the command line, and a pinned one the route does not configure
// fails every run with "model provider not configured".
var signInFields = []string{"models", "recent_models", "options"}

// StripDataConfig removes those fields from the eval home's data config after
// a sign-in, atomically and with mode 0o600. Providers and every other field
// stay as they are, byte for byte.
func StripDataConfig(h *home.Home) error {
	path := filepath.Join(h.DataDir(), "crush.json")
	doc, err := readDataConfig(path)
	if err != nil || doc == nil {
		return err
	}
	changed := false
	for _, field := range signInFields {
		if _, ok := doc[field]; ok {
			delete(doc, field)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the eval home data config: %w", err)
	}
	return writeFileAtomic(path, append(raw, '\n'))
}

// CheckDataConfig refuses a run while the eval home's data config pins a
// model: that outranks the route's own model on the command line, and fails
// the run when the route does not configure the provider it names. The file
// is not edited here, because a batch may be reading it.
func CheckDataConfig(h *home.Home) error {
	path := filepath.Join(h.DataDir(), "crush.json")
	doc, err := readDataConfig(path)
	if err != nil || doc == nil {
		return err
	}
	if _, ok := doc["models"]; ok {
		return errors.New("the eval home data config holds a model selection in `models`; run `crush-eval login <route>` to clear it")
	}
	return nil
}

// readDataConfig reads the top-level fields of a data config, or nil when the
// file does not exist. Values stay raw, so the providers object and unknown
// fields keep their exact bytes.
func readDataConfig(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read the eval home data config: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the eval home data config: %w", err)
	}
	return doc, nil
}

// writeFileAtomic writes data beside path, then renames it into place with
// mode 0o600, so a crash cannot leave half a config behind.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".crush-*.json")
	if err != nil {
		return fmt.Errorf("create the temporary config: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // Only there when the rename failed.
	if _, err := tmp.Write(data); err != nil {
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
