package dialog

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/ui/common"
)

// NewOAuthHyper creates an OAuth dialog for signing in with a Hyper
// account through the device flow.
func NewOAuthHyper(
	com *common.Common,
	isOnboarding bool,
	provider catwalk.Provider,
	model config.SelectedModel,
	modelType config.SelectedModelType,
) (*OAuth, tea.Cmd) {
	// Keep the initializing spinner up briefly, so a fast device auth
	// does not flash the dialog.
	return newOAuth(com, isOnboarding, provider, model, modelType, newOAuthFlow(signin.NewHyper(), 750*time.Millisecond))
}
