package limits

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// TestNearestRank checks the 90th percentile of the design: the value at
// index ceil(0.9 n) of the sorted values.
func TestNearestRank(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(7), nearestRank([]int64{7}))
	require.Equal(t, int64(30), nearestRank([]int64{10, 20, 30}))
	require.Equal(t, int64(9), nearestRank([]int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}))
}

// TestComputeClamps checks that a limit doubles the 90th percentile of the
// passing runs, rounds a timeout up to the whole minute, and stays between
// its floor and the suite default.
func TestComputeClamps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		steps       int
		duration    time.Duration
		wantSteps   int
		wantTimeout time.Duration
	}{
		{name: "floors", steps: 10, duration: 3 * time.Minute, wantSteps: 40, wantTimeout: 10 * time.Minute},
		{name: "doubles", steps: 30, duration: 6 * time.Minute, wantSteps: 60, wantTimeout: 12 * time.Minute},
		{name: "ceilings", steps: 80, duration: 20 * time.Minute, wantSteps: 120, wantTimeout: 25 * time.Minute},
		{name: "rounds up", steps: 30, duration: 6*time.Minute + 20*time.Second, wantSteps: 60, wantTimeout: 13 * time.Minute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tasks := []*task.Task{{ID: "pr-0001"}}
			runs := []result.Result{{
				Task:       "pr-0001",
				Route:      "fake",
				Rep:        1,
				Pass:       true,
				Steps:      test.steps,
				DurationMS: test.duration.Milliseconds(),
			}}

			got, err := Compute(tasks, runs, "fake")
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, test.wantSteps, got[0].MaxSteps)
			require.Equal(t, test.wantTimeout, got[0].Timeout)
		})
	}
}

// TestComputeUsesPassingRuns checks that only passing runs of the task's
// route set its limits, that every variant is pooled, and that a task with
// no passing run keeps the suite defaults.
func TestComputeUsesPassingRuns(t *testing.T) {
	t.Parallel()

	tasks := []*task.Task{{ID: "pr-0001"}, {ID: "pr-0002"}}
	runs := []result.Result{
		{Task: "pr-0001", Variant: "a", Route: "fake", Rep: 1, Pass: true, Steps: 10, DurationMS: 60_000},
		{Task: "pr-0001", Variant: "b", Route: "fake", Rep: 1, Pass: true, Steps: 30, DurationMS: 180_000},
		{Task: "pr-0001", Variant: "a", Route: "fake", Rep: 2, Pass: false, Steps: 500, DurationMS: 3_600_000},
		{Task: "pr-0001", Variant: "a", Route: "other", Rep: 1, Pass: true, Steps: 500, DurationMS: 3_600_000},
		{Task: "pr-0001", Variant: "b", Route: "fake", Rep: 2, Pass: true, Contaminated: true, Steps: 500, DurationMS: 3_600_000},
		{Task: "pr-0002", Variant: "a", Route: "fake", Rep: 1, Pass: false, Steps: 40, DurationMS: 120_000},
	}

	got, err := Compute(tasks, runs, "fake")
	require.NoError(t, err)
	require.Equal(t, []Limit{
		{
			Task: "pr-0001", Runs: 4, Passes: 2,
			Steps: 30, Duration: 3 * time.Minute,
			MaxSteps: 60, Timeout: 10 * time.Minute,
		},
		{
			Task: "pr-0002", Runs: 1, Passes: 0,
			MaxSteps: task.DefaultMaxSteps, Timeout: task.DefaultTimeout,
		},
	}, got)
}

// TestComputeUnknownTask checks that a batch naming a task the suite no
// longer holds is an error, not a silent default.
func TestComputeUnknownTask(t *testing.T) {
	t.Parallel()

	_, err := Compute(nil, []result.Result{{Task: "pr-0009", Route: "fake"}}, "fake")
	require.ErrorContains(t, err, "pr-0009")
}

// TestWriteLimits replaces only the limits block of a task file and leaves
// every other byte alone.
func TestWriteLimits(t *testing.T) {
	t.Parallel()

	const before = "schema: 1\nid: pr-0001\nlimits:\n  max_steps: 120\n" +
		"  timeout: 25m\nvetted: {}\nrisks: []\n"
	want := task.Limits{MaxSteps: 40, Timeout: task.Duration(10 * time.Minute)}

	t.Run("replaces the block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0001.yaml")
		require.NoError(t, os.WriteFile(path, []byte(before), 0o644))

		require.NoError(t, WriteLimits(path, want))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t,
			"schema: 1\nid: pr-0001\nlimits:\n  max_steps: 40\n"+
				"  timeout: 10m\nvetted: {}\nrisks: []\n", string(got))

		tk, err := task.Load(path)
		require.NoError(t, err)
		require.Equal(t, want, tk.Limits)
	})

	t.Run("keeps a column-0 comment after the block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0001.yaml")
		text := "schema: 1\nid: pr-0001\nlimits:\n  max_steps: 120\n  timeout: 25m\n" +
			"\n# Frozen from the A/A batch.\nrisks: []\n"
		require.NoError(t, os.WriteFile(path, []byte(text), 0o644))

		require.NoError(t, WriteLimits(path, want))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t,
			"schema: 1\nid: pr-0001\nlimits:\n  max_steps: 40\n  timeout: 10m\n"+
				"\n# Frozen from the A/A batch.\nrisks: []\n", string(got))
	})

	t.Run("appends a missing block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0001.yaml")
		require.NoError(t, os.WriteFile(path, []byte("schema: 1\nid: pr-0001\n"), 0o644))

		require.NoError(t, WriteLimits(path, want))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t,
			"schema: 1\nid: pr-0001\nlimits:\n  max_steps: 40\n  timeout: 10m\n",
			string(got))
	})
}
