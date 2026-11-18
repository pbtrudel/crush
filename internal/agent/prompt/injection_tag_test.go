package prompt

import (
	"testing"

	"github.com/charmbracelet/crush/internal/logreport"
	"github.com/stretchr/testify/require"
)

// The report names the hidden environment snapshot by the tag it opens
// with (logreport.InjectionTag).
func TestEnvSnapshotCarriesTheReportTag(t *testing.T) {
	t.Parallel()

	snapshot := SessionEnvSnapshot(t.Context(), t.TempDir(), fixedSnapshotTime())
	require.Equal(t, "env_snapshot", logreport.InjectionTag(snapshot))
}
