package route

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/charmbracelet/crush/internal/eval/fsx"
	"github.com/charmbracelet/crush/internal/eval/home"
)

// signInFields are the data config fields an interactive `crush login`
// leaves behind: a model selection, its recent models and UI options. A run
// names its model on the command line, and a pinned one the route does not
// configure fails every run with "model provider not configured".
var signInFields = []string{"models", "recent_models", "options"}

// StripDataConfig removes those fields from the eval home's data config after
// a sign-in, or before a run whose child may have written them back, and does
// it atomically and with mode 0o600. Providers and every other field
// keep their values: the file is re-encoded as indented JSON, so key order,
// whitespace and JSON escaping can change, and nothing else does.
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
	return fsx.WriteFileAtomic(path, append(raw, '\n'), 0o600)
}

// readDataConfig reads the top-level fields of a data config, or nil when the
// file does not exist. Values stay raw, so removing a field changes no value:
// only key order, indentation and JSON escaping change.
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
