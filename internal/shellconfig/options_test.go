package shellconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOption_Bool(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option debug true
option progress false`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["debug"])
	require.Equal(t, false, opts["progress"])
}

func TestOption_BoolCaseInsensitive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option debug TRUE
option progress False
option metrics YES`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["debug"])
	require.Equal(t, false, opts["progress"])
	require.Equal(t, false, opts["disable_metrics"])
}

func TestOption_String(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option data-directory .crush
option notifications osc`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, ".crush", opts["data_directory"])
	require.Equal(t, "osc", opts["notifications"])
}

func TestOption_List(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option context-path .cursorrules
option context-path CRUSH.md`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	paths := opts["context_paths"].([]any)
	require.Len(t, paths, 2)
	require.Equal(t, ".cursorrules", paths[0])
	require.Equal(t, "CRUSH.md", paths[1])
}

func TestOption_Reset(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option skill-path ./a
option skill-path ./b
option reset skill-path`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Empty(t, opts["skills_paths"].([]any))
}

func TestOption_ResetThenReadd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option skill-path ./inherited-a
option skill-path ./inherited-b
option reset skill-path
option skill-path ./mine`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	paths := opts["skills_paths"].([]any)
	require.Len(t, paths, 1)
	require.Equal(t, "./mine", paths[0])
}

func TestOption_ResetUnknownKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option reset bogus-key`
	path := filepath.Join(dir, "crushrc")

	_, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown key")
}

func TestOption_ResetNonListKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option reset debug`
	path := filepath.Join(dir, "crushrc")

	_, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not one")
}

func TestOption_UIUnknownKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "crushrc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`option ui bogus true`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown key")
}

func TestOption_UIExitBanner(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "crushrc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(`option ui exit-banner compact`))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	ui := result["options"].(map[string]any)["tui"].(map[string]any)
	require.Equal(t, "compact", ui["exit_banner"])

	_, err = LoadShellConfig(t.Context(), path, []byte(`option ui exit-banner bogus`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "expects default, compact, or none")
}

func TestOption_UINerdFonts(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "crushrc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(`option ui nerd-fonts true`))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	ui := result["options"].(map[string]any)["tui"].(map[string]any)
	require.Equal(t, true, ui["nerd_fonts"])

	_, err = LoadShellConfig(t.Context(), path, []byte(`option ui nerd-fonts auto`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "nerd-fonts expects true/false")
}

func TestOption_BoolShorthand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option debug
option metrics`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["debug"])
	require.Equal(t, false, opts["disable_metrics"])
}

func TestOption_InvertedBool(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option metrics false`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["disable_metrics"])
}

func TestOption_UnknownKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option bogus-key value`
	path := filepath.Join(dir, "crushrc")

	_, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown key")
}

func TestOption_RequestTimeout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option request-timeout 300
option request-timeout 0`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, float64(0), opts["request_timeout"])
}

func TestOption_MaxBackgroundJobs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option max-background-jobs 200`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, float64(200), opts["max_background_jobs"])
}

func TestOption_RequestTimeoutInvalid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option request-timeout soon`
	path := filepath.Join(dir, "crushrc")

	_, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.Error(t, err)
	require.Contains(t, err.Error(), "expects an integer")
}

func TestOption_CompactionTuning(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "crushrc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(`option auto-compact-threshold 0.5
option auto-compact-max-tokens 250000
option compact-tail-tokens 16384
option compact-background-margin 0.1
option history-slim-idle-minutes 30
option history-slim-reasoning`))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, float64(0.5), opts["auto_compact_threshold"])
	require.Equal(t, float64(250000), opts["auto_compact_max_tokens"])
	require.Equal(t, float64(16384), opts["compact_tail_tokens"])
	require.Equal(t, 0.1, opts["compact_background_margin"])
	require.Equal(t, float64(30), opts["history_slim_idle_minutes"])
	require.Equal(t, true, opts["history_slim_reasoning"])
}

func TestOption_AutoCompactThresholdInvalid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "crushrc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`option auto-compact-threshold soon`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "expects a number")
}

func TestOption_CompactTailTokensInvalid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "crushrc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`option compact-tail-tokens many`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "expects an integer")
}

func TestOption_Web(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "crushrc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte("option web"))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))
	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["web"])
}

func TestOption_UIVerbosity(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "crushrc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(`option ui verbosity low`))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	ui := result["options"].(map[string]any)["tui"].(map[string]any)
	require.Equal(t, "low", ui["verbosity"])

	_, err = LoadShellConfig(t.Context(), path, []byte(`option ui verbosity sometimes`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "expects low or high")
}

// The edit-without-read option is a plain boolean: the key alone turns the
// experiment on, and false turns it off.
func TestOption_EditWithoutRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   bool
	}{
		{name: "key alone turns it on", script: "option edit-without-read", want: true},
		{name: "false turns it off", script: "option edit-without-read false", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "crushrc")
			jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(tt.script))
			require.NoError(t, err)

			var result map[string]any
			require.NoError(t, json.Unmarshal(jsonBytes, &result))
			opts := result["options"].(map[string]any)
			require.Equal(t, tt.want, opts["edit_without_read"])
		})
	}
}

func TestOption_Prompt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option prompt coder /etc/crush/coder.md.tpl
option prompt summary /etc/crush/summary.md`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	prompts := result["options"].(map[string]any)["prompts"].(map[string]any)
	require.Equal(t, "/etc/crush/coder.md.tpl", prompts["coder"])
	require.Equal(t, "/etc/crush/summary.md", prompts["summary"])
}

// The config is a shell script, so bash expands ~ before the builtin runs.
func TestOption_PromptExpandsTilde(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `HOME=/home/someone
option prompt coder ~/coder.md.tpl`
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	prompts := result["options"].(map[string]any)["prompts"].(map[string]any)
	require.Equal(t, "/home/someone/coder.md.tpl", prompts["coder"])
}

func TestOption_PromptMissingPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option prompt coder`
	path := filepath.Join(dir, "crushrc")

	_, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage: option prompt")
}
