package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The getter reports what was configured, verbatim, so the agent can tell an
// unset profile from one the user chose: what an unset profile renders
// depends on the model family.
func TestGetPromptProfile(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		opts *Options
		want string
	}{
		{name: "nil options", want: ""},
		{name: "unset", opts: &Options{}, want: ""},
		{name: "full", opts: &Options{PromptProfile: PromptProfileFull}, want: PromptProfileFull},
		{name: "lean", opts: &Options{PromptProfile: PromptProfileLean}, want: PromptProfileLean},
		{name: "unknown name", opts: &Options{PromptProfile: "tiny"}, want: "tiny"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, test.opts.GetPromptProfile())
		})
	}
}
