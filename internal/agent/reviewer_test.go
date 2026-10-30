package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/permission"
	"github.com/stretchr/testify/require"
)

func TestParseReviewDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want permission.ReviewDecision
	}{
		{name: "approve", in: "APPROVE", want: permission.ReviewApprove},
		{name: "approve with whitespace", in: "  approve\n", want: permission.ReviewApprove},
		{name: "deny", in: "DENY", want: permission.ReviewDeny},
		{name: "ask", in: "ASK", want: permission.ReviewAsk},
		{name: "ambiguous", in: "I cannot decide", want: permission.ReviewAsk},
		{name: "empty", in: "", want: permission.ReviewAsk},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, parseReviewDecision(tt.in))
		})
	}
}
