package secretmask

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "openai style key",
			in:   "use sk-proj-abcdefghijklmnopqrstuvwxyz012345 for the call",
			want: "use sk-p… for the call",
		},
		{
			name: "anthropic style key",
			in:   "key: sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
			want: "key: sk-a…",
		},
		{
			name: "github classic token",
			in:   "token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 end",
			want: "token ghp_… end",
		},
		{
			name: "github fine grained token",
			in:   "github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz entered",
			want: "gith… entered",
		},
		{
			name: "gitlab token",
			in:   "glpat-ABCDEFGHIJKLMNOPQRSTUVWX used",
			want: "glpa… used",
		},
		{
			name: "aws access key",
			in:   "AKIAIOSFODNN7EXAMPLE is the key",
			want: "AKIA… is the key",
		},
		{
			name: "aws temporary key",
			in:   "ASIAIOSFODNN7EXAMPLE too",
			want: "ASIA… too",
		},
		{
			name: "slack token",
			// Split across literals so secret scanners do not flag the
			// fixture; the runtime value still matches the Slack shape.
			in:   "xoxb-" + "nota1234realtoken5678 here",
			want: "xoxb… here",
		},
		{
			name: "google api key",
			in:   "AIzaSyD-3exampleKey1234567890abcdefghij sent",
			want: "AIza… sent",
		},
		{
			name: "huggingface token",
			in:   "hf_abcdefghijklmnopqrstuvwxyz012345 saved",
			want: "hf_a… saved",
		},
		{
			name: "npm token",
			in:   "npm_abcdefghijklmnopqrstuvwxyz0123456789 published",
			want: "npm_… published",
		},
		{
			name: "stripe key",
			// Split across literals so secret scanners do not flag the
			// fixture; the runtime value still matches the Stripe shape.
			in:   "sk_" + "live_" + "abcdefghijklmnopqrstuvwxyz0123 charged",
			want: "sk_l… charged",
		},
		{
			name: "tavily key",
			in:   "tvly-abcdefghijklmnopqrstuvwx searched",
			want: "tvly… searched",
		},
		{
			name: "jwt",
			in:   "bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U done",
			want: "bearer eyJh… done",
		},
		{
			name: "private key block",
			in: "before\n-----BEGIN RSA PRIVATE KEY-----\n" +
				"MIIEpAIBAAKCAQEA1234567890abcdefghijklmnopqrstuv\n" +
				"-----END RSA PRIVATE KEY-----\nafter",
			want: "before\n[redacted private key]\nafter",
		},
		{
			name: "multiple secrets",
			in:   "key1 sk-abcdefghijklmnopqrstuvwx and AKIAIOSFODNN7EXAMPLE and tvly-abcdefghijklmnopqrstuvwx",
			want: "key1 sk-a… and AKIA… and tvly…",
		},
		{
			name: "secret at start and end",
			in:   "sk-abcdefghijklmnopqrstuvwx ends with AKIAIOSFODNN7EXAMPLE",
			want: "sk-a… ends with AKIA…",
		},
		{
			name: "plain text untouched",
			in:   "this is a normal sentence about masks and tasks",
			want: "this is a normal sentence about masks and tasks",
		},
		{
			name: "sk prefix inside a word is not a key",
			in:   "task-abcdefghijklmnopqrstuvwx is pending",
			want: "task-abcdefghijklmnopqrstuvwx is pending",
		},
		{
			name: "short sk text untouched",
			in:   "sk-short is not a key",
			want: "sk-short is not a key",
		},
		{
			name: "short aws text untouched",
			in:   "AKIA123 is not a key",
			want: "AKIA123 is not a key",
		},
		{
			name: "short hf text untouched",
			in:   "hf_short is not a key",
			want: "hf_short is not a key",
		},
		{
			name: "short slack text untouched",
			in:   "xoxb-123456 is not a key",
			want: "xoxb-123456 is not a key",
		},
		{
			name: "short jwt text untouched",
			in:   "eyJhbG.eyJzdWI.notenough",
			want: "eyJhbG.eyJzdWI.notenough",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, Mask(tt.in))
		})
	}
}
