package agentteam

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// fileStamp identifies one version of the state file. A write replaces the
// file, so its identity changes with it: an equal stamp means no other
// process wrote since this one last read.
type fileStamp struct {
	info os.FileInfo
	size int64
	mod  time.Time
}

// equal reports whether two stamps name the same bytes of the same file.
func (s fileStamp) equal(other fileStamp) bool {
	if s.info == nil || other.info == nil {
		return s.info == nil && other.info == nil
	}
	return os.SameFile(s.info, other.info) && s.size == other.size && s.mod.Equal(other.mod)
}

// statState reads the current stamp of the state file.
func statState(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	// Windows fills in the file id on the first SameFile call, not on the
	// stat, so stamping without this would leave size and mtime alone.
	os.SameFile(info, info)
	return fileStamp{info: info, size: info.Size(), mod: info.ModTime()}, nil
}

// lockPath is the advisory lock file guarding path. It sits beside the state
// so every process that can reach the state reaches the same lock.
func lockPath(path string) string {
	return path + ".lock"
}

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

// saveState writes the durable Team record atomically and returns the stamp
// of the file it wrote.
func saveState(path string, state State) (fileStamp, error) {
	if path == "" {
		return fileStamp{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fileStamp{}, fmt.Errorf("create team state directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fileStamp{}, fmt.Errorf("encode team state: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".team-*.json")
	if err != nil {
		return fileStamp{}, fmt.Errorf("create team state temp file: %w", err)
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fileStamp{}, fmt.Errorf("write team state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fileStamp{}, fmt.Errorf("close team state: %w", err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fileStamp{}, fmt.Errorf("chmod team state: %w", err)
	}
	if err := renameStateFile(name, path); err != nil {
		return fileStamp{}, fmt.Errorf("replace team state: %w", err)
	}
	stamp, err := statState(path)
	if err != nil {
		return fileStamp{}, fmt.Errorf("stat team state: %w", err)
	}
	return stamp, nil
}

// LoadState reads a Team record for read-only consumers such as the CLI.
func LoadState(path string) (State, error) {
	return loadState(path)
}
