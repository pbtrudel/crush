package dialog

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/oauth/signin"
)

// oauthFlow adapts a signin.Flow to OAuthProvider, so the dialog and the
// web gateway run the same sign-in code.
type oauthFlow struct {
	flow     signin.Flow
	minStart time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
}

var _ OAuthProvider = (*oauthFlow)(nil)

// newOAuthFlow wraps a sign-in flow for the dialog. minStart is the
// minimum time the initializing spinner is shown, so a fast start does
// not flash.
func newOAuthFlow(f signin.Flow, minStart time.Duration) OAuthProvider {
	return &oauthFlow{flow: f, minStart: minStart}
}

func (m *oauthFlow) name() string { return m.flow.Name() }

func (m *oauthFlow) initiateAuth() tea.Msg {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := m.flow.Start(ctx)
	if wait := m.minStart - time.Since(started); wait > 0 {
		time.Sleep(wait)
	}
	if err != nil {
		return ActionOAuthErrored{Error: err}
	}
	return initiateOAuthMsg(c)
}

func (m *oauthFlow) startPolling(deviceCode string, expiresIn int) tea.Cmd {
	return func() tea.Msg {
		ctx := m.newContext()
		tok, next, err := m.flow.Wait(ctx, signin.Challenge{DeviceCode: deviceCode, ExpiresIn: expiresIn})
		switch {
		case ctx.Err() != nil:
			return nil // Cancelled, don't report an error.
		case err != nil:
			return ActionOAuthErrored{Error: err}
		case next != nil:
			return initiateOAuthMsg(*next)
		}
		return ActionCompleteOAuth{Token: tok}
	}
}

func (m *oauthFlow) stopPolling() tea.Msg {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.flow.Close()
	return nil
}

func (m *oauthFlow) supportsCodeEntry() bool { return m.flow.CodeEntry() }

func (m *oauthFlow) submitCode(input string) tea.Cmd {
	return func() tea.Msg {
		ctx := m.newContext()
		tok, err := m.flow.SubmitCode(ctx, input)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return ActionOAuthErrored{Error: err}
		}
		return ActionCompleteOAuth{Token: tok}
	}
}

// newContext replaces the cancel func under the lock: the dialog's
// commands run on their own goroutines.
func (m *oauthFlow) newContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()
	return ctx
}

// initiateOAuthMsg turns a challenge into the message that moves the
// dialog to its code-display state.
func initiateOAuthMsg(c signin.Challenge) ActionInitiateOAuth {
	return ActionInitiateOAuth{
		DeviceCode:      c.DeviceCode,
		UserCode:        c.UserCode,
		VerificationURL: c.VerificationURL,
		ExpiresIn:       c.ExpiresIn,
		Interval:        c.Interval,
	}
}
