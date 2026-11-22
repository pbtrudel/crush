package route

import (
	"net"
	"net/url"
	"strings"
	"testing"

	"charm.land/catwalk/v3/pkg/embedded"
	hyperp "github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/stretchr/testify/require"
)

// TestAPIHost is the host:port a route's network jail allows: the base_url
// host, with the scheme's default port, or the endpoint of a provider Crush
// ships models for.
func TestAPIHost(t *testing.T) {
	t.Parallel()

	// The Hyper host comes from Crush's own base URL, so the expectation
	// follows $HYPER_URL instead of assuming the default.
	hyperBase, err := url.Parse(hyperp.BaseURL())
	require.NoError(t, err)
	hyperPort := "443"
	if hyperBase.Scheme == "http" {
		hyperPort = "80"
	}
	hyperHost := strings.ToLower(net.JoinHostPort(hyperBase.Hostname(), hyperPort))

	tests := []struct {
		name     string
		provider string
		config   map[string]any
		want     string
		wantErr  string
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
			// Crush takes the Copilot endpoint from its model catalog, so a
			// route names no base_url and the jail still has to allow the
			// API, never the token endpoint on api.github.com.
			name:     "copilot built-in endpoint",
			provider: "copilot",
			want:     "api.githubcopilot.com:443",
		},
		{
			// Hyper's API host is the one Crush's own base URL names.
			name:     "hyper built-in endpoint",
			provider: "hyper",
			want:     hyperHost,
		},
		{
			name:     "base_url beats the built-in endpoint",
			provider: "hyper",
			config:   map[string]any{"base_url": "http://127.0.0.1:8080/v1"},
			want:     "127.0.0.1:8080",
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
			r := &Route{Name: "r", Provider: tt.provider, ProviderConfig: tt.config}
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

// TestCopilotHostMatchesTheCatalog checks the one host the code cannot take
// from Crush's own source: Crush reads the Copilot endpoint from its model
// catalog, so the literal here must agree with the catalog's Copilot entry,
// or the jail would allow a host no run uses.
func TestCopilotHostMatchesTheCatalog(t *testing.T) {
	t.Parallel()

	var endpoint string
	for _, provider := range embedded.GetAll() {
		if string(provider.ID) == ProviderCopilot {
			endpoint = provider.APIEndpoint
		}
	}
	require.NotEmpty(t, endpoint, "the embedded catalog has no copilot provider")

	base, err := url.Parse(endpoint)
	require.NoError(t, err)
	want := strings.ToLower(net.JoinHostPort(base.Hostname(), "443"))

	got, err := (&Route{Name: "gpt", Provider: ProviderCopilot}).APIHost()
	require.NoError(t, err)
	require.Equal(t, want, got)
}
