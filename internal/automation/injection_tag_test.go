package automation

import (
	"testing"

	"github.com/charmbracelet/crush/internal/logreport"
	"github.com/stretchr/testify/require"
)

// A goal or a loop prompt starts a run the user did not type, so the
// report counts it as a hidden turn. Opening it with an injection tag
// would turn it into an injection.
func TestAutomationPromptsCarryNoInjectionTag(t *testing.T) {
	t.Parallel()

	require.Empty(t, logreport.InjectionTag(GoalPrompt("ship it", 0, 5, "")))
	require.Empty(t, logreport.InjectionTag(GoalPrompt("ship it", 2, 5, "tests fail")))
	require.Empty(t, logreport.InjectionTag(LoopPrompt(Task{ID: "t1", Kind: LoopFixed, Task: "check CI"}, 0)))
	require.Empty(t, logreport.InjectionTag(LoopPrompt(Task{ID: "t2", Kind: LoopSelfPaced, Task: "check CI"}, 1)))
}
