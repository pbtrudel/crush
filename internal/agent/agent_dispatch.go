package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/roles"
)

//go:embed templates/agent_dispatch_tool.md
var agentDispatchToolDescription string

//go:embed templates/agent_send_tool.md
var agentSendToolDescription string

//go:embed templates/agent_stop_tool.md
var agentStopToolDescription string

const (
	AgentDispatchToolName = "agent_dispatch"
	AgentSendToolName     = "agent_send"
	AgentStopToolName     = "agent_stop"
)

// agentsOffRefusal is the tool error a tool that creates agent work returns
// when Agent Teams is off at call time.
const agentsOffRefusal = "Agents are turned off; enable Agent Teams to delegate work to another agent"

// refuseAgentsOff reports the tool error a call that needs Agent Teams
// answers with, and false when it may run. A turn that started while Agent
// Teams was on holds a tool list that cannot be rebuilt under it, so the
// check happens per call, and every caller decides with this one rule:
// a call that starts, steers or stops agent work is refused while the Team
// is off. That is agent_dispatch, agent_send (including its teammate
// delivery), the agent tool, spawn_teammate, send_message (which can wake a
// teammate), interrupt_agent (including its teammate path) and
// team_task_create. The calls that only observe keep working: agent_status,
// agent_stop, list_agents, wait_agent, team_task_get, team_task_list and
// team_task_update, so work already in flight can still be watched, waited
// on and read while the toggle settles.
func (c *coordinator) refuseAgentsOff() (fantasy.ToolResponse, bool) {
	if !c.agentsOff() {
		return fantasy.ToolResponse{}, false
	}
	return fantasy.NewTextErrorResponse(agentsOffRefusal), true
}

// stopForShutdown is the stop reason used when Crush itself is quitting.
// Nobody is left to read a report then, so none is written; the next run on
// the parent session finds the dispatch unanswered and reports it as
// interrupted instead.
const stopForShutdown = "Crush is shutting down"

type AgentDispatchParams struct {
	Prompt string `json:"prompt" description:"The task for the subagent to perform"`
	Label  string `json:"label" description:"A short label for this subagent's scope; the result returns the name to address it by"`
	Access string `json:"access,omitempty" description:"\"read\" (default) for a subagent that can only inspect the codebase, or \"write\" for one that can also edit files"`
	Role   string `json:"role,omitempty" description:"Optional role from the catalog below. It sets the subagent's tool allow-list, prompt and default model; access can only narrow it"`

	// Model override. When none of these is given the subagent runs on the
	// session's subagent model with its configured settings.
	Model     string `json:"model,omitempty" description:"The model ID for this subagent, as used by the provider API. Omit provider to use the parent agent's provider. Other settings default to the model's own"`
	Provider  string `json:"provider,omitempty" description:"The provider ID for the overridden model; requires model. Defaults to the parent agent's provider when only model is given"`
	Reasoning string `json:"reasoning,omitempty" description:"How hard the subagent reasons: one of the values the [models] section of crush_info lists in brackets for its model. Defaults to the model's own"`
}

// AgentSendResponseMetadata tells the UI which subagent a message went to,
// and whether it restarted a finished one, so the transcript can show that
// subagent as working again.
type AgentSendResponseMetadata struct {
	Label    string `json:"label"`
	Reopened bool   `json:"reopened,omitempty"`
	// Set when the message moved the subagent to another model, so a
	// restart resumes it there.
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	RunModel  string `json:"run_model,omitempty"`
}

// AgentDispatchResponseMetadata records a started dispatch's override as the
// call asked for it, with the provider resolved, so a resume after restart
// runs on the same provider whatever the session's model is by then.
type AgentDispatchResponseMetadata struct {
	Label     string `json:"label"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	Role      string `json:"role,omitempty"`
	// Access is the effective access a role dispatch ran on, so a resume
	// and agent_status do not re-derive it from the raw request.
	Access string `json:"access,omitempty"`
	// RunModel is provider/model for display; rehydration ignores it.
	RunModel string `json:"run_model,omitempty"`
}

type AgentSendParams struct {
	Label   string `json:"label" description:"The name of the subagent to send to, as returned when it was created"`
	Message string `json:"message" description:"The message to deliver to that subagent"`

	// Model switch, for a subagent that is not running. Same meaning as on
	// agent_dispatch; the subagent keeps its session.
	Model     string `json:"model,omitempty" description:"Move the subagent to this model before delivering, keeping its session. Only for a subagent that is not running. The [models] section of crush_info lists the IDs"`
	Provider  string `json:"provider,omitempty" description:"The provider ID for model; requires model. Defaults to the parent agent's provider"`
	Reasoning string `json:"reasoning,omitempty" description:"How hard the subagent reasons on its new model: one of the values the [models] section of crush_info lists in brackets for it"`
}

func (p AgentSendParams) switchesModel() bool {
	return strings.TrimSpace(p.Model) != "" || strings.TrimSpace(p.Provider) != "" || strings.TrimSpace(p.Reasoning) != ""
}

type AgentStopParams struct {
	Label  string `json:"label" description:"The name of the subagent to stop, as returned when it was created"`
	Reason string `json:"reason,omitempty" description:"Why it is being stopped; recorded in its report"`
}

// dispatchState is where a dispatched subagent is in its life.
type dispatchState int

const (
	dispatchRunning dispatchState = iota
	dispatchFinished
	dispatchFailed
	dispatchStopped
)

// reportArrivesOnItsOwn closes every result that leaves a subagent working.
// Models read a started-but-unfinished job as something to wait on, and
// without this they sleep and poll agent_status until the report lands,
// which it would have done anyway.
const reportArrivesOnItsOwn = " The report arrives on its own and wakes you, so carry on with" +
	" anything that does not need it, then end your turn. Do not sleep or" +
	" check agent_status to wait for it."

// SubagentState is a subagent's state as agent_status reports it.
type SubagentState string

const (
	SubagentRunning     SubagentState = "running"
	SubagentStopping    SubagentState = "stopping"
	SubagentFinished    SubagentState = "finished"
	SubagentFailed      SubagentState = "failed"
	SubagentStopped     SubagentState = "stopped"
	SubagentInterrupted SubagentState = "interrupted"
)

func (s dispatchState) reported() SubagentState {
	switch s {
	case dispatchRunning:
		return SubagentRunning
	case dispatchFailed:
		return SubagentFailed
	case dispatchStopped:
		return SubagentStopped
	}
	return SubagentFinished
}

// dispatchedAgent tracks one detached subagent across all of its runs: the
// first one, and every follow-up that reopens it.
type dispatchedAgent struct {
	Label           string
	SessionID       string
	ParentSessionID string
	Access          string
	// Role names the role this subagent runs in, or "" for one without a
	// role. A rebuild of its agent resolves the role again, so it never
	// comes back ungated.
	Role string
	// agent is the SessionAgent instance actually running this subagent.
	// Busy state and the message queue live on that instance, not on the
	// coordinator's main agent, so a message meant for a subagent has to
	// be addressed to it directly.
	agent SessionAgent

	mu sync.Mutex
	// gen numbers the runs. Each run settles only its own generation, so a
	// run that is slow to unwind cannot mark a newer run as finished.
	gen        uint64
	state      dispatchState
	cancel     context.CancelFunc
	stopReason string
	started    time.Time
	ended      time.Time
	// done closes when the current run settles.
	done chan struct{}
	// inline is set while a caller waits on the run and takes its outcome
	// instead of a report.
	inline  bool
	outcome runOutcome

	// rolledUpCost is the child session's cumulative cost already added to
	// the parent. Each run rolls up only what it spent since, since the
	// child's own total is cumulative across every turn.
	rolledUpCost atomic.Uint64

	// cleanup removes what the subagent's runs own, such as the fetch
	// folder a reopened run recreates. It runs after every run the entry
	// performs, and cleanedGen names the run whose cleanup already ran so
	// one run removes it once.
	cleanup    func()
	cleanedGen uint64
}

// runOutcome is what one run of a subagent returned.
type runOutcome struct {
	resp fantasy.ToolResponse
	err  error
}

// newDispatchedAgent returns an entry for a subagent that is not running.
// Callers start it with claim.
func newDispatchedAgent(label, access, role, sessionID, parentSessionID string, agent SessionAgent) *dispatchedAgent {
	return &dispatchedAgent{
		Label:           label,
		Access:          access,
		Role:            role,
		SessionID:       sessionID,
		ParentSessionID: parentSessionID,
		agent:           agent,
		state:           dispatchFinished,
	}
}

// claim starts a new run if the subagent is idle. The returned context is
// detached from whatever turn asked for the run: a subagent outlives the
// turn that started it and is stopped only through stop.
func (d *dispatchedAgent) claim() (context.Context, uint64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == dispatchRunning {
		return nil, 0, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.gen++
	d.state = dispatchRunning
	d.cancel = cancel
	d.stopReason = ""
	d.started = time.Now()
	d.ended = time.Time{}
	d.done = make(chan struct{})
	d.inline = false
	d.outcome = runOutcome{}
	return ctx, d.gen, true
}

// settle records how run gen ended. It returns the stop reason if the run
// was stopped, and whether the outcome should be reported rather than taken
// by an inline caller. A stale generation is ignored.
func (d *dispatchedAgent) settle(gen uint64, out runOutcome) (stopReason string, report bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if gen != d.gen {
		return "", false
	}
	switch {
	case d.stopReason != "":
		d.state = dispatchStopped
	case out.err != nil || out.resp.IsError:
		d.state = dispatchFailed
	default:
		d.state = dispatchFinished
	}
	d.ended = time.Now()
	d.outcome = out
	d.cancel()
	close(d.done)
	return d.stopReason, !d.inline
}

// runInline marks run gen as having a caller that takes its outcome, and
// returns the channel that closes when it settles.
func (d *dispatchedAgent) runInline(gen uint64) <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if gen == d.gen {
		d.inline = true
	}
	return d.done
}

// detach hands run gen over to the report path. It returns false if the run
// already settled, in which case the inline caller takes its outcome.
func (d *dispatchedAgent) detach(gen uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if gen != d.gen || d.state != dispatchRunning {
		return false
	}
	d.inline = false
	return true
}

func (d *dispatchedAgent) result() (fantasy.ToolResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outcome.resp, d.outcome.err
}

// stop cancels the run in flight. It returns false if nothing is running.
// The entry stays running until the run has actually unwound.
func (d *dispatchedAgent) stop(reason string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state != dispatchRunning {
		return false
	}
	if d.stopReason == "" {
		d.stopReason = reason
	}
	d.cancel()
	return true
}

func (d *dispatchedAgent) running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state == dispatchRunning
}

// stopping reports whether a stop was requested for a run still unwinding.
func (d *dispatchedAgent) stopping() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state == dispatchRunning && d.stopReason != ""
}

// dispatchSnapshot is a consistent read of an entry's lifecycle fields.
type dispatchSnapshot struct {
	State      dispatchState
	StopReason string
	Started    time.Time
	Ended      time.Time
}

func (d *dispatchedAgent) snapshot() dispatchSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return dispatchSnapshot{
		State:      d.state,
		StopReason: d.stopReason,
		Started:    d.started,
		Ended:      d.ended,
	}
}

// awaitSettled waits until the run in flight has unwound, or the timeout
// passes. It reports whether the subagent is idle.
func (d *dispatchedAgent) awaitSettled(timeout time.Duration) bool {
	d.mu.Lock()
	running, done := d.state == dispatchRunning, d.done
	d.mu.Unlock()
	if !running {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// awaitDeliverable waits until the subagent's session is actually running
// and can accept a message, and reports whether it can. A run is claimed
// before its goroutine has started the session, so a message sent right
// after dispatching arrives while the session is not yet busy; treating that
// window as "finished" would drop the message. Waiting closes the gap
// without blocking indefinitely on a subagent that died on startup.
func (d *dispatchedAgent) awaitDeliverable(ctx context.Context) bool {
	const (
		pollEvery = 50 * time.Millisecond
		giveUp    = 10 * time.Second
	)
	deadline := time.NewTimer(giveUp)
	defer deadline.Stop()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()

	for {
		if !d.running() || d.stopping() {
			return false
		}
		if d.runner().HasActiveTurn(d.SessionID) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

// dispatchKey namespaces a label to its parent session, so two sessions can
// use the same label without colliding.
func dispatchKey(parentSessionID, label string) string {
	return parentSessionID + "\x00" + label
}

// agentDispatchTool launches a subagent that runs detached from this tool
// call. The tool returns as soon as the subagent starts; the result is
// delivered later as a message into the parent session.
func (c *coordinator) agentDispatchTool(ctx context.Context, parentAgentID string, parentModel func() Model) (fantasy.AgentTool, error) {
	readOnly := readOnlyParent(parentAgentID)
	agents, err := c.taskAgents(ctx, readOnly)
	if err != nil {
		return nil, err
	}

	return fantasy.NewParallelAgentTool(
		AgentDispatchToolName,
		strings.TrimSpace(agentDispatchToolDescription)+roleCatalog(c.cfg.Config()),
		func(ctx context.Context, params AgentDispatchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if refusal, off := c.refuseAgentsOff(); off {
				return refusal, nil
			}
			if params.Prompt == "" {
				return fantasy.NewTextErrorResponse("prompt is required"), nil
			}
			if strings.TrimSpace(params.Label) == "" {
				return fantasy.NewTextErrorResponse("label is required"), nil
			}

			access := accessOrRead(params.Access)
			prompt := params.Prompt
			var (
				agent       SessionAgent
				sel         config.SelectedModel
				hasOverride bool
				roleName    string
			)
			if strings.TrimSpace(params.Role) != "" {
				role, refusal := resolveRole(c.cfg.Config(), params.Role)
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				if refusal := planModeRoleRefusal(readOnly, role, params.Access); refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				agent, sel, hasOverride, refusal = c.roleTaskAgent(ctx, role, params.Access, params, parentModel())
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				access, _ = roleAccess(role, params.Access)
				roleName = role.Name
				prompt = roles.Block(*role) + params.Prompt
			} else {
				var refusal string
				agent, refusal = resolveSubAgent(agents, params.Access, readOnly)
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				// A model override swaps the shared subagent for a dedicated
				// one running on the requested model. It is resolved before the
				// label is taken, so a refused override leaves the label free
				// and the call can be retried immediately.
				overrideAgent, overrideSel, override, refusal := c.overrideDispatchAgent(ctx, access, params, parentModel())
				if refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				if overrideAgent != nil {
					agent = overrideAgent
				}
				sel, hasOverride = overrideSel, override
			}
			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}

			parentSessionID := tools.GetSessionFromContext(ctx)
			if parentSessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			// The sub-session ID derives from the bare tool call ID so the
			// UI can walk back from a child-session event to this tool's
			// chat item and nest the subagent's tool calls under it.
			toolCallID := call.ID

			// Named last, so a refused dispatch does not use up a first name.
			label := c.names.name(params.Label)

			// Checking the label and registering the entry happen under
			// one lock, so two parallel dispatches cannot both take it.
			key := dispatchKey(parentSessionID, label)
			c.dispatchMu.Lock()
			if existing, ok := c.dispatched.Get(key); ok && existing.running() {
				c.dispatchMu.Unlock()
				return fantasy.NewTextErrorResponse(fmt.Sprintf("subagent %q is already running; pick another label or stop it first", label)), nil
			}
			entry := newDispatchedAgent(
				label,
				access,
				roleName,
				c.sessions.CreateAgentToolSessionID(agentMessageID, toolCallID),
				parentSessionID,
				agent,
			)
			runCtx, gen, _ := entry.claim()
			c.dispatched.Set(key, entry)
			c.dispatchMu.Unlock()

			c.runDispatched(runCtx, entry, gen, func(ctx context.Context) (fantasy.ToolResponse, error) {
				return c.runSubAgent(ctx, subAgentParams{
					Agent:          agent,
					SessionID:      parentSessionID,
					AgentMessageID: agentMessageID,
					ToolCallID:     toolCallID,
					Prompt:         prompt,
					SessionTitle:   "Subagent: " + label,
					Label:          label,
					SkipCostRollup: true,
				})
			})

			meta := AgentDispatchResponseMetadata{
				Label:    label,
				RunModel: modelDisplayName(agent.Model()),
			}
			if hasOverride {
				meta.Model, meta.Provider, meta.Reasoning = overrideRecord(params, sel)
			}
			meta.Role = roleName
			if roleName != "" {
				// The transcript keeps the effective access, so a
				// resume and agent_status never see the raw request.
				meta.Access = access
			}
			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(fmt.Sprintf("Started subagent %q.", label)+reportArrivesOnItsOwn),
				meta,
			), nil
		},
	), nil
}

// runDispatched runs one generation of a subagent in the background, then
// settles the entry and reports the outcome to the parent.
func (c *coordinator) runDispatched(ctx context.Context, entry *dispatchedAgent, gen uint64, run func(context.Context) (fantasy.ToolResponse, error)) {
	// Tracked from here, not from when the session starts, so the busy
	// check never sees a claimed run as idle.
	// The dispatched entry above already routes its own cancel, so the
	// run needs no agent recorded here.
	untrack := c.trackSubSession(entry.SessionID, nil)
	go func() {
		resp, err := func() (fantasy.ToolResponse, error) {
			defer entry.runCleanup(gen)
			return run(ctx)
		}()
		untrack()
		c.rollUpDispatchCost(context.Background(), entry)
		// Settle before reporting: the report starts a parent turn, and
		// that turn may well send this subagent a follow-up, which needs
		// it idle.
		stopReason, report := entry.settle(gen, runOutcome{resp, err})
		c.pruneDispatched()
		if !report {
			return
		}

		switch stopReason {
		case "":
			c.reportDispatchResult(entry, resp, err)
		case stopForShutdown:
		default:
			// A stopped subagent has nothing to report and its parent
			// already knows it was stopped, so record the outcome
			// without starting a turn over it.
			c.recordDispatchReport(entry.ParentSessionID, message.SubAgentReport{
				Label:  entry.Label,
				Output: "Stopped: " + stopReason + ".",
				Failed: true,
			})
		}
	}()
}

// setCleanup records what the entry's runs must remove when they return.
func (d *dispatchedAgent) setCleanup(cleanup func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cleanup = cleanup
}

// runCleanup runs the entry's cleanup once for gen, so the paths a run takes
// out of the registry cannot remove the same resources twice.
func (d *dispatchedAgent) runCleanup(gen uint64) {
	d.mu.Lock()
	cleanup := d.cleanup
	if cleanup == nil || d.cleanedGen == gen {
		d.mu.Unlock()
		return
	}
	d.cleanedGen = gen
	d.mu.Unlock()
	cleanup()
}

// runner is the agent running this subagent; agent_send can swap it.
func (d *dispatchedAgent) runner() SessionAgent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.agent
}

func (d *dispatchedAgent) setRunner(agent SessionAgent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.agent = agent
}

// reopen runs another turn on a finished subagent's existing session, so it
// answers with its whole task still in context. This is what makes a
// subagent answerable rather than a one-shot: the session is real and its
// history is intact, so a follow-up is just the next turn on it.
//
// A non-nil agent replaces the subagent's runner, and only when the claim
// succeeds, so a refused reopen leaves the subagent as it was.
//
// Returns false if the subagent is not idle, meaning somebody else claimed
// it first or it is still working.
func (c *coordinator) reopen(entry *dispatchedAgent, prompt string, agent SessionAgent) bool {
	// Claiming and registering are one step under dispatchMu: two concurrent
	// follow-ups resolve to one reopen, and an entry pruned meanwhile is put
	// back rather than left running outside the registry.
	c.dispatchMu.Lock()
	key := dispatchKey(entry.ParentSessionID, entry.Label)
	if current, ok := c.dispatched.Get(key); ok {
		entry = current
	}
	swap := agent != nil
	if !swap {
		agent = entry.runner()
	}
	agentCall, _, err := c.subAgentCall(agent, entry.SessionID, prompt, subAgentParams{})
	if err != nil {
		c.dispatchMu.Unlock()
		return false
	}
	runCtx, gen, ok := entry.claim()
	if ok {
		if swap {
			entry.setRunner(agent)
		}
		c.dispatched.Set(key, entry)
	}
	c.dispatchMu.Unlock()
	if !ok {
		return false
	}

	c.runDispatched(runCtx, entry, gen, func(ctx context.Context) (fantasy.ToolResponse, error) {
		result, err := agent.Run(ctx, agentCall)
		if err != nil {
			return subAgentRunError("Subagent failed", err), nil
		}
		return subAgentResponse(result), nil
	})
	return true
}

// rollUpDispatchCost adds what a subagent spent since the last roll-up to
// its parent session.
func (c *coordinator) rollUpDispatchCost(ctx context.Context, entry *dispatchedAgent) {
	child, err := c.sessions.Get(ctx, entry.SessionID)
	if err != nil {
		return
	}
	previous := math.Float64frombits(entry.rolledUpCost.Load())
	delta := child.Cost - previous
	if delta <= 0 {
		return
	}
	entry.rolledUpCost.Store(math.Float64bits(child.Cost))

	parent, err := c.sessions.Get(ctx, entry.ParentSessionID)
	if err != nil {
		return
	}
	parent.Cost += delta
	if _, err := c.sessions.Save(ctx, parent); err != nil {
		slog.Warn("Failed to roll up subagent cost", "label", entry.Label, "error", err)
	}
}

// reportDispatchResult delivers a finished subagent's output to the parent:
// it folds into a turn in progress or starts one. A busy parent queues it,
// and it reaches the transcript when the model reads it; if waking the parent
// fails before then, it is recorded anyway so it is never lost.
func (c *coordinator) reportDispatchResult(entry *dispatchedAgent, resp fantasy.ToolResponse, err error) {
	var (
		body   string
		failed bool
	)
	switch {
	case err != nil:
		body, failed = fmt.Sprintf("Subagent failed: %s", err), true
	case resp.Content == "":
		body, failed = "Subagent completed but produced no text output.", true
	default:
		body, failed = resp.Content, resp.IsError
	}

	report := message.SubAgentReport{
		Label:  entry.Label,
		Output: body,
		Failed: failed,
		Model:  modelDisplayName(entry.runner().Model()),
	}
	ctx := context.Background()
	if _, err := c.runReport(ctx, entry.ParentSessionID, report); err != nil {
		slog.Error(
			"Failed to wake the parent with a subagent report",
			"label", entry.Label,
			"parent_session", entry.ParentSessionID,
			"error", err,
		)
		if !c.reportRecorded(ctx, entry.ParentSessionID, report) {
			c.recordDispatchReport(entry.ParentSessionID, report)
		}
	}
}

// reportRecorded reports whether report is already in the parent's
// transcript, as it is when the turn it started failed after writing it.
func (c *coordinator) reportRecorded(ctx context.Context, parentSessionID string, report message.SubAgentReport) bool {
	msgs, err := c.messages.List(ctx, parentSessionID)
	if err != nil {
		return false
	}
	for _, msg := range slices.Backward(msgs) {
		if slices.Contains(msg.SubAgentReports(), report) {
			return true
		}
	}
	return false
}

// recordDispatchReport stores a report in the parent session without starting
// a turn. The model reads it with the rest of the history on its next turn,
// and the UI settles the subagent's item when it lands.
func (c *coordinator) recordDispatchReport(parentSessionID string, report message.SubAgentReport) (message.Message, bool) {
	msg, err := c.messages.Create(context.Background(), parentSessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{report},
	})
	if err != nil {
		slog.Error(
			"Failed to record subagent report",
			"label", report.Label,
			"parent_session", parentSessionID,
			"error", err,
		)
		return message.Message{}, false
	}
	return msg, true
}

// agentSendTool delivers a message to a running detached subagent. The
// message is queued on the subagent's session, where sessionAgent folds it
// into the subagent's turn at its next step.
func (c *coordinator) agentSendTool(parentModel func() Model) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		AgentSendToolName,
		agentSendToolDescription,
		func(ctx context.Context, params AgentSendParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if refusal, off := c.refuseAgentsOff(); off {
				return refusal, nil
			}
			label := strings.TrimSpace(params.Label)
			if label == "" {
				return fantasy.NewTextErrorResponse("label is required"), nil
			}
			if params.Message == "" {
				return fantasy.NewTextErrorResponse("message is required"), nil
			}

			parentSessionID := tools.GetSessionFromContext(ctx)
			if parentSessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			key := dispatchKey(parentSessionID, label)
			entry, ok := c.dispatched.Get(key)
			if !ok {
				// Not in the registry, which after a restart is every
				// subagent. The transcript and the child session still hold
				// the work, so rebuild the entry rather than refusing.
				rebuilt, err := c.rehydrateDispatch(ctx, parentSessionID, label)
				if err != nil {
					// The name may belong to a Team teammate instead.
					if resp, handled := c.deliverToTeammate(ctx, label, params.Message); handled {
						return resp, nil
					}
					return fantasy.NewTextErrorResponse(fmt.Sprintf(
						"no subagent or teammate %q; subagents: %s; teammates: %s",
						label, c.runningDispatchLabels(parentSessionID), c.teammateNames(ctx),
					)), nil
				}
				entry = rebuilt
			}

			if params.switchesModel() {
				return c.sendOnNewModel(ctx, entry, params, parentModel()), nil
			}

			return c.deliverDispatched(ctx, parentSessionID, entry, label, params.Message), nil
		},
	)
}

// deliverDispatched queues text on a dispatched subagent, reopening it when it
// had finished. The caller reports failure in the response, not as an error.
func (c *coordinator) deliverDispatched(ctx context.Context, parentSessionID string, entry *dispatchedAgent, label, text string) fantasy.ToolResponse {
	key := dispatchKey(parentSessionID, label)
	// Each pass either folds the message into a running turn or
	// reopens a finished subagent. Losing a race to another follow-up
	// that reopened it first means its new run takes the message.
	for range 3 {
		if current, ok := c.dispatched.Get(key); ok {
			entry = current
		}
		if entry.stopping() {
			entry.awaitSettled(10 * time.Second)
		}
		if entry.awaitDeliverable(ctx) {
			agent := entry.runner()
			agentCall, _, err := c.subAgentCall(agent, entry.SessionID, text, subAgentParams{})
			if err != nil {
				return fantasy.NewTextErrorResponse("Failed to deliver message: " + err.Error())
			}
			agentCall.QueueOnly = true
			_, err = agent.Run(context.WithoutCancel(ctx), agentCall)
			if err == nil {
				return fantasy.WithResponseMetadata(fantasy.NewTextResponse(fmt.Sprintf(
					"Delivered to %q. It will answer in its report.",
					label,
				)+reportArrivesOnItsOwn), AgentSendResponseMetadata{Label: label})
			}
			if !errors.Is(err, ErrSessionIdle) {
				return fantasy.NewTextErrorResponse("Failed to deliver message: " + err.Error())
			}
			entry.awaitSettled(10 * time.Second)
		}
		if c.reopen(entry, text, nil) {
			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(fmt.Sprintf(
				"Delivered to %q, which had finished. It will answer in a new report.",
				label,
			)+reportArrivesOnItsOwn), AgentSendResponseMetadata{Label: label, Reopened: true})
		}
	}
	if ctx.Err() != nil {
		return fantasy.NewTextErrorResponse(ctx.Err().Error())
	}
	return fantasy.NewTextErrorResponse(fmt.Sprintf(
		"subagent %q is still starting up and could not take the message yet; try again shortly",
		label,
	))
}

// deliverToTeammate forwards a message to a Team teammate on a dispatch miss.
// It reports whether this path handled the call, so a real send failure keeps
// its own message.
func (c *coordinator) deliverToTeammate(ctx context.Context, label, text string) (fantasy.ToolResponse, bool) {
	if _, off := c.refuseAgentsOff(); off {
		return fantasy.ToolResponse{}, false
	}
	team := c.teamService()
	if team == nil {
		return fantasy.ToolResponse{}, false
	}
	err := team.SendMessage(ctx, agentteam.MemberFromContext(ctx), tools.GetSessionFromContext(ctx), agentteam.SendRequest{
		Target:  label,
		Message: text,
	})
	if err != nil {
		if agentteam.ErrorCode(err) == agentteam.CodeMemberNotFound {
			return fantasy.ToolResponse{}, false
		}
		return fantasy.NewTextErrorResponse(err.Error()), true
	}
	return fantasy.NewTextResponse(fmt.Sprintf("Delivered to teammate %q.", label)), true
}

// deliverToSubagent delivers a Team message to a dispatched subagent when the
// name is not a teammate. It reports whether a subagent took the message.
func (c *coordinator) deliverToSubagent(ctx context.Context, label, text string) (fantasy.ToolResponse, bool) {
	parentSessionID := tools.GetSessionFromContext(ctx)
	if parentSessionID == "" || c.dispatched == nil {
		return fantasy.ToolResponse{}, false
	}
	entry, ok := c.dispatched.Get(dispatchKey(parentSessionID, label))
	if !ok {
		// Not in the registry, which after a restart is every subagent.
		rebuilt, err := c.rehydrateDispatch(ctx, parentSessionID, label)
		if err != nil {
			return fantasy.ToolResponse{}, false
		}
		entry = rebuilt
	}
	return c.deliverDispatched(ctx, parentSessionID, entry, label, text), true
}

// interruptTeammate stops a teammate's current turn when the name is not a
// running subagent of this session. It stays out of the Team entirely while
// Agent Teams is turned off, the same rule interrupt_agent follows.
func (c *coordinator) interruptTeammate(ctx context.Context, name string) (fantasy.ToolResponse, bool) {
	if _, off := c.refuseAgentsOff(); off {
		return fantasy.ToolResponse{}, false
	}
	team := c.teamService()
	if team == nil {
		return fantasy.ToolResponse{}, false
	}
	previous, err := team.Interrupt(agentteam.MemberFromContext(ctx), tools.GetSessionFromContext(ctx), name)
	if err != nil {
		if agentteam.ErrorCode(err) == agentteam.CodeMemberNotFound {
			return fantasy.ToolResponse{}, false
		}
		return fantasy.NewTextErrorResponse(err.Error()), true
	}
	return fantasy.NewTextResponse(fmt.Sprintf(
		"Interrupted teammate %q (was %s). Its pending inbox is kept.", name, previous)), true
}

// agentStopTool stops a running detached subagent. Its session is kept, so a
// later agent_send resumes it with its work still in context.
func (c *coordinator) agentStopTool() fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		AgentStopToolName,
		agentStopToolDescription,
		func(ctx context.Context, params AgentStopParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			label := strings.TrimSpace(params.Label)
			if label == "" {
				return fantasy.NewTextErrorResponse("label is required"), nil
			}
			parentSessionID := tools.GetSessionFromContext(ctx)
			if parentSessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			entry, ok := c.dispatched.Get(dispatchKey(parentSessionID, label))
			if !ok || !entry.running() {
				// The name may belong to a Team teammate instead.
				if resp, handled := c.interruptTeammate(ctx, label); handled {
					return resp, nil
				}
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"subagent or teammate %q is not running; subagents: %s; teammates: %s",
					label, c.runningDispatchLabels(parentSessionID), c.teammateNames(ctx),
				)), nil
			}
			reason := strings.TrimSpace(params.Reason)
			if reason == "" {
				reason = "stopped by the main agent"
			}
			entry.stop(reason)
			if !entry.awaitSettled(10 * time.Second) {
				return fantasy.NewTextResponse(fmt.Sprintf("Stopping subagent %q.", label)), nil
			}
			return fantasy.NewTextResponse(fmt.Sprintf("Stopped subagent %q.", label)), nil
		},
	)
}

// keptFinishedDispatches is how many finished subagents stay in the registry.
// Each pins its agent; a forgotten one is rebuilt from the transcript if it
// is messaged again.
const keptFinishedDispatches = 8

// pruneDispatched forgets all but the most recently finished subagents.
func (c *coordinator) pruneDispatched() {
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	type finished struct {
		key   string
		ended time.Time
	}
	var idle []finished
	for key, entry := range c.dispatched.Seq2() {
		if snap := entry.snapshot(); snap.State != dispatchRunning {
			idle = append(idle, finished{key, snap.Ended})
		}
	}
	if len(idle) <= keptFinishedDispatches {
		return
	}
	slices.SortFunc(idle, func(a, b finished) int { return b.ended.Compare(a.ended) })
	for _, f := range idle[keptFinishedDispatches:] {
		c.dispatched.Del(f.key)
	}
}

// runningDispatchLabels lists the subagents still running for a session.
func (c *coordinator) runningDispatchLabels(parentSessionID string) string {
	if c.dispatched == nil {
		return "none"
	}
	var labels []string
	for _, entry := range c.dispatched.Seq2() {
		if entry.ParentSessionID == parentSessionID && entry.running() {
			labels = append(labels, entry.Label)
		}
	}
	if len(labels) == 0 {
		return "none"
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}

// dispatchRunProvider reads the provider a subagent ran on from its own
// transcript, for a dispatch whose result never landed. A read failure is an
// error, not "never ran", so a resume cannot pick the wrong provider.
func (c *coordinator) dispatchRunProvider(ctx context.Context, sessionID string) (string, error) {
	msgs, err := c.messages.List(ctx, sessionID)
	if err != nil {
		return "", err
	}
	for _, msg := range msgs {
		if msg.Role == message.Assistant && msg.Provider != "" {
			return msg.Provider, nil
		}
	}
	return "", nil
}

// rehydrateDispatch rebuilds a subagent's registry entry from what is on
// disk. The registry is in-memory, so a restart empties it, but nothing that
// matters was in memory to begin with: the parent's transcript records the
// dispatch call, the child session ID derives from that call's IDs, and the
// child session holds the whole conversation. The entry is reconstructed as a
// finished subagent, which is what it is, so the caller reopens it.
func (c *coordinator) rehydrateDispatch(ctx context.Context, parentSessionID, label string) (*dispatchedAgent, error) {
	messages, err := c.messages.List(ctx, parentSessionID)
	if err != nil {
		return nil, err
	}
	record, ok := dispatchHistory(messages).byLabel[label]
	if !ok {
		return nil, fmt.Errorf("no dispatch recorded for label %q", label)
	}
	// A backgrounded search ran on a fetch subagent, not a task agent, so
	// it cannot be rebuilt from the task variants below. Its session is
	// kept, but reopening it after a restart is not supported.
	if record.Tool == tools.AgenticFetchToolName {
		return nil, fmt.Errorf("subagent %q was a search and cannot be resumed", label)
	}

	if record.Role != "" {
		role, refusal := resolveRole(c.cfg.Config(), record.Role)
		if refusal != "" {
			return nil, fmt.Errorf("subagent %q cannot be resumed: role %q no longer exists", label, record.Role)
		}
		// The record keeps the effective access, so the entry, agent_send
		// and agent_status all see the variant the role picks now.
		if access, refusal := roleAccess(role, record.Access); refusal == "" {
			record.Access = access
		}
		params := AgentDispatchParams{Model: record.Model, Provider: record.Provider, Reasoning: record.Reasoning}
		agent, _, _, refusal := c.roleTaskAgent(ctx, role, record.Access, params, c.mainModel())
		if refusal != "" {
			return nil, fmt.Errorf("subagent %q cannot be resumed: %s", label, refusal)
		}
		return c.reopenDispatched(ctx, parentSessionID, label, record, agent)
	}

	// Rehydration replays the access level recorded in the transcript, so
	// both variants have to be available to look it up. A read-only parent
	// could never have recorded a write dispatch in the first place.
	agents, err := c.taskAgents(ctx, false)
	if err != nil {
		return nil, err
	}
	agent, ok := agents[record.Access]
	if !ok {
		return nil, fmt.Errorf("unknown access %q", record.Access)
	}

	// An override resumes as it was asked for, re-resolved against the live
	// config; if it no longer serves, the subagent is not quietly moved to
	// the session default.
	if record.Model != "" || record.Reasoning != "" {
		params := AgentDispatchParams{Model: record.Model, Provider: record.Provider, Reasoning: record.Reasoning}
		parent := Model{}
		if params.Model != "" && params.Provider == "" {
			// The result never landed: use the provider it ran on, else the session's.
			provider, err := c.dispatchRunProvider(ctx, c.sessions.CreateAgentToolSessionID(record.MessageID, record.CallID))
			if err != nil {
				return nil, fmt.Errorf("subagent %q cannot be resumed: reading its transcript failed: %w", label, err)
			}
			params.Provider = provider
			if provider == "" {
				parent = c.mainModel()
			}
		}
		overrideAgent, _, _, refusal := c.overrideDispatchAgent(ctx, record.Access, params, parent)
		if refusal != "" {
			return nil, fmt.Errorf("subagent %q cannot be resumed on its model override: %s", label, refusal)
		}
		agent = overrideAgent
	}

	return c.reopenDispatched(ctx, parentSessionID, label, record, agent)
}

// reopenDispatched registers a rehydrated subagent under its label, seeded
// with what its session has already cost.
func (c *coordinator) reopenDispatched(ctx context.Context, parentSessionID, label string, record *dispatchRecord, agent SessionAgent) (*dispatchedAgent, error) {
	sessionID := c.sessions.CreateAgentToolSessionID(record.MessageID, record.CallID)
	child, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		// The dispatch was recorded but its session is gone, so there is
		// no history to reopen into.
		return nil, err
	}

	entry := newDispatchedAgent(label, record.Access, record.Role, sessionID, parentSessionID, agent)
	// Seed the roll-up with what the child has already cost. The parent was
	// billed for all of it during the original run, so starting from zero
	// would bill the whole total a second time.
	entry.rolledUpCost.Store(math.Float64bits(child.Cost))

	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	if existing, ok := c.dispatched.Get(dispatchKey(parentSessionID, label)); ok {
		return existing, nil
	}
	c.dispatched.Set(dispatchKey(parentSessionID, label), entry)
	return entry, nil
}

// stopAllDispatched stops every running subagent and waits, up to timeout,
// for them to unwind.
func (c *coordinator) stopAllDispatched(reason string, timeout time.Duration) {
	var stopped []*dispatchedAgent
	for _, entry := range c.dispatched.Seq2() {
		if entry.stop(reason) {
			stopped = append(stopped, entry)
		}
	}
	deadline := time.Now().Add(timeout)
	for _, entry := range stopped {
		if !entry.awaitSettled(time.Until(deadline)) {
			slog.Warn("Subagent still running after being stopped", "label", entry.Label)
		}
	}
}

// subSessionRun is the in-flight state of one subagent session: the run
// count the busy check reads, and the agent instance every run executes on
// so a cancel by session id reaches it.
type subSessionRun struct {
	count  int
	agents map[uint64]SessionAgent
}

// trackSubSession marks a subagent session as live until the returned func
// is called. The coordinator's busy check consults this, so a reader settling
// half-finished sessions never mistakes a working subagent for a dead one.
// The agent is recorded too, so cancelling the session reaches the instance
// that runs it; pass nil when the run needs no such routing.
func (c *coordinator) trackSubSession(sessionID string, agent SessionAgent) func() {
	c.subSessionsMu.Lock()
	if c.subSessions == nil {
		c.subSessions = map[string]*subSessionRun{}
	}
	run := c.subSessions[sessionID]
	if run == nil {
		run = &subSessionRun{}
		c.subSessions[sessionID] = run
	}
	run.count++
	var runID uint64
	if agent != nil {
		if run.agents == nil {
			run.agents = map[uint64]SessionAgent{}
		}
		c.subSessionSeq++
		runID = c.subSessionSeq
		run.agents[runID] = agent
	}
	c.subSessionsMu.Unlock()
	return func() {
		c.subSessionsMu.Lock()
		defer c.subSessionsMu.Unlock()
		run, ok := c.subSessions[sessionID]
		if !ok {
			return
		}
		run.count--
		delete(run.agents, runID)
		if run.count <= 0 {
			delete(c.subSessions, sessionID)
		}
	}
}

// errSubAgentCancelled is the cause a user cancel leaves on a subagent's
// context.
var errSubAgentCancelled = errors.New("cancelled by the user")

// beginSubSession makes sessionID cancellable before its run is listed
// running. The returned release drops the entry; call it when the run
// ends however it ends.
func (c *coordinator) beginSubSession(ctx context.Context, sessionID string) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	c.subSessionsMu.Lock()
	if c.subCancels == nil {
		c.subCancels = map[string]context.CancelCauseFunc{}
	}
	c.subCancels[sessionID] = cancel
	c.subSessionsMu.Unlock()
	return ctx, func() {
		c.subSessionsMu.Lock()
		delete(c.subCancels, sessionID)
		c.subSessionsMu.Unlock()
		cancel(nil)
	}
}

func (c *coordinator) subSessionBusy(sessionID string) bool {
	c.subSessionsMu.Lock()
	defer c.subSessionsMu.Unlock()
	run := c.subSessions[sessionID]
	return run != nil && run.count > 0
}
