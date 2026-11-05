package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

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
		teamService.SetTaskCompletedHook(c.completeMatchingTodo)
		c.team = teamService
	case !enabled && c.team != nil:
		c.team.Shutdown()
		c.team = nil
	}
	return nil
}

// completeMatchingTodo closes the session todo that matches a completed team
// task. The match is exact after normalization, so an unrelated todo is
// never closed by mistake; the Lead is told to reuse the todo text as the
// task subject.
func (c *coordinator) completeMatchingTodo(sessionID, subject string) {
	if c.sessions == nil || sessionID == "" || subject == "" {
		return
	}
	c.todoMu.Lock()
	defer c.todoMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		return
	}
	target := normalizeTodoText(subject)
	changed := false
	for i := range sess.Todos {
		if sess.Todos[i].Status != session.TodoStatusInProgress {
			continue
		}
		if normalizeTodoText(sess.Todos[i].Content) != target {
			continue
		}
		sess.Todos[i].Status = session.TodoStatusCompleted
		changed = true
	}
	if !changed {
		return
	}
	if err := c.sessions.UpdateTodos(ctx, sessionID, sess.Todos); err != nil {
		slog.Warn("Failed to complete session todo for team task", "session", sessionID, "error", err)
	}
}

// normalizeTodoText lowercases and collapses whitespace so a todo and a
// team task subject compare reliably.
func normalizeTodoText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
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
	// The member identity has to reach tool.Run: the agent and question
	// tools stay in the palette for cache parity but refuse outside the
	// Lead. WithoutCancel keeps the value across the turn's timeout.
	memberCtx := agentteam.WithMember(ctx, member.Name)

	sess, err := r.teammateSession(memberCtx, member)
	if err != nil {
		return nil, err
	}

	agent, err := r.teammateAgent(memberCtx, member)
	if err != nil {
		return nil, err
	}

	// The Lead's session decides how the teammate's request is filtered and
	// routed: its channel keeps channel-scoped MCP tools in the palette, and
	// its session id is the provider's sticky-routing hint, so both requests
	// land on the same upstream provider and can share its cache.
	lead := r.leadSession(memberCtx, member)

	// A tool the Lead adopted mid-turn sits at the end of its palette. The
	// teammate has to keep that order or its tools block stops matching the
	// one the Lead already cached.
	if lead.ID != "" {
		if leadAgent := r.c.coderAgent(); leadAgent != nil {
			agent.SetAdoptedToolOrder(sess.ID, leadAgent.AdoptedToolOrder(lead.ID))
		}
	}

	if member.Context == agentteam.ContextFork {
		promptText = r.forkPrefix(memberCtx, member) + promptText
	}

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(memberCtx), 30*time.Minute)
	run := &teamRun{
		sessionID: sess.ID,
		done:      make(chan struct{}),
		cancel:    cancel,
	}
	go func() {
		defer close(run.done)
		defer cancel()
		output, runErr := r.c.executeSubAgent(runCtx, agent, sess, subAgentParams{
			Agent:           agent,
			SessionID:       member.ParentID,
			AgentMessageID:  member.ID,
			ToolCallID:      member.ID,
			Prompt:          promptText,
			SessionTitle:    "Teammate " + member.Name,
			Channel:         lead.Channel,
			StickySessionID: lead.ID,
		})
		run.output = output
		run.err = runErr
	}()
	return run, nil
}

// leadSession returns the session a teammate runs on behalf of. Its channel
// and id are what keep the teammate's request filtered and routed like the
// Lead's; a member without a reachable parent session gets an empty one and
// the teammate keeps its own bindings.
func (r *teamRunner) leadSession(ctx context.Context, member agentteam.Member) session.Session {
	if member.ParentID == "" {
		return session.Session{}
	}
	lead, err := r.c.sessions.Get(ctx, member.ParentID)
	if err != nil {
		slog.Warn("Failed to read the Lead session for a teammate run", "session", member.ParentID, "error", err)
	}
	return lead
}

// teammateAgent builds the agent a teammate turn runs on. The base prompt is
// the Lead's built prompt, used verbatim, and only the member identity is
// appended. Re-rendering the template here would let date, git state, and
// context files drift between the two renders, and the provider caches on the
// longest shared prefix of the request, which starts with the tools block and
// the system message.
func (r *teamRunner) teammateAgent(ctx context.Context, member agentteam.Member) (SessionAgent, error) {
	// The Lead's prompt is rendered in the background, so wait for it rather
	// than starting a teammate on an empty base prompt.
	if err := r.c.readyWg.Wait(); err != nil {
		return nil, err
	}

	agentCfg, ok := r.c.cfg.Config().Agents[config.AgentCoder]
	if !ok {
		return nil, errors.New("coder agent not configured")
	}
	lead := r.c.coderAgent()
	if lead == nil {
		return nil, errors.New("coder agent not configured")
	}

	return r.c.buildAgent(ctx, agentSystemPrompt{
		Prebuilt: lead.SystemPrompt(),
		Suffix:   teamIdentity(member),
	}, agentCfg, roleTeammate)
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
