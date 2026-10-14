package agent

import (
	"errors"
	"fmt"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func TestIsContextOverflowError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"plain error", errors.New("boom"), false},
		{
			"provider context too large flag",
			&fantasy.ProviderError{StatusCode: 400, ContextTooLargeErr: true},
			true,
		},
		{
			"provider context max tokens",
			&fantasy.ProviderError{StatusCode: 400, ContextMaxTokens: 128000},
			true,
		},
		{
			"wrapped provider context error",
			fmt.Errorf("request failed: %w", &fantasy.ProviderError{ContextTooLargeErr: true}),
			true,
		},
		{
			"provider error without context fields",
			&fantasy.ProviderError{StatusCode: 400, Message: "prompt is too long"},
			false,
		},
		{
			"generic fantasy error",
			&fantasy.Error{Message: "context length exceeded"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, isContextOverflowError(tt.err))
		})
	}
}

func TestShouldRecoverFromOverflow(t *testing.T) {
	t.Parallel()

	overflow := &fantasy.ProviderError{StatusCode: 400, ContextTooLargeErr: true}
	serverErr := &fantasy.ProviderError{StatusCode: 500}

	require.True(t, shouldRecoverFromOverflow(overflow, false), "a fresh overflow should be recovered")
	require.False(t, shouldRecoverFromOverflow(overflow, true), "a retried call must not recover again")
	require.False(t, shouldRecoverFromOverflow(serverErr, false), "non-overflow errors must not recover")
	require.False(t, shouldRecoverFromOverflow(nil, false))
}
