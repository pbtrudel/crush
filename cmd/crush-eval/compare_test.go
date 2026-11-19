package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/stretchr/testify/require"
)

func TestVariantLabels(t *testing.T) {
	t.Parallel()

	exp := &score.Experiment{Variants: []score.Variant{
		{Name: "control"}, {Name: "variant"}, {Name: "control2"},
	}}
	runs := []result.Result{
		{Variant: "control@aaaa", Task: "t1"},
		{Variant: "variant@bbbb", Task: "t1"},
		{Variant: "control2@aaaa", Task: "t1"},
	}
	labels, err := variantLabels(exp, runs)
	require.NoError(t, err)
	require.Equal(t, "control@aaaa", labels["control"])
	require.Equal(t, "variant@bbbb", labels["variant"])
	require.Equal(t, "control2@aaaa", labels["control2"], "a name that prefixes another stays apart")

	_, err = variantLabels(exp, runs[:2])
	require.ErrorContains(t, err, `"control2"`)

	mixed := append(runs, result.Result{Variant: "control@cccc", Task: "t1"})
	_, err = variantLabels(exp, mixed)
	require.ErrorContains(t, err, "both")
}

func TestWriteDetectableEffect(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "experiment.yaml")
	require.NoError(t, os.WriteFile(path, []byte("date: 2026-11-19\n# a comment\ndetectable_effect: 0\nmargin: 0.05\n"), 0o644))
	require.NoError(t, writeDetectableEffect(path, 0.03125))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "date: 2026-11-19\n# a comment\ndetectable_effect: 0.03125\nmargin: 0.05\n", string(data))

	other := filepath.Join(dir, "other.yaml")
	require.NoError(t, os.WriteFile(other, []byte("margin: 0.05"), 0o644))
	require.NoError(t, writeDetectableEffect(other, 0.5))
	data, err = os.ReadFile(other)
	require.NoError(t, err)
	require.Equal(t, "margin: 0.05\ndetectable_effect: 0.5\n", string(data))
}
