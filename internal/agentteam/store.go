package agentteam

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// loadState reads the durable Team record. A missing file is an empty Team.
func loadState(path string) (State, error) {
	if path == "" {
		return State{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read team state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode team state: %w", err)
	}
	return state, nil
}

// saveState writes the durable Team record atomically.
func saveState(path string, state State) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create team state directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode team state: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".team-*.json")
	if err != nil {
		return fmt.Errorf("create team state temp file: %w", err)
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write team state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close team state: %w", err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("chmod team state: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace team state: %w", err)
	}
	return nil
}

// LoadState reads a Team record for read-only consumers such as the CLI.
func LoadState(path string) (State, error) {
	return loadState(path)
}
