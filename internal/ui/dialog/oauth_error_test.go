package dialog

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// errorStateProvider is an OAuthProvider that records what the dialog asks
// of it.
type errorStateProvider struct {
	starts  int
	stops   int
	submits int
}

func (*errorStateProvider) name() string          { return "test" }
func (*errorStateProvider) initiateAuth() tea.Msg { return nil }
func (p *errorStateProvider) startPolling(string, int) tea.Cmd {
	p.starts++
	return nil
}
func (p *errorStateProvider) stopPolling() tea.Msg  { p.stops++; return nil }
func (*errorStateProvider) supportsCodeEntry() bool { return true }
func (p *errorStateProvider) submitCode(string) tea.Cmd {
	p.submits++
	return nil
}

// TestOAuthErrorStateStartsNoNewPoll pins what a rejected code or a failed
// poll leaves behind: the dialog stops polling and offers only Close. A flow
// with both a poll and code entry never restarts the poll after an error;
// the user closes the dialog and starts the sign-in again, so a dismissed
// dialog owns no goroutine.
func TestOAuthErrorStateStartsNoNewPoll(t *testing.T) {
	t.Parallel()
	provider := &errorStateProvider{}
	m := &OAuth{oAuthProvider: provider, State: OAuthStateDisplay}

	action := m.HandleMsg(ActionOAuthErrored{Error: errors.New("bad code")})

	require.IsType(t, ActionCmd{}, action)
	require.Equal(t, OAuthStateError, m.State)
	require.False(t, m.codeEntryActive(), "the error state takes no pasted code")
	require.Len(t, m.ShortHelp(), 1, "the error state offers only its close key")
	require.Zero(t, provider.starts, "nothing restarts the poll after an error")
}
