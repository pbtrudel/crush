package grade_test

import (
	"testing"

	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/stretchr/testify/require"
)

// TestPattern quotes the names it joins: a name is literal text in the -run
// pattern, and a character that the regular expression engine reads, such as
// a dot or a parenthesis, must match itself.
func TestPattern(t *testing.T) {
	t.Parallel()

	require.Equal(t, "^(TestA|TestB)$", grade.Pattern([]string{"TestA", "TestB"}))
	require.Equal(t, `^(TestA\(x\)|TestB\.c)$`, grade.Pattern([]string{"TestA(x)", "TestB.c"}))
}
