package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetPromptCacheTTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts *Options
		want string
	}{
		{name: "nil options", opts: nil, want: PromptCacheTTLDefault},
		{name: "unset", opts: &Options{}, want: PromptCacheTTLDefault},
		{name: "default", opts: &Options{PromptCacheTTL: PromptCacheTTLDefault}, want: PromptCacheTTLDefault},
		{name: "extended", opts: &Options{PromptCacheTTL: PromptCacheTTLExtended}, want: PromptCacheTTLExtended},
		{name: "extended padded and cased", opts: &Options{PromptCacheTTL: " 1H "}, want: PromptCacheTTLExtended},
		{name: "unknown", opts: &Options{PromptCacheTTL: "2h"}, want: PromptCacheTTLDefault},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, test.opts.GetPromptCacheTTL())
		})
	}
}
