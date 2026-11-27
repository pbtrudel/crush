package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOptionsIgnoreARemovedWebDisplayBlock pins what happens to a config
// written before M0: the block is unknown after the field is gone, and the
// loader must read the rest of the file without an error.
func TestOptionsIgnoreARemovedWebDisplayBlock(t *testing.T) {
	t.Parallel()

	raw := `{"options":{"web":true,"web_display":{"theme":"solid","motion":"off","low_power":true,"cinematic_hint":false}}}`
	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
	require.NotNil(t, cfg.Options)
	require.True(t, cfg.Options.Web)
}
