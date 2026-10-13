package update

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckForUpdate_Old(t *testing.T) {
	info, err := Check(t.Context(), "v0.10.0", testClient{"v0.11.0"})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.True(t, info.Available())
}

func TestCheckForUpdate_Beta(t *testing.T) {
	t.Run("current is stable", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.10.0", testClient{"v0.11.0-beta.1"})
		require.NoError(t, err)
		require.NotNil(t, info)
		require.False(t, info.Available())
	})

	t.Run("current is also beta", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.11.0-beta.1", testClient{"v0.11.0-beta.2"})
		require.NoError(t, err)
		require.NotNil(t, info)
		require.True(t, info.Available())
	})

	t.Run("current is beta, latest isn't", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.11.0-beta.1", testClient{"v0.11.0"})
		require.NoError(t, err)
		require.NotNil(t, info)
		require.True(t, info.Available())
	})
}

func TestCheckForUpdate_Fork(t *testing.T) {
	t.Run("fork build of the latest upstream release is up to date", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.98.1-fork.10", testClient{"v0.98.1"})
		require.NoError(t, err)
		require.False(t, info.Available())
		// The suffix stays on the version for display.
		require.Equal(t, "0.98.1-fork.10", info.Current)
	})

	t.Run("fork build of an upstream pre-release is up to date", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.99.0-beta.1-fork.2", testClient{"v0.99.0-beta.1"})
		require.NoError(t, err)
		require.False(t, info.Available())
	})

	t.Run("fork build of an older upstream release sees the newer one", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.98.0-fork.4", testClient{"v0.98.1"})
		require.NoError(t, err)
		require.True(t, info.Available())
	})

	t.Run("newer upstream release is available", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.98.1-fork.10", testClient{"v0.98.2"})
		require.NoError(t, err)
		require.True(t, info.Available())
	})
}

type testClient struct{ tag string }

// Latest implements Client.
func (t testClient) Latest(ctx context.Context) (*Release, error) {
	return &Release{
		TagName: t.tag,
		HTMLURL: "https://example.org",
	}, nil
}
