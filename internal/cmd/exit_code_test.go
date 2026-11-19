package cmd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/app"
)

func TestExitCode(t *testing.T) {
	t.Parallel()
	chosen := fmt.Errorf("run: %w", &app.ExitError{Code: 2, Reason: "max_tokens"})
	tests := []struct {
		name        string
		err         error
		interrupted bool
		want        int
	}{
		{name: "success", want: 0},
		{name: "a failure", err: errors.New("no prompt provided"), want: 1},
		{name: "a code the command chose", err: chosen, want: 2},
		{name: "a signal wins", err: chosen, interrupted: true, want: 130},
		{name: "a signal without an error", interrupted: true, want: 130},
		{name: "an interrupted spinner", err: app.InterruptedExit(true, nil), want: 130},
		{name: "an interrupted spinner over its cancel", err: app.InterruptedExit(true, context.Canceled), want: 130},
		{name: "a spinner that was not interrupted", err: app.InterruptedExit(false, context.Canceled), want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, exitCode(tt.err, tt.interrupted))
		})
	}
}
