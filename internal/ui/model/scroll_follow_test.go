package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newScrolledChatUI builds a chat taller than its viewport, pinned to the
// bottom, so tests can scroll away and append.
func newScrolledChatUI(t *testing.T) *UI {
	t.Helper()
	m := newPendingUI(t)
	m.chat.SetSize(40, 6)
	body := strings.Repeat("line\n", 8)
	for i := range 4 {
		m.appendSessionMessage(userMessage(fmt.Sprintf("u%d", i), "s1", body))
	}
	m.chat.ScrollToBottom()
	require.True(t, m.chat.Follow())
	return m
}

// A message landing while the user reads history must not yank the view
// back to the bottom.
func TestAppendedUserMessageLeavesScrolledUpViewAlone(t *testing.T) {
	m := newScrolledChatUI(t)
	m.chat.ScrollBy(-5)
	require.False(t, m.chat.Follow())
	before := m.chat.Offset()

	m.appendSessionMessage(userMessage("report", "s1", "a subagent report"))

	require.Equal(t, before, m.chat.Offset())
	require.False(t, m.chat.AtBottom())
}

// At the bottom, every new message keeps the view following.
func TestAppendedUserMessageFollowsAtBottom(t *testing.T) {
	m := newScrolledChatUI(t)

	m.appendSessionMessage(userMessage("report", "s1", "a subagent report"))

	require.True(t, m.chat.Follow())
	require.True(t, m.chat.AtBottom())
}
