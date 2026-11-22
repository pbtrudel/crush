package webtest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Empty fake collections must encode as {} and [] rather than null so a
// gateway answer matches the real implementations.
func TestFakeEmptyCollectionsEncodeAsEmptyJSON(t *testing.T) {
	t.Parallel()
	f := NewFake()

	children, err := f.ListChildSessions(t.Context(), "s1")
	require.NoError(t, err)
	require.NotNil(t, children)
	encoded, err := json.Marshal(children)
	require.NoError(t, err)
	require.Equal(t, "[]", string(encoded))

	diags := f.LSPDiagnosticsByFile(t.Context())
	require.NotNil(t, diags)
	encoded, err = json.Marshal(diags)
	require.NoError(t, err)
	require.Equal(t, "{}", string(encoded))
}
