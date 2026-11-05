package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/session"
)

// teamIdentity renders the member identity section.
func teamIdentity(member agentteam.Member) string {
	if member.Role == agentteam.RoleLead {
		return `## Team identity

You are the Team Lead named "lead".
Use list_agents({}) to find your teammates and their names.
To message a teammate, use send_message({ target: "<teammate name>", message: "..." }).`
	}
	return fmt.Sprintf(`## Team identity

You are teammate %q.
Your Team Lead is named "lead".
To message your Team Lead, use send_message({ target: "lead", message: "..." }).
Use list_agents({}) to find your teammates and their names.
To message another teammate, use send_message({ target: "<teammate name>", message: "..." }).`, member.Name)
}

// teamPrompt builds the teammate system prompt: the coder prompt plus the
// member identity section.
func teamPrompt(member agentteam.Member, workingDir string) (*prompt.Prompt, error) {
	template := string(coderPromptTmpl) + "\n\n" + teamIdentity(member) + "\n"
	return prompt.NewPrompt("team-"+member.Name, template, prompt.WithWorkingDir(workingDir))
}

// teammateTools keeps the full coder tool surface but removes nested
// delegation, which the Team domain does not support.
func teammateTools(allowed []string) []string {
	result := make([]string, 0, len(allowed))
	for _, name := range allowed {
		if name == AgentToolName || name == SubagentOutputToolName {
			continue
		}
		result = append(result, name)
	}
	return result
}

// teamRunner runs teammate turns with the coordinator's agent machinery.
type teamRunner struct {
	c *coordinator
}

// syncAgentTeams creates or tears down the Team service to match the
// persisted option. It runs at startup and on every config reload.
func (c *coordinator) syncAgentTeams() error {
	enabled := c.cfg.Config().Options.GetAgentTeams()
	switch {
	case enabled && c.team == nil:
		teamPath := filepath.Join(c.cfg.Config().Options.DataDirectory, "agent-teams", "team.json")
		teamService, err := agentteam.New(teamPath, agentteam.Config{}, nil)
		if err != nil {
			return fmt.Errorf("initialize Agent Teams: %w", err)
		}
		teamService.SetRunner(&teamRunner{c: c})
		c.team = teamService
	case !enabled && c.team != nil:
		c.team.Shutdown()
		c.team = nil
	}
	return nil
}

// teamRun is one live teammate turn.
type teamRun struct {
	sessionID string
	done      chan struct{}
	output    string
	err       error
	cancel    context.CancelFunc
}

func (r *teamRun) SessionID() string     { return r.sessionID }
func (r *teamRun) Done() <-chan struct{} { return r.done }
func (r *teamRun) Output() string        { return r.output }
func (r *teamRun) Err() error            { return r.err }
func (r *teamRun) Cancel()               { r.cancel() }

// Start creates or reuses the teammate session and runs one turn.
func (r *teamRunner) Start(ctx context.Context, member agentteam.Member, promptText string) (agentteam.Run, error) {
	cfg := r.c.cfg.Config()
	agentCfg, ok := cfg.Agents[config.AgentCoder]
	if !ok {
		return nil, errors.New("coder agent not configured")
	}
	agentCfg.AllowedTools = teammateTools(agentCfg.AllowedTools)

	systemPrompt, err := teamPrompt(member, r.c.cfg.WorkingDir())
	if err != nil {
		return nil, err
	}

	agent, err := r.c.buildAgent(agentteam.WithMember(ctx, member.Name), systemPrompt, agentCfg, true)
	if err != nil {
		return nil, err
	}

	sess, err := r.teammateSession(ctx, member)
	if err != nil {
		return nil, err
	}

	if member.Context == agentteam.ContextFork {
		promptText = r.forkPrefix(ctx, member) + promptText
	}

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
	run := &teamRun{
		sessionID: sess.ID,
		done:      make(chan struct{}),
		cancel:    cancel,
	}
	go func() {
		defer close(run.done)
		defer cancel()
		output, runErr := r.c.executeSubAgent(runCtx, agent, sess, subAgentParams{
			Agent:          agent,
			SessionID:      member.ParentID,
			AgentMessageID: member.ID,
			ToolCallID:     member.ID,
			Prompt:         promptText,
			SessionTitle:   "Teammate " + member.Name,
		})
		run.output = output
		run.err = runErr
	}()
	return run, nil
}

// teammateSession reuses the member's session, or creates it on first run.
func (r *teamRunner) teammateSession(ctx context.Context, member agentteam.Member) (session.Session, error) {
	if member.SessionID != "" {
		existing, err := r.c.sessions.Get(ctx, member.SessionID)
		if err == nil {
			return existing, nil
		}
	}
	return r.c.sessions.CreateTaskSession(ctx, "team-"+member.ID, member.ParentID, "Teammate "+member.Name)
}

// forkPrefix renders the Lead's completed turns for a fork teammate.
func (r *teamRunner) forkPrefix(ctx context.Context, member agentteam.Member) string {
	if member.ParentID == "" {
		return ""
	}
	messages, err := r.c.messages.List(ctx, member.ParentID)
	if err != nil || len(messages) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# Context inherited from the Team Lead\n\n")
	for _, msg := range messages {
		text := strings.TrimSpace(msg.Content().Text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&sb, "%s: %s\n\n", msg.Role, text)
		if sb.Len() > 16000 {
			sb.WriteString("[... inherited context truncated ...]\n\n")
			break
		}
	}
	return sb.String()
}
