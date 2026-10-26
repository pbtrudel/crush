package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsGetShowAllProviders(t *testing.T) {
	t.Parallel()

	var nilOptions *Options
	require.False(t, nilOptions.GetShowAllProviders())
	require.False(t, (&Options{}).GetShowAllProviders())
	require.False(t, (&Options{Models: &ModelsOptions{}}).GetShowAllProviders())
	require.True(t, (&Options{Models: &ModelsOptions{ShowAllProviders: true}}).GetShowAllProviders())
}

func TestOptionsShowAllProvidersFromJSON(t *testing.T) {
	t.Parallel()

	var opts Options
	require.NoError(t, json.Unmarshal([]byte(`{"models":{"show_all_providers":true}}`), &opts))
	require.True(t, opts.GetShowAllProviders())
}
