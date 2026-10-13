package discord

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		t.Setenv(EnvBotToken, "")
		t.Setenv(EnvChannelIDs, "123")
		_, err := LoadConfig()
		require.ErrorContains(t, err, EnvBotToken)
	})

	t.Run("missing channels", func(t *testing.T) {
		t.Setenv(EnvBotToken, "tok")
		t.Setenv(EnvChannelIDs, "")
		_, err := LoadConfig()
		require.ErrorContains(t, err, EnvChannelIDs)
	})

	t.Run("full config", func(t *testing.T) {
		t.Setenv(EnvBotToken, "  tok  ")
		t.Setenv(EnvChannelIDs, " 1, 2 ,, 3 ")
		t.Setenv(EnvAllowedUserIDs, "u1,u2")
		cfg, err := LoadConfig()
		require.NoError(t, err)
		require.Equal(t, "tok", cfg.Token)
		require.Equal(t, []string{"1", "2", "3"}, cfg.ChannelIDs)
		require.Equal(t, []string{"u1", "u2"}, cfg.AllowedUserIDs)
	})
}

func TestConfigAllowedUser(t *testing.T) {
	t.Parallel()
	open := Config{}
	require.True(t, open.allowedUser("anyone"), "empty allow-list must allow everyone")

	restricted := Config{AllowedUserIDs: []string{"u1", "u2"}}
	require.True(t, restricted.allowedUser("u1"))
	require.False(t, restricted.allowedUser("u3"))
}

func TestSplitList(t *testing.T) {
	t.Parallel()
	require.Nil(t, splitList(""))
	require.Nil(t, splitList(" , , "))
	require.Equal(t, []string{"a", "b"}, splitList("a,b"))
}
