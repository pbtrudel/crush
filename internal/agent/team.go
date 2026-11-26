package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/roles"
	"github.com/charmbracelet/crush/internal/session"
)

// teamIdentity renders the member identity section appended to a teammate's
// system prompt. It carries no member name: N teammates spawned in one
// fan-out send this same text, and the provider only reuses a cached prefix
// when the bytes match. The name arrives in the task message instead, after
// every shared field.
func teamIdentity(member agentteam.Member) string {
	if member.Role == agentteam.RoleLead {
		return `## Team identity

You are the Team Lead named "lead".
Use list_agents({}) to find your teammates and their names.
To message a teammate, use send_message({ target: "<teammate name>", message: "..." }).`
	}
	return `## Team identity

You are a teammate. You are not the Lead. The message you were given names you.
The Agent Teams section above is the Lead's coordination brief. Its
instructions to delegate, spawn teammates, create worktrees, and wait for
reports do not apply to you: you do the work yourself with your own tools,
in the working directory the Lead named.
Claim the shared task before you start, and end your turn only when the work
is done, you are blocked, or you need the Lead to decide something.
Your Team Lead is named "lead".
To message your Team Lead, use send_message({ target: "lead", message: "..." }).
Use list_agents({}) to find your teammates and their names.
To message another teammate, use send_message({ target: "<teammate name>", message: "..." }).`
}

// teammateTaskMessage is the message a teammate turn starts from: its name,
// then the task. The name is the only per-sibling value in the request that
// used to sit in the system prompt, so it lives here, where it follows the
// whole shared prefix rather than interrupting it.
func teammateTaskMessage(member agentteam.Member, task string) string {
	return fmt.Sprintf("You are teammate %q.\n\n%s", member.Name, task)
}

// teammatePrompt is the message a teammate turn starts from: the inherited
// context a fork member gets, then, on its first turn only, its role block,
// then its name, then the task.
//
// The inherited context goes first because it is the same bytes for every fork
// sibling of one Lead. The role block follows because it is the same bytes for
// every sibling with that role; the per-sibling name comes after both, so the
// shared prefix runs through the whole role block.
func (r *teamRunner) teammatePrompt(ctx context.Context, member agentteam.Member, task string, first bool) string {
	task = teammateTaskMessage(member, task)
	if first && member.RoleName != "" {
		if role, refusal := resolveRole(r.c.cfg.Config(), member.RoleName); refusal == "" {
			task = roles.Block(*role) + task
		}
	}
	if member.Context == agentteam.ContextFork {
		return r.forkPrefix(ctx, member) + task
	}
	return task
}

// teamRunner runs teammate turns with the coordinator's agent machinery.
type teamRunner struct {
	c *coordinator
	// team is the service this runner was created for. The coordinator's
	// pointer can be retired by a reload while a turn is in flight, and
	// the session ids the turn hands out must keep the team that owns
	// them.
	team *agentteam.Service
}

// teamService returns the live Team service, or nil when Agent Teams is off.
// Callers load it once and use only that value, so a config reload cannot
// swap the service out from under them.
func (c *coordinator) teamService() *agentteam.Service {
	return c.team.Load()
}

// setTeam publishes the live Team service; nil retires it.
func (c *coordinator) setTeam(s *agentteam.Service) {
	c.team.Store(s)
}

// syncAgentTeams creates or tears down the Team service to match the
// persisted option. It runs at startup and on every config reload.
func (c *coordinator) syncAgentTeams() error {
	enabled := c.cfg.Config().Options.GetAgentTeams()
	c.teamSyncMu.Lock()
	defer c.teamSyncMu.Unlock()
	team := c.teamService()
	switch {
	case enabled && team == nil:
		teamPath := filepath.Join(c.cfg.Config().Options.DataDirectory, "agent-teams", "team.json")
		teamService, err := agentteam.New(teamPath, agentteam.Config{}, nil)
		if err != nil {
			return fmt.Errorf("initialize Agent Teams: %w", err)
		}
		teamService.SetRunner(&teamRunner{c: c, team: teamService})
		teamService.SetTaskCompletedHook(c.completeMatchingTodo)
		c.setTeam(teamService)
	case !enabled && team != nil:
		// Retire the service before shutting it down, so the next reader
		// sees Agent Teams as off. A reader that loaded it just before
		// still holds a valid service and gets its normal shut-down error.
		c.setTeam(nil)
		team.Shutdown()
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

	sess, created, err := r.teammateSession(memberCtx, member)
	if err != nil {
		return nil, err
	}
	r.c.approveForHeadlessRun(sess.ID, member.ParentID)

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

	promptText = r.teammatePrompt(memberCtx, member, promptText, created)

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(memberCtx), 30*time.Minute)
	run := &teamRun{
		sessionID: sess.ID,
		done:      make(chan struct{}),
		cancel:    cancel,
	}
	r.c.startTeammateRun(sess, member)
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
		r.c.finishAgentRun(sess.ID, runErr)
	}()
	return run, nil
}

// approveForHeadlessRun gives a teammate of a run with no user the approval
// its Lead has. The run command approves its own session; a teammate it
// starts would otherwise wait on a prompt nobody can answer. A Lead that is
// not approved, such as a client of the server, passes nothing on, and a
// nested teammate inherits from its parent teammate.
func (c *coordinator) approveForHeadlessRun(sessionID, leadSessionID string) {
	if c.interactive || c.permissions == nil || leadSessionID == "" {
		return
	}
	if !c.permissions.IsSessionAutoApproved(leadSessionID) {
		return
	}
	c.permissions.AutoApproveSession(sessionID)
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

	var role *roles.Role
	if member.RoleName != "" {
		resolved, refusal := resolveRole(r.c.cfg.Config(), member.RoleName)
		if refusal != "" {
			return nil, fmt.Errorf("teammate %q: role %q no longer exists", member.Name, member.RoleName)
		}
		role = resolved
		agentCfg.Role = role
	}

	models, err := r.teammateModels(member, role, lead.Model())
	if err != nil {
		return nil, err
	}

	// A group of its own: the coordinator's group is only safe for the
	// constructor, and the turn must not start until this teammate's own
	// tool palette is built. Building into c.readyWg and returning at once
	// left the turn with no tools, so the model could only narrate what it
	// was about to do and end the turn.
	var ready errgroup.Group
	agent, err := r.c.buildAgentWithModels(ctx, &ready, agentSystemPrompt{
		Prebuilt: lead.SystemPrompt(),
		Suffix:   teamIdentity(member),
	}, agentCfg, roleTeammate, models)
	if err != nil {
		return nil, err
	}
	if err := ready.Wait(); err != nil {
		return nil, err
	}
	return agent, nil
}

// teammateModels returns the model set a teammate turn runs on, or nil for
// the shared subagent set. A member with an override is re-resolved against
// the current config on every build, so a provider or model that went away
// is an error rather than a silent fallback to the default model.
func (r *teamRunner) teammateModels(member agentteam.Member, role *roles.Role, parent Model) (func() ModelSet, error) {
	params := AgentDispatchParams{
		Model:     member.Model,
		Provider:  member.ModelProvider,
		Reasoning: member.Reasoning,
	}
	if strings.TrimSpace(member.Model) == "" {
		if role == nil || !role.PinsModel() {
			return nil, nil
		}
		params = roleModelParams(role)
	}
	sel, ok, refusal := r.c.resolveModelOverride(params, parent)
	if refusal != "" {
		return nil, fmt.Errorf("teammate %q: %s", member.Name, refusal)
	}
	if !ok {
		return nil, fmt.Errorf("teammate %q has no model override to resolve", member.Name)
	}
	entry, err := r.c.overrideModel(sel)
	if err != nil {
		return nil, fmt.Errorf("teammate %q: failed to set up the requested model: %w", member.Name, err)
	}
	return func() ModelSet {
		return ModelSet{Large: entry.current(r.c), Small: r.c.subAgentModels().Small}
	}, nil
}

// teammateSession reuses the member's session, or creates it on first run.
// The bool is true when the session was created, which is what makes the
// member's first turn the only one that carries its role block.
func (r *teamRunner) teammateSession(ctx context.Context, member agentteam.Member) (session.Session, bool, error) {
	if member.SessionID != "" {
		existing, err := r.c.sessions.Get(ctx, member.SessionID)
		if err == nil {
			return existing, false, nil
		}
	}
	created, err := r.c.sessions.CreateTaskSession(ctx, r.teammateSessionID(member), member.ParentID, "Teammate "+member.Name)
	return created, err == nil, err
}

// teammateSessionID names a teammate session. Member ids restart with every
// new Team, so the Team id keeps a fresh team from colliding with the sessions
// an earlier team left in the same workspace database. The runner's own
// service supplies it, so a reload that retires the coordinator's pointer
// mid-turn cannot drop the team id.
func (r *teamRunner) teammateSessionID(member agentteam.Member) string {
	if r.team == nil {
		return "team-" + member.ID
	}
	teamID := strings.TrimPrefix(r.team.TeamID(), "team-")
	if teamID == "" {
		return "team-" + member.ID
	}
	return "team-" + member.ID + "-" + teamID
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
