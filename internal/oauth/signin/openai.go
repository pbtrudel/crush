package signin

import (
	"context"
	"fmt"

	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/openai"
)

// openAIDeps are the provider calls the OpenAI flow makes, so tests can
// run it without opening a listener.
type openAIDeps struct {
	startBrowser func() (browserFlow, error)
}

// NewOpenAI returns the ChatGPT loopback browser sign-in.
func NewOpenAI() Flow {
	return newOpenAI(openAIDeps{startBrowser: func() (browserFlow, error) {
		flow, err := openai.StartBrowserFlow()
		if err != nil {
			return nil, err
		}
		return openAIBrowser{flow}, nil
	}})
}

func newOpenAI(deps openAIDeps) Flow {
	return &openAIFlow{deps: deps}
}

type openAIFlow struct {
	deps openAIDeps
	flow browserFlow
}

// openAIBrowser adapts the OpenAI browser flow, which has no pasted-code
// fallback, to browserFlow.
type openAIBrowser struct {
	*openai.BrowserFlow
}

func (openAIBrowser) CompleteWithCode(context.Context, string) (*oauth.Token, error) {
	return nil, ErrNoBrowserFlow
}

func (f *openAIFlow) Name() string { return "ChatGPT" }

func (f *openAIFlow) Start(context.Context) (Challenge, error) {
	flow, err := f.deps.startBrowser()
	if err != nil {
		return Challenge{}, fmt.Errorf("failed to start browser auth: %w", err)
	}
	f.flow = flow
	return Challenge{VerificationURL: flow.StartURL()}, nil
}

func (f *openAIFlow) Wait(ctx context.Context, _ Challenge) (*oauth.Token, *Challenge, error) {
	if f.flow == nil {
		return nil, nil, ErrNoBrowserFlow
	}
	tok, err := f.flow.Wait(ctx)
	if err != nil {
		return nil, nil, err
	}
	return tok, nil, nil
}

func (f *openAIFlow) CodeEntry() bool { return false }

// SubmitCode never succeeds: the OpenAI browser flow has no pasted-code
// fallback.
func (f *openAIFlow) SubmitCode(context.Context, string) (*oauth.Token, error) {
	return nil, ErrNoBrowserFlow
}

func (f *openAIFlow) Close() {
	if f.flow != nil {
		f.flow.Close()
	}
}
