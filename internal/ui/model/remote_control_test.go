package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/session"
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
