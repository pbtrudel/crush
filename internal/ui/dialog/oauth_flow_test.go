package dialog

import (
	"context"
	"errors"
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// fakeFlow is a signin.Flow the adapter tests drive by hand.
type fakeFlow struct {
	name      string
	start     signin.Challenge
	startErr  error
	tok       *oauth.Token
	next      *signin.Challenge
	waitErr   error
	codeEntry bool
	codeErr   error
	closed    bool

	// startCtx and waitCtx carry the contexts the adapter handed over.
	startCtx context.Context
	waitCtx  context.Context
	// entered receives a value when Wait or SubmitCode starts, so a test
	// can cancel while the command is in flight.
	entered chan struct{}
}

func (f *fakeFlow) Name() string { return f.name }

func (f *fakeFlow) Start(ctx context.Context) (signin.Challenge, error) {
	f.startCtx = ctx
	if f.startErr != nil {
		return signin.Challenge{}, f.startErr
	}
	return f.start, nil
}

func (f *fakeFlow) Wait(ctx context.Context, _ signin.Challenge) (*oauth.Token, *signin.Challenge, error) {
	f.waitCtx = ctx
	if f.entered != nil {
		f.entered <- struct{}{}
		<-ctx.Done()
	}
	if f.waitErr != nil {
		return nil, nil, f.waitErr
	}
	return f.tok, f.next, nil
}

func (f *fakeFlow) CodeEntry() bool { return f.codeEntry }

func (f *fakeFlow) SubmitCode(ctx context.Context, _ string) (*oauth.Token, error) {
	if f.entered != nil {
		f.entered <- struct{}{}
		<-ctx.Done()
	}
	if f.codeErr != nil {
		return nil, f.codeErr
	}
	return f.tok, nil
}

func (f *fakeFlow) Close() { f.closed = true }

func TestOAuthFlowInitiates(t *testing.T) {
	t.Parallel()
	flow := &fakeFlow{start: signin.Challenge{
		DeviceCode: "dc", UserCode: "ABCD", VerificationURL: "https://example/device",
		ExpiresIn: 600, Interval: 5,
	}}
	msg := newOAuthFlow(flow, 0).initiateAuth()
	require.Equal(t, ActionInitiateOAuth{
		DeviceCode: "dc", UserCode: "ABCD", VerificationURL: "https://example/device",
		ExpiresIn: 600, Interval: 5,
	}, msg)

	deadline, ok := flow.startCtx.Deadline()
	require.True(t, ok, "the start must be time limited")
	require.WithinDuration(t, time.Now().Add(30*time.Second), deadline, 5*time.Second)
}

func TestOAuthFlowInitiatesReportsErrors(t *testing.T) {
	t.Parallel()
	flow := &fakeFlow{startErr: errors.New("failed to initiate device auth: boom")}
	msg := newOAuthFlow(flow, 0).initiateAuth()
	errored, ok := msg.(ActionOAuthErrored)
	require.True(t, ok)
	require.EqualError(t, errored.Error, "failed to initiate device auth: boom")
}

func TestOAuthFlowInitiatesHonoursMinStart(t *testing.T) {
	t.Parallel()
	started := time.Now()
	newOAuthFlow(&fakeFlow{}, 100*time.Millisecond).initiateAuth()
	require.GreaterOrEqual(t, time.Since(started), 100*time.Millisecond)
}

func TestOAuthFlowPollsToCompletion(t *testing.T) {
	t.Parallel()
	tok := &oauth.Token{AccessToken: "tok"}
	msg := newOAuthFlow(&fakeFlow{tok: tok}, 0).startPolling("dc", 60)()
	require.Equal(t, ActionCompleteOAuth{Token: tok}, msg)
}

func TestOAuthFlowPollsToTheNextChallenge(t *testing.T) {
	t.Parallel()
	next := &signin.Challenge{DeviceCode: "dc2", UserCode: "NEXT", VerificationURL: "https://example/device"}
	msg := newOAuthFlow(&fakeFlow{next: next}, 0).startPolling("", 0)()
	require.Equal(t, ActionInitiateOAuth{
		DeviceCode: "dc2", UserCode: "NEXT", VerificationURL: "https://example/device",
	}, msg)
}

func TestOAuthFlowPollsReportsErrors(t *testing.T) {
	t.Parallel()
	msg := newOAuthFlow(&fakeFlow{waitErr: errors.New("boom")}, 0).startPolling("dc", 60)()
	errored, ok := msg.(ActionOAuthErrored)
	require.True(t, ok)
	require.EqualError(t, errored.Error, "boom")
}

func TestOAuthFlowStopCancelsAndCloses(t *testing.T) {
	t.Parallel()
	flow := &fakeFlow{entered: make(chan struct{}, 1)}
	m := newOAuthFlow(flow, 0)
	cmd := m.startPolling("dc", 60)

	done := make(chan any, 1)
	go func() { done <- cmd() }()
	<-flow.entered
	m.stopPolling()

	require.Nil(t, <-done, "a cancelled poll reports nothing")
	require.True(t, flow.closed)
	require.ErrorIs(t, flow.waitCtx.Err(), context.Canceled)
}

func TestOAuthFlowStopsSubmitCodeWhenCancelled(t *testing.T) {
	t.Parallel()
	flow := &fakeFlow{entered: make(chan struct{}, 1), codeErr: errors.New("boom")}
	m := newOAuthFlow(flow, 0)

	done := make(chan any, 1)
	go func() { done <- m.submitCode("code")() }()
	<-flow.entered
	m.stopPolling()

	require.Nil(t, <-done, "a cancelled submit reports nothing")
}

func TestOAuthFlowSubmitsCode(t *testing.T) {
	t.Parallel()
	tok := &oauth.Token{AccessToken: "tok"}
	msg := newOAuthFlow(&fakeFlow{tok: tok}, 0).submitCode("code")()
	require.Equal(t, ActionCompleteOAuth{Token: tok}, msg)

	msg = newOAuthFlow(&fakeFlow{codeErr: errors.New("bad code")}, 0).submitCode("code")()
	errored, ok := msg.(ActionOAuthErrored)
	require.True(t, ok)
	require.EqualError(t, errored.Error, "bad code")
}

func TestOAuthFlowCodeEntryFollowsTheFlow(t *testing.T) {
	t.Parallel()
	require.False(t, newOAuthFlow(&fakeFlow{}, 0).supportsCodeEntry())
	require.True(t, newOAuthFlow(&fakeFlow{codeEntry: true}, 0).supportsCodeEntry())
}

func TestOAuthDialogsKeepTheirSignInFlows(t *testing.T) {
	t.Parallel()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	provider := catwalk.Provider{ID: catwalk.InferenceProviderOpenAI, Name: "OpenAI"}
	model := config.SelectedModel{Provider: "openai", Model: "gpt-5.1"}

	copilot, _ := NewOAuthCopilot(com, false, provider, model, config.SelectedModelTypeMain)
	require.Equal(t, "GitHub Copilot", copilot.oAuthProvider.name())
	require.False(t, copilot.oAuthProvider.supportsCodeEntry())
	require.Empty(t, copilot.codeInput.Prompt, "no paste field")

	hyper, _ := NewOAuthHyper(com, false, provider, model, config.SelectedModelTypeMain)
	require.Equal(t, "Hyper", hyper.oAuthProvider.name())
	require.False(t, hyper.oAuthProvider.supportsCodeEntry())

	openai, _ := NewOAuthOpenAI(com, false, provider, model, config.SelectedModelTypeMain)
	require.Equal(t, "ChatGPT", openai.oAuthProvider.name())
	require.False(t, openai.oAuthProvider.supportsCodeEntry())

	grok, _ := NewOAuthGrok(com, false, provider, model, config.SelectedModelTypeMain)
	require.Equal(t, "Grok", grok.oAuthProvider.name())
	// The dialog builds its paste field from this at construction, so the
	// Grok code fallback must be on before the flow starts.
	require.True(t, grok.oAuthProvider.supportsCodeEntry())
	require.Equal(t, "> ", grok.codeInput.Prompt, "the paste field exists")
}
