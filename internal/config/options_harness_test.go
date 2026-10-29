package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsHarnessBudgets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options *Options
		context int
		prune   int
		tool    int
		wakes   int
	}{
		{
			name:    "nil options",
			options: nil,
			context: DefaultContextMaxBytes,
			prune:   DefaultCompactionPruneChars,
			tool:    DefaultToolOutputMaxTokens,
			wakes:   DefaultMaxConsecutiveWakes,
		},
		{
			name:    "unset fields",
			options: &Options{},
			context: DefaultContextMaxBytes,
			prune:   DefaultCompactionPruneChars,
			tool:    DefaultToolOutputMaxTokens,
			wakes:   DefaultMaxConsecutiveWakes,
		},
		{
			name: "configured values",
			options: &Options{
				ContextMaxBytes:      ptr(1024),
				CompactionPruneChars: ptr(2048),
				ToolOutputMaxTokens:  ptr(4096),
				MaxConsecutiveWakes:  ptr(3),
			},
			context: 1024,
			prune:   2048,
			tool:    4096,
			wakes:   3,
		},
		{
			name: "zero disables",
			options: &Options{
				ContextMaxBytes:      ptr(0),
				CompactionPruneChars: ptr(0),
				ToolOutputMaxTokens:  ptr(0),
				MaxConsecutiveWakes:  ptr(0),
			},
			context: 0,
			prune:   0,
			tool:    0,
			wakes:   0,
		},
		{
			name: "negative is treated as zero",
			options: &Options{
				ContextMaxBytes:      ptr(-1),
				CompactionPruneChars: ptr(-1),
				ToolOutputMaxTokens:  ptr(-1),
				MaxConsecutiveWakes:  ptr(-1),
			},
			context: 0,
			prune:   0,
			tool:    0,
			wakes:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.context, tt.options.GetContextMaxBytes())
			require.Equal(t, tt.prune, tt.options.GetCompactionPruneChars())
			require.Equal(t, tt.tool, tt.options.GetToolOutputMaxTokens())
			require.Equal(t, tt.wakes, tt.options.GetMaxConsecutiveWakes())
		})
	}
}

func TestOptionsHarnessTogglesFromJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{"context_max_bytes":1024,"compaction_prune_chars":2048,"tool_output_max_tokens":4096,"max_consecutive_wakes":3,"repeat_tool_reminders":false,"repetition_detection":false}}`), &cfg))

	require.Equal(t, 1024, cfg.Options.GetContextMaxBytes())
	require.Equal(t, 2048, cfg.Options.GetCompactionPruneChars())
	require.Equal(t, 4096, cfg.Options.GetToolOutputMaxTokens())
	require.Equal(t, 3, cfg.Options.GetMaxConsecutiveWakes())
	require.False(t, cfg.Options.GetRepeatToolReminders())
	require.False(t, cfg.Options.GetRepetitionDetection())
}
