package dialog

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/ui/common"
)

// NewOAuthGrok creates an OAuth dialog for signing in with a Grok
// account. The sign-in starts with the ChatGPT-style browser flow; when
// the user declines that permission (or the loopback callback is out of
// reach), it falls back to the device flow, showing a code the user
// enters on accounts.x.ai.
func NewOAuthGrok(
	com *common.Common,
	isOnboarding bool,
	provider catwalk.Provider,
	model config.SelectedModel,
	modelType config.SelectedModelType,
) (*OAuth, tea.Cmd) {
	return newOAuth(com, isOnboarding, provider, model, modelType, newOAuthFlow(signin.NewGrok(), 0))
}
