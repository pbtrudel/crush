package tools

import (
	"context"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// With the default attribution settings the description must not mention any
// attribution at all: the model only writes a trailer when the user opted in,
// and a default that suggests one is what put "Assisted-by" in commits.
func TestBashDescription_NoAttributionByDefault(t *testing.T) {
	description := bashDescription(&config.Attribution{}, nil)

	require.NotContains(t, description, "Assisted-by")
	require.NotContains(t, description, "Co-Authored-By")
	require.NotContains(t, description, "Generated with Crush")
}

// Opting into the assisted-by trailer must not embed the model id in the
// description (that rewrites the tools block, and with it the prompt cache,
// every time the model changes); the description carries a stable token the
// tool expands when the command runs.
func TestBashDescription_AssistedByUsesStableToken(t *testing.T) {
	description := bashDescription(&config.Attribution{TrailerStyle: config.TrailerStyleAssistedBy}, nil)

	require.Contains(t, description, "Assisted-by: Crush:"+modelIDPlaceholder)
	require.NotContains(t, description, "test-model")
}

// The commit and pull request procedure used to be part of this description,
// which every request carries whether or not a commit is involved. It now
// lives in a skill, and the description keeps only a pointer plus the rules
// that have to hold at all times.
func TestBashDescription_PointsAtGitWorkflowSkill(t *testing.T) {
	description := bashDescription(&config.Attribution{}, nil)

	require.Contains(t, description, "crush://skills/git-workflow/SKILL.md")
	for _, gone := range []string{
		"<git_message_quality>",
		"<commit_messages>",
		"<git_commits>",
		"<pull_requests>",
		"<pr_analysis>",
	} {
		require.NotContains(t, description, gone)
	}
	require.Contains(t, description, "Never update git config")
	require.Contains(t, description, "never use interactive flags")
}

// Opting into attribution must still reach the model, now from the short
// block rather than from the full procedure.
func TestBashDescription_AttributionLinesWhenConfigured(t *testing.T) {
	tests := []struct {
		name        string
		attribution config.Attribution
		want        []string
	}{
		{
			name:        "generated with",
			attribution: config.Attribution{GeneratedWith: true},
			want:        []string{"End every pull request body with: 💘 Generated with Crush"},
		},
		{
			name:        "assisted by",
			attribution: config.Attribution{TrailerStyle: config.TrailerStyleAssistedBy},
			want:        []string{"Assisted-by: Crush:" + modelIDPlaceholder},
		},
		{
			name:        "co-authored by",
			attribution: config.Attribution{TrailerStyle: config.TrailerStyleCoAuthoredBy},
			want:        []string{"Co-Authored-By: Crush <crush@charm.land>"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			description := bashDescription(&test.attribution, nil)
			require.Contains(t, description, "End every commit message with these lines")
			require.Contains(t, description, "one blank line between them when more than one is listed",
				"a literal-minded model must be told not to stack the trailers")
			for _, want := range test.want {
				require.Contains(t, description, want)
			}
		})
	}
}

// The gh hint is only useful when the command exists, so it stays behind the
// same check it had when the whole procedure lived here.
func TestBashDescription_GhLineOnlyWhenAvailable(t *testing.T) {
	original := ghAvailable
	t.Cleanup(func() { ghAvailable = original })

	ghAvailable = true
	require.Contains(t, bashDescription(&config.Attribution{}, nil), "Use the `gh` command for ALL GitHub tasks.")

	ghAvailable = false
	require.NotContains(t, bashDescription(&config.Attribution{}, nil), "Use the `gh` command")
}

func TestBashTool_ExpandsModelIDPlaceholder(t *testing.T) {
	tool := newBashToolForTest(t.TempDir())
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "attribution trailer",
		Command:     "echo 'Assisted-by: Crush:<MODEL>'",
	})

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Assisted-by: Crush:test-model")
}
