package discord

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// threadInteraction builds a native slash command invocation in the
// control thread.
func threadInteraction(name string, args ...string) gatewayInteraction {
	in := gatewayInteraction{
		ID:        "i1",
		Token:     "itok",
		Type:      gwInteractionApplicationCommand,
		ChannelID: "thread-1",
	}
	in.Data.Name = name
	for _, arg := range args {
		in.Data.Options = append(in.Data.Options, gatewayInteractionOption{
			Name:  "query",
			Type:  3,
			Value: arg,
		})
	}
	in.Member.User.ID = "u1"
	return in
}

func TestBridgeInteractionRunsCommandInThread(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	b.handleThreadInteraction(threadInteraction("status"))

	callbacks := api.callbacksSnapshot()
	require.Len(t, callbacks, 1)
	require.Equal(t, "i1", callbacks[0].interactionID)
	require.Equal(t, "itok", callbacks[0].token)
	require.Equal(t, "Running `/status`", callbacks[0].content)
	require.True(t, callbacks[0].ephemeral)

	require.True(t, api.hasContentContaining("Test Session"), "status output lands in the thread")
	require.Zero(t, env.runCount(), "commands must not reach the agent")
}

func TestBridgeInteractionOptionBecomesArgs(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	b.handleThreadInteraction(threadInteraction("yolo", "on"))

	require.True(t, env.PermissionSkipRequests())
	require.True(t, api.hasContentContaining("YOLO mode enabled"))
	require.Equal(t, "Running `/yolo on`", api.callbacksSnapshot()[0].content)
}

func TestBridgeInteractionCompactAliasesSummarize(t *testing.T) {
	t.Parallel()
	b, env, _, _ := testBridge(t, testConfig())

	b.handleThreadInteraction(threadInteraction("compact"))

	require.Eventually(t, func() bool { return env.summarizeCount() == 1 }, 2*time.Second, 10*time.Millisecond)
}

func TestBridgeInteractionUnknownCommandAnswered(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	// /effort is registered on the bot but has no remote-control
	// equivalent; it must still get an answer instead of timing out.
	b.handleThreadInteraction(threadInteraction("effort"))

	require.Equal(t, "Running `/effort`", api.callbacksSnapshot()[0].content)
	require.True(t, api.hasContentContaining("Unknown command"))
}

func TestBridgeInteractionForeignChannelIgnored(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	in := threadInteraction("status")
	in.ChannelID = "elsewhere"
	b.handleThreadInteraction(in)

	require.Empty(t, api.callbacksSnapshot())
	require.Zero(t, env.runCount())
	require.Zero(t, env.cancelCount())
}

func TestBridgeInteractionUnauthorized(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.AllowedUserIDs = []string{"u9"}
	b, env, api, _ := testBridge(t, cfg)

	b.handleThreadInteraction(threadInteraction("stop"))

	callbacks := api.callbacksSnapshot()
	require.Len(t, callbacks, 1)
	require.Contains(t, callbacks[0].content, "Not authorized")
	require.Zero(t, env.cancelCount(), "unauthorized commands must not run")
}

func TestBridgeInteractionViaInputQueue(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	in := threadInteraction("stop")
	b.enqueueInput(inputEvent{interaction: &in})

	require.Eventually(t, func() bool { return env.cancelCount() == 1 }, 2*time.Second, 10*time.Millisecond)
	// The cancel confirmation is posted asynchronously after the counter
	// increments; wait for it instead of racing it (the immediate
	// assertion was flaky on Windows CI runners).
	require.Eventually(t, func() bool { return api.hasContentContaining("Cancelling") }, 2*time.Second, 10*time.Millisecond)
}

func TestInteractionCommandMapping(t *testing.T) {
	t.Parallel()

	name, args := interactionCommand(threadInteraction("MODEL", "gpt-5", "extra"))
	require.Equal(t, "model", name)
	require.Equal(t, "gpt-5 extra", args)

	name, args = interactionCommand(threadInteraction("compact"))
	require.Equal(t, "summarize", name)
	require.Empty(t, args)
}
