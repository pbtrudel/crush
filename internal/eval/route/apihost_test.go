package route

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAPIHost is the host:port a route's network jail allows: the base_url
// host, with the scheme's default port.
func TestAPIHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  map[string]any
		want    string
		wantErr string
	}{
		{
			name:   "https default port",
			config: map[string]any{"base_url": "https://api.commandcode.ai/provider/v1"},
			want:   "api.commandcode.ai:443",
		},
		{
			name:   "http default port",
			config: map[string]any{"base_url": "http://localhost:8080/v1"},
			want:   "localhost:8080",
		},
		{
			name:   "explicit port",
			config: map[string]any{"base_url": "https://Example.COM:8443/v1"},
			want:   "example.com:8443",
		},
		{
			name:    "no base_url",
			config:  map[string]any{"api_key": "$KEY"},
			wantErr: "no base_url",
		},
		{
			name:    "no host",
			config:  map[string]any{"base_url": "not a url"},
			wantErr: "base_url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &Route{Name: "r", ProviderConfig: tt.config}
			got, err := r.APIHost()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
