package signin

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/copilot"
	"github.com/charmbracelet/crush/internal/oauth/grok"
	"github.com/charmbracelet/crush/internal/oauth/hyper"
	"github.com/stretchr/testify/require"
)

// fakeBrowser stands in for a loopback browser flow, so no test opens a
// listener or a browser.
type fakeBrowser struct {
	url     string
	waitErr error
	tok     *oauth.Token
	closed  bool
	code    string
}

func (b *fakeBrowser) StartURL() string { return b.url }

func (b *fakeBrowser) Wait(ctx context.Context) (*oauth.Token, error) {
	if b.waitErr != nil {
		return nil, b.waitErr
	}
	return b.tok, nil
}

func (b *fakeBrowser) CompleteWithCode(_ context.Context, code string) (*oauth.Token, error) {
	b.code = code
	return b.tok, nil
}

func (b *fakeBrowser) Close() { b.closed = true }

func TestGrokFallsBackToDeviceCode(t *testing.T) {
	t.Parallel()
	br := &fakeBrowser{url: "http://localhost:1/start", waitErr: errors.New("callback never came")}
	f := newGrok(grokDeps{
		startBrowser: func() (browserFlow, error) { return br, nil },
		requestDevice: func(context.Context) (*grok.DeviceCode, error) {
			return &grok.DeviceCode{DeviceCode: "dc", UserCode: "ABCD-EFGH", VerificationURI: "https://x.ai/device", ExpiresIn: 600, Interval: 5}, nil
		},
		pollDevice: func(_ context.Context, dc string, _ int) (*oauth.Token, error) {
			require.Equal(t, "dc", dc)
			return &oauth.Token{AccessToken: "tok"}, nil
		},
	})
	c, err := f.Start(t.Context())
	require.NoError(t, err)
	require.Equal(t, "http://localhost:1/start", c.VerificationURL)
	tok, next, err := f.Wait(t.Context(), c)
	require.NoError(t, err)
	require.Nil(t, tok)
	require.Equal(t, "ABCD-EFGH", next.UserCode)
	tok, next, err = f.Wait(t.Context(), *next)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Equal(t, "tok", tok.AccessToken)
}

func TestGrokStartsWithDeviceCodeWhenTheBrowserFlowCannotStart(t *testing.T) {
	t.Parallel()
	f := newGrok(grokDeps{
		startBrowser: func() (browserFlow, error) { return nil, errors.New("port in use") },
		requestDevice: func(context.Context) (*grok.DeviceCode, error) {
			return &grok.DeviceCode{DeviceCode: "dc", UserCode: "WXYZ", VerificationURI: "https://x.ai/device"}, nil
		},
	})
	c, err := f.Start(t.Context())
	require.NoError(t, err)
	require.Equal(t, "WXYZ", c.UserCode)
	require.Equal(t, "dc", c.DeviceCode)
	require.Equal(t, "https://x.ai/device", c.VerificationURL)
}

func TestGrokReportsBothWhenNeitherMethodStarts(t *testing.T) {
	t.Parallel()
	f := newGrok(grokDeps{
		startBrowser:  func() (browserFlow, error) { return nil, errors.New("port in use") },
		requestDevice: func(context.Context) (*grok.DeviceCode, error) { return nil, errors.New("no device code") },
	})
	_, err := f.Start(t.Context())
	require.EqualError(t, err, "failed to start browser auth: port in use (device auth: no device code)")
}

func TestGrokCodeEntryUsesTheBrowserFlow(t *testing.T) {
	t.Parallel()
	br := &fakeBrowser{url: "http://localhost:1/start", tok: &oauth.Token{AccessToken: "tok"}}
	f := newGrok(grokDeps{startBrowser: func() (browserFlow, error) { return br, nil }})
	_, err := f.Start(t.Context())
	require.NoError(t, err)
	require.True(t, f.CodeEntry())
	tok, err := f.SubmitCode(t.Context(), "pasted-code")
	require.NoError(t, err)
	require.Equal(t, "tok", tok.AccessToken)
	require.Equal(t, "pasted-code", br.code)
	f.Close()
	require.True(t, br.closed)
}

func TestGrokSubmitCodeWithoutABrowserFlow(t *testing.T) {
	t.Parallel()
	f := newGrok(grokDeps{
		startBrowser:  func() (browserFlow, error) { return nil, errors.New("port in use") },
		requestDevice: func(context.Context) (*grok.DeviceCode, error) { return &grok.DeviceCode{DeviceCode: "dc"}, nil },
	})
	_, err := f.Start(t.Context())
	require.NoError(t, err)
	require.True(t, f.CodeEntry())
	_, err = f.SubmitCode(t.Context(), "pasted-code")
	require.ErrorIs(t, err, ErrNoBrowserFlow)
}

func TestGrokCodeEntryDoesNotDependOnStart(t *testing.T) {
	t.Parallel()
	// The TUI dialog initializes its paste field from CodeEntry once,
	// before Start runs as a command, so it must already be true.
	f := newGrok(grokDeps{})
	require.True(t, f.CodeEntry())
}

func TestHyperRefusesAnInactiveToken(t *testing.T) {
	t.Parallel()
	f := newHyper(hyperDeps{
		initiate: func(context.Context) (*hyper.DeviceAuthResponse, error) {
			return &hyper.DeviceAuthResponse{DeviceCode: "dc", UserCode: "U", VerificationURL: "https://hyper/device", ExpiresIn: 60}, nil
		},
		poll:     func(context.Context, string, int) (string, error) { return "refresh", nil },
		exchange: func(context.Context, string) (*oauth.Token, error) { return &oauth.Token{AccessToken: "a"}, nil },
		introspect: func(context.Context, string) (*hyper.IntrospectTokenResponse, error) {
			return &hyper.IntrospectTokenResponse{Active: false}, nil
		},
	})
	c, err := f.Start(t.Context())
	require.NoError(t, err)
	_, _, err = f.Wait(t.Context(), c)
	require.EqualError(t, err, "access token is not active")
}

func TestHyperWrapsTheStepErrors(t *testing.T) {
	t.Parallel()
	deps := func() hyperDeps {
		return hyperDeps{
			initiate: func(context.Context) (*hyper.DeviceAuthResponse, error) {
				return &hyper.DeviceAuthResponse{DeviceCode: "dc", UserCode: "U", VerificationURL: "https://hyper/device", ExpiresIn: 60}, nil
			},
			poll:     func(context.Context, string, int) (string, error) { return "refresh", nil },
			exchange: func(context.Context, string) (*oauth.Token, error) { return &oauth.Token{AccessToken: "a"}, nil },
			introspect: func(context.Context, string) (*hyper.IntrospectTokenResponse, error) {
				return &hyper.IntrospectTokenResponse{Active: true}, nil
			},
		}
	}

	startDeps := deps()
	startDeps.initiate = func(context.Context) (*hyper.DeviceAuthResponse, error) { return nil, errors.New("boom") }
	_, err := newHyper(startDeps).Start(t.Context())
	require.EqualError(t, err, "failed to initiate device auth: boom")

	exchangeDeps := deps()
	exchangeDeps.exchange = func(context.Context, string) (*oauth.Token, error) { return nil, errors.New("boom") }
	c := Challenge{DeviceCode: "dc", UserCode: "U", ExpiresIn: 60}
	_, _, err = newHyper(exchangeDeps).Wait(t.Context(), c)
	require.EqualError(t, err, "token exchange failed: boom")

	introspectDeps := deps()
	introspectDeps.introspect = func(context.Context, string) (*hyper.IntrospectTokenResponse, error) {
		return nil, errors.New("boom")
	}
	_, _, err = newHyper(introspectDeps).Wait(t.Context(), c)
	require.EqualError(t, err, "token introspection failed: boom")
}

func TestCopilotWaitHonoursCancel(t *testing.T) {
	t.Parallel()
	f := newCopilot(copilotDeps{
		requestDevice: func(context.Context) (*copilot.DeviceCode, error) {
			return &copilot.DeviceCode{DeviceCode: "dc", UserCode: "U", VerificationURI: "https://github/device", ExpiresIn: 900, Interval: 5}, nil
		},
		poll: func(ctx context.Context, dc *copilot.DeviceCode) (*oauth.Token, error) {
			require.Equal(t, "dc", dc.DeviceCode)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	c, err := f.Start(t.Context())
	require.NoError(t, err)
	require.Equal(t, "dc", c.DeviceCode)
	require.Equal(t, "U", c.UserCode)
	require.Equal(t, "https://github/device", c.VerificationURL)
	require.Equal(t, 900, c.ExpiresIn)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tok, next, err := f.Wait(ctx, c)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, tok)
	require.Nil(t, next)
}

func TestCopilotHasNoCodeEntry(t *testing.T) {
	t.Parallel()
	f := newCopilot(copilotDeps{})
	require.False(t, f.CodeEntry())
	_, err := f.SubmitCode(t.Context(), "code")
	require.ErrorIs(t, err, ErrNoBrowserFlow)
	f.Close()
}

func TestOpenAIClosesTheListener(t *testing.T) {
	t.Parallel()
	br := &fakeBrowser{url: "http://localhost:1455/start"}
	f := newOpenAI(openAIDeps{startBrowser: func() (browserFlow, error) { return br, nil }})
	c, err := f.Start(t.Context())
	require.NoError(t, err)
	require.Equal(t, "http://localhost:1455/start", c.VerificationURL)
	require.False(t, f.CodeEntry())
	_, err = f.SubmitCode(t.Context(), "code")
	require.ErrorIs(t, err, ErrNoBrowserFlow)
	f.Close()
	require.True(t, br.closed)
}

func TestOpenAIWrapsTheStartError(t *testing.T) {
	t.Parallel()
	f := newOpenAI(openAIDeps{startBrowser: func() (browserFlow, error) { return nil, errors.New("boom") }})
	_, err := f.Start(t.Context())
	require.EqualError(t, err, "failed to start browser auth: boom")
}

func TestNewRefusesAPIKeyProviders(t *testing.T) {
	t.Parallel()
	_, err := New("anthropic")
	require.ErrorIs(t, err, ErrUnsupported)
	for _, id := range []string{"hyper", "copilot", "openai", "xai"} {
		f, err := New(id)
		require.NoError(t, err, id)
		require.NotEmpty(t, f.Name())
	}
}

func TestNamesMatchTheDialogs(t *testing.T) {
	t.Parallel()
	require.Equal(t, "GitHub Copilot", NewCopilot().Name())
	require.Equal(t, "Hyper", NewHyper().Name())
	require.Equal(t, "ChatGPT", NewOpenAI().Name())
	require.Equal(t, "Grok", NewGrok().Name())
}
