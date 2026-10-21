package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/discord"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

func TestRemoteControlCreatesSessionWhenNoneActive(t *testing.T) {
	ws := &channelWorkspace{ready: true, newSession: session.Session{ID: "rc-sess"}}
	m := newChannelUI(ws)

	cmds := m.toggleRemoteControl()

	if ws.createCalls != 1 {
		t.Fatalf("CreateSession calls = %d, want 1", ws.createCalls)
	}
	if m.session == nil || m.session.ID != "rc-sess" {
		t.Fatalf("session = %+v, want rc-sess", m.session)
	}
	if len(cmds) == 0 {
		t.Fatal("expected commands to load the session and start the bridge")
	}
}

// TestRemoteControlSecondToggleWhileStartingIsIgnored pins that a toggle
// pressed while the first start is still in flight (discord.Start is
// asynchronous) does not launch a second bridge. Two live starts used to
// produce two bridges; the second one's arrival stopped the first, posting
// a farewell on a thread the user had just connected and silently moving
// remote control to a new thread.
func TestRemoteControlSecondToggleWhileStartingIsIgnored(t *testing.T) {
	ws := &channelWorkspace{ready: true, newSession: session.Session{ID: "rc-sess"}}
	m := newChannelUI(ws)
	m.session = &session.Session{ID: "rc-sess"}

	first := m.toggleRemoteControl()
	if !m.rcStarting {
		t.Fatal("a start must be marked in flight until remoteControlStartedMsg arrives")
	}
	if len(first) == 0 {
		t.Fatal("the first toggle must schedule the start")
	}

	second := m.toggleRemoteControl()
	if !m.rcStarting {
		t.Fatal("the refused toggle must leave the start in flight")
	}
	var texts []string
	for _, cmd := range second {
		if cmd == nil {
			continue
		}
		if info, ok := cmd().(util.InfoMsg); ok {
			texts = append(texts, info.Msg)
		}
	}
	require.Len(t, texts, 1, "the second toggle must only report that a start is in flight")
	require.Contains(t, texts[0], "already starting")
}

// TestRemoteControlClearFollowsNewSession pins that a /clear from Discord
// moves the TUI to the freshly created session while remote control keeps
// the same thread, so the terminal and the thread stay on one session.
func TestRemoteControlClearFollowsNewSession(t *testing.T) {
	t.Parallel()
	ws := &channelWorkspace{ready: true}
	m := newChannelUI(ws)
	bridge := &discord.Bridge{}
	m.rc = bridge
	m.session = &session.Session{ID: "sess-1"}

	cmds := m.followRemoteClear(discord.SessionClearedMsg{Bridge: bridge, From: "sess-1", To: "sess-2"})
	require.NotEmpty(t, cmds)
	runCmds(m, tea.Batch(cmds...))

	require.Equal(t, "sess-2", ws.loadedSessionID, "the new session must be loaded into the view")
	require.Equal(t, "sess-2", ws.declaredCurrent, "the workspace must hear about the switch")
}

// TestRemoteControlClearIgnoredForOtherSessions pins that a /clear only
// moves the view when the TUI is showing the cleared session for the
// bridge it tracks.
func TestRemoteControlClearIgnoredForOtherSessions(t *testing.T) {
	t.Parallel()
	ws := &channelWorkspace{ready: true}
	m := newChannelUI(ws)
	bridge := &discord.Bridge{}
	m.rc = bridge
	m.session = &session.Session{ID: "other"}

	cmds := m.followRemoteClear(discord.SessionClearedMsg{Bridge: bridge, From: "sess-1", To: "sess-2"})
	require.Empty(t, cmds, "a clear of another session must not switch the view")

	// A bridge the TUI no longer tracks is ignored even when the IDs match.
	m.session = &session.Session{ID: "sess-1"}
	m.rc = nil
	cmds = m.followRemoteClear(discord.SessionClearedMsg{Bridge: bridge, From: "sess-1", To: "sess-2"})
	require.Empty(t, cmds, "an untracked bridge must not switch the view")
	require.Empty(t, ws.loadedSessionID)
}

// TestRemoteControlStartedClearsStartingFlag pins that both outcomes of an
// asynchronous start release the in-flight guard, so a failed start does not
// leave remote control permanently un-toggleable.
func TestRemoteControlStartedClearsStartingFlag(t *testing.T) {
	ws := &channelWorkspace{ready: true, newSession: session.Session{ID: "rc-sess"}}
	m := newChannelUI(ws)
	m.session = &session.Session{ID: "rc-sess"}

	m.rcStarting = true
	_, _ = m.Update(remoteControlStartedMsg{err: errors.New("start failed")})
	if m.rcStarting {
		t.Fatal("a failed start must release the in-flight guard")
	}

	m.rcStarting = true
	_, _ = m.Update(remoteControlStartedMsg{bridge: &discord.Bridge{}})
	if m.rcStarting {
		t.Fatal("a completed start must release the in-flight guard")
	}
	if m.rc == nil {
		t.Fatal("a completed start must install the bridge")
	}
}

// TestRemoteControlSurvivesConfigReload pins that /reload does not disturb an
// active Discord bridge. The reload runs in place (config snapshot, agent
// models, tools and skills), so long-lived subscribers such as the bridge must
// keep their connection instead of being restarted with the workspace.
func TestRemoteControlSurvivesConfigReload(t *testing.T) {
	t.Parallel()
	ws := &channelWorkspace{ready: true}
	m := newChannelUI(ws)
	m.dialog = dialog.NewOverlay()
	bridge := &discord.Bridge{}
	m.rc = bridge

	_, _ = m.Update(configReloadedMsg{})

	require.Same(t, bridge, m.rc, "a config reload must not drop remote control")
}
