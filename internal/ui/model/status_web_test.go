package model

import (
	"context"
	"testing"

	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/stretchr/testify/require"
)

type fakeWeb struct {
	running bool
	starts  int
	opens   int
}

func (f *fakeWeb) Start(context.Context) (string, error) {
	f.starts++
	return "http://127.0.0.1:1/#t=x", nil
}

func (f *fakeWeb) OpenBrowser() error {
	f.opens++
	return nil
}

func (f *fakeWeb) Running() bool { return f.running }

func (f *fakeWeb) SetInputMode(bool, uint64) {}

func (f *fakeWeb) ReportInputModeRefused(string) {}

func TestStatusBadgesIncludeWebWhenRunning(t *testing.T) {
	t.Parallel()
	com := common.DefaultCommon(nil)
	s := &Status{com: com}
	require.NotContains(t, s.badges(), "WEB")

	com.Web = &fakeWeb{running: false}
	require.NotContains(t, s.badges(), "WEB")

	com.Web = &fakeWeb{running: true}
	require.Contains(t, s.badges(), "WEB")
}
