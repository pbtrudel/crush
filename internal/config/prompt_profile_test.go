package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The default profile has to stay the full prompt: an unset or unrecognised
// value must not silently shrink the system prompt a model is given.
func TestGetPromptProfile(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		opts *Options
		want string
	}{
		{name: "nil options", want: PromptProfileFull},
		{name: "unset", opts: &Options{}, want: PromptProfileFull},
		{name: "full", opts: &Options{PromptProfile: PromptProfileFull}, want: PromptProfileFull},
		{name: "lean", opts: &Options{PromptProfile: PromptProfileLean}, want: PromptProfileLean},
		{name: "unknown name", opts: &Options{PromptProfile: "tiny"}, want: PromptProfileFull},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, test.opts.GetPromptProfile())
		})
	}
}
