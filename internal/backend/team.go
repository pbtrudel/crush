package backend

import (
	"context"

	"github.com/charmbracelet/crush/internal/agent"
)

// TeamSendMessage sends text to a teammate of sessionID's team as its Lead.
func (b *Backend) TeamSendMessage(ctx context.Context, workspaceID, sessionID, member, text string) error {
	tc, err := b.teamController(workspaceID)
	if err != nil {
		return err
	}
	return tc.TeamSendMessage(ctx, sessionID, member, text)
}

// TeamInterrupt stops a teammate's current turn.
func (b *Backend) TeamInterrupt(ctx context.Context, workspaceID, sessionID, member string) error {
	tc, err := b.teamController(workspaceID)
	if err != nil {
		return err
	}
	return tc.TeamInterrupt(ctx, sessionID, member)
}

func (b *Backend) teamController(workspaceID string) (agent.TeamController, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	if ws.AgentCoordinator == nil {
		return nil, ErrAgentNotInitialized
	}
	tc, ok := ws.AgentCoordinator.(agent.TeamController)
	if !ok {
		return nil, agent.ErrAgentTeamsDisabled
	}
	return tc, nil
}
