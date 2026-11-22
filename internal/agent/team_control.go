package agent

import (
	"context"
	"errors"

	"github.com/charmbracelet/crush/internal/agentteam"
)

// ErrAgentTeamsDisabled reports a team action while Agent Teams is off.
var ErrAgentTeamsDisabled = errors.New("agent teams are not enabled")

// TeamController is implemented by coordinators that let a frontend steer
// a session's teammates the way the Lead's own tools do.
type TeamController interface {
	// TeamSendMessage sends text to member as the Lead of sessionID's
	// team and wakes the member when it is inactive.
	TeamSendMessage(ctx context.Context, sessionID, member, text string) error
	// TeamInterrupt stops member's current turn.
	TeamInterrupt(ctx context.Context, sessionID, member string) error
}

var _ TeamController = (*coordinator)(nil)

func (c *coordinator) TeamSendMessage(ctx context.Context, sessionID, member, text string) error {
	// Read the service once, so the nil check and the call use the same one.
	return sendTeamMessage(ctx, c.team, sessionID, member, text)
}

func (c *coordinator) TeamInterrupt(_ context.Context, sessionID, member string) error {
	return interruptTeammate(c.team, sessionID, member)
}

// sendTeamMessage speaks as the Lead: the user steers the team through the
// Lead, and the Team service has no other sender a teammate would trust.
func sendTeamMessage(ctx context.Context, team *agentteam.Service, sessionID, member, text string) error {
	if team == nil {
		return ErrAgentTeamsDisabled
	}
	// The send may start the member's turn, which must outlive the request.
	return team.SendMessage(context.WithoutCancel(ctx), agentteam.LeadName, sessionID, agentteam.SendRequest{
		Target:  member,
		Message: text,
	})
}

func interruptTeammate(team *agentteam.Service, sessionID, member string) error {
	if team == nil {
		return ErrAgentTeamsDisabled
	}
	_, err := team.Interrupt(agentteam.LeadName, sessionID, member)
	return err
}
