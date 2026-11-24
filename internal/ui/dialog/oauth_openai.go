package dialog

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/ui/common"
)

// NewOAuthOpenAI creates an OAuth dialog for signing in with a ChatGPT
// account. Unlike the device-flow providers, the browser flow has no code
// to paste: the user authorizes in the browser and the loopback callback
// delivers the result.
func NewOAuthOpenAI(
	com *common.Common,
	isOnboarding bool,
	provider catwalk.Provider,
	model config.SelectedModel,
	modelType config.SelectedModelType,
) (*OAuth, tea.Cmd) {
	return newOAuth(com, isOnboarding, provider, model, modelType, newOAuthFlow(signin.NewOpenAI(), 0))
}
