package discord

import (
	"fmt"
	"os"
	"strings"
)

// Environment variables read by the bridge. The token and at least one
// channel id are required; the channel is where /rc creates its control
// thread. The optional allow-list restricts which Discord users may
// drive the session (empty means any member who can post in the thread).
const (
	EnvBotToken       = "DISCORD_BOT_TOKEN"
	EnvChannelIDs     = "DISCORD_CHANNEL_IDS"
	EnvAllowedUserIDs = "DISCORD_ALLOWED_USER_IDS"
)

// Config is the Discord bridge configuration.
type Config struct {
	// Token authenticates the bot. It is sent as the REST Authorization
	// header and in the gateway IDENTIFY payload, and must never appear
	// in logs or error messages.
	Token string

	// ChannelIDs are candidate parent channels for the control thread.
	// The first one is used.
	ChannelIDs []string

	// AllowedUserIDs optionally restricts who can drive the session.
	// Empty means every member of the thread.
	AllowedUserIDs []string
}

// LoadConfig builds a Config from the environment. Error messages never
// include the token value.
func LoadConfig() (Config, error) {
	token := strings.TrimSpace(os.Getenv(EnvBotToken))
	if token == "" {
		return Config{}, fmt.Errorf("%s is not set", EnvBotToken)
	}
	channels := splitList(os.Getenv(EnvChannelIDs))
	if len(channels) == 0 {
		return Config{}, fmt.Errorf("%s is not set (comma-separated channel ids)", EnvChannelIDs)
	}
	return Config{
		Token:          token,
		ChannelIDs:     channels,
		AllowedUserIDs: splitList(os.Getenv(EnvAllowedUserIDs)),
	}, nil
}

// allowedUser reports whether a Discord user id may drive the session.
func (c Config) allowedUser(id string) bool {
	if len(c.AllowedUserIDs) == 0 {
		return true
	}
	for _, allowed := range c.AllowedUserIDs {
		if allowed == id {
			return true
		}
	}
	return false
}

// splitList splits a comma-separated environment value, trimming and
// dropping empty entries.
func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
