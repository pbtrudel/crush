package agent

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/gitutil"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

// gateOpenTag starts the hidden user row the turn-end gate writes. The log
// report and `crush run --json` recognise the row by this tag, as they do
// for the other injections the harness writes.
const gateOpenTag = "<turn_end_gate>"

// gateIntro is the first line of the harness row. It says where the
// message comes from, because the model reads the row with the user role.
const gateIntro = "Crush checked your turn before ending it. " +
	"This message comes from Crush, not from the user, and the user does not " +
	"see it. Act on it, then finish your reply to the user."

// gateInput is what a turn-end check sees when a turn ends.
type gateInput struct {
	SessionID string
	// TurnEnd is how the run ended, before any compaction.
	TurnEnd message.TurnEnd
	// Reply is the assistant text of the run that is ending.
	Reply string
	// ChangedFiles are the files this prompt changed, from the working
	// tree and from the tool calls. It is empty when nothing changed.
	ChangedFiles []string
	// HeadFrom is HEAD at the prompt's start and HeadTo HEAD at the gate.
	// Both are empty when the directory is not a repository.
	HeadFrom string
	HeadTo   string
	// Continuations counts the continuations this prompt already used.
	Continuations int
}

// gateCheck is one source of continuation requests for an ending turn.
type gateCheck interface {
	// Name labels the check's text in the harness row.
	Name() string
	// Check answers what the check thinks of the ending turn.
	Check(ctx context.Context, in gateInput) gateVerdict
}

// gateVerdict is what one check says about an ending turn.
type gateVerdict struct {
	// Reason, when not empty, asks for one more run with this text.
	Reason string
	// Stop refuses every continuation this time, whatever the other
	// checks said.
	Stop bool
}

// gateReason is one check's text with the label the harness row gives it.
type gateReason struct {
	label string
	text  string
}

// gateMessage renders the harness row: where it comes from, then each
// check's text under its own label.
func gateMessage(reasons []gateReason) string {
	var b strings.Builder
	b.WriteString(gateOpenTag)
	b.WriteString("\n")
	b.WriteString(gateIntro)
	for _, reason := range reasons {
		b.WriteString("\n\n[")
		b.WriteString(reason.label)
		b.WriteString("]\n")
		b.WriteString(strings.TrimSpace(reason.text))
	}
	b.WriteString("\n</turn_end_gate>")
	return b.String()
}

// gateNextCall builds the call that continues a prompt with the harness
// row as its prompt. The session, the tree's limits and the channel stay,
// so the continuation draws on the same budget and answers the same way.
func gateNextCall(call SessionAgentCall, text string) *SessionAgentCall {
	next := call
	next.Prompt = text
	next.HiddenUserMessage = true
	next.Attachments = nil
	next.HostContext = proto.HostContext{}
	next.Report = nil
	next.OnComplete = nil
	next.Accepted = nil
	next.gateContinuations = call.gateContinuations + 1
	next.gateContinued = true
	return &next
}

// hasUserPrompt reports whether a queue holds a prompt the user typed. A
// report, a wake-up and a hidden prompt are not the user's: they are
// folded into whatever runs next.
func hasUserPrompt(queued []SessionAgentCall) bool {
	for _, q := range queued {
		if q.HiddenUserMessage || q.Report != nil || q.Wakeup {
			continue
		}
		return true
	}
	return false
}

// userPromptQueued reports whether the session's queue already holds a
// prompt the user typed. The queue is read under the session's dispatch
// mutex, as the hand-off reads it.
func (a *sessionAgent) userPromptQueued(sessionID string) bool {
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	queued, _ := a.messageQueue.Get(sessionID)
	return hasUserPrompt(queued)
}

// gateWorkingDir is the directory the gate's working-tree snapshot reads.
func (a *sessionAgent) gateWorkingDir() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.WorkingDir()
}

// snapshotWorktree reads the working tree for the gate. It is a method so a
// test can count the calls; nil means gitutil.Snapshot.
func (a *sessionAgent) snapshotWorktree(ctx context.Context, dir string) (gitutil.WorktreeState, error) {
	if a.snapshotState != nil {
		return a.snapshotState(ctx, dir)
	}
	return gitutil.Snapshot(ctx, dir)
}

// gateNeedsChangedFiles reports whether anything reads the working tree. Only
// the completion check does: a TurnEnd hook that wants changed_files gets the
// tool calls alone, which costs no git call. Without a working directory
// there is nothing to read.
func (a *sessionAgent) gateNeedsChangedFiles() bool {
	return a.gateWorkingDir() != "" && a.completionCheckOn()
}

// turnEndMaxContinuations returns how many continuations one prompt may
// get. Zero turns them off.
func (a *sessionAgent) turnEndMaxContinuations() int {
	if a.cfg == nil {
		return config.DefaultTurnEndMaxContinuations
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return config.DefaultTurnEndMaxContinuations
	}
	return cfg.Options.GetTurnEndMaxContinuations()
}

// turnEndGateEvent is the TurnEnd event the gate fires: the payload a
// TurnEnd hook always got, plus the fields that describe the ending.
func (a *sessionAgent) turnEndGateEvent(call SessionAgentCall, assistant *message.Message, end message.TurnEnd, changed []string) hooks.Event {
	ev := turnEndEvent(nil, assistant)
	ev.SessionID = call.SessionID
	ev.ChangedFiles = pathEntries(changed)
	// stop_hook_active is Claude Code's name for "this run continues a turn
	// that was already checked", so a Stop script can skip its second call.
	active := call.gateContinued
	ev.StopHookActive = &active
	ev.TurnEnd = string(end)
	left := max(0, a.turnEndMaxContinuations()-call.gateContinuations)
	ev.ContinuationsLeft = &left
	return ev
}

// hookGateReasons runs the turn's TurnEnd hooks and reads their answers as
// gate reasons, each labelled with its hook's display name. A hook that
// halts the turn has its reason logged instead, because no continuation
// follows it.
func (a *sessionAgent) hookGateReasons(ctx context.Context, call SessionAgentCall, assistant *message.Message, end message.TurnEnd, changed []string) (reasons []gateReason, stop bool) {
	if call.NonInteractive {
		return nil, false
	}
	agg := a.hooks.Run(ctx, a.turnEndGateEvent(call, assistant, end, changed))
	answers, stop := hooks.TurnEndReasons(agg)
	reasons = make([]gateReason, 0, len(answers))
	for _, answer := range answers {
		if answer.Halt {
			slog.Info("Turn-end gate: a TurnEnd hook halted the turn",
				"session_id", call.SessionID, "hook", answer.Hook, "reason", answer.Reason)
			continue
		}
		reasons = append(reasons, gateReason{
			label: "hook: " + answer.Hook,
			text:  answer.Reason,
		})
	}
	return reasons, stop
}

// runTurnGate asks the checks whether an ending turn may continue the
// prompt, and builds the call that does when they agree. It returns nil
// when the turn ends here.
//
// The TurnEnd hooks are the first check, then the built-in checks in
// registration order. Every check is asked before the cap is applied, so a
// check always sees a candidate ending even when its answer cannot be used.
// A stop, an ending other than completed, a limit and a cancel all outrank a
// request to continue: a guard, a halt or a budget is never overridden.
func (a *sessionAgent) runTurnGate(ctx context.Context, call SessionAgentCall, assistant *message.Message, end message.TurnEnd, userQueued bool) *SessionAgentCall {
	// The working tree is read once here, before any check runs, so every
	// check and the hook payload see the same files. A failed snapshot
	// leaves nothing to compare, so the turn falls back to its tool calls
	// rather than reading the baseline as all deletions.
	var endState *gitutil.WorktreeState
	if call.gateBaseline != nil {
		state, snapErr := a.snapshotWorktree(ctx, a.gateWorkingDir())
		if snapErr != nil {
			slog.Debug("Turn-end gate: no working-tree snapshot at the turn's end",
				"session_id", call.SessionID, "error", snapErr)
		} else {
			endState = &state
		}
	}
	changed, headFrom, headTo := turnChangedFiles(call, endState)
	in := gateInput{
		SessionID:     call.SessionID,
		TurnEnd:       end,
		Reply:         assistant.Content().String(),
		ChangedFiles:  changed,
		HeadFrom:      headFrom,
		HeadTo:        headTo,
		Continuations: call.gateContinuations,
	}
	// The hooks run even when the user already has a prompt queued, so a
	// TurnEnd hook keeps its once-per-prompt scope; only its answer is
	// dropped then.
	hookReasons, hookStop := a.hookGateReasons(ctx, call, assistant, end, changed)
	if userQueued {
		if hookStop || len(hookReasons) > 0 {
			slog.Debug("Turn-end gate: a queued prompt wins over a continuation",
				"session_id", call.SessionID)
		}
		return nil
	}
	reasons := hookReasons
	stopped := hookStop
	checks := slices.Clone(a.gateChecks)
	if a.completionCheckOn() {
		checks = append(checks, completionGateCheck{})
	}
	for _, check := range checks {
		verdict := check.Check(ctx, in)
		stopped = stopped || verdict.Stop
		if verdict.Reason == "" {
			continue
		}
		reasons = append(reasons, gateReason{
			label: "check: " + check.Name(),
			text:  verdict.Reason,
		})
	}
	if len(reasons) == 0 {
		return nil
	}
	switch {
	case stopped:
		slog.Info("Turn-end gate: a check stopped the turn",
			"session_id", call.SessionID, "turn_end", end)
	case end != message.TurnEndCompleted:
		slog.Info("Turn-end gate: only a completed turn continues",
			"session_id", call.SessionID, "turn_end", end)
	case call.limits.reached():
		slog.Info("Turn-end gate: the session tree reached a limit",
			"session_id", call.SessionID)
	case call.gateContinuations >= a.turnEndMaxContinuations():
		slog.Info("Turn-end gate: the prompt has no continuation left",
			"session_id", call.SessionID, "continuations", call.gateContinuations)
	case ctx.Err() != nil:
		slog.Info("Turn-end gate: the run was cancelled",
			"session_id", call.SessionID)
	default:
		return gateNextCall(call, gateMessage(reasons))
	}
	return nil
}

// compactDroppedGate runs the threshold compaction a dropped continuation
// put off. The turn it belongs to has already ended, so a failure is logged
// rather than returned: the reply and the queued prompt still have to go.
func (a *sessionAgent) compactDroppedGate(ctx context.Context, call SessionAgentCall, largeModel Model, sess session.Session) {
	if !a.shouldAutoCompact(largeModel, sess) {
		return
	}
	threshold, _ := a.compactionConfig()
	trigger := turnCompactionTrigger(false, false, effectiveContextWindow(largeModel), threshold, a.autoCompactMaxTokens())
	if err := a.summarize(ctx, call.SessionID, largeModel, call.ProviderOptions, call.OnAuthRefresh, true, trigger, false); err != nil {
		slog.Warn("Failed to compact a dropped turn-end continuation",
			"session_id", call.SessionID, "error", err)
	}
}

// gateFinish is what a run still owes the user at its end: the stale list
// note, the channel reply, the release of the session, the TurnEnd hook
// and the AgentFinished notification.
type gateFinish struct {
	assistant *message.Message
	// summarized and overflow describe a run that compaction resumes;
	// such a run owes no reply.
	summarized bool
	overflow   bool
	// finished reports that this run is the prompt's last, so it owes the
	// stale list note and the notification.
	finished bool
	// turnEndHook reports that this run still owes the TurnEnd hook. The
	// gate fires that hook on every run it evaluates.
	turnEndHook bool
	// released reports that the run already gave up the session before
	// the hand-off, so it is not released twice.
	released bool
	cancel   context.CancelFunc
	// staleItem and staleN describe the todo list that stayed behind.
	staleItem string
	staleN    int
	completed map[string]struct{}
	title     string
}

// finishWithoutGate ends a run the gate will not continue: it writes the
// stale list note, sends the channel reply, releases the session, fires the
// TurnEnd hook and publishes the AgentFinished notification. The path that
// drops a continuation calls it too, so the two cannot drift.
func (a *sessionAgent) finishWithoutGate(ctx context.Context, call SessionAgentCall, f gateFinish) {
	// A turn that ends with the list still behind the work is reported to
	// the user by Crush, not by the model. By the time execution reaches
	// here the model has ignored repeated reminders about the same item,
	// so a further reminder would not help; the note keeps the user from
	// discovering the stale list only when they open it.
	if f.finished && !a.isSubAgent && f.staleItem != "" {
		// A compaction can outlast cleanupCtx, so the note gets its own
		// short deadline, detached from the run like the other final
		// writes.
		noteCtx, cancelNote := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelNote()
		if latest, getErr := a.sessions.Get(noteCtx, call.SessionID); getErr != nil {
			slog.Warn("Failed to read todos for the stale list note", "session_id", call.SessionID, "error", getErr)
		} else if staleTodoAtTurnEnd(f.staleItem, f.staleN, latest.Todos) {
			f.assistant.AppendContent(staleTodoNote(f.staleItem, f.staleN+1))
			if updateErr := a.messages.Update(noteCtx, *f.assistant); updateErr != nil {
				slog.Warn("Failed to append the stale list note", "session_id", call.SessionID, "error", updateErr)
			}
		}
	}

	// Route the finished turn's response back to the channel it came from.
	if f.assistant != nil && (!f.summarized || (!f.overflow && len(f.assistant.ToolCalls()) == 0)) {
		a.sendChannelReply(ctx, call, channelReplyText(f.assistant), f.completed)
	}

	// Release the active request before publishing the notification. TUI
	// handlers poll IsSessionBusy() and only re-evaluate when a tea.Msg
	// arrives, so the cleanup must precede the notify or subscribers see
	// stale busy state at the moment of receipt.
	if !f.released {
		a.activeRequests.Del(call.SessionID)
		f.cancel()
	}

	if f.turnEndHook {
		a.fireTurnHook(ctx, call, turnEndEvent(nil, f.assistant))
	}

	// Send notification that the agent has finished its turn (skip for
	// nested/non-interactive sessions).
	if !call.NonInteractive && a.notify != nil {
		a.notify.Publish(pubsub.CreatedEvent, notify.Notification{
			SessionID:    call.SessionID,
			SessionTitle: f.title,
			Type:         notify.TypeAgentFinished,
		})
	}
}
