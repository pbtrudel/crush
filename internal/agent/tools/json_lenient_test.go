package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrimTrailingCommas(t *testing.T) {
	t.Parallel()

	require.Equal(t, `[{"a":1}]`, trimTrailingCommas(`[{"a":1},]`))
	require.Equal(t, `{"a":[1,2]}`, trimTrailingCommas(`{"a":[1,2,],}`))
	require.Equal(t, `["x,]"]`, trimTrailingCommas(`["x,]"]`))
	require.Equal(t, `["a\",]"]`, trimTrailingCommas(`["a\",]"]`))
}
