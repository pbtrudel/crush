package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Edit without read is an experiment: the nil receiver and an unset field
// both mean off, and the field is read from the config JSON.
func TestOptionsGetEditWithoutRead(t *testing.T) {
	t.Parallel()

	var nilOptions *Options
	require.False(t, nilOptions.GetEditWithoutRead(), "the nil receiver means off")
	require.False(t, (&Options{}).GetEditWithoutRead(), "an unset field means off")
	require.True(t, (&Options{EditWithoutRead: true}).GetEditWithoutRead())

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{"edit_without_read":true}}`), &cfg))
	require.True(t, cfg.Options.GetEditWithoutRead())
}
