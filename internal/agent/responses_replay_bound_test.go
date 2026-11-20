package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrefixHashesRefusesUnboundedInput(t *testing.T) {
	t.Parallel()

	require.Len(t, prefixHashes(make([][]byte, 2)), 3)
	require.Nil(t, prefixHashes(make([][]byte, maxResponsesReplayInputItems+1)))
}
