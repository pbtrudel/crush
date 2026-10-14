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
	description := bashDescription(&config.Attribution{})

	require.NotContains(t, description, "Assisted-by")
	require.NotContains(t, description, "Co-Authored-By")
	require.NotContains(t, description, "Generated with Crush")
}

// Opting into the assisted-by trailer must not embed the model id in the
// description (that rewrites the tools block, and with it the prompt cache,
// every time the model changes); the description carries a stable token the
// tool expands when the command runs.
func TestBashDescription_AssistedByUsesStableToken(t *testing.T) {
	description := bashDescription(&config.Attribution{TrailerStyle: config.TrailerStyleAssistedBy})

	require.Contains(t, description, "Assisted-by: Crush:"+modelIDPlaceholder)
	require.NotContains(t, description, "test-model")
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
