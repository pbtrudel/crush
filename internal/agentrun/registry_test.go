package agentrun

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRegistryStartAndFinish(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "agents.json")
	registry := New(path)

	registry.Start(Run{ID: "run-1", Kind: KindSubagent, Name: "First"})
	registry.Start(Run{ID: "run-2", Kind: KindSubagent, Name: "Second"})

	runs := registry.List()
	require.Len(t, runs, 2)
	require.Equal(t, StatusRunning, runs[0].Status)
	require.Equal(t, StatusRunning, runs[1].Status)

	registry.Finish("run-1", StatusDone)
	runs = registry.List()
	require.Len(t, runs, 2)
	require.Equal(t, "run-2", runs[0].ID, "running runs sort before finished ones")
	require.Equal(t, StatusDone, runs[1].Status, "finished runs stay visible during the grace period")
	require.False(t, runs[1].FinishedAt.IsZero())
}

func TestRegistryPrunesStaleRuns(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "agents.json")
	registry := New(path)
	registry.Start(Run{ID: "run-1", Name: "Finished"})
	registry.Finish("run-1", StatusDone)
	registry.Start(Run{ID: "run-2", Name: "Crashed"})

	registry.now = func() time.Time { return time.Now().Add(maxRunningAge + time.Minute) }
	require.Empty(t, registry.List(), "stale running and finished runs must not linger")
}

func TestRegistryMergesAcrossInstances(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "agents.json")

	first := New(path)
	second := New(path)

	first.Start(Run{ID: "run-1", Name: "First"})
	second.Start(Run{ID: "run-2", Name: "Second"})

	runs := first.List()
	require.Len(t, runs, 2, "a second writer must not drop the first writer's runs")
	require.Equal(t, "run-1", runs[0].ID)
	require.Equal(t, "run-2", runs[1].ID)
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	require.Empty(t, Load(filepath.Join(t.TempDir(), "missing.json")))
}

func TestRegistryCreatesMissingDirectory(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "agents.json")
	registry := New(path)

	registry.Start(Run{ID: "run-1", Name: "First"})

	require.Len(t, registry.List(), 1)
}
