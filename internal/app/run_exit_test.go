package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
)

// Every turn-end value the agent stamps, with the exit code of its row in
// the measurement design's table (3.5).
func TestRunExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reason message.TurnEnd
		want   int
	}{
		{message.TurnEndCompleted, 0},
		{message.TurnEndEmpty, 2},
		{message.TurnEndMaxTokens, 2},
		{message.TurnEndContentFilter, 2},
		{message.TurnEndToolStop, 2},
		{message.TurnEndLoopGuard, 2},
		{message.TurnEndRepetitionGuard, 2},
		{message.TurnEndContextGuard, 2},
		{message.TurnEndContextOverflow, 2},
		{message.TurnEndCompactionBlocked, 2},
		{message.TurnEndCanceled, 3},
		{message.TurnEndRequestTimeout, 3},
		{message.TurnEndProviderError, 3},
		{message.TurnEndCompactionFailed, 3},
		{message.TurnEndError, 3},
		// A value a newer build may write says nothing about the model.
		{message.TurnEnd("not_yet_known"), 3},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, runExitCode(tt.reason))
			err := runExitError(tt.reason)
			if tt.want == 0 {
				require.NoError(t, err)
				return
			}
			var exitErr *ExitError
			require.ErrorAs(t, err, &exitErr)
			require.Equal(t, tt.want, exitErr.Code)
			require.Equal(t, "the run ended with "+string(tt.reason), err.Error())
		})
	}
}
