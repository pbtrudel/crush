package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSteeringSignal(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	ch := agent.beginSteering("session")
	select {
	case <-ch:
		t.Fatal("steering channel must start open")
	default:
	}

	agent.signalSteering("session")
	select {
	case <-ch:
	default:
		t.Fatal("steering channel must close on signal")
	}

	// Signaling twice must not panic.
	agent.signalSteering("session")

	agent.endSteering("session", ch)
	agent.steeringMu.Lock()
	_, ok := agent.steering["session"]
	agent.steeringMu.Unlock()
	require.False(t, ok)
}

func TestSteeringSignalStaleEndKeepsNewerSignal(t *testing.T) {
	t.Parallel()

	agent := &sessionAgent{}
	first := agent.beginSteering("session")
	second := agent.beginSteering("session")
	agent.endSteering("session", first)

	agent.steeringMu.Lock()
	signal, ok := agent.steering["session"]
	agent.steeringMu.Unlock()
	require.True(t, ok)
	require.Equal(t, second, signal.ch)
}
