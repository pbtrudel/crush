// Package agent is the core orchestration layer for Crush AI agents.
//
// It provides session-based AI agent functionality for managing
// conversations, tool execution, and message handling. It coordinates
// interactions between language models, messages, sessions, and tools while
// handling features like automatic summarization, queuing, and token
// management.
package agent

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openrouter"
	"charm.land/fantasy/providers/vercel"
	"charm.land/lipgloss/v2"
	"charm.land/tone"
	"github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/claudeagent"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/gitutil"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/stringext"
	"github.com/charmbracelet/crush/internal/version"
	"github.com/charmbracelet/x/ansi"
)

const (
	DefaultSessionName = "Untitled Session"

	// defaultAutoCompactThreshold is the fraction of the context window
	// at which a finished turn triggers automatic compaction when the
	// user has not configured one. The mid-turn guard below still fires
	// near the hard limit regardless; this controls how much headroom
	// remains before the session is compacted.
	defaultAutoCompactThreshold = config.DefaultAutoCompactThreshold

	// defaultCompactTailTokens is how many tokens of recent conversation
	// a compaction keeps verbatim when the user has not configured a
	// budget. The recent working set — file reads, command outputs, and
	// edits — is worth far more to the model than more summary prose.
	defaultCompactTailTokens = config.DefaultCompactTailTokens

	// defaultCompactBackgroundMargin is how far below the auto-compaction
	// limit a finished turn starts the summary when the user has not
	// configured a margin. Zero keeps compaction blocking.
	defaultCompactBackgroundMargin = config.DefaultCompactBackgroundMargin

	// defaultCompactionMaxOutputTokens bounds the summary a compaction
	// request may generate. Reasoning models spend part of this budget on
	// reasoning, and the summary must fit entirely inside it: an 8k cap
	// was observed truncating a live summary mid-sentence, dropping the
	// final required section. The prompt asks for a shorter summary; this
	// is only the hard ceiling.
	defaultCompactionMaxOutputTokens = 16_384

	// compactionMaxSteps bounds the compaction run against a model that
	// keeps calling tools despite the instruction not to. The tools
	// themselves refuse to run during compaction, so each retry returns
	// a refusal tool result and the model gets one more step.
	compactionMaxSteps = 4

	// Constants for the mid-turn overflow guard.
	largeContextWindowThreshold = 200_000
	largeContextWindowBuffer    = 20_000
	smallContextWindowRatio     = 0.2

	// overflowRecoveryTimeout bounds the summarize-and-retry recovery
	// after a provider context-overflow rejection. The recovery runs on
	// a context detached from the failed run, so the timeout caps how
	// long a stalled provider can pin the run goroutine.
	overflowRecoveryTimeout = 5 * time.Minute
)

var userAgent = fmt.Sprintf("Charm-Crush/%s (https://charm.land/crush)", version.Version)

// providerStoppedTurnMessage is the banner title recorded when a provider
// ends the response on its own terms (fantasy's error/other finish
// reasons) instead of completing it. Details are filled in per turn.
const providerStoppedTurnMessage = "Provider stopped the response"

// Used to remove <think> tags from generated titles.
var (
	thinkTagRegex       = regexp.MustCompile(`(?s)<think>.*?</think>`)
	orphanThinkTagRegex = regexp.MustCompile(`</?think>`)
)

type SessionAgentCall struct {
	SessionID string
	// RunID, when non-empty, is the caller-supplied correlator that
	// gets echoed back on the notify.RunComplete event emitted for
	// this turn. It is preserved when the call is enqueued behind a
	// busy session so the queued turn's terminal event is still
	// recognisable to the original caller. Callers that need a
	// reliable completion contract (e.g. `crush run` against a
	// session that may be busy) MUST set it; SessionID alone is
	// ambiguous when concurrent turns share the same session.
	RunID             string
	Channel           string
	HiddenUserMessage bool
	// Wakeup marks a turn opened by a background completion notice rather
	// than a user prompt. It feeds the consecutive-wake budget so a chain
	// of self-triggered completions cannot run forever.
	Wakeup bool
	Prompt string
	// Ephemeral answers the prompt against the session's existing
	// history as a one-off exchange: no user or assistant message is
	// persisted and no tools are offered. It backs side questions such
	// as /btw, whose answers must not enter the conversation or touch
	// the workspace.
	Ephemeral bool
	// QueueOnly delivers the call only into an active turn; on an idle
	// session Run returns ErrSessionIdle instead of starting one.
	QueueOnly bool
	// Report is a subagent report this call delivers. It is written to
	// the transcript only when delivered, so it lands where the model
	// reads it. Prompt is still what the model reads for it.
	Report           *message.SubAgentReport
	ProviderOptions  fantasy.ProviderOptions
	Attachments      []message.Attachment
	MaxOutputTokens  int64
	Temperature      *float64
	TopP             *float64
	TopK             *int64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	NonInteractive   bool

	// HostContext is what the host application had in view when the
	// prompt was sent. The model sees it with this prompt only; it is
	// not stored with the message.
	HostContext proto.HostContext
	// OnComplete, when non-nil, replaces the default RunComplete
	// publish path: the inner Run hands the terminal payload to this
	// callback instead of emitting it on the RunComplete broker. The
	// coordinator uses this hook to coalesce the unauthorized →
	// re-auth → retry chain into a single user-visible terminal
	// event, so non-interactive clients (e.g. `crush run`) don't
	// exit on a stale failed-attempt RunComplete before the
	// successful retry. It is intentionally stripped when queueing
	// a busy-session call (see Run): the originating
	// coordinator.Run has long returned by the time the queued
	// recursion drains, so falling back to the default broker
	// publish keeps the event visible to subscribers.
	OnComplete func(notify.RunComplete)
	// limits are the step budget and the deadline of the session tree the
	// call runs in, found when the run starts. Nil has no limit.
	limits *treeLimits
	// Accepted, when non-nil, is the accept reservation taken by
	// BeginAccepted before the call was dispatched onto a goroutine
	// (the client/server fire-and-forget path). Run consumes it under
	// dispatchMu[SessionID] once the accepted -> (cancel-on-entry |
	// queued | active) transition has been chosen. When nil
	// (in-process / local callers like AppWorkspace), behavior is
	// unchanged and no accept tracking applies.
	Accepted *AcceptedRun
	// acceptSeq carries the accept sequence of the handle that produced
	// this call after it has been enqueued and its Accepted handle
	// stripped. The queue-drain paths compare it against a session's
	// cancel mark so a follow-up queued before a cancel is dropped while
	// one queued after the cancel survives. 0 means untracked (an
	// in-process enqueue with no accept reservation), which the drain
	// paths treat as covered by any present mark, preserving the
	// pre-sequence behavior.
	acceptSeq uint64
	// OnAuthRefresh, when non-nil, is called by fantasy when a stream
	// fails with an authentication error (HTTP 401). The callback should
	// refresh credentials and return nil on success, in which case
	// fantasy retries the stream transparently. Returning an error
	// surfaces the original auth error without retry.
	OnAuthRefresh func(ctx context.Context, err *fantasy.ProviderError) error
	// channelMeta holds the attributes of the <channel> element a
	// channel-originated turn was started with, parsed once at Run
	// entry. It survives the auto-summarize continuation, whose Prompt
	// is rewritten and no longer carries the element, so the reply
	// target is not lost when a long channel turn is summarized.
	channelMeta map[string]string
	// overflowRetried marks a call that already consumed its one-time
	// context-overflow recovery, so a second overflow fails normally
	// instead of looping between summarize and retry.
	overflowRetried bool
	// gateContinuations counts the turn-end continuations this prompt
	// already used. It is carried on the call, so a compaction
	// continuation, which copies the call, keeps the count.
	gateContinuations int
	// gateContinued marks a call the turn-end gate started, so its run
	// does not fire TurnStart again.
	gateContinued bool
	// gateTodoAsked marks a call whose gate asked the model about a todo
	// item that stayed open, so the end of that continuation does not
	// repeat the question as a note in the reply.
	gateTodoAsked bool
	// gateTodoItem is the item the gate asked about, so the end of that
	// continuation can report the item if the reply ignored it.
	gateTodoItem string
	// gateBaseline is the working tree at the prompt's start, so the gate
	// can see a change no tool call made. Nil when nothing reads it, or
	// when it could not be taken.
	gateBaseline *gitutil.WorktreeState
	// gateBaselineErr records that the baseline could not be taken, so the
	// prompt falls back to tool-call detection and does not retry.
	gateBaselineErr bool
	// gateRoot is the repository root resolved with the baseline, so the
	// tool paths and git's paths compare as one. Empty outside a
	// repository.
	gateRoot string
	// gateChangedFiles are the files this prompt's tools changed, from
	// every run of it.
	gateChangedFiles []string
}

// filterToolsForChannel scopes the tool list for a turn. A channel-originated
// turn (channel != "") sees only the originating channel server's tools plus
// all non-channel tools — the model's reach is restricted to the channel it
// is replying through, so it cannot accidentally send via a different
// messaging backend. A local turn (channel == "") keeps every tool,
// including channel server tools, so a user in the TUI can still ask the
// agent to send a message through Signal or any other enabled channel.
func filterToolsForChannel(agentTools []fantasy.AgentTool, channel string, states map[string]mcp.ClientInfo) []fantasy.AgentTool {
	if channel == "" {
		return agentTools
	}
	filtered := make([]fantasy.AgentTool, 0, len(agentTools))
	for _, agentTool := range agentTools {
		mcpTool, ok := agentTool.(interface{ MCP() string })
		if !ok {
			filtered = append(filtered, agentTool)
			continue
		}
		state, found := states[mcpTool.MCP()]
		if !found || !state.Channel || channel == mcpTool.MCP() {
			filtered = append(filtered, agentTool)
		}
	}
	return filtered
}

// ErrSessionIdle is returned for a QueueOnly call when no turn is active.
var ErrSessionIdle = errors.New("session is idle")

type SessionAgent interface {
	Run(context.Context, SessionAgentCall) (*fantasy.AgentResult, error)
	BeginAccepted(sessionID string) *AcceptedRun
	SetTools(tools []fantasy.AgentTool)
	SetSystemPrompt(systemPrompt string)
	// SystemPrompt returns the agent's system prompt: the base prompt plus
	// the configured suffix, without the per-session MCP instructions.
	// Teammates use it to extend the Lead's prompt byte for byte.
	SystemPrompt() string
	// AdoptedToolOrder returns the tool names a session adopted mid-turn, in
	// adoption order.
	AdoptedToolOrder(sessionID string) []string
	// SetAdoptedToolOrder seeds a session's adopted tool order. A teammate
	// inherits the Lead's order so its tools block stays identical to the
	// one the Lead already cached.
	SetAdoptedToolOrder(sessionID string, names []string)
	Cancel(sessionID string)
	CancelAll()
	// CancelBackgroundCompaction stops the summary written ahead of the
	// auto-compaction limit without touching the session's runs.
	CancelBackgroundCompaction(sessionID string)
	HasActiveTurn(sessionID string) bool
	HasPendingWork(sessionID string) bool
	IsBusy() bool
	QueuedPrompts(sessionID string) int
	QueuedPromptsList(sessionID string) []string
	ClearQueue(sessionID string)
	EnqueueNotice(sessionID, text string)
	AddNote(sessionID, text string)
	SetModels(large, small Model)
	Summarize(context.Context, string, fantasy.ProviderOptions, func(context.Context, *fantasy.ProviderError) error) error
	Model() Model
	GenerateTitle(ctx context.Context, sessionID, userPrompt string)
}

type Model struct {
	Model      fantasy.LanguageModel
	CatwalkCfg catwalk.Model
	ModelCfg   config.SelectedModel
	// Fusion is set when the model is a Copilot Fusion entry. The session
	// agent swaps the model for the full phase runner on its own turns.
	Fusion   *FusionConfig
	FlatRate bool
	// OAuth is true when the provider signs in by OAuth, so a rejected
	// credential calls for a new sign-in rather than a config change.
	OAuth bool
	// ProviderName is the provider's display name, such as "GitHub Copilot".
	ProviderName string
	// ServingProvider is the provider the request travels through (see
	// servingProvider), such as "anthropic" for a Claude model behind a
	// gateway. It decides the prompt-cache breakpoint layout.
	ServingProvider string
}

// activeCancel wraps a context.CancelFunc with a unique pointer identity.
// The pointer is used for compare-and-delete in the dispatch completion path:
// when a finishing run's deferred cleanup fires, it must only remove its own
// entry — not a newer run's entry that was installed in the window between
// the explicit Del and the function return.
type activeCancel struct {
	cancel context.CancelFunc
}

type sessionAgent struct {
	largeModel         *csync.Value[Model]
	smallModel         *csync.Value[Model]
	models             func() ModelSet
	systemPromptPrefix *csync.Value[string]
	systemPrompt       *csync.Value[string]
	// systemPromptSuffix is fixed at construction: it extends the system
	// prompt after the per-session MCP instructions so a teammate request
	// keeps the Lead's system message as an exact cacheable prefix.
	systemPromptSuffix string
	tools              *csync.Slice[fantasy.AgentTool]

	// modelSwitchMu guards the last model pair retired by SetModels. The
	// next run uses it to compact with the previous model before adopting a
	// smaller context window, rather than letting the new model overflow on
	// history it cannot fit.
	modelSwitchMu          sync.Mutex
	previousLarge          Model
	previousSmall          Model
	modelSwitchPending     bool
	fusionCompactionModels *csync.Map[string, fusionCompactionModel]

	isSubAgent bool
	sessions   session.Service
	messages   message.Service
	// history lists the files a session changed, which the state block
	// repeats after a compaction. Nil in most tests and in agents built
	// without a history service; the block then leaves the list out.
	history history.Service
	// cfg backs channel reply routing (config lookup + MCP tool
	// invocation). Nil in tests and subagents that never see channel
	// turns; sendChannelReply treats nil as "routing disabled".
	cfg *config.ConfigStore
	// replyToolRun invokes the MCP tool that answers a channel turn. It is
	// a field so a test can watch the reply without an MCP server; nil
	// means mcp.RunTool.
	replyToolRun         func(ctx context.Context, cfg *config.ConfigStore, name, tool, input string) (mcp.ToolResult, error)
	disableAutoSummarize bool
	// nonInteractive marks an agent that answers one prompt and exits
	// (`crush run`), which never starts a background compaction: the
	// process would exit before the summary is used.
	nonInteractive bool
	isYolo         bool
	// prompts is every prompt's text as read at startup; summarize and
	// generateTitle take theirs from it. Nil falls back to the built-ins.
	prompts map[string]string
	// permissions answers whether the user is being asked about tool
	// calls at all. Nil in tests and anywhere that never needs to know.
	permissions permission.Service
	notify      pubsub.Publisher[notify.Notification]
	runComplete pubsub.Publisher[notify.RunComplete]
	hooks       *hooks.Runner
	// gateChecks are extra built-in checks a test registers ahead of the
	// completion check; production leaves the list nil. It is a test seam,
	// like snapshotState.
	gateChecks []gateCheck
	// snapshotState reads the working tree for the gate. It is a field so
	// a test can count the calls; nil means gitutil.Snapshot.
	snapshotState func(ctx context.Context, dir string) (gitutil.WorktreeState, error)
	// beforeHandoff, when set, runs once between the gate's decision and
	// the hand-off. It is a test seam for the window in between, where a
	// prompt or a cancel can arrive.
	beforeHandoff func()

	messageQueue   *csync.Map[string, []SessionAgentCall]
	activeRequests *csync.Map[string, *activeCancel]
	// notices holds background completion notices waiting for the next step
	// of a busy session. They are drained into the request and persisted as
	// hidden user messages so the model sees them exactly once.
	noticesMu sync.Mutex
	notices   map[string][]string
	// steeringMu guards steering, the per-session channels that release
	// tools blocked on external work when the user sends a new prompt.
	steeringMu sync.Mutex
	steering   map[string]*steeringSignal
	// notes are what the host told the agent since its last turn, by
	// session, to add to the next prompt. They are not chat messages.
	notes   *csync.Map[string, []string]
	notesMu sync.Mutex
	// mcpInstructions freezes the MCP server instruction suffix per session
	// so a server that connects mid-session cannot rewrite the system prompt
	// (and with it the whole prompt-cache prefix). See
	// mcpInstructionsForSession.
	mcpInstructions *csync.Map[string, string]
	// promptHashes records, per session, a hash of every message in the last
	// prompt sent, so a cache-busting change can be attributed to the message
	// that caused it. See recordPromptFingerprint.
	promptHashes *csync.Map[string, []string]
	// toolsHashes records, per session, a hash of every tool definition in
	// the last request, so a cache-busting change to the tools block can be
	// attributed to the tool that caused it. See recordToolsFingerprint.
	toolsHashes *csync.Map[string, []string]
	// adoptedTools records, per session, the tools adopted mid-turn in
	// adoption order. Turn and compaction assembly replay that order so the
	// adopted tools stay at the end of the tools block instead of snapping
	// back to their sorted positions, which would rewrite the block and
	// re-read the conversation from the first adopted tool.
	adoptedTools *csync.Map[string, []string]
	// stepTools records, per session, the tool list the last step actually
	// sent. The tools block is serialized ahead of the messages, so a
	// compaction re-sends that exact list instead of rebuilding it from a
	// registry a server may have changed since (see toolsForStep).
	stepTools *csync.Map[string, []fantasy.AgentTool]
	// bgCompactions records, per session, the summary being written ahead of
	// the auto-compaction limit, or the one already written and waiting for
	// the limit. See startBackgroundCompaction.
	bgCompactions *csync.Map[string, *backgroundCompaction]
	// titles tracks in-flight title generation so CancelAll can drain it.
	titles *titleTracker
	// limits are the limits callers set on session trees. Nil holds none.
	limits *runLimits
	// lazyTools resolves the registry behind options.lazy_tools, the one the
	// coordinator's tool_search tool reveals through. Nil sends the full
	// palette.
	lazyTools func() *tools.LazyRegistry

	// dispatchMu holds a per-session mutex that serializes the
	// accepted -> (cancel-on-entry | queued | active) transition in
	// Run against a concurrent Cancel. The lock is held only during
	// the brief handoff (no DB or LLM I/O under the lock).
	dispatchMu *csync.Map[string, *sync.Mutex]
	// acceptedRuns counts dispatched-but-not-yet-active runs per
	// session. A counter > 0 means a dispatched prompt is in flight
	// and has not yet completed the dispatch handoff in Run. Only
	// BeginAccepted increments it; only AcceptedRun.Close decrements
	// it.
	acceptedRuns *csync.Map[string, int]
	// cancelMark records, per session, a high-water accept sequence: an
	// accepted handle is canceled by it iff the handle's sequence is at
	// or below the mark. Cancel raises the mark to the latest sequence
	// assigned at cancel time, so a single Cancel covers every prompt
	// accepted-but-not-yet-active then, while a prompt accepted later
	// (higher sequence) is never poisoned. Absent or 0 means no pending
	// cancel. It is only raised by Cancel when acceptedRuns > 0, so an
	// idle Escape never records a mark.
	cancelMark *csync.Map[string, uint64]
	// dispatchMuCreate guards lazy creation of per-session entries in
	// dispatchMu so two goroutines can't race to lock different mutex
	// instances for the same session.
	dispatchMuCreate sync.Mutex
	// acceptedMu serializes increments/decrements of acceptedRuns and
	// the assignment of accept sequence numbers from acceptSeqGen. It
	// is separate from dispatchMu so AcceptedRun.Close (which may run
	// while Run holds dispatchMu for the same session) does not
	// deadlock by re-entering the dispatch lock.
	acceptedMu sync.Mutex
	// acceptSeqGen is the monotonic source of accept sequence numbers.
	// Each BeginAccepted increments it under acceptedMu and stamps the
	// returned handle, so sequences strictly increase in accept order
	// across the agent. Cancel uses its current value as the per-session
	// high-water mark.
	acceptSeqGen uint64

	// now supplies the timestamp for the per-session environment snapshot.
	// Injectable so VCR recordings stay reproducible.
	now func() time.Time
}

type SessionAgentOptions struct {
	// Models hands out the agent's current models on every call, so a
	// credential refresh reaches requests already in flight.
	Models             func() ModelSet
	SystemPromptPrefix string
	SystemPrompt       string
	// SystemPromptSuffix is appended to the system prompt after the
	// per-session MCP instructions. Teammates use it to extend the Lead's
	// built prompt instead of replacing it, so the Lead's system message
	// stays an exact prefix of the teammate's and the provider's cached
	// prefix still covers it.
	SystemPromptSuffix   string
	IsSubAgent           bool
	DisableAutoSummarize bool
	NonInteractive       bool
	Sessions             session.Service
	Messages             message.Service
	Permissions          permission.Service
	Cfg                  *config.ConfigStore
	// Prompts is every prompt's text, read once at startup. Nil uses the
	// built-ins.
	Prompts     map[string]string
	Tools       []fantasy.AgentTool
	Notify      pubsub.Publisher[notify.Notification]
	RunComplete pubsub.Publisher[notify.RunComplete]
	// History is optional: it supplies the changed-file list the
	// post-compaction state block repeats. Nil leaves the list out.
	History history.Service
	// Now is the clock used for per-session data (the environment
	// snapshot). Tests pin it so recorded requests stay reproducible;
	// production leaves it nil for time.Now.
	Now func() time.Time
	// IsYolo is true when tool calls run without asking the user. It
	// feeds the turn telemetry and channel auto-discovery.
	IsYolo bool
	// Hooks runs lifecycle hooks for turns and compaction. Nil runs none.
	Hooks *hooks.Runner
	// titles is shared by a coordinator's agents so one drain covers all.
	titles *titleTracker
	// limits is shared by a coordinator's agents, so every run in a session
	// tree draws on the limits set on it.
	limits *runLimits
	// LazyTools resolves the registry behind options.lazy_tools. It is nil
	// when no coordinator owns the agent (tests, the agentic-fetch subagent),
	// and returning nil means the full palette is sent.
	LazyTools func() *tools.LazyRegistry
}

func NewSessionAgent(
	opts SessionAgentOptions,
) SessionAgent {
	var initial ModelSet
	if opts.Models != nil {
		initial = opts.Models()
	}
	a := &sessionAgent{
		largeModel:             csync.NewValue(initial.Large),
		smallModel:             csync.NewValue(initial.Small),
		systemPromptPrefix:     csync.NewValue(opts.SystemPromptPrefix),
		systemPrompt:           csync.NewValue(opts.SystemPrompt),
		systemPromptSuffix:     opts.SystemPromptSuffix,
		isSubAgent:             opts.IsSubAgent,
		sessions:               opts.Sessions,
		messages:               opts.Messages,
		history:                opts.History,
		cfg:                    opts.Cfg,
		prompts:                opts.Prompts,
		disableAutoSummarize:   opts.DisableAutoSummarize,
		nonInteractive:         opts.NonInteractive,
		isYolo:                 opts.IsYolo,
		permissions:            opts.Permissions,
		tools:                  csync.NewSliceFrom(opts.Tools),
		notify:                 opts.Notify,
		runComplete:            opts.RunComplete,
		hooks:                  opts.Hooks,
		messageQueue:           csync.NewMap[string, []SessionAgentCall](),
		notes:                  csync.NewMap[string, []string](),
		activeRequests:         csync.NewMap[string, *activeCancel](),
		notices:                make(map[string][]string),
		mcpInstructions:        csync.NewMap[string, string](),
		promptHashes:           csync.NewMap[string, []string](),
		toolsHashes:            csync.NewMap[string, []string](),
		adoptedTools:           csync.NewMap[string, []string](),
		stepTools:              csync.NewMap[string, []fantasy.AgentTool](),
		bgCompactions:          csync.NewMap[string, *backgroundCompaction](),
		titles:                 cmp.Or(opts.titles, new(titleTracker)),
		limits:                 opts.limits,
		lazyTools:              opts.LazyTools,
		dispatchMu:             csync.NewMap[string, *sync.Mutex](),
		acceptedRuns:           csync.NewMap[string, int](),
		cancelMark:             csync.NewMap[string, uint64](),
		fusionCompactionModels: csync.NewMap[string, fusionCompactionModel](),
		now:                    opts.Now,
	}
	if a.now == nil {
		a.now = time.Now
	}
	if opts.Models != nil {
		a.models = opts.Models
	} else {
		a.models = func() ModelSet {
			return ModelSet{Large: a.largeModel.Get(), Small: a.smallModel.Get()}
		}
	}
	return a
}

// AcceptedRun owns exactly one accept reservation taken by
// BeginAccepted. It is the only carrier of accept-state across the
// backend.runAgent / Coordinator.Run / sessionAgent.Run layers: a
// counter > 0 means a dispatched prompt is in flight and has not yet
// completed the dispatch handoff in Run. Close is the only way to
// release the reservation and is idempotent.
type AcceptedRun struct {
	agent     *sessionAgent
	sessionID string
	// seq is the monotonic accept sequence stamped by BeginAccepted. A
	// cancel covers this handle iff seq is at or below the session's
	// cancel mark, so a handle accepted after a cancel (higher seq) is
	// never poisoned by it.
	seq  uint64
	done atomic.Bool
}

// Close decrements the accept counter for this reservation. It is safe
// to call multiple times; only the first call has effect.
func (r *AcceptedRun) Close() {
	if r == nil {
		return
	}
	if !r.done.CompareAndSwap(false, true) {
		return
	}
	r.agent.endAccepted(r.sessionID)
}

// SessionID exposes the session this reservation is for so the run path
// can use it without an extra parameter.
func (r *AcceptedRun) SessionID() string {
	if r == nil {
		return ""
	}
	return r.sessionID
}

// BeginAccepted increments the accept counter for sessionID and returns
// a handle whose Close is the only way to decrement it. It is the only
// entry point that mutates acceptedRuns.
func (a *sessionAgent) BeginAccepted(sessionID string) *AcceptedRun {
	a.acceptedMu.Lock()
	defer a.acceptedMu.Unlock()
	count, _ := a.acceptedRuns.Get(sessionID)
	a.acceptedRuns.Set(sessionID, count+1)
	a.acceptSeqGen++
	return &AcceptedRun{agent: a, sessionID: sessionID, seq: a.acceptSeqGen}
}

// endAccepted decrements the accept counter for sessionID. It is only
// called via AcceptedRun.Close. It uses a dedicated lock (not the
// per-session dispatch mutex) so it can run while Run holds dispatchMu
// for the same session without deadlocking.
//
// When the count reaches zero the session's cancel mark is dropped: no
// accepted handle remains for it to cover, and any handle accepted later
// gets a strictly higher sequence that the mark would not match anyway.
// Handles canceled on entry never reach RunComplete, so this is the only
// place that clears the mark for an all-canceled batch. Sibling handles
// covered by the same mark are serialized on the per-session dispatch
// mutex and read the mark before they Close, so this never clears it out
// from under a covered handle still waiting to enter Run.
func (a *sessionAgent) endAccepted(sessionID string) {
	a.acceptedMu.Lock()
	defer a.acceptedMu.Unlock()
	count, ok := a.acceptedRuns.Get(sessionID)
	if !ok || count <= 1 {
		a.acceptedRuns.Del(sessionID)
		a.cancelMark.Del(sessionID)
		return
	}
	a.acceptedRuns.Set(sessionID, count-1)
}

// sessionMu returns the per-session dispatch mutex, creating it on first
// use. Creation is guarded so concurrent callers always observe the same
// mutex instance for a given session.
func (a *sessionAgent) sessionMu(sessionID string) *sync.Mutex {
	if mu, ok := a.dispatchMu.Get(sessionID); ok {
		return mu
	}
	a.dispatchMuCreate.Lock()
	defer a.dispatchMuCreate.Unlock()
	if mu, ok := a.dispatchMu.Get(sessionID); ok {
		return mu
	}
	mu := &sync.Mutex{}
	a.dispatchMu.Set(sessionID, mu)
	return mu
}

// enqueueCall appends call to the session's message queue. The
// OnComplete hook is stripped: the caller that supplied it (typically
// coordinator.Run) has its own retry/coalesce scope that ends when it
// returns, so by the time the queue drains nobody is left to consume the
// buffered terminal event. The recursive Run falls back to the default
// broker publish, which is what existing subscribers expect for queued
// turns.
func (a *sessionAgent) enqueueCall(call SessionAgentCall) {
	existing, ok := a.messageQueue.Get(call.SessionID)
	if !ok {
		existing = []SessionAgentCall{}
	}
	queued := call
	if call.Accepted != nil {
		// Preserve the accept sequence after the handle is stripped so
		// the queue-drain paths can tell a follow-up queued before a
		// cancel (covered by the mark) from one queued after it.
		queued.acceptSeq = call.Accepted.seq
	}
	queued.OnComplete = nil
	queued.Accepted = nil
	// Once queued the call is accepted: if no step folds it in, it runs as
	// its own turn rather than being refused as idle.
	queued.QueueOnly = false
	a.messageQueue.Set(call.SessionID, insertQueued(existing, queued))
	a.signalSteering(call.SessionID)
}

// insertQueued appends call, except that the user's own prompts go ahead of
// queued subagent reports: delivered together they read as one message, and
// a short line trailing a long report gets missed.
func insertQueued(queue []SessionAgentCall, call SessionAgentCall) []SessionAgentCall {
	at := len(queue)
	if call.Report == nil {
		for at > 0 && queue[at-1].Report != nil {
			at--
		}
	}
	return slices.Insert(queue, at, call)
}

// drainQueueForStep partitions the session's queued calls for the current
// streaming step under the per-session dispatch mutex so the filtering is
// atomic against a concurrent Cancel: canceledBySeq requires the caller to
// hold that mutex, and evaluating it here (rather than after unlocking)
// prevents a cancel recorded between the drain and the check from being
// observed inconsistently.
//
// Calls covered by a pending cancel are dropped and returned in canceled
// for the caller to settle (see settleDroppedCalls). Uncanceled calls without
// a RunID are returned in fold to be folded into the active turn,
// preserving the existing follow-up behavior. Uncanceled calls that carry
// a RunID are left in the queue so each runs as its own turn via the
// recursive run path and publishes its own RunComplete, giving every
// RunID-bearing prompt an explicit lifecycle instead of being silently
// absorbed into another turn. fold is processed by the caller without the
// lock held.
func (a *sessionAgent) drainQueueForStep(sessionID string) (fold, canceled []SessionAgentCall) {
	dispatchLock := a.sessionMu(sessionID)
	dispatchLock.Lock()
	defer dispatchLock.Unlock()
	queuedCalls, _ := a.messageQueue.Get(sessionID)
	var keep []SessionAgentCall
	for _, queued := range queuedCalls {
		if a.canceledBySeq(sessionID, queued.acceptSeq) {
			canceled = append(canceled, queued)
			continue
		}
		if queued.RunID != "" {
			keep = append(keep, queued)
			continue
		}
		fold = append(fold, queued)
	}
	if len(keep) == 0 {
		a.messageQueue.Del(sessionID)
	} else {
		a.messageQueue.Set(sessionID, keep)
	}
	return fold, canceled
}

// settleDroppedCalls settles queued calls removed without running. A
// subagent report is written to the transcript, where the model reads it
// next turn, so cancelling a turn never loses one. Each call that carries a
// RunID gets a terminal cancelled RunComplete. A queued prompt removed
// from the queue without ever running — covered by a pending cancel, or
// cleared by Cancel/ClearQueue — would otherwise leave a caller blocked on
// that RunID: `crush run` ignores live message events and exits only on a
// RunComplete whose RunID matches. Calls without a RunID had no such waiter
// and are dropped silently as before. A detached, bounded context keeps the
// must-deliver publish alive even when the run context that triggered the
// drop is already canceled.
func (a *sessionAgent) settleDroppedCalls(drops []SessionAgentCall) {
	if len(drops) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, d := range drops {
		if d.Report != nil {
			if _, err := a.createUserMessage(ctx, d, ""); err != nil {
				slog.Error("Failed to record a dropped subagent report", "session_id", d.SessionID, "error", err)
			}
		}
		if d.RunID == "" {
			continue
		}
		a.publishRunComplete(ctx, d, notify.RunComplete{
			SessionID: d.SessionID,
			RunID:     d.RunID,
			Cancelled: true,
		})
	}
}

// clearQueueAndNotify removes all queued prompts for the session and
// publishes a terminal cancelled RunComplete for any that carried a RunID,
// so callers waiting on those RunIDs (e.g. `crush run`) are not left
// hanging when their queued prompt is discarded without running.
func (a *sessionAgent) clearQueueAndNotify(sessionID string) {
	queued, ok := a.messageQueue.Get(sessionID)
	a.messageQueue.Del(sessionID)
	if !ok {
		return
	}
	a.settleDroppedCalls(queued)
}

// clearPendingCancel removes any pending-cancel mark for sessionID. It
// takes the per-session dispatch lock so it is ordered against Cancel
// and the dispatch handoff.
func (a *sessionAgent) clearPendingCancel(sessionID string) {
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	a.cancelMark.Del(sessionID)
}

// canceledBySeq reports whether an accepted handle or queued call with
// the given accept sequence is covered by a pending cancel for the
// session. Callers must hold the session's dispatch mutex. A tracked
// sequence (seq > 0) is covered only when it is at or below the cancel
// high-water mark, so a prompt accepted after the cancel (higher seq) is
// never poisoned. An untracked sequence (seq == 0, an in-process enqueue
// with no accept reservation) is covered whenever any mark is present,
// preserving the pre-sequence behavior. The mark is not consumed: it
// stays so every sibling handle it covers observes the same cancel, and
// a later handle (higher seq) ignores it regardless.
func (a *sessionAgent) canceledBySeq(sessionID string, seq uint64) bool {
	mark, ok := a.cancelMark.Get(sessionID)
	if !ok || mark == 0 {
		return false
	}
	return seq == 0 || seq <= mark
}

// canceledFinishTitle is the finish title of a run a cancel stopped. When
// the cancel is the tree's deadline arriving, the row names the deadline
// instead of the user.
func canceledFinishTitle(deadlinePassed bool) string {
	if deadlinePassed {
		return "Time limit reached"
	}
	return "User canceled request"
}

// providerDeadlineDetails is the finish text of a deadline the provider
// raised itself. It never names a budget Crush set, because the provider's
// own timeout is what ran out.
const providerDeadlineDetails = "The provider reported a deadline for this request, not a Crush timeout. Retry, and check the provider's status if it repeats."

// providerDeadlineError reports a deadline a provider raised as a bare
// context error, which skips the request guard. It embeds the guard's error
// type so the turn ends as the timeout it is, and its text never claims a
// budget Crush imposed.
type providerDeadlineError struct {
	requestTimeoutError
}

func (e *providerDeadlineError) Error() string {
	return fmt.Sprintf("LLM provider reported a request deadline: %v", e.cause)
}

// Unwrap exposes the embedded timeout error, so errors.As finds the type the
// turn-end and finish rules key on.
func (e *providerDeadlineError) Unwrap() error { return &e.requestTimeoutError }

// persistCanceledTurn writes the user/assistant records for a turn that
// was canceled before (or just as) streaming would have produced them.
// It creates the user message only when it was not already created by an
// earlier createUserMessage call (userMsgCreated), then writes an
// assistant message with FinishReasonCanceled. Both writes use
// context.WithoutCancel(ctx) so workspace shutdown (which cancels the run
// context) can't drop them.
func (a *sessionAgent) persistCanceledTurn(ctx context.Context, call SessionAgentCall, userMsgCreated bool) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if !userMsgCreated {
		if _, err := a.createUserMessage(writeCtx, call, ""); err != nil {
			return err
		}
	}
	largeModel := a.models().Large
	assistant, err := a.messages.Create(writeCtx, call.SessionID, message.CreateMessageParams{
		Role:      message.Assistant,
		Parts:     []message.ContentPart{},
		Model:     largeModel.ModelCfg.Model,
		ModelName: a.modelDisplayName(largeModel.ModelCfg),
		Provider:  largeModel.ModelCfg.Provider,
	})
	if err != nil {
		return err
	}
	assistant.AddFinish(message.FinishReasonCanceled, canceledFinishTitle(call.limits.deadlinePassed()), "")
	assistant.SetBuild(version.BuildStamp())
	assistant.SetTurnEnd(turnEndForError(context.Canceled, call.limits.deadlinePassed()))
	return a.messages.Update(writeCtx, assistant)
}

// publishRunComplete emits the authoritative terminal event for a turn.
// It honors the per-call OnComplete hook when set (so the coordinator can
// coalesce retries) and otherwise falls back to the RunComplete broker.
// ctx is used only for the bounded-blocking must-deliver publish; the
// terminal payload is supplied by the caller. This is the single emit path
// shared by the streaming defer and the cancel-on-entry early return so a
// caller waiting on RunComplete (e.g. `crush run` with a RunID) always
// observes exactly one terminal event regardless of which Run branch ends
// the turn.
func (a *sessionAgent) publishRunComplete(ctx context.Context, call SessionAgentCall, complete notify.RunComplete) {
	if call.OnComplete != nil {
		call.OnComplete(complete)
		return
	}
	if a.runComplete == nil {
		return
	}
	a.runComplete.PublishMustDeliver(ctx, pubsub.UpdatedEvent, complete)
}

// ValidateCall performs the cheap structural validation that
// sessionAgent.Run requires before a call can be dispatched: a call must
// carry either a non-empty prompt or a text attachment, and it must name a
// session. It is exported so callers that accept a run before dispatching it
// (e.g. backend.SendMessage) can apply the same checks and keep the error
// contract consistent.
func ValidateCall(call SessionAgentCall) error {
	if strings.TrimSpace(call.Prompt) == "" && !message.ContainsTextAttachment(call.Attachments) {
		return ErrEmptyPrompt
	}
	if call.SessionID == "" {
		return ErrSessionMissing
	}
	return nil
}

// sideQuestionRetrySuffix is appended to the question when a side question
// comes back empty. The system prompt already says the model has no tools,
// but a history full of tool turns is a stronger signal than the prompt, so
// the retry states the instruction where the model reads it last.
const sideQuestionRetrySuffix = "\n\nAnswer from the conversation above only. Do not call any tools."

// runEphemeral answers a side question against the session's existing
// history without persisting anything or exposing tools to the model.
func (a *sessionAgent) runEphemeral(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
	largeModel := a.largeModel.Get()
	policy := cachePolicyForModel(largeModel)
	promptPrefix := a.systemPromptPrefix.Get()

	currentSession, err := a.sessions.Get(ctx, call.SessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}

	history, files := a.preparePromptFor(msgs, largeModel.ModelCfg.Provider, largeModel.ModelCfg.Model, largeModel.CatwalkCfg.Capabilities.Vision, largeModel.CatwalkCfg.Capabilities.Audio, call.Attachments...)

	agent := fantasy.NewAgent(
		largeModel.Model,
		fantasy.WithSystemPrompt(sideQuestionPrompt()),
		fantasy.WithUserAgent(userAgent),
	)

	var maxOutputTokens *int64
	if call.MaxOutputTokens > 0 {
		maxOutputTokens = &call.MaxOutputTokens
	}

	prompt := message.PromptWithTextAttachments(call.Prompt, call.Attachments)
	var result *fantasy.AgentResult
	for attempt := 0; ; attempt++ {
		result, err = agent.Stream(ctx, fantasy.AgentStreamCall{
			Prompt:           prompt,
			Files:            files,
			Messages:         history,
			Headers:          sessionHeaders(call.SessionID),
			ProviderOptions:  call.ProviderOptions,
			MaxOutputTokens:  maxOutputTokens,
			TopP:             call.TopP,
			Temperature:      call.Temperature,
			PresencePenalty:  call.PresencePenalty,
			TopK:             call.TopK,
			FrequencyPenalty: call.FrequencyPenalty,
			OnAuthRefresh:    call.OnAuthRefresh,
			PrepareStep: func(callContext context.Context, options fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
				// No tools are registered on this agent; disable them
				// explicitly so nothing a caller added can leak in.
				prepared := fantasy.PrepareStepResult{Messages: options.Messages, DisableAllTools: true}
				for i := range prepared.Messages {
					prepared.Messages[i].ProviderOptions = nil
				}
				a.normalizePromptMessages(prepared.Messages)
				prepared.Messages = a.workaroundProviderMediaLimitations(prepared.Messages, largeModel)
				a.applyCacheBreakpoints(prepared.Messages, policy)
				if promptPrefix != "" {
					prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(promptPrefix)}, prepared.Messages...)
				}
				return callContext, prepared, nil
			},
		})
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(subAgentOutput(result)) != "" {
			return result, nil
		}

		// An empty answer with no error is a provider stop, not an answer:
		// the stream can end on an abnormal finish reason (capacity,
		// truncation) without yielding any text. A long agentic history
		// also teaches the model to answer with a tool call; with tools
		// disabled the provider drops the call and reports a normal stop
		// with no content. Retry once spelling out the only move left.
		reason := fantasy.FinishReasonUnknown
		if n := len(result.Steps); n > 0 {
			reason = result.Steps[n-1].FinishReason
		}
		if attempt > 0 {
			slog.Warn(
				"Side question returned no text",
				"session_id", call.SessionID,
				"provider", largeModel.ModelCfg.Provider,
				"model", largeModel.ModelCfg.Model,
				"finish_reason", string(reason),
			)
			return nil, fmt.Errorf("the model returned no answer (finish_reason=%s)", reason)
		}
		slog.Warn(
			"Side question returned no text; retrying with an answer-only instruction",
			"session_id", call.SessionID,
			"provider", largeModel.ModelCfg.Provider,
			"model", largeModel.ModelCfg.Model,
			"finish_reason", string(reason),
		)
		prompt = message.PromptWithTextAttachments(call.Prompt+sideQuestionRetrySuffix, call.Attachments)
	}
}

// turnFinish maps the upstream finish reason of a step onto what the
// assistant message records: the reason itself plus the banner copy the
// agent owns for the stops it has to explain (empty copy means the TUI
// supplies its own). Abnormal stops are logged because they otherwise
// look exactly like a turn that completed normally — the response just
// stops short.
func turnFinish(largeModel Model, sessionID string, reason fantasy.FinishReason) (message.FinishReason, string, string) {
	switch reason {
	case fantasy.FinishReasonStop:
		return message.FinishReasonEndTurn, "", ""
	case fantasy.FinishReasonToolCalls:
		return message.FinishReasonToolUse, "", ""
	case fantasy.FinishReasonLength:
		// The turn looks like a normal, completed answer that simply
		// stops mid-sentence, so leave a trace in the log even though
		// the TUI owns the display copy.
		slog.Warn(
			"Model response truncated at the output token limit",
			"session_id", sessionID,
			"provider", largeModel.ModelCfg.Provider,
			"model", largeModel.ModelCfg.Model,
			"finish_reason", string(reason),
		)
		return message.FinishReasonMaxTokens, "", ""
	case fantasy.FinishReasonContentFilter:
		// Provider safety classifier stopped the model
		// (Anthropic stop_reason=refusal, OpenAI content_filter).
		// The TUI owns the display copy; we only persist the
		// reason so the UI can show a REFUSED banner.
		slog.Warn(
			"Provider content filter stopped the model",
			"session_id", sessionID,
			"finish_reason", string(reason),
		)
		return message.FinishReasonContentFilter, "", ""
	case fantasy.FinishReasonError, fantasy.FinishReasonOther:
		// The provider gave up on the response from its own side
		// (e.g. insufficient_system_resource) but the stream still
		// completed, so no error reaches the run's error path. Record
		// it as an error finish, with copy generic enough for any
		// provider reason, or the turn renders as a normal completed
		// answer that stops short.
		slog.Warn(
			"Provider stopped the model",
			"session_id", sessionID,
			"provider", largeModel.ModelCfg.Provider,
			"model", largeModel.ModelCfg.Model,
			"finish_reason", string(reason),
		)
		return message.FinishReasonError, providerStoppedTurnMessage, fmt.Sprintf(
			"The provider ended this response before it was complete (finish_reason=%s). Try again, or use a different model.",
			string(reason),
		)
	}
	return message.FinishReasonUnknown, "", ""
}

func (a *sessionAgent) Run(ctx context.Context, call SessionAgentCall) (result *fantasy.AgentResult, retErr error) {
	if err := ValidateCall(call); err != nil {
		return nil, err
	}

	if call.Ephemeral {
		return a.runEphemeral(ctx, call)
	}

	// Once its session tree has spent its step budget or passed its
	// deadline, no run in the tree starts, a teammate's included.
	if call.limits == nil {
		call.limits = a.limits.forSession(ctx, a.sessions, call.SessionID)
	}
	if call.limits.reached() {
		slog.Info("Run refused: its session tree reached a limit", "session_id", call.SessionID)
		call.limits.refuse()
		call.Accepted.Close()
		if call.Report != nil {
			call.RunID = ""
			a.settleDroppedCalls([]SessionAgentCall{call})
		}
		return nil, errTreeLimitReached
	}

	if call.Channel != "" && call.channelMeta == nil {
		call.channelMeta, _ = parseChannelMeta(call.Prompt)
	}

	// genCtx/cancel are the run context and its cancel func, created under
	// the per-session dispatch mutex below so a concurrent Cancel can observe
	// the activeRequests entry before the assistant message exists.
	var (
		genCtx         context.Context
		cancel         context.CancelFunc
		userMsgCreated bool
	)

	// Serialize the dispatch decision (cancel-on-entry | queued | active)
	// against a concurrent Cancel. Cancel takes the same per-session lock, so
	// every cancel observes at least one of: a cancel mark, an activeRequests
	// entry, or a messageQueue entry it then clears. Holding the lock across
	// the busy check and the active registration also makes them atomic, so
	// two concurrent in-process callers — a burst of channel events, or a
	// channel event racing a typed prompt — cannot both pass the busy check
	// and start two runs on the same session.
	sessMu := a.sessionMu(call.SessionID)
	sessMu.Lock()

	if call.Accepted != nil && a.canceledBySeq(call.SessionID, call.Accepted.seq) {
		// Cancel-on-entry: a cancel arrived while this accepted run was
		// dispatched but not yet active, and this handle's accept sequence
		// is at or below the session's cancel mark. The mark is left in
		// place so sibling handles it also covers observe the same cancel;
		// release the accept reservation, drop the lock, and persist a
		// canceled turn without entering Stream.
		//
		// This path returns before the streaming defer that publishes
		// RunComplete is installed, so emit the terminal event explicitly.
		// Without it, a caller waiting on RunComplete for this RunID (e.g.
		// `crush run`, which ignores message events and blocks on
		// RunComplete) would hang on an immediately-canceled accepted run.
		call.Accepted.Close()
		sessMu.Unlock()
		complete := notify.RunComplete{
			SessionID: call.SessionID,
			RunID:     call.RunID,
			Cancelled: true,
		}
		if err := a.persistCanceledTurn(ctx, call, false); err != nil {
			complete.Error = err.Error()
			a.publishRunComplete(ctx, call, complete)
			return nil, err
		}
		a.publishRunComplete(ctx, call, complete)
		return nil, nil
	}

	if a.HasActiveTurn(call.SessionID) {
		// Busy: an earlier prompt is active. Queue this call so it is
		// folded into (or sequenced after) the active turn, and release any
		// accept reservation. A Cancel arriving after this point sees the
		// active entry and clears the queue.
		//
		// enqueueCall strips OnComplete: the caller that supplied the hook
		// (typically coordinator.Run) has its own retry/coalesce scope that
		// ends when it returns, so by the time the queue drains nobody is
		// left to consume the buffered terminal event. The queued turn falls
		// back to the default broker publish, which is what existing
		// subscribers expect.
		a.enqueueCall(call)
		if call.Accepted != nil {
			call.Accepted.Close()
		}
		sessMu.Unlock()
		return nil, nil
	}

	if call.QueueOnly {
		if call.Accepted != nil {
			call.Accepted.Close()
		}
		sessMu.Unlock()
		return nil, ErrSessionIdle
	}

	// Idle: become the active run. Register the cancel func before dropping
	// the lock so a Cancel that arrives between here and assistant creation
	// is not lost.
	runCtx := context.WithValue(ctx, tools.SessionIDContextKey, call.SessionID)
	runCtx = withRefusalRetries(runCtx, refusalRetriesPerTurn)
	genCtx, cancel = context.WithCancel(runCtx)
	ac := &activeCancel{cancel: cancel}
	a.activeRequests.Set(call.SessionID, ac)
	if call.Accepted != nil {
		call.Accepted.Close()
	}
	sessMu.Unlock()

	defer cancel()
	steering := a.beginSteering(call.SessionID)
	defer a.endSteering(call.SessionID, steering)
	// Conditional cleanup: only remove our entry if it hasn't been replaced
	// by a newer run. Without this guard, the deferred Del fires after a
	// concurrent run registers in the completion window, silently wiping
	// the new run's cancel and breaking cancellation.
	defer a.activeRequests.CompareAndDelete(call.SessionID, ac)

	// Copy mutable fields under lock to avoid races with SetTools/SetModels.
	largeModel := a.models().Large
	isHyper := largeModel.ModelCfg.Provider == hyper.Name

	// skipRunComplete is set just before the queued-recursion path so
	// the outer Run doesn't publish a RunComplete that would race
	// with — and be superseded by — the recursive call's own
	// RunComplete (each queued user prompt is its own turn and
	// publishes exactly one terminal event).
	var skipRunComplete bool
	// currentAssistant is declared here so the deferred RunComplete
	// publish below can capture the pointer that PrepareStep will
	// later (re)assign for each streaming step. The final assistant
	// message of the turn is the value reachable through this
	// pointer when the defer runs.
	var currentAssistant *message.Message
	// modelSteps counts model round-trips in this turn. The goal loop
	// budgets by model turn, not by user prompt, so it needs the count
	// even when one prompt drives several tool steps.
	var modelSteps int
	// Drain any debounced message updates before returning. message.Service
	// already flushes synchronously on terminal updates, but a defer here
	// guarantees the contract at every Run exit (success, error, panic
	// recovery upstream) without callers needing to know.
	//
	// After the flush completes — meaning all per-message
	// Publish(UpdatedEvent) calls have fired and been buffered into
	// every subscriber's channel — publish the authoritative
	// RunComplete event for this turn. The flush-then-publish order
	// gives well-behaved clients the best chance of seeing the final
	// message event before RunComplete; the embedded Text field
	// reconciles for clients that observe the events out of order
	// (the pubsub broker fan-in does not serialize publishes from
	// different upstream brokers).
	defer func() {
		// Use a context detached from the run context: workspace
		// shutdown cancels ctx before this goroutine returns, but the
		// buffered streaming deltas must still land before the DB is
		// closed. A short timeout bounds the flush.
		flushCtx, flushCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer flushCancel()
		if flushErr := a.messages.FlushAll(flushCtx); flushErr != nil {
			slog.Error("Failed to flush pending message updates after run", "error", flushErr)
		}
		if skipRunComplete {
			return
		}
		complete := notify.RunComplete{SessionID: call.SessionID, RunID: call.RunID, Steps: modelSteps}
		if currentAssistant != nil {
			complete.MessageID = currentAssistant.ID
			complete.Text = currentAssistant.Content().String()
		}
		if retErr != nil {
			complete.Error = retErr.Error()
			complete.Cancelled = errors.Is(retErr, context.Canceled)
		} else if ctx.Err() != nil {
			complete.Cancelled = true
		}
		// Prefer the per-call hook when supplied so the coordinator
		// can coalesce retries (e.g. unauthorized → re-auth → retry)
		// into a single user-visible terminal event. The fallback
		// must-deliver publish applies bounded-blocking semantics to
		// the authoritative terminal event so a momentarily-full
		// subscriber channel can't silently drop it and hang
		// non-interactive clients waiting on RunComplete.
		a.publishRunComplete(ctx, call, complete)
	}()

	// Copy mutable fields under lock to avoid races with SetTools.
	promptPrefix := a.systemPromptPrefix.Get()
	policy := cachePolicyForModel(largeModel)

	// Resolve the tool list and the MCP instruction block once per turn and
	// reuse them for every step (see the PrepareStep closure below).
	// Recomputing either mid-turn, or letting a server that connects
	// mid-session rewrite them, would move the tools/system prefix and
	// invalidate the provider's prompt cache for the whole conversation. The
	// MCP registry keeps a server's tools after a disconnect, so a flaky
	// server cannot drop them from the palette either. Tools the session
	// adopted mid-turn stay at the end of the list, in adoption order.
	agentTools := a.filterDisabledMCPTools(ctx, filterToolsForChannel(a.tools.Copy(), call.Channel, mcp.GetStates()))
	serverToolSearch := a.anthropicToolSearchForModel(largeModel)
	if serverToolSearch {
		// The server searches the deferred half, so this request carries
		// the whole palette and never changes when one is revealed.
		agentTools = anthropicToolSearchPalette(agentTools)
	} else {
		agentTools = a.lazyToolsForSession(call.SessionID, agentTools)
	}
	agentTools = stableToolOrder(agentTools, a.adoptedToolOrder(call.SessionID))
	agentTools = a.boundToolOutputs(agentTools)
	systemPrompt := a.systemPromptForSession(call.SessionID)

	a.applyToolCacheBreakpoint(agentTools, policy)

	// Copilot Fusion resolves its plan per turn, so the phase runner is built
	// here where the turn's prompt and the read-only review tools are known.
	// ModelProvider below has to hand this instance to fantasy: the fresh
	// build it normally returns is the plain router, not the phase runner.
	turnModel := largeModel.Model
	pendingNotes := a.takeNotes(call.SessionID)
	notesPersisted := false
	defer func() {
		if !notesPersisted {
			a.restoreNotes(call.SessionID, pendingNotes)
		}
	}()
	var routedPrimary *fusionCompactionModel
	if largeModel.Fusion != nil {
		fusion := newFusionModel(*largeModel.Fusion, agentTools, call.Prompt)
		fusion.requestedOutput = call.MaxOutputTokens
		prepareErr := fusion.prepare(genCtx)
		if prepareErr == nil {
			if budget, known := fusion.PromptBudget(); known && budget == 0 {
				prepareErr = fmt.Errorf("%w: no usable input capacity", errFusionPhaseBudget)
			}
		}
		if prepareErr != nil {
			if errors.Is(prepareErr, context.Canceled) {
				return nil, errors.Join(prepareErr, a.persistCanceledTurn(ctx, call, false))
			}
			modelContext := hostContextPrompt(call.HostContext) + notesPrompt(pendingNotes)
			if _, persistErr := a.createUserMessage(context.WithoutCancel(ctx), call, modelContext); persistErr != nil {
				return nil, errors.Join(prepareErr, persistErr)
			}
			notesPersisted = true
			return nil, prepareErr
		}
		if routed, budget, known, routeErr := fusion.primaryCompactionModel(genCtx, largeModel); routeErr == nil && known {
			routedPrimary = &fusionCompactionModel{model: routed, budget: budget}
		}
		turnModel = fusion
		largeModel.Model = turnModel
	}

	agent := fantasy.NewAgent(
		largeModel.Model,
		fantasy.WithSystemPrompt(systemPrompt),
		fantasy.WithTools(agentTools...),
		fantasy.WithUserAgent(userAgent),
	)

	sessionLock := sync.Mutex{}
	currentSession, err := a.sessions.Get(ctx, call.SessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}

	// A model switch can land on a smaller context window. Compact with
	// the previous model before adopting it, so the first request on the
	// new model is not rejected for history it can no longer hold.
	persistPromptOnError := func(cause error) error {
		if errors.Is(cause, context.Canceled) {
			return errors.Join(cause, a.persistCanceledTurn(ctx, call, false))
		}
		modelContext := hostContextPrompt(call.HostContext) + notesPrompt(pendingNotes)
		if _, persistErr := a.createUserMessage(context.WithoutCancel(ctx), call, modelContext); persistErr != nil {
			return errors.Join(cause, persistErr)
		}
		notesPersisted = true
		return cause
	}
	preflightPrompt := call.Prompt + hostContextPrompt(call.HostContext) + notesPrompt(pendingNotes)
	compacted, compactErr := a.maybeCompactForModelSwitch(genCtx, call.SessionID, currentSession, msgs, largeModel, call.ProviderOptions, call.OnAuthRefresh)
	if compactErr != nil {
		return nil, persistPromptOnError(compactErr)
	}
	preflightCompact := a.shouldCompactFusionPreflight(largeModel, currentSession, msgs, systemPrompt+promptPrefix, preflightPrompt, agentTools, call.Attachments)
	if !compacted && routedPrimary != nil && preflightCompact {
		compacted, compactErr = a.maybeCompactForFusionRoute(genCtx, call.SessionID, effectiveContextWindow(largeModel), call.ProviderOptions, call.OnAuthRefresh)
		if compactErr != nil {
			return nil, persistPromptOnError(compactErr)
		}
	}
	if !compacted && preflightCompact {
		if _, err := a.summarize(genCtx, compactionRequest{
			sessionID:     call.SessionID,
			largeModel:    largeModel,
			opts:          call.ProviderOptions,
			onAuthRefresh: call.OnAuthRefresh,
			trigger:       compactionTriggerFusionPreflight,
			active:        true,
		}); err != nil {
			return nil, persistPromptOnError(err)
		}
		compacted = true
	}
	if compacted {
		currentSession, err = a.sessions.Get(ctx, call.SessionID)
		if err != nil {
			return nil, fmt.Errorf("failed to reload session after model-switch compaction: %w", err)
		}
		msgs, err = a.getSessionMessages(ctx, currentSession)
		if err != nil {
			return nil, fmt.Errorf("failed to reload messages after model-switch compaction: %w", err)
		}
		if fusion, ok := largeModel.Model.(*fusionModel); ok {
			if budget, known := fusion.PromptBudget(); known && a.fusionPromptEstimate(largeModel, msgs, systemPrompt+promptPrefix, preflightPrompt, agentTools, call.Attachments) > budget {
				return nil, persistPromptOnError(fmt.Errorf("%w: prompt exceeds routed model capacity after compaction", errFusionPhaseBudget))
			}
		}
	}

	// Generate title from the first real (non-shell) user prompt in the
	// background; Run must not wait on it. CancelAll drains it instead.
	if !hasUserTextMessage(msgs) {
		titleCtx := context.WithoutCancel(ctx)
		a.titles.begin()
		go func() {
			defer a.titles.end()
			a.generateTitle(titleCtx, call.SessionID, call.Prompt)
		}()
		if a.shouldCreateEnvSnapshot(msgs) {
			snapshotMsg, err := a.createEnvSnapshotMessage(ctx, call.SessionID)
			if err != nil {
				return nil, fmt.Errorf("failed to persist environment snapshot: %w", err)
			}
			// Append the snapshot to this turn's history: it is created
			// after everything already in msgs, so that is exactly where
			// the next turn's replay finds it. Prepending would diverge
			// from the stored order whenever the session already had
			// text-less messages (a bang-mode shell command), re-reading
			// the whole opening turn.
			msgs = append(msgs, snapshotMsg)
		}
	}

	// Add the user message to the session. The context sent ahead of the
	// prompt is stored with it so later requests replay the same text.
	modelContext := hostContextPrompt(call.HostContext) + notesPrompt(pendingNotes)
	userMsg, err := a.createUserMessage(ctx, call, modelContext)
	if err != nil {
		return nil, err
	}
	notesPersisted = true
	userMsgCreated = true

	// Add the session to the context. The run context (genCtx) and its
	// cancel func were already created and registered under the dispatch
	// mutex above for both the accepted and in-process paths.
	ctx = context.WithValue(ctx, tools.SessionIDContextKey, call.SessionID)

	// The prompt just stored is what marks the end of any idle gap: a
	// session picked up after a long pause has no warm cache to lose.
	a.slimHistory(largeModel, msgs, userMsg.CreatedAt)
	history, files := a.preparePromptFor(msgs, largeModel.ModelCfg.Provider, largeModel.ModelCfg.Model, largeModel.CatwalkCfg.Capabilities.Vision, largeModel.CatwalkCfg.Capabilities.Audio, call.Attachments...)

	// The gate compares the working tree at the prompt's start with the
	// tree at its end, so a change made by a shell command or by a
	// teammate in the same tree counts too. A compaction continuation and
	// a gate continuation both copy the call, so this is taken once per
	// prompt.
	// Only a Lead turn can continue, so only a Lead turn pays for the
	// baseline.
	if call.gateBaseline == nil && !call.gateBaselineErr && a.gateNeedsChangedFiles() &&
		!call.NonInteractive && !call.Ephemeral && !a.isSubAgent {
		state, snapErr := a.snapshotWorktree(genCtx, a.gateWorkingDir())
		if snapErr != nil {
			call.gateBaselineErr = true
			slog.Debug("Turn-end gate: no working-tree baseline; only tool calls are compared",
				"session_id", call.SessionID, "error", snapErr)
		} else {
			call.gateBaseline = &state
			// Resolved once, here, so the tool paths can be expressed
			// against the root the porcelain paths use.
			call.gateRoot, _ = gitutil.RepoRoot(genCtx, a.gateWorkingDir())
		}
	}

	startTime := time.Now()
	a.eventPromptSent(call.SessionID)
	// A continuation is the same prompt, as Claude Code's Stop
	// continuation is: it does not submit a new prompt.
	if !call.gateContinued {
		a.fireTurnHook(genCtx, call, hooks.Event{Name: hooks.EventTurnStart})
	}

	var stepMessages []fantasy.Message
	// What the request being sent carried, and how much of it the session
	// had already sent. Recorded with the step's usage, see OnStepFinish.
	var (
		promptMessages   int
		previousMessages int
		sharedMessages   int
		toolsChanged     bool
	)
	var shouldSummarize bool
	// Written from OnToolCall and read from OnToolResult, which fantasy
	// invokes concurrently for parallel tool calls.
	sanitizedToolCalls := csync.NewMap[string, bool]()
	// reads decides whether the step streaming now may run its read-only
	// calls at once. See parallel_reads.go.
	reads := &readGate{}
	// Full names of tool calls that completed without error this turn.
	// Written from OnToolResult, which runs concurrently for parallel
	// tool calls, and read after Stream returns, where sendChannelReply
	// uses it to tell whether the model already replied on the
	// originating channel.
	completedToolCalls := csync.NewMap[string, struct{}]()
	// Calls that got a reply this turn; the rest are settled when it ends.
	answeredToolCalls := csync.NewMap[string, struct{}]()
	// Don't send MaxOutputTokens if 0 — some providers (e.g. LM Studio) reject it
	var maxOutputTokens *int64
	if call.MaxOutputTokens > 0 {
		maxOutputTokens = &call.MaxOutputTokens
	}
	var textRepetition *repetitionDetector
	var reasoningRepetition *repetitionDetector
	if a.repetitionDetectionEnabled() {
		textRepetition = newRepetitionDetector()
		reasoningRepetition = newRepetitionDetector()
	}
	repetitionDetected := false
	repetitionSafeRunes := 0
	repetitionFromReasoning := false
	var stepResults []fantasy.StepResult
	repeatRemindersSent := make(map[int]bool)
	lastRepeatSignature := ""
	pendingRepeatReminder := ""
	pendingTodoReminder := ""
	var injections []stepInjection
	todoRemindersSent := 0
	lastTodoReminderStep := -todoReminderMinGap
	// todoStaleItem is the item the reminders have been about, so the
	// unanswered count only grows while the same item stays behind. A turn
	// that ends with it still open is reported to the user.
	todoStaleItem := ""
	todoReminderUnanswered := 0
	result, err = agent.Stream(genCtx, fantasy.AgentStreamCall{
		Prompt:           modelContext + message.PromptWithTextAttachments(call.Prompt, call.Attachments),
		Files:            files,
		Messages:         history,
		Headers:          sessionHeaders(call.SessionID),
		ProviderOptions:  call.ProviderOptions,
		MaxOutputTokens:  maxOutputTokens,
		TopP:             call.TopP,
		Temperature:      call.Temperature,
		PresencePenalty:  call.PresencePenalty,
		TopK:             call.TopK,
		FrequencyPenalty: call.FrequencyPenalty,
		RepairToolCall:   repairToolCall,
		PrepareStep: func(callContext context.Context, options fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			modelSteps++
			// Each step decides its own parallelism from the calls it
			// streams, so the gate starts empty.
			reads.reset()
			call.limits.takeStep()
			base := options.Messages
			for i := range base {
				base[i].ProviderOptions = nil
			}
			a.normalizePromptMessages(base)

			// Reuse the turn's tool list, adopting anything registered since
			// the turn began (a lazy MCP server the model just activated, a
			// server that finished connecting). Rebuilding the list from
			// scratch per step, or letting a flaky server shrink it, would
			// move the tools block and its cache breakpoint.
			stepTools, adopted := a.toolsForStep(callContext, call.SessionID, call.Channel, agentTools, serverToolSearch)
			if adopted {
				a.applyToolCacheBreakpoint(stepTools, policy)
			}
			a.stepTools.Set(call.SessionID, slices.Clone(stepTools))
			toolsChanged = a.recordToolsFingerprint(call.SessionID, stepTools)
			// The request carries the gate wrapper, not the stored list: the
			// compaction turn replays the stored one, and it must not inherit
			// the verdict of the step this turn ended on.
			prepared.Tools = parallelReadTools(stepTools, reads)

			// Drain queued follow-up prompts for this step. Calls covered
			// by a cancel recorded while they sat in the queue are dropped:
			// a cancel that arrived after a prompt was queued must not let
			// it run as part of this step. Coverage is per-call by accept
			// sequence so a follow-up queued after the cancel (higher seq)
			// is not dropped. A dropped prompt carrying a RunID still gets
			// its terminal cancelled RunComplete so a caller waiting on it
			// does not hang. Uncanceled prompts without a RunID are folded
			// into this turn; uncanceled prompts with a RunID are left
			// queued so each runs as its own turn (with its own
			// RunComplete) via the recursive run path below.
			fold, canceled := a.drainQueueForStep(call.SessionID)
			a.settleDroppedCalls(canceled)
			// Add the messages in the order the session log stores them,
			// because that is the order the next turn replays: reminders
			// were persisted when the previous step finished, queued
			// prompts and notices are persisted here.
			var added []fantasy.Message
			if pendingRepeatReminder != "" {
				added = append(added, fantasy.NewUserMessage(pendingRepeatReminder))
				pendingRepeatReminder = ""
			}
			if pendingTodoReminder != "" {
				added = append(added, fantasy.NewUserMessage(pendingTodoReminder))
				pendingTodoReminder = ""
			}
			for _, queued := range fold {
				userMessage, createErr := a.createUserMessage(callContext, queued, "")
				if createErr != nil {
					return callContext, prepared, createErr
				}
				added = append(added, userMessage.ToAIMessage()...)
			}
			for _, notice := range a.drainNotices(call.SessionID) {
				if _, createErr := a.messages.Create(callContext, call.SessionID, message.CreateMessageParams{
					Role:  message.User,
					Parts: []message.ContentPart{message.TextContent{Text: notice, Hidden: true}},
				}); createErr != nil {
					slog.Warn("Failed to persist background notice", "error", createErr)
				}
				added = append(added, fantasy.NewUserMessage(notice))
			}
			if len(added) > 0 {
				injections = append(injections, stepInjection{at: len(base), messages: added})
			}
			prepared.Messages = withInjections(base, injections)

			prepared.Messages = dropEmptyToolMedia(prepared.Messages)
			prepared.Messages = a.workaroundProviderMediaLimitations(prepared.Messages, largeModel)
			prepared.Messages = applyImageBudget(prepared.Messages, a.maxRequestImages())

			a.applyCacheBreakpoints(prepared.Messages, policy)
			if serverToolSearch {
				callContext = withAnthropicToolSearch(callContext)
			}

			if promptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(promptPrefix)}, prepared.Messages...)
			}

			previousMessages, sharedMessages = a.recordPromptFingerprint(call.SessionID, prepared.Messages)
			promptMessages = len(prepared.Messages)

			sessionLock.Lock()
			stepMessages = cloneFantasyMessages(prepared.Messages)
			sessionLock.Unlock()

			var assistantMsg message.Message
			assistantMsg, err = a.messages.Create(callContext, call.SessionID, message.CreateMessageParams{
				Role:      message.Assistant,
				Parts:     []message.ContentPart{},
				Model:     largeModel.ModelCfg.Model,
				ModelName: a.modelDisplayName(largeModel.ModelCfg),
				Provider:  largeModel.ModelCfg.Provider,
			})
			if err != nil {
				return callContext, prepared, err
			}
			callContext = context.WithValue(callContext, tools.MessageIDContextKey, assistantMsg.ID)
			callContext = context.WithValue(callContext, tools.ChannelContextKey, call.Channel)
			callContext = context.WithValue(callContext, tools.SupportsImagesContextKey, largeModel.CatwalkCfg.Capabilities.Vision)
			callContext = context.WithValue(callContext, tools.ModelNameContextKey, largeModel.CatwalkCfg.Name)
			callContext = context.WithValue(callContext, tools.SteeringContextKey, steering)
			currentAssistant = &assistantMsg
			return callContext, prepared, err
		},
		OnReasoningStart: func(id string, reasoning fantasy.ReasoningContent) error {
			if repetitionDetected {
				return nil
			}
			if reasoningRepetition != nil {
				if safe, detected := reasoningRepetition.Observe(reasoning.Text); detected {
					repetitionDetected = true
					repetitionSafeRunes = safe
					repetitionFromReasoning = true
					cancel()
					return nil
				}
			}
			currentAssistant.AppendReasoningContent(reasoning.Text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnReasoningDelta: func(id string, text string) error {
			if repetitionDetected {
				return nil
			}
			if reasoningRepetition != nil {
				if safe, detected := reasoningRepetition.Observe(text); detected {
					repetitionDetected = true
					repetitionSafeRunes = safe
					repetitionFromReasoning = true
					cancel()
					return nil
				}
			}
			currentAssistant.AppendReasoningContent(text)
			if text != "" {
				currentAssistant.SetFirstTokenAt(time.Now())
			}
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnReasoningEnd: func(id string, reasoning fantasy.ReasoningContent) error {
			// handle anthropic signature
			if anthropicData, ok := reasoning.ProviderMetadata[anthropic.Name]; ok {
				if reasoning, ok := anthropicData.(*anthropic.ReasoningOptionMetadata); ok {
					currentAssistant.AppendReasoningSignature(reasoning.Signature)
					currentAssistant.SetReasoningRedactedData(reasoning.RedactedData)
				}
			}
			if googleData, ok := reasoning.ProviderMetadata[google.Name]; ok {
				if reasoning, ok := googleData.(*google.ReasoningMetadata); ok {
					currentAssistant.AppendThoughtSignature(reasoning.Signature, reasoning.ToolID)
				}
			}
			if openaiData, ok := reasoning.ProviderMetadata[openai.Name]; ok {
				if reasoning, ok := openaiData.(*openai.ResponsesReasoningMetadata); ok {
					currentAssistant.SetReasoningResponsesData(reasoning)
				}
			}
			currentAssistant.FinishThinking()
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnTextDelta: func(id string, text string) error {
			if repetitionDetected {
				return nil
			}
			if textRepetition != nil {
				if safe, detected := textRepetition.Observe(text); detected {
					repetitionDetected = true
					repetitionSafeRunes = safe
					cancel()
					return nil
				}
			}
			// Persist the model's text verbatim. Trimming it here (for
			// example a leading newline) would make the stored message
			// differ from the bytes sent in this same turn, so the next
			// turn's replay would miss the prompt cache. Display code
			// trims leading whitespace instead.
			currentAssistant.AppendContent(text)
			if text != "" {
				currentAssistant.SetFirstTokenAt(time.Now())
			}
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnToolInputStart: func(id string, toolName string) error {
			reads.announce(id, toolName)
			toolCall := message.ToolCall{
				ID:               id,
				Name:             toolName,
				ProviderExecuted: false,
				Finished:         false,
			}
			currentAssistant.AddToolCall(toolCall)
			// Use parent ctx instead of genCtx to ensure the update succeeds
			// even if the request is canceled mid-stream
			return a.messages.Update(ctx, *currentAssistant)
		},
		OnRetry: func(err *fantasy.ProviderError, delay time.Duration) {
			slog.Warn("Provider request failed, retrying", providerRetryLogFields(err, delay)...)
			// A retry streams the step again, so the calls the failed
			// attempt announced are gone.
			reads.reset()
			// Reset streamed content so the retried response doesn't
			// concatenate with partial content from the failed attempt.
			// On the final attempt (no more retries), any partial content
			// stays in the message as useful context beneath the error.
			currentAssistant.ResetStreamedContent()
			if updateErr := a.messages.Update(genCtx, *currentAssistant); updateErr != nil {
				slog.Error("Failed to reset message on retry", "error", updateErr)
			}
		},
		OnAuthRefresh: call.OnAuthRefresh,
		ModelProvider: func() fantasy.LanguageModel {
			if _, ok := turnModel.(*fusionModel); ok {
				return turnModel
			}
			return a.currentBuildOf(largeModel)
		},
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			reads.observe(tc.ToolCallID)
			input, wasSanitized := sanitizeToolInput(tc.ToolName, tc.ToolCallID, tc.Input)
			if wasSanitized {
				sanitizedToolCalls.Set(tc.ToolCallID, true)
			}
			toolCall := message.ToolCall{
				ID:               tc.ToolCallID,
				Name:             tc.ToolName,
				Input:            input,
				ProviderExecuted: tc.ProviderExecuted,
				Finished:         true,
			}
			currentAssistant.AddToolCall(toolCall)
			// Use parent ctx instead of genCtx to ensure the update succeeds
			// even if the request is canceled mid-stream
			return a.messages.Update(ctx, *currentAssistant)
		},
		OnToolResult: func(result fantasy.ToolResultContent) error {
			toolResult := a.convertToToolResult(result)
			if _, ok := sanitizedToolCalls.Get(result.ToolCallID); ok {
				toolResult.Content = sanitizedToolCallResult
				toolResult.IsError = true
				toolResult.Metadata = tools.SetErrorKind(toolResult.Metadata, tools.ErrorKindInvalidArguments)
			} else if toolResult.IsError && genCtx.Err() != nil {
				// The turn was cancelled out from under the tool.
				toolResult.Content = interruptedToolCallResult
				toolResult.Metadata = tools.SetErrorKind(toolResult.Metadata, tools.ErrorKindInterrupted)
			}
			toolResult = stampLibraryErrorKind(toolResult)
			if !toolResult.IsError && toolResult.Name != "" {
				completedToolCalls.Set(toolResult.Name, struct{}{})
			}
			toolResult.FinishedAtMs = time.Now().UnixMilli()
			// Use parent ctx instead of genCtx to ensure the message is created
			// even if the request is canceled mid-stream
			_, createMsgErr := a.messages.Create(ctx, currentAssistant.SessionID, message.CreateMessageParams{
				Role: message.Tool,
				Parts: []message.ContentPart{
					toolResult,
				},
			})
			if createMsgErr == nil {
				answeredToolCalls.Set(result.ToolCallID, struct{}{})
			}
			return createMsgErr
		},
		OnStepFinish: func(stepResult fantasy.StepResult) error {
			for _, w := range stepResult.Warnings {
				slog.Warn("Provider warning", "type", w.Type, "message", w.Message)
			}
			finishReason, finishMessage, finishDetails := turnFinish(largeModel, call.SessionID, stepResult.FinishReason)
			// If a tool result halted the turn (e.g. a hook halt or a
			// permission denial), the step ends on FinishReasonToolCalls but
			// the model will not be called again. Treat it as the end of the
			// turn so the UI can render the assistant footer.
			if finishReason == message.FinishReasonToolUse {
				for _, tr := range stepResult.Content.ToolResults() {
					if tr.StopTurn {
						finishReason = message.FinishReasonEndTurn
						break
					}
				}
			}
			currentAssistant.AddFinish(finishReason, finishMessage, finishDetails)
			currentAssistant.SetBuild(version.BuildStamp())
			currentAssistant.PrismModelID, currentAssistant.PrismModelName = extractPrismModel(stepResult.ProviderMetadata)
			currentAssistant.PrismHypercreditSavings, currentAssistant.PrismDollarSavings = extractPrismSavings(stepResult.ProviderMetadata)
			sessionLock.Lock()
			defer sessionLock.Unlock()

			// One incrementing UPDATE replaces a read plus a full-row
			// write per step, and it can no longer clobber a concurrent
			// todo, title, or sub-agent rollup written between the read and
			// the write.
			usage, estimated := fallbackStepUsage(stepMessages, stepResult)
			currentAssistant.SetRequestUsage(message.RequestUsage{
				InputTokens:      usage.InputTokens,
				CacheReadTokens:  usage.CacheReadTokens,
				CacheWriteTokens: usage.CacheCreationTokens,
				OutputTokens:     usage.OutputTokens,
				PromptMessages:   promptMessages,
				PreviousMessages: previousMessages,
				SharedMessages:   sharedMessages,
				ToolsChanged:     toolsChanged,
				Estimated:        estimated,
			})
			pricingModel := a.modelForStepCost(largeModel, stepResult.ProviderMetadata)
			updatedSession, sessionErr := a.sessions.AddStepUsage(
				ctx,
				call.SessionID,
				a.stepUsageFor(pricingModel, currentSession, usage, a.openrouterCost(stepResult.ProviderMetadata), estimated),
			)
			if sessionErr != nil {
				return sessionErr
			}
			currentSession = updatedSession

			stepResults = append(stepResults, stepResult)
			// Free to collect, and it catches an edit the working tree
			// cannot tell apart from one made before the prompt.
			call.gateChangedFiles = append(call.gateChangedFiles, stepChangedFiles(stepResult)...)
			if a.repeatToolRemindersEnabled() {
				signature := getToolInteractionSignature(stepResult.Content)
				if signature != lastRepeatSignature {
					lastRepeatSignature = signature
					repeatRemindersSent = make(map[int]bool)
				}
				count, toolName := consecutiveToolCallRepeats(stepResults)
				for _, threshold := range repeatToolReminderThresholds {
					if count < threshold || repeatRemindersSent[threshold] {
						continue
					}
					repeatRemindersSent[threshold] = true
					pendingRepeatReminder = repeatToolReminder(toolName, count)
					if _, createErr := a.messages.Create(ctx, call.SessionID, message.CreateMessageParams{
						Role:  message.User,
						Parts: []message.ContentPart{message.TextContent{Text: pendingRepeatReminder, Hidden: true}},
					}); createErr != nil {
						slog.Warn("Failed to persist repeat-tool reminder", "error", createErr)
					}
					break
				}
			}
			if todoReminderDue(stepResult, currentSession.Todos, modelSteps-lastTodoReminderStep, todoRemindersSent) {
				stale := staleTodo(currentSession.Todos)
				if stale != "" && stale == todoStaleItem {
					todoReminderUnanswered++
				} else {
					todoReminderUnanswered = 0
				}
				todoStaleItem = stale
				pendingTodoReminder = todoReminder(currentSession.Todos, todoReminderUnanswered)
				lastTodoReminderStep = modelSteps
				todoRemindersSent++
				if _, createErr := a.messages.Create(ctx, call.SessionID, message.CreateMessageParams{
					Role:  message.User,
					Parts: []message.ContentPart{message.TextContent{Text: pendingTodoReminder, Hidden: true}},
				}); createErr != nil {
					slog.Warn("Failed to persist todo reminder", "error", createErr)
				}
			}

			return a.messages.Update(genCtx, *currentAssistant)
		},
		StopWhen: []fantasy.StopCondition{
			func(_ []fantasy.StepResult) bool {
				if _, ok := largeModel.Model.(*fusionModel); ok {
					threshold, _ := a.compactionConfig()
					if threshold <= 0 {
						return false
					}
				}
				cw := effectiveContextWindow(largeModel)
				// If context window is unknown (0), skip auto-summarize
				// to avoid immediately truncating custom/local models.
				if cw == 0 {
					return false
				}
				tokens := currentSession.CompletionTokens + currentSession.PromptTokens
				remaining := cw - tokens
				var threshold int64
				if cw > largeContextWindowThreshold {
					threshold = largeContextWindowBuffer
				} else {
					threshold = int64(float64(cw) * smallContextWindowRatio)
				}
				if (remaining <= threshold) && !a.disableAutoSummarize {
					shouldSummarize = true
					return true
				}
				return false
			},
			func(steps []fantasy.StepResult) bool {
				return hasRepeatedToolCalls(steps, loopDetectionWindowSize, loopDetectionMaxRepeats)
			},
			// Once the tree's step budget is spent or its deadline passed,
			// every run in the tree stops after its current step.
			func(_ []fantasy.StepResult) bool {
				return call.limits.reached()
			},
		},
	})

	a.eventPromptResponded(call.SessionID, time.Since(startTime).Truncate(time.Second))
	if err == nil && routedPrimary != nil && a.fusionCompactionModels != nil {
		a.fusionCompactionModels.Set(call.SessionID, *routedPrimary)
	}

	if repetitionDetected {
		// The run context was cancelled to stop the stream. Trim the
		// repeated tail before the error path persists the final state,
		// so the session does not keep thousands of duplicate runes.
		if currentAssistant != nil {
			if repetitionFromReasoning {
				currentAssistant.TruncateReasoning(repetitionSafeRunes)
			} else {
				currentAssistant.TruncateContent(repetitionSafeRunes)
			}
			if updateErr := a.messages.Update(context.WithoutCancel(ctx), *currentAssistant); updateErr != nil {
				slog.Error("Failed to trim repeated stream content", "error", updateErr)
			}
		}
		err = errRepetitionDetected
	}

	// overflowRecovery tracks a failed turn that will be recovered by
	// compacting the session and requeueing the same call once (see the
	// compaction block below). origOverflowErr keeps the provider error
	// so a failed recovery still reports why the turn was rejected.
	overflowRecovery := false
	var origOverflowErr error

	// Final writes use a context detached from the run: workspace shutdown
	// cancels ctx before agent goroutines finish, and the turn's ending
	// must still land.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cleanupCancel()
	if currentAssistant != nil {
		answered := func(id string) bool {
			_, ok := answeredToolCalls.Get(id)
			return ok
		}
		if settleErr := a.settleTurn(cleanupCtx, currentAssistant, answered, err); settleErr != nil {
			slog.Error("Failed to settle unanswered tool calls",
				"session_id", call.SessionID, "error", settleErr)
		}
	}
	if err != nil {
		// A provider that surfaces its own request deadline as a raw context
		// error skips the request guard, so label it as the timeout it is
		// rather than as a provider fault. Only a deadline no context of this
		// run caused qualifies: an ended genCtx or a passed tree deadline is a
		// cancel, and naming it a timeout would hide what actually happened.
		if errors.Is(err, context.DeadlineExceeded) && genCtx.Err() == nil && !call.limits.deadlinePassed() {
			err = &providerDeadlineError{requestTimeoutError: requestTimeoutError{cause: err}}
		}
		isCancelErr := errors.Is(err, context.Canceled)
		slog.Info("Agent stream returned error",
			"error", err.Error(),
			"error_type", fmt.Sprintf("%T", err),
			"oauth", largeModel.OAuth,
			"is_cancel", isCancelErr)
		// A provider context-overflow rejection is recoverable: instead
		// of failing the turn, compact the session and requeue this call
		// once so the work resumes. This is the backstop for overflows
		// the mid-turn guard missed and the only recovery for models
		// whose context window is unknown; the call flag bounds it to a
		// single retry.
		overflowRecovery = shouldRecoverFromOverflow(err, call.overflowRetried)
		if overflowRecovery {
			call.overflowRetried = true
			origOverflowErr = err
		}
		// Fire once the turn's final state is written, whichever return
		// below is taken, and outlive the cancelled run context.
		defer a.fireTurnHook(context.WithoutCancel(ctx), call, turnEndEvent(err, currentAssistant))
		if currentAssistant == nil {
			// Cancel-before-assistant-creation window: the run was
			// canceled after activeRequests.Set but before PrepareStep
			// created the assistant message. Without this, the turn
			// would return with no FinishReasonCanceled marker and no
			// user-visible record. The user message was already created
			// above, so persistCanceledTurn only writes the assistant
			// record.
			if isCancelErr {
				if persistErr := a.persistCanceledTurn(ctx, call, userMsgCreated); persistErr != nil {
					return nil, persistErr
				}
			}
			if !overflowRecovery {
				return result, err
			}
		}
		if currentAssistant != nil {
			// Ensure we finish thinking on error to close the reasoning state.
			currentAssistant.FinishThinking()
			var fantasyErr *fantasy.Error
			var providerErr *fantasy.ProviderError
			var requestTimedOutErr *requestTimeoutError
			const defaultTitle = "Provider Error"
			linkStyle := lipgloss.NewStyle().Foreground(tone.Guac).Underline(true)
			if errors.Is(err, errRepetitionDetected) {
				currentAssistant.AddFinish(
					message.FinishReasonError,
					"Repetition detected",
					"The model started repeating the same text, so the response was stopped. Retry to get a fresh answer.",
				)
			} else if isCancelErr {
				currentAssistant.AddFinish(message.FinishReasonCanceled, canceledFinishTitle(call.limits.deadlinePassed()), "")
			} else if errors.Is(err, errRefused) {
				// Every retry was refused; keep the refusal banner rather than
				// reporting a generic provider error.
				currentAssistant.AddFinish(message.FinishReasonContentFilter, "", "")
			} else if errors.As(err, &requestTimedOutErr) {
				// Checked before the provider branches so a deadline our own
				// request timeout imposed is never reported as a provider error.
				details := requestTimedOutErr.userMessage()
				var deadlineErr *providerDeadlineError
				if errors.As(err, &deadlineErr) {
					details = providerDeadlineDetails
				}
				currentAssistant.AddFinish(message.FinishReasonError, "Request timed out", details)
			} else if isHyper && errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusUnauthorized {
				currentAssistant.AddFinish(message.FinishReasonError, "Unauthorized", `Please re-authenticate with Hyper. You can also run "crush auth" to re-authenticate.`)
			} else if largeModel.OAuth && errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusUnauthorized {
				currentAssistant.AddFinish(message.FinishReasonError, "Unauthorized", signInAgainMessage(largeModel))
			} else if errors.As(err, &providerErr) {
				if providerErr.Message == "The requested model is not supported." {
					url := "https://github.com/settings/copilot/features"
					link := linkStyle.Hyperlink(url, "id=copilot").Render(url)
					currentAssistant.AddFinish(
						message.FinishReasonError,
						"Copilot model not enabled",
						fmt.Sprintf("%q is not enabled in Copilot. Go to the following page to enable it. Then, wait 5 minutes before trying again. %s", largeModel.CatwalkCfg.Name, link),
					)
				} else {
					currentAssistant.AddFinish(message.FinishReasonError, cmp.Or(stringext.Capitalize(providerErr.Title), defaultTitle), providerErr.Message)
				}
			} else if errors.As(err, &fantasyErr) {
				currentAssistant.AddFinish(message.FinishReasonError, cmp.Or(stringext.Capitalize(fantasyErr.Title), defaultTitle), fantasyErr.Message)
			} else if fantasy.IsTransportError(err) {
				wrapped := fantasy.NewTransportError(err)
				currentAssistant.AddFinish(message.FinishReasonError, stringext.Capitalize(wrapped.Title), wrapped.Message)
			} else {
				currentAssistant.AddFinish(message.FinishReasonError, defaultTitle, err.Error())
			}
			// Every branch above replaced the finish part, and with it any
			// stamp the step had already written.
			currentAssistant.SetBuild(version.BuildStamp())
			// A recovered overflow is stamped again once its compaction
			// has run, since a blocked or failed one changes the ending.
			currentAssistant.SetTurnEnd(turnEndForError(err, call.limits.deadlinePassed()))
			// Note: we use the cleanup context here because the genCtx has been
			// cancelled.
			updateErr := a.messages.Update(cleanupCtx, *currentAssistant)
			if updateErr != nil {
				return nil, updateErr
			}
		}
		if overflowRecovery {
			// Fall through to the compaction block below, which
			// summarizes the session and requeues this call so the turn
			// resumes.
			shouldSummarize = true
			err = nil
		} else {
			// A channel-originated turn has no caller watching the error, so
			// tell the channel side something went wrong instead of leaving
			// the sender hanging. sendChannelReply detaches from the run
			// context so the notice survives a provider error that tore it down.
			if channelErrorReplyWanted(call.Channel, err) {
				a.sendChannelReply(ctx, call,
					"Something went wrong while handling your message. Please try again.",
					completedToolCalls.Copy())
			}
			return nil, err
		}
	}

	// turnEnd is how this run ended, as known before any compaction. The
	// error path above already stamped a run that will not continue.
	var turnEnd message.TurnEnd
	if overflowRecovery {
		turnEnd = message.TurnEndContextOverflow
	} else if currentAssistant != nil {
		ending := lastStepEnding(currentAssistant, stepResults, shouldSummarize)
		ending.stepsSpent = call.limits.stepsSpent()
		ending.deadlinePassed = call.limits.deadlinePassed()
		turnEnd = turnEndForStep(ending)
	}
	recordTurnEnd := func() {
		if currentAssistant == nil || turnEnd == "" {
			return
		}
		currentAssistant.SetTurnEnd(turnEnd)
		// A compaction can outlast cleanupCtx, so the write gets its own
		// short deadline, detached from the run like the other final writes.
		writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelWrite()
		if updateErr := a.messages.Update(writeCtx, *currentAssistant); updateErr != nil {
			slog.Warn("Failed to record how the turn ended", "session_id", call.SessionID, "error", updateErr)
		}
	}
	// The turn-end gate decides whether the prompt gets one more run
	// before the turn ends. It runs with the run context, so a cancel
	// interrupts a slow check, and before the reply, so a continued turn
	// is answered once.
	var gateNext *SessionAgentCall
	gateRan := false
	if !overflowRecovery && !shouldSummarize && currentAssistant != nil &&
		!call.NonInteractive && !call.Ephemeral {
		gateRan = true
		gateNext = a.runTurnGate(genCtx, call, currentAssistant, turnEnd, a.userPromptQueued(call.SessionID),
			gateTodoState{item: todoStaleItem, unanswered: todoReminderUnanswered})
	}
	// Compact when the mid-turn overflow guard tripped, when a provider
	// context overflow is being recovered, or when the finished turn left
	// the session at or above the configured threshold. The guard and
	// recovery paths requeue the prompt: there the turn was cut short or
	// rejected mid-work and has to resume, while the threshold path
	// compacted a turn that already completed, and re-running the prompt
	// would repeat finished work.
	//
	// A turn the gate continues is not compacted here: the continuation is
	// about to work on the same question, and a summary would hand the
	// model a summary in place of the work it is asked to check. Its own
	// ending runs the threshold check.
	continued := false
	autoCompacting := a.shouldAutoCompact(largeModel, currentSession)
	if gateNext == nil && !shouldSummarize && !autoCompacting {
		// The turn stopped short of the limit but inside the margin the
		// user configured, so the summary is written now, while the next
		// prompt runs, instead of making it wait at the limit.
		a.startBackgroundCompaction(genCtx, call, largeModel, currentSession)
	}
	if gateNext == nil && (shouldSummarize || autoCompacting) {
		a.activeRequests.Del(call.SessionID)
		summarizeCtx := genCtx
		if overflowRecovery {
			// The run context's cancellation state after a failed
			// stream is unreliable, so derive a fresh context the
			// recovery summary can complete on; the timeout bounds how
			// long a stalled provider can pin the run.
			var cancelSummarize context.CancelFunc
			summarizeCtx, cancelSummarize = context.WithTimeout(context.WithoutCancel(ctx), overflowRecoveryTimeout)
			defer cancelSummarize()
		}
		var summarizeErr error
		if shouldSummarize || !a.adoptBackgroundCompaction(summarizeCtx, call.SessionID) {
			// The limit needs space now: any summary written ahead of it
			// is superseded by the blocking compaction.
			a.cancelBackgroundCompaction(call.SessionID)
			threshold, _ := a.compactionConfig()
			trigger := turnCompactionTrigger(overflowRecovery, shouldSummarize, effectiveContextWindow(largeModel), threshold, a.autoCompactMaxTokens())
			_, summarizeErr = a.summarize(summarizeCtx, compactionRequest{
				sessionID:     call.SessionID,
				largeModel:    largeModel,
				opts:          call.ProviderOptions,
				onAuthRefresh: call.OnAuthRefresh,
				trigger:       trigger,
				active:        true,
			})
		}
		// Recorded before the returns below, which a failed compaction
		// takes, and before a blocked one is cleared.
		turnEnd = turnEndAfterCompaction(turnEnd, summarizeErr, call.limits.deadlinePassed())
		recordTurnEnd()
		compactionBlocked := errors.Is(summarizeErr, ErrCompactionBlocked)
		if compactionBlocked {
			// Uncompacted, the next step would only hit the limit
			// again, so the turn ends here.
			slog.Info("PreCompact hook blocked compaction", "session_id", call.SessionID)
			summarizeErr = nil
		}
		if summarizeErr != nil {
			if overflowRecovery {
				// The recovery failed, so the continuation that would
				// have carried the channel reply never runs; the
				// channel side still needs its failure notice.
				if channelErrorReplyWanted(call.Channel, origOverflowErr) {
					a.sendChannelReply(ctx, call,
						"Something went wrong while handling your message. Please try again.",
						completedToolCalls.Copy())
				}
				// A failed recovery must not mask why the turn failed.
				return nil, origOverflowErr
			}
			return nil, summarizeErr
		}
		// A limit that refuses the continuation ends the work here, so it is
		// what the turn reads as its ending. The local flag keeps the rest of
		// Run's reading of shouldSummarize and overflowRecovery unchanged.
		limitStopped := false
		if call.limits.reached() && (turnEnd == message.TurnEndContextGuard || turnEnd == message.TurnEndContextOverflow) {
			turnEnd = message.TurnEndStepLimit
			if call.limits.deadlinePassed() {
				turnEnd = message.TurnEndTimeLimit
			}
			recordTurnEnd()
			limitStopped = true
		}
		// If the agent wasn't done, no compaction was blocked and no limit
		// refuses the continuation, resume the prompt.
		resumable := shouldSummarize && (overflowRecovery || (currentAssistant != nil && len(currentAssistant.ToolCalls()) > 0))
		if !compactionBlocked && !limitStopped && resumable {
			existing, ok := a.messageQueue.Get(call.SessionID)
			if !ok {
				existing = []SessionAgentCall{}
			}
			call.Prompt = fmt.Sprintf("The previous session was interrupted because it got too long, the initial user request was: `%s`", call.Prompt)
			// The rewritten prompt is a new message, not the report.
			call.Report = nil
			a.messageQueue.Set(call.SessionID, append(existing, call))
			continued = true
		}
		// A blocked compaction ends a run that would have resumed, so the
		// work stopped with no reply to send: a channel peer gets the same
		// failure notice a failed turn sends instead of waiting forever.
		if compactionBlocked && resumable {
			if channelErrorReplyWanted(call.Channel, ErrCompactionBlocked) {
				a.sendChannelReply(ctx, call,
					"Something went wrong while handling your message. Please try again.",
					completedToolCalls.Copy())
			}
		}
	} else {
		recordTurnEnd()
	}
	finished := currentAssistant != nil && !continued && gateNext == nil

	// What the turn still owes the user. A continued turn owes it from its
	// continuation, and a continuation the hand-off drops owes it then.
	finish := gateFinish{
		assistant:   currentAssistant,
		summarized:  shouldSummarize,
		overflow:    overflowRecovery,
		finished:    finished,
		turnEndHook: finished && !gateRan,
		cancel:      cancel,
		staleItem:   todoStaleItem,
		staleN:      todoReminderUnanswered,
		completed:   completedToolCalls.Copy(),
		title:       currentSession.Title,
	}
	if gateNext == nil {
		a.finishWithoutGate(ctx, call, finish)
	}
	// A continuation the hand-off drops leaves a turn that ended here
	// after all, so it owes the stale note and the notification, and it
	// runs the threshold compaction the gate put off.
	dropContinuation := func() {
		finish.finished = true
		finish.released = true
		a.compactDroppedGate(ctx, call, largeModel, currentSession)
		a.finishWithoutGate(ctx, call, finish)
	}
	// A continued turn keeps the session registered until the hand-off
	// reads the queue, so a prompt typed in between is queued rather than
	// started, and drops the continuation. The seam lets a test stand in
	// that window.
	if gateNext != nil && a.beforeHandoff != nil {
		a.beforeHandoff()
	}

	// Hand off to the next queued prompt (if any) under dispatchMu so
	// the transition from this finished run to the queued run is atomic
	// against a concurrent Cancel. activeRequests for this session was
	// just deleted above, so without the lock there is a window in
	// which the session looks idle and a cancel becomes a no-op that
	// fails to stop the queued prompt. Holding the lock lets us observe
	// a pending cancel recorded against the session and drop the queue
	// instead of running it, and (for the recursion) hand a fresh
	// accept reservation to the dequeued call so acceptedRuns stays > 0
	// across the recursive Run's own dispatch handoff — keeping the
	// session observable to Cancel for the entire transition and
	// closing the dequeue -> re-register window.
	mu := a.sessionMu(call.SessionID)
	mu.Lock()
	queuedMessages, _ := a.messageQueue.Get(call.SessionID)
	if mark, ok := a.cancelMark.Get(call.SessionID); ok && mark > 0 && len(queuedMessages) > 0 {
		// A cancel was recorded for this session (e.g. it arrived while
		// this run was active and follow-ups had been queued). Drop the
		// queued prompts it covers (accept sequence at or below the
		// mark, or untracked); keep any queued after the cancel (higher
		// sequence) so they still run.
		var kept, dropped []SessionAgentCall
		for _, q := range queuedMessages {
			if q.acceptSeq == 0 || q.acceptSeq <= mark {
				dropped = append(dropped, q)
				continue
			}
			kept = append(kept, q)
		}
		queuedMessages = kept
		a.messageQueue.Set(call.SessionID, kept)
		a.settleDroppedCalls(dropped)
	}
	if len(queuedMessages) > 0 && call.limits.reached() {
		// The tree's limit refuses every run, so queued work would only
		// be refused one call at a time. Settle it now: reports are
		// recorded and waiters released. A continuation of this call
		// carries its RunID, which this turn's own completion covers.
		for i := range queuedMessages {
			if queuedMessages[i].RunID == call.RunID {
				queuedMessages[i].RunID = ""
			}
		}
		a.settleDroppedCalls(queuedMessages)
		queuedMessages = nil
	}
	// The gate's continuation is dropped by a cancel, a limit or a prompt
	// the user typed while the checks ran. Its prompt then ends as it
	// stands, and the user's prompt runs next. The cancel reaches the run
	// through its context: genCtx is cancelled here for the last time, so
	// anything set before now is a cancel the gate must respect.
	gateDropped := false
	if gateNext != nil {
		mark, marked := a.cancelMark.Get(call.SessionID)
		if (marked && mark > 0) || call.limits.reached() || genCtx.Err() != nil || hasUserPrompt(queuedMessages) {
			gateNext, gateDropped = nil, true
		}
	}
	if gateNext != nil || gateDropped {
		// Give up the session here, under the lock a new prompt must take
		// to start: anything typed before now was queued, and the
		// decision above saw it.
		a.activeRequests.Del(call.SessionID)
		cancel()
	}
	if gateNext == nil && len(queuedMessages) == 0 {
		// No queued work. Clear the cancel mark only when no accepted
		// run remains in flight that it might still cover; otherwise a
		// sibling prompt (sequence at or below the mark) waiting to
		// enter Run would lose its cancellation. When accepted runs are
		// gone, this also clears a stale mark so it can't catch a
		// future run.
		a.messageQueue.Del(call.SessionID)
		a.acceptedMu.Lock()
		inFlight, _ := a.acceptedRuns.Get(call.SessionID)
		a.acceptedMu.Unlock()
		if inFlight == 0 {
			a.cancelMark.Del(call.SessionID)
		}
		mu.Unlock()
		if gateDropped {
			dropContinuation()
		}
		return result, err
	}
	// There is work to run next, so restart the loop. Suppress the outer
	// defer's emit: it would otherwise observe the recursive Run's retErr
	// (named-return clobbering through the return below) against this
	// turn's MessageID/Text and publish a mixed, racing event.
	skipRunComplete = true
	// Decide whether this turn still owes its own terminal RunComplete.
	// Each submitted prompt with a RunID has its own lifecycle, so a turn
	// that is finished and handing off to a *different* queued prompt must
	// publish its own RunComplete here — leaving it to the recursive turn
	// (which carries a different RunID) would hang a caller waiting on
	// this turn's RunID. The exceptions are the summarize-continuation
	// path, which re-queues this same call (same RunID) to resume after a
	// summary, and the gate's continuation, which carries this call's
	// RunID; in both cases the eventual terminal turn for this RunID
	// publishes, so publishing now would double-emit.
	outerOwesRunComplete := call.RunID != ""
	if gateNext != nil {
		outerOwesRunComplete = false
	}
	if outerOwesRunComplete {
		for _, q := range queuedMessages {
			if q.RunID == call.RunID {
				outerOwesRunComplete = false
				break
			}
		}
	}
	var firstQueuedMessage SessionAgentCall
	if gateNext != nil {
		// Queued reports and wake-ups stay behind the continuation and
		// are folded into its first step.
		firstQueuedMessage = *gateNext
	} else {
		firstQueuedMessage = queuedMessages[0]
		a.messageQueue.Set(call.SessionID, queuedMessages[1:])
	}
	// Reserve a fresh accept for the dequeued prompt before dropping the
	// lock so acceptedRuns > 0 across the handoff into the recursive
	// Run. This closes the window between this dequeue and the recursive
	// Run registering its activeRequests entry: a cancel arriving in
	// that window now records a pending cancel (acceptedRuns > 0) that
	// the recursive Run's accepted path observes as cancel-on-entry.
	firstQueuedMessage.Accepted = a.BeginAccepted(call.SessionID)
	mu.Unlock()
	if gateDropped {
		dropContinuation()
	}
	if outerOwesRunComplete {
		complete := notify.RunComplete{SessionID: call.SessionID, RunID: call.RunID}
		if currentAssistant != nil {
			complete.MessageID = currentAssistant.ID
			complete.Text = currentAssistant.Content().String()
		}
		if ctx.Err() != nil {
			complete.Cancelled = true
		}
		a.publishRunComplete(ctx, call, complete)
	}
	return a.Run(ctx, firstQueuedMessage)
}

// Summarize compacts the session on the user's request. It returns
// ErrCompactionBlocked when a PreCompact hook blocks it.
func (a *sessionAgent) Summarize(ctx context.Context, sessionID string, opts fantasy.ProviderOptions, onAuthRefresh func(context.Context, *fantasy.ProviderError) error) error {
	// The user asked for a compaction now: a summary written ahead of the
	// limit is superseded by this one.
	a.cancelBackgroundCompaction(sessionID)
	_, err := a.summarize(ctx, compactionRequest{
		sessionID:     sessionID,
		largeModel:    a.largeModel.Get(),
		opts:          opts,
		onAuthRefresh: onAuthRefresh,
		trigger:       compactionTriggerManual,
	})
	return err
}

// compactionRequest is one compaction run: what to summarize, what started
// it, and how its result reaches the session.
type compactionRequest struct {
	sessionID     string
	largeModel    Model
	opts          fantasy.ProviderOptions
	onAuthRefresh func(context.Context, *fantasy.ProviderError) error
	trigger       compactionTrigger
	// active reports that the caller already owns the session's active-run
	// registration (the model-switch path runs inside Run), so this call
	// must not replace or clear it.
	active bool
	pinned bool
	// snapshot, when non-nil, is the history to summarize instead of the
	// stored one. A background summary passes the history as it stood at
	// the turn that crossed the pre-threshold.
	snapshot []message.Message
	// deferred writes the summary without adopting it: the session keeps
	// its context, its step fingerprints, and its queue until the caller
	// splices the result in. A deferred run happens beside a live turn, so
	// it must leave every piece of live state alone.
	deferred bool
}

// summarize runs one compaction request and returns what it wrote. The
// outcome is nil unless the request is deferred, because a compaction that
// adopts its own summary needs nothing back.
func (a *sessionAgent) summarize(ctx context.Context, req compactionRequest) (*compactionOutcome, error) {
	sessionID, largeModel, opts, onAuthRefresh := req.sessionID, req.largeModel, req.opts, req.onAuthRefresh
	trigger, active, pinned := req.trigger, req.active, req.pinned
	if !active && a.HasPendingWork(sessionID) {
		return nil, ErrSessionBusy
	}
	systemPromptPrefix := a.systemPromptPrefix.Get()
	policy := cachePolicyForModel(largeModel)

	currentSession, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	// The previous summary's stats carry the skills its state block listed;
	// this compaction must list again whatever nothing has read since.
	previousSummaryID := currentSession.SummaryMessageID
	msgs := req.snapshot
	// The message a deferred summary ends on, which is what the splice
	// checks against the history before adopting it.
	snapshotEnd := ""
	if len(msgs) > 0 {
		snapshotEnd = msgs[len(msgs)-1].ID
	}
	if msgs == nil {
		msgs, err = a.getSessionMessages(ctx, currentSession)
		if err != nil {
			return nil, err
		}
	}
	if len(msgs) == 0 {
		// Nothing to summarize.
		return nil, nil
	}
	// The compaction request must send the bytes the turn sent, or it
	// misses the cache it exists to preserve. Slim from the trailing
	// message's own time, the way the turn that filled the cache measured
	// its idle gap from the prompt it had just stored: measuring from "now"
	// would stub the whole history of a session that has simply been quiet
	// for longer than the idle window. A preflight compaction still measures
	// from "now", because its gap is the one the coming prompt will close.
	anchor := a.now().Unix()
	if !trigger.measuresFromNow() && len(msgs) > 0 && msgs[len(msgs)-1].CreatedAt > 0 {
		anchor = msgs[len(msgs)-1].CreatedAt
	}
	a.slimHistory(largeModel, msgs, anchor)
	tokensBefore := currentSession.PromptTokens + currentSession.CompletionTokens

	// The compaction request re-enters through the same system prompt,
	// tools, and message stream the live turn ended on, so the provider's
	// prefix cache still covers the whole conversation and only the
	// instruction and the summary are billed as new tokens. Rebuilding
	// the prompt from a different system prompt or tool list — as this
	// path once did — discards the cache for the entire context.
	systemPrompt := a.systemPromptForSession(sessionID)
	// The tools block is serialized ahead of the messages, so rebuilding it
	// here from a registry that changed since the turn's last step would
	// rewrite the cached prefix for the whole conversation. Re-send the list
	// that step sent. Only a compaction with no live step behind it, such as
	// a manual /compact in a fresh process, has to build one.
	serverToolSearch := a.anthropicToolSearchForModel(largeModel)
	compactionTools, ok := a.stepTools.Get(sessionID)
	if ok {
		// The list may name a tool the registry has dropped since. That is
		// intended: the guard refuses to run any tool, and re-sending the
		// list the step sent is what keeps the prefix cacheable.
		compactionTools = slices.Clone(compactionTools)
	} else {
		compactionTools = a.filterDisabledMCPTools(ctx, filterToolsForChannel(a.tools.Copy(), currentSession.Channel, mcp.GetStates()))
		if serverToolSearch {
			compactionTools = anthropicToolSearchPalette(compactionTools)
		} else {
			// Lazy mode filters here too: a compaction that has to rebuild
			// the tools block must send the list the next step would send,
			// or this one request carries every deferred schema and
			// rewrites the cached prefix for nothing.
			compactionTools = a.lazyToolsForSession(sessionID, compactionTools)
		}
		compactionTools = stableToolOrder(compactionTools, a.adoptedToolOrder(sessionID))
	}
	// Both wrappers apply: the guard refuses to execute, and the bound
	// keeps any text a tool did produce within the same budget the session
	// log uses. Composing them means neither layer can be lost by editing
	// the other, and both delegate Info and the provider options, so the
	// tool block still serializes exactly as the live turn's did.
	for i, tool := range compactionTools {
		compactionTools[i] = boundedOutputTool{
			inner: compactionGuardTool{inner: tool},
			bound: a.truncateToolOutput,
		}
	}
	a.applyToolCacheBreakpoint(compactionTools, policy)

	// Everything before the cut is summarized; the matching suffix is
	// kept verbatim so recent reads, command outputs, and edits survive
	// into the next context. The budget is clamped so a small session is
	// never kept whole, which would leave the summary with nothing to
	// summarize.
	cutIndex := len(msgs)
	cutID := ""
	if tailBudget := a.compactionTailBudget(largeModel, msgs); tailBudget > 0 {
		cutIndex = selectCompactionTail(msgs, tailBudget)
		if cutIndex < len(msgs) {
			cutID = msgs[cutIndex].ID
		}
	}
	var tailTokens int64
	for _, msg := range msgs[cutIndex:] {
		tailTokens += messageTokens(msg)
	}

	// The state block repeats what the summary can only describe. Build it
	// from the messages as they stand before the cut, before pruning
	// rewrites their contents and before the request runs.
	carriedSkills := a.compactionStateCarriedSkills(ctx, previousSummaryID)
	stateText, listedSkills := a.compactionStateText(
		ctx, sessionID, currentSession.Todos,
		compactionStateSkills(msgs, cutIndex, carriedSkills),
	)

	// Pruning rewrites tool results the provider has already cached, so the
	// compaction request stops matching the conversation from the first
	// pruned result on and is billed in full, and the summarizer sees less.
	// Keep the history byte-identical unless the request cannot fit
	// otherwise, whatever trigger asked for it: the mid-turn guard fires
	// while the request still fits, and rewriting there would throw away a
	// warm cache for nothing. An overflow recovery is the exception, because
	// the provider has already rejected that request as too large: the size
	// below is a heuristic built from the last request's count and an
	// estimate, and neither can be trusted against a rejection. Decide from
	// the larger of the reported context and an estimate of what this
	// request will actually carry. The reported count is exact for the
	// request it came from but predates anything stored afterwards, such as
	// a large tool result, and preflight compaction decided to run from the
	// assembled prompt, not from that count. Zero reported usage still means
	// the size is unknown, and an unknown size never fits.
	contextTokens := tokensBefore
	if contextTokens > 0 {
		if estimate := a.fusionPromptEstimate(largeModel, msgs, systemPrompt, "", compactionTools, nil); estimate > contextTokens {
			contextTokens = estimate
		}
	}
	if trigger == compactionTriggerOverflow || !compactionFits(effectiveContextWindow(largeModel), contextTokens, compactionMaxOutputTokens(largeModel)) {
		if pruned, saved := pruneToolResultsForCompaction(msgs[:cutIndex], a.compactionPruneChars()); pruned > 0 {
			slog.Debug(
				"Pruned tool results before compaction",
				"session_id", sessionID,
				"results", pruned,
				"saved_chars", saved,
			)
		}
	}

	if pre := a.hooks.Run(ctx, hooks.Event{Name: hooks.EventPreCompact, SessionID: sessionID, Trigger: trigger.hookTrigger()}); pre.Blocked() {
		return nil, fmt.Errorf("%w: %s", ErrCompactionBlocked, pre.Reason)
	}

	aiMsgs, _ := a.preparePromptFor(msgs, largeModel.ModelCfg.Provider, largeModel.ModelCfg.Model, largeModel.CatwalkCfg.Capabilities.Vision, largeModel.CatwalkCfg.Capabilities.Audio)

	genCtx, cancel := context.WithCancel(ctx)
	if !active {
		ac := &activeCancel{cancel: cancel}
		a.activeRequests.Set(sessionID, ac)
		defer a.activeRequests.CompareAndDelete(sessionID, ac)
	}
	defer cancel()
	defer func() {
		if flushErr := a.messages.FlushAll(ctx); flushErr != nil {
			slog.Error("Failed to flush pending message updates after summarize", "error", flushErr)
		}
	}()

	summaryModel := largeModel.Model
	if fusion, ok := largeModel.Model.(*fusionModel); ok && fusion.plan != nil && len(fusion.plan.Steps) > 0 {
		var err error
		summaryModel, err = fusion.stepModel(ctx, fusion.plan.Steps[0])
		if err != nil {
			return nil, err
		}
	}
	agent := fantasy.NewAgent(
		summaryModel,
		fantasy.WithSystemPrompt(systemPrompt),
		fantasy.WithTools(compactionTools...),
		fantasy.WithUserAgent(userAgent),
		fantasy.WithMaxOutputTokens(compactionMaxOutputTokens(largeModel)),
		fantasy.WithStopConditions(fantasy.StepCountIs(compactionMaxSteps)),
	)
	summaryMessage, err := a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:             message.Assistant,
		Model:            largeModel.ModelCfg.Model,
		ModelName:        a.modelDisplayName(largeModel.ModelCfg),
		Provider:         largeModel.ModelCfg.Provider,
		IsSummaryMessage: true,
	})
	if err != nil {
		return nil, err
	}

	var (
		previousMessages int
		sharedMessages   int
		toolsChanged     bool
		promptMessages   int
	)
	instruction := buildCompactionPrompt(promptOrBuiltin(a.prompts, "summary"), currentSession, len(msgs)-cutIndex, len(msgs))

	resp, err := agent.Stream(genCtx, fantasy.AgentStreamCall{
		Prompt:          instruction,
		Messages:        aiMsgs,
		Headers:         sessionHeaders(sessionID),
		ProviderOptions: opts,
		OnAuthRefresh:   onAuthRefresh,
		ModelProvider: func() fantasy.LanguageModel {
			if _, ok := largeModel.Model.(*fusionModel); ok || pinned {
				return summaryModel
			}
			return a.currentBuildOf(largeModel)
		},
		PrepareStep: func(callContext context.Context, options fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = options.Messages
			for i := range prepared.Messages {
				prepared.Messages[i].ProviderOptions = nil
			}
			a.normalizePromptMessages(prepared.Messages)
			prepared.Tools = compactionTools
			// Every live step rewrites empty tool media and applies the
			// request image budget before sending. Skipping either here
			// would change the bytes at the first rewritten part and
			// re-read the conversation behind it.
			prepared.Messages = dropEmptyToolMedia(prepared.Messages)
			prepared.Messages = a.workaroundProviderMediaLimitations(prepared.Messages, largeModel)
			prepared.Messages = applyImageBudget(prepared.Messages, a.maxRequestImages())
			// The last-message marker lands on the message that closed
			// the live turn, so a provider with explicit caching serves
			// the conversation from cache instead of rewriting it.
			a.applyCacheBreakpoints(prepared.Messages, policy)
			if serverToolSearch {
				callContext = withAnthropicToolSearch(callContext)
			}
			if systemPromptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(systemPromptPrefix)}, prepared.Messages...)
			}
			// The compaction prompt is what the provider cached last, so the
			// next turn's shared-prefix count has to be measured against it,
			// not against the turn that came before the compaction. A
			// deferred run happens beside a live turn and must leave the
			// fingerprints that turn is measured against alone.
			if !req.deferred {
				previousMessages, sharedMessages = a.recordPromptFingerprint(sessionID, prepared.Messages)
				toolsChanged = a.recordToolsFingerprint(sessionID, prepared.Tools)
			}
			promptMessages = len(prepared.Messages)
			return callContext, prepared, nil
		},
		OnReasoningDelta: func(id string, text string) error {
			summaryMessage.AppendReasoningContent(text)
			return a.messages.Update(genCtx, summaryMessage)
		},
		OnReasoningEnd: func(id string, reasoning fantasy.ReasoningContent) error {
			// Handle anthropic signature.
			if anthropicData, ok := reasoning.ProviderMetadata["anthropic"]; ok {
				if signature, ok := anthropicData.(*anthropic.ReasoningOptionMetadata); ok && signature.Signature != "" {
					summaryMessage.AppendReasoningSignature(signature.Signature)
				}
			}
			summaryMessage.FinishThinking()
			return a.messages.Update(genCtx, summaryMessage)
		},
		OnTextDelta: func(id, text string) error {
			summaryMessage.AppendContent(text)
			return a.messages.Update(genCtx, summaryMessage)
		},
	})
	if err != nil {
		isCancelErr := errors.Is(err, context.Canceled)
		if isCancelErr {
			// User cancelled summarize we need to remove the summary message.
			deleteErr := a.messages.Delete(ctx, summaryMessage.ID)
			return nil, deleteErr
		}
		// Mark the summary message as finished with an error so the UI
		// stops spinning.
		summaryMessage.AddFinish(message.FinishReasonError, "Summarization Error", err.Error())
		summaryMessage.SetBuild(version.BuildStamp())
		if updateErr := a.messages.Update(ctx, summaryMessage); updateErr != nil {
			return nil, updateErr
		}
		return nil, err
	}

	if strings.TrimSpace(summaryMessage.Content().Text) == "" {
		// The model never produced a summary — most likely it burned
		// every step retrying a (refused) tool call. Leave the session
		// pointer untouched so the conversation survives intact.
		emptyErr := errors.New("compaction produced an empty summary")
		summaryMessage.AddFinish(message.FinishReasonError, "Compaction Error", emptyErr.Error())
		summaryMessage.SetBuild(version.BuildStamp())
		if updateErr := a.messages.Update(genCtx, summaryMessage); updateErr != nil {
			return nil, updateErr
		}
		return nil, emptyErr
	}

	// The measured outcome rides on the summary message itself so it
	// persists with the session and reaches the UI wherever the message
	// does. It is display metadata: replay paths never send it to the
	// provider.
	usage := resp.Response.Usage
	summaryTokens := summaryCompletionTokens(usage, summaryMessage)
	keptMessages := len(msgs) - cutIndex
	savedTokens := max(0, tokensBefore-summaryTokens-tailTokens)
	summaryMessage.Parts = append(summaryMessage.Parts, message.CompactionStats{
		TokensBefore:  tokensBefore,
		SummaryTokens: summaryTokens,
		TailTokens:    tailTokens,
		SavedTokens:   savedTokens,
		KeptMessages:  keptMessages,
		Trigger:       string(trigger),
		ReloadSkills:  listedSkills,
	})

	for _, step := range resp.Steps {
		summaryMessage.PrismModelID, summaryMessage.PrismModelName = extractPrismModel(step.ProviderMetadata)
		summaryMessage.PrismHypercreditSavings, summaryMessage.PrismDollarSavings = extractPrismSavings(step.ProviderMetadata)
	}

	var overrideCost *float64
	for _, step := range resp.Steps {
		summaryMessage.PrismModelID, summaryMessage.PrismModelName = extractPrismModel(step.ProviderMetadata)
		summaryMessage.PrismHypercreditSavings, summaryMessage.PrismDollarSavings = extractPrismSavings(step.ProviderMetadata)
		stepCost := a.openrouterCost(step.ProviderMetadata)
		if stepCost != nil {
			newCost := *stepCost
			if overrideCost != nil {
				newCost += *overrideCost
			}
			overrideCost = &newCost
		}
	}
	if hyperCost := a.hyperStepsCost(largeModel, resp.Steps); hyperCost != nil {
		overrideCost = hyperCost
	}

	// A compaction summary that stopped short is the record the session
	// goes on reading from, so record and log why it is incomplete
	// rather than filing it under a clean end_turn. Unknown finishes
	// (providers that never report one) keep the historical end_turn.
	finishReason, finishMessage, finishDetails := turnFinish(largeModel, sessionID, resp.Response.FinishReason)
	if finishReason == message.FinishReasonUnknown {
		finishReason = message.FinishReasonEndTurn
	}
	summaryMessage.AddFinish(finishReason, finishMessage, finishDetails)
	summaryMessage.SetBuild(version.BuildStamp())
	// A compaction is a model request like any other, and the most
	// expensive one of a session: record what it consumed with it.
	summaryMessage.SetRequestUsage(message.RequestUsage{
		InputTokens:      resp.TotalUsage.InputTokens,
		CacheReadTokens:  resp.TotalUsage.CacheReadTokens,
		CacheWriteTokens: resp.TotalUsage.CacheCreationTokens,
		OutputTokens:     resp.TotalUsage.OutputTokens,
		PromptMessages:   promptMessages,
		PreviousMessages: previousMessages,
		SharedMessages:   sharedMessages,
		ToolsChanged:     toolsChanged,
		Estimated:        usageIsZero(resp.TotalUsage),
	})
	err = a.messages.Update(genCtx, summaryMessage)
	if err != nil {
		return nil, err
	}

	// A deferred run leaves the session alone: the caller adopts the
	// summary when the limit is reached, and a summary that is never
	// adopted must not have reset the context of a live session. Its cost
	// is still recorded, with an incrementing write that cannot lose a
	// concurrent step or rollup, so the tokens it spent stay visible.
	if req.deferred {
		step := a.stepUsageFor(largeModel, currentSession, resp.TotalUsage, overrideCost, false)
		if usageErr := a.sessions.AddSessionUsage(genCtx, sessionID, step.CacheReadTokens, step.ContextTokens, step.Cost); usageErr != nil {
			slog.Warn("Failed to record the background compaction usage", "session_id", sessionID, "error", usageErr)
		}
		return &compactionOutcome{
			summaryID:     summaryMessage.ID,
			cutID:         cutID,
			snapshotEnd:   snapshotEnd,
			stateText:     stateText,
			summaryTokens: summaryTokens,
			tokensBefore:  tokensBefore,
			tailTokens:    tailTokens,
			savedTokens:   savedTokens,
			keptMessages:  keptMessages,
			estimated:     usageIsZero(resp.TotalUsage),
		}, nil
	}

	a.updateSessionUsage(largeModel, &currentSession, resp.TotalUsage, overrideCost, false)

	currentSession.SummaryMessageID = summaryMessage.ID
	currentSession.SummaryCutMessageID = cutID
	currentSession.CompletionTokens = summaryTokens
	currentSession.PromptTokens = 0
	currentSession.EstimatedUsage = usageIsZero(resp.TotalUsage)
	_, err = a.sessions.Save(genCtx, currentSession)
	if err != nil {
		return nil, err
	}
	// The state block lands after the summary and the preserved tail, which
	// is where it is also true: it describes the session at compaction time.
	if stateText != "" {
		if _, err := a.createCompactionStateMessage(genCtx, sessionID, stateText); err != nil {
			// The compaction itself succeeded, so a missing block costs
			// the next turn its reminders and nothing more.
			slog.Warn("Failed to store the compaction state message", "session_id", sessionID, "error", err)
		}
	}
	a.hooks.Run(ctx, hooks.Event{Name: hooks.EventPostCompact, SessionID: sessionID, Trigger: trigger.hookTrigger()})

	slog.Info(
		"Context compacted",
		"session_id", sessionID,
		"tokens_before", tokensBefore,
		"summary_tokens", summaryTokens,
		"tail_tokens", tailTokens,
		"saved_tokens", savedTokens,
		"kept_messages", keptMessages,
	)

	// Release the active request before processing queued messages so that
	// Run() does not see the session as busy.
	a.activeRequests.Del(sessionID)
	cancel()

	// Process any messages that were queued while summarizing.
	queuedMessages, ok := a.messageQueue.Get(sessionID)
	if !ok || len(queuedMessages) == 0 {
		return nil, nil
	}
	firstQueuedMessage := queuedMessages[0]
	a.messageQueue.Set(sessionID, queuedMessages[1:])
	_, qErr := a.Run(ctx, firstQueuedMessage)
	return nil, qErr
}

// compactionConfig returns the auto-compaction threshold (as a fraction of
// the context window) and the verbatim tail budget in tokens. Both fall
// back to their defaults when no config store is available (tests,
// sub-agents) or the user left them unset.
func (a *sessionAgent) compactionConfig() (threshold float64, tailTokens int64) {
	threshold = defaultAutoCompactThreshold
	tailTokens = defaultCompactTailTokens
	if a.cfg == nil {
		return threshold, tailTokens
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return threshold, tailTokens
	}
	threshold = cfg.Options.GetAutoCompactThreshold()
	tailTokens = cfg.Options.GetCompactTailTokens()
	return threshold, tailTokens
}

func effectiveContextWindow(model Model) int64 {
	if fusion, ok := model.Model.(*fusionModel); ok {
		if budget, known := fusion.PromptBudget(); known {
			return budget
		}
	}
	return int64(model.CatwalkCfg.ContextWindow)
}

func (a *sessionAgent) shouldCompactFusionPreflight(model Model, sess session.Session, msgs []message.Message, systemPrompt, userPrompt string, agentTools []fantasy.AgentTool, attachments []message.Attachment) bool {
	fusion, ok := model.Model.(*fusionModel)
	if !ok || a.disableAutoSummarize {
		return false
	}
	window, known := fusion.PromptBudget()
	if !known {
		return false
	}
	threshold, _ := a.compactionConfig()
	if threshold <= 0 {
		return false
	}
	estimate := a.fusionPromptEstimate(model, msgs, systemPrompt, userPrompt, agentTools, attachments)
	return float64(estimate) >= threshold*float64(window)
}

type fusionCompactionModel struct {
	model  Model
	budget int64
}

func (a *sessionAgent) maybeCompactForFusionRoute(
	ctx context.Context,
	sessionID string,
	nextBudget int64,
	opts fantasy.ProviderOptions,
	onAuthRefresh func(context.Context, *fantasy.ProviderError) error,
) (bool, error) {
	if a.fusionCompactionModels == nil {
		return false, nil
	}
	previous, ok := a.fusionCompactionModels.Get(sessionID)
	if !ok || previous.model.Model == nil || previous.budget <= nextBudget {
		return false, nil
	}
	if _, err := a.summarize(ctx, compactionRequest{
		sessionID:     sessionID,
		largeModel:    previous.model,
		opts:          opts,
		onAuthRefresh: onAuthRefresh,
		trigger:       compactionTriggerFusionRoute,
		active:        true,
		pinned:        true,
	}); err != nil {
		return false, fmt.Errorf("compact before switching to a smaller routed model: %w", err)
	}
	return true, nil
}

func (a *sessionAgent) fusionPromptEstimate(model Model, msgs []message.Message, systemPrompt, userPrompt string, agentTools []fantasy.AgentTool, attachments []message.Attachment) int64 {
	estimate := approxTokenCount(systemPrompt) + approxTokenCount(userPrompt)
	for _, msg := range msgs {
		estimate += messageTokens(msg)
	}
	history, files := a.preparePromptFor(msgs, model.ModelCfg.Provider, model.ModelCfg.Model, model.CatwalkCfg.Capabilities.Vision, model.CatwalkCfg.Capabilities.Audio, attachments...)
	assembled := estimateMessageTokens(history) + approxTokenCount(systemPrompt) + approxTokenCount(userPrompt)
	for _, file := range files {
		assembled += estimateMessagePartTokens(file)
	}
	for _, tool := range agentTools {
		data, err := json.Marshal(tool.Info())
		if err == nil {
			assembled += approxTokenCount(string(data))
		}
	}
	return max(estimate, assembled)
}

// compactionRequestReserve covers the compaction instruction and the error
// of counting tokens, on top of the summary's own output budget.
const compactionRequestReserve = 8_192

// compactionFits reports whether a compaction request can carry the whole
// history unpruned: the context the last request reported, the summary the
// model may write, and the instruction. Unknown sizes do not fit.
func compactionFits(window, contextTokens, maxOutputTokens int64) bool {
	if window <= 0 || contextTokens <= 0 {
		return false
	}
	return contextTokens+maxOutputTokens+compactionRequestReserve <= window
}

// slimHistory applies slimColdHistory when the option is on. The Claude
// Agent provider keeps the conversation in its own session and treats a
// changed history as a rewrite, replaying all of it as text, so its
// history is left alone.
func (a *sessionAgent) slimHistory(model Model, msgs []message.Message, next int64) {
	idle, dropReasoning := a.historySlimConfig()
	if idle <= 0 || (model.Model != nil && model.Model.Provider() == claudeagent.Name) {
		return
	}
	// Kimi, MiMo and DeepSeek require the reasoning content they produced
	// back in the history, so slimming must not drop it.
	if vendorReasoningNeedsHistory(model.CatwalkCfg.ID) {
		dropReasoning = false
	}
	if cut := slimColdHistory(msgs, next, idle, dropReasoning); cut > 0 {
		slog.Debug("Slimmed history before the last idle gap", "messages", cut)
	}
}

// historySlimConfig returns the idle duration and the reasoning switch, with
// the defaults when there is no config store (tests, sub-agents). A config
// without options is possible (see config.NewTestStore), and only the
// duration getter tolerates a nil receiver, so both are guarded here.
func (a *sessionAgent) historySlimConfig() (time.Duration, bool) {
	if a.cfg == nil {
		return config.DefaultHistorySlimIdleMinutes * time.Minute, false
	}
	cfg := a.cfg.Config()
	if cfg == nil || cfg.Options == nil {
		return config.DefaultHistorySlimIdleMinutes * time.Minute, false
	}
	return cfg.Options.GetHistorySlimIdle(), cfg.Options.HistorySlimReasoning
}

// compactionPruneChars returns the per-tool-result character budget applied
// before a compaction request, or zero when pruning is disabled.
func (a *sessionAgent) compactionPruneChars() int {
	if a.cfg == nil {
		return config.DefaultCompactionPruneChars
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return config.DefaultCompactionPruneChars
	}
	return cfg.Options.GetCompactionPruneChars()
}

// maybeCompactForModelSwitch consumes a pending model switch. When the new
// model has a smaller context window and the session is already above its
// compaction threshold, it compacts with the previous model first. A failed
// compaction reverts the switch so the session keeps a model that can hold
// its history.
func (a *sessionAgent) maybeCompactForModelSwitch(
	ctx context.Context,
	sessionID string,
	sess session.Session,
	msgs []message.Message,
	current Model,
	opts fantasy.ProviderOptions,
	onAuthRefresh func(context.Context, *fantasy.ProviderError) error,
) (bool, error) {
	a.modelSwitchMu.Lock()
	if !a.modelSwitchPending {
		a.modelSwitchMu.Unlock()
		return false, nil
	}
	previousLarge := a.previousLarge
	previousSmall := a.previousSmall
	a.modelSwitchPending = false
	a.modelSwitchMu.Unlock()

	if a.disableAutoSummarize {
		if _, ok := current.Model.(*fusionModel); ok {
			return false, nil
		}
	}
	if previousLarge.Model == nil || current.Model == nil {
		return false, nil
	}
	threshold, _ := a.compactionConfig()
	tokens := sess.PromptTokens + sess.CompletionTokens
	if tokens == 0 {
		for _, msg := range msgs {
			tokens += messageTokens(msg)
		}
	}
	if !needsModelSwitchCompaction(previousLarge, current, tokens, threshold) {
		return false, nil
	}
	if _, err := a.summarize(ctx, compactionRequest{
		sessionID:     sessionID,
		largeModel:    previousLarge,
		opts:          opts,
		onAuthRefresh: onAuthRefresh,
		trigger:       compactionTriggerModelSwitch,
		active:        true,
	}); err != nil {
		a.largeModel.Set(previousLarge)
		a.smallModel.Set(previousSmall)
		return false, fmt.Errorf("compact before switching to a smaller model: %w", err)
	}
	return true, nil
}

// needsModelSwitchCompaction reports whether switching from previous to
// next requires compacting the existing history first.
func needsModelSwitchCompaction(previous, next Model, tokens int64, threshold float64) bool {
	if threshold <= 0 || tokens <= 0 {
		return false
	}
	previousWindow := effectiveContextWindow(previous)
	nextWindow := effectiveContextWindow(next)
	if previousWindow <= 0 || nextWindow <= 0 || nextWindow >= previousWindow {
		return false
	}
	return float64(tokens) >= threshold*float64(nextWindow)
}

// autoCompactLimit is the context size at which a finished turn compacts:
// the configured fraction of the window, capped by the absolute ceiling.
func autoCompactLimit(window int64, threshold float64, ceiling int64) int64 {
	limit := int64(threshold * float64(window))
	if ceiling > 0 && ceiling < limit {
		limit = ceiling
	}
	return limit
}

// autoCompactMaxTokens returns the absolute auto-compaction ceiling, or zero
// when the user disabled it.
func (a *sessionAgent) autoCompactMaxTokens() int64 {
	if a.cfg == nil {
		return config.DefaultAutoCompactMaxTokens
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return config.DefaultAutoCompactMaxTokens
	}
	return cfg.Options.GetAutoCompactMaxTokens()
}

// shouldAutoCompact reports whether a finished turn left the session at or
// above the configured compaction threshold. Unlike the mid-turn guard,
// this runs between turns, so crossing it never interrupts work in flight.
func (a *sessionAgent) shouldAutoCompact(model Model, sess session.Session) bool {
	if a.disableAutoSummarize {
		return false
	}
	cw := effectiveContextWindow(model)
	if cw == 0 {
		return false
	}
	threshold, _ := a.compactionConfig()
	if threshold <= 0 {
		return false
	}
	return sess.PromptTokens+sess.CompletionTokens >= autoCompactLimit(cw, threshold, a.autoCompactMaxTokens())
}

// compactionTailBudget resolves the verbatim tail budget for a compaction:
// the configured or default value, clamped to what the model's window can
// afford and to half of the context being compacted, so a small session is
// never kept whole (which would leave a summary with nothing to summarize).
func (a *sessionAgent) compactionTailBudget(model Model, msgs []message.Message) int64 {
	_, budget := a.compactionConfig()
	if budget <= 0 {
		return 0
	}
	if cw := effectiveContextWindow(model); cw > 0 && cw/4 < budget {
		budget = cw / 4
	}
	var total int64
	for _, msg := range msgs {
		total += messageTokens(msg)
	}
	if total > 0 && total/2 < budget {
		budget = total / 2
	}
	return budget
}

// compactionMaxOutputTokens bounds the summary a compaction may generate,
// clamped down for small windows and models with a lower output limit.
func compactionMaxOutputTokens(model Model) int64 {
	maxTokens := int64(defaultCompactionMaxOutputTokens)
	if cw := effectiveContextWindow(model); cw > 0 && cw/4 < maxTokens {
		maxTokens = cw / 4
	}
	if model.CatwalkCfg.DefaultMaxTokens > 0 && model.CatwalkCfg.DefaultMaxTokens < maxTokens {
		maxTokens = model.CatwalkCfg.DefaultMaxTokens
	}
	return maxTokens
}

// selectCompactionTail returns the index of the first message a compaction
// keeps verbatim: the largest suffix of msgs whose estimated token size
// fits budget, trimmed forward to a boundary that stays valid for the
// provider and complete for the model.
//
// A cut at index i keeps msgs[i:]. It is valid only when every tool call
// before i already has its result before i (a request may not carry a tool
// call without its result) and msgs[i] is neither a tool result, which
// would be orphaned from its call, nor a previous summary message, whose
// content the new summary absorbs. Returns len(msgs) when nothing fits.
func selectCompactionTail(msgs []message.Message, budget int64) int {
	if budget <= 0 || len(msgs) == 0 {
		return len(msgs)
	}
	// pending[i] counts tool calls made before i that have no result
	// before i, i.e. results the cut at i would strip away.
	pending := make([]int, len(msgs))
	count := 0
	for i := range msgs {
		pending[i] = count
		switch msgs[i].Role {
		case message.Assistant:
			count += clientToolCallCount(msgs[i])
		case message.Tool:
			count -= len(msgs[i].ToolResults())
			if count < 0 {
				count = 0
			}
		}
	}

	cut := len(msgs)
	var tokens int64
	for i := len(msgs) - 1; i >= 0; i-- {
		tokens += messageTokens(msgs[i])
		if tokens > budget {
			break
		}
		cut = i
	}
	for cut < len(msgs) {
		msg := msgs[cut]
		if pending[cut] == 0 && msg.Role != message.Tool && !msg.IsSummaryMessage {
			break
		}
		cut++
	}
	return cut
}

// messageTokens estimates the token footprint of a persisted message, the
// same way session usage estimates are produced: every text, reasoning,
// tool input, and tool output part counts.
func messageTokens(msg message.Message) int64 {
	total := approxTokenCount(msg.Content().Text) + approxTokenCount(msg.ReasoningContent().String())
	for _, tc := range msg.ToolCalls() {
		total += approxTokenCount(tc.Name) + approxTokenCount(tc.Input)
	}
	for _, tr := range msg.ToolResults() {
		total += approxTokenCount(tr.Content)
	}
	return total
}

// compactionGuardTool keeps a tool's definition byte-identical to the live
// turn's so the compaction request still matches every provider's prefix
// cache, while refusing to execute it. The compaction instruction forbids
// tool calls; if a model calls one anyway, the refusal arrives as an error
// tool result and the model gets another step to write the summary. The
// inner tool receives provider-option mutations untouched, so the cache
// marker placed by applyToolCacheBreakpoint still serializes identically.
type compactionGuardTool struct {
	inner fantasy.AgentTool
}

func (t compactionGuardTool) Info() fantasy.ToolInfo { return t.inner.Info() }

func (t compactionGuardTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return fantasy.ToolResponse{
		Content: "Tool execution is disabled during context compaction. Respond with the summary only.",
		IsError: true,
	}, nil
}

func (t compactionGuardTool) ProviderOptions() fantasy.ProviderOptions {
	return t.inner.ProviderOptions()
}

func (t compactionGuardTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	t.inner.SetProviderOptions(opts)
}

// systemPromptForSession is the complete system message a turn sends: the
// base prompt, then the session's MCP instructions, then the configured
// suffix.
//
// The suffix goes last on purpose. A teammate's base prompt is the Lead's
// built prompt verbatim, so keeping the suffix behind everything the Lead
// also sends leaves the Lead's whole system message as an exact prefix of
// the teammate's, which is what the provider's prompt cache matches on.
func (a *sessionAgent) systemPromptForSession(sessionID string) string {
	return a.systemPrompt.Get() + a.mcpInstructionsForSession(sessionID) + a.systemPromptSuffix
}

// mcpInstructionsForSession returns the MCP server instruction suffix for a
// session, computed on the session's first turn and reused afterwards.
//
// MCP servers connect asynchronously, so recomputing the suffix per turn would
// let a server that connects mid-session rewrite the system prompt — the first
// thing in every request — and invalidate the provider's prompt cache for the
// whole conversation. A server that appears later still contributes its tools
// to later turns; only its instructions stay out of the system prompt.
func (a *sessionAgent) mcpInstructionsForSession(sessionID string) string {
	if suffix, ok := a.mcpInstructions.Get(sessionID); ok {
		return suffix
	}
	var pairs []namedInstruction
	for name, server := range mcp.GetStates() {
		if server.State != mcp.StateConnected {
			continue
		}
		if s := server.Client.InitializeResult().Instructions; s != "" {
			pairs = append(pairs, namedInstruction{name: name, instructions: s})
		}
	}
	suffix := buildMCPInstructions(pairs)
	a.mcpInstructions.Set(sessionID, suffix)
	return suffix
}

// namedInstruction pairs a connected MCP server's name with the instructions
// it contributed at initialization.
type namedInstruction struct {
	name         string
	instructions string
}

// buildMCPInstructions renders the <mcp-instructions> system-prompt suffix
// from a set of server instructions, ordered by server name.
//
// Sorting is what makes the suffix deterministic across sessions in the same
// process: GetStates is a map, so without it two sessions that see the same
// connected servers could get the blocks in different orders: different
// system-prompt bytes and therefore separate cache entries on providers whose
// prefix cache is content-addressed (Anthropic especially).
func buildMCPInstructions(pairs []namedInstruction) string {
	if len(pairs) == 0 {
		return ""
	}
	slices.SortFunc(pairs, func(a, b namedInstruction) int {
		return strings.Compare(a.name, b.name)
	})
	var instructions strings.Builder
	for _, pair := range pairs {
		instructions.WriteString(pair.instructions)
		instructions.WriteString("\n\n")
	}
	return "\n\n<mcp-instructions>\n" + instructions.String() + "\n</mcp-instructions>"
}

// recordPromptFingerprint hashes the prompt about to be sent, compares it
// with the previous request of the same session, and reports how many
// messages that request carried and how many leading ones this request
// shares with it. The first message that changed is logged.
//
// Providers cache a request prefix only while it stays byte-identical between
// turns, so a cache that stops paying off always means "some message changed".
// Reporting the shared count next to the usage stored on the message is what
// makes a miss attributable after the fact: the provider reports a low
// cache-read count, not what caused it. The cost is one hash per message on
// every request, which is a few milliseconds of JSON on a large prompt and is
// paid to answer the only question a cache miss raises.
//
// Cache breakpoints are excluded: applyCacheBreakpoints moves them to the
// last two messages of every request by design, so hashing them would report
// a divergence on every request. The parts' own provider options stay, since
// signed reasoning is content the provider matches on.
func (a *sessionAgent) recordPromptFingerprint(sessionID string, messages []fantasy.Message) (previousCount, shared int) {
	hashes := make([]string, len(messages))
	for i := range messages {
		hashed := messages[i]
		hashed.ProviderOptions = nil
		data, err := json.Marshal(hashed)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		hashes[i] = hex.EncodeToString(sum[:8])
	}
	previous, hadPrevious := a.promptHashes.Get(sessionID)
	a.promptHashes.Set(sessionID, hashes)
	if !hadPrevious {
		return 0, 0
	}
	common := min(len(hashes), len(previous))
	for i := range common {
		if hashes[i] != previous[i] {
			slog.Debug(
				"Prompt prefix diverged from the previous request",
				"session_id", sessionID,
				"message_index", i,
				"message_role", string(messages[i].Role),
				"previous_messages", len(previous),
				"current_messages", len(hashes),
			)
			return len(previous), i
		}
	}
	return len(previous), common
}

// recordToolsFingerprint hashes the tools about to be sent and compares them
// with the previous request of the same session, logging the first tool that
// changed.
//
// The message fingerprint (recordPromptFingerprint) covers the conversation
// but not the tools block, yet a tool whose serialized definition drifts
// between requests (a dynamic description, an unsorted insertion, an MCP
// server re-listing with different schema bytes) rewrites the tools prefix
// and invalidates the cache for everything after it. The provider reports
// that only as a low cache-read count; this names the tool, and its result is
// stored with the request's usage so the miss can be explained later. The
// cost is one hash per tool on every request.
//
// It reports whether the block changed, which also covers a request that adds
// or drops a tool.
func (a *sessionAgent) recordToolsFingerprint(sessionID string, tools []fantasy.AgentTool) (changed bool) {
	hashes := make([]string, len(tools))
	for i, tool := range tools {
		data, err := json.Marshal(tool.Info())
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		hashes[i] = hex.EncodeToString(sum[:8])
	}
	previous, hadPrevious := a.toolsHashes.Get(sessionID)
	a.toolsHashes.Set(sessionID, hashes)
	if !hadPrevious {
		return false
	}
	common := min(len(hashes), len(previous))
	for i := range common {
		if hashes[i] != previous[i] {
			slog.Debug(
				"Tools block diverged from the previous request",
				"session_id", sessionID,
				"tool_index", i,
				"tool", tools[i].Info().Name,
				"previous_tools", len(previous),
				"current_tools", len(hashes),
			)
			return true
		}
	}
	if len(hashes) != len(previous) {
		slog.Debug(
			"Tools block size changed since the previous request",
			"session_id", sessionID,
			"previous_tools", len(previous),
			"current_tools", len(hashes),
		)
		return true
	}
	return false
}

// applyToolCacheBreakpoint puts the cache breakpoint on the last tool
// definition and clears it from every other tool in the list, so the tool
// block caches independently of the conversation while the request stays
// within the provider's breakpoint limit. A policy without tool breakpoints
// leaves the definitions alone; no other caller ever sets one.
func (a *sessionAgent) applyToolCacheBreakpoint(tools []fantasy.AgentTool, policy cachePolicy) {
	if len(tools) == 0 || !policy.Tools {
		return
	}
	cacheOptions := a.getCacheControlOptions()
	for i, tool := range tools {
		if i == len(tools)-1 {
			tool.SetProviderOptions(cacheOptions)
			continue
		}
		tool.SetProviderOptions(fantasy.ProviderOptions{})
	}
}

// toolsForStep returns the tool list for one step of a turn: the list the
// turn started with, plus anything registered since (an MCP server the model
// activated, a server that finished connecting). Additions are adopted at
// the end of the list — costing one prompt-cache rewrite of just the new
// tools, which is the point of activating a server — while removals are
// ignored, so a flaky server cannot shrink the palette and invalidate the
// cache for no reason. Appending instead of re-sorting alphabetically keeps
// the previously sent tools block an exact prefix of the next step's
// request, and the adoption is recorded per session so later turns and
// compaction replay the same order (see stableToolOrder) instead of
// re-serializing the block from the first adopted tool. The second return
// value reports whether anything was adopted, so the caller knows the tool
// cache breakpoint has to move.
func (a *sessionAgent) toolsForStep(ctx context.Context, sessionID, channel string, turnTools []fantasy.AgentTool, serverToolSearch bool) ([]fantasy.AgentTool, bool) {
	current := a.filterDisabledMCPTools(ctx, filterToolsForChannel(a.tools.Copy(), channel, mcp.GetStates()))
	if serverToolSearch {
		// Nothing is hidden in this mode: the deferred tools ride along and
		// the transport marks them, so a server that connects mid-turn only
		// extends the deferred tail.
		current = anthropicToolSearchPalette(current)
	} else {
		// A tool this session revealed with tool_search during the last
		// step passes the lazy filter now, so the adoption below is what
		// adds it to the palette; a still-unrevealed one stays out of the
		// request.
		current = a.lazyToolsForSession(sessionID, current)
	}
	known := make(map[string]struct{}, len(turnTools))
	for _, tool := range turnTools {
		known[tool.Info().Name] = struct{}{}
	}
	var added []fantasy.AgentTool
	for _, tool := range current {
		if _, ok := known[tool.Info().Name]; !ok {
			added = append(added, boundedOutputTool{inner: tool, bound: a.truncateToolOutput})
		}
	}
	if len(added) == 0 {
		return turnTools, false
	}
	adopted := a.adoptedToolOrder(sessionID)
	for _, tool := range added {
		adopted = append(adopted, tool.Info().Name)
	}
	a.adoptedTools.Set(sessionID, adopted)
	merged := append(slices.Clone(turnTools), added...)
	slog.Debug(
		"Adopted newly registered tools mid-turn; the prompt cache will be rewritten once",
		"channel", channel,
		"added", len(added),
	)
	return merged, true
}

// adoptedToolOrder returns the tool names the session adopted mid-turn, in
// adoption order.
func (a *sessionAgent) adoptedToolOrder(sessionID string) []string {
	names, _ := a.adoptedTools.Get(sessionID)
	return names
}

func (a *sessionAgent) AdoptedToolOrder(sessionID string) []string {
	return a.adoptedToolOrder(sessionID)
}

// SetAdoptedToolOrder seeds a session's adopted tool order. A teammate takes
// the Lead's order because the tools block is serialized ahead of the system
// prompt: a palette ordered differently from the Lead's re-reads the whole
// cached prefix at full input price.
func (a *sessionAgent) SetAdoptedToolOrder(sessionID string, names []string) {
	if len(names) == 0 {
		return
	}
	a.adoptedTools.Set(sessionID, slices.Clone(names))
}

// stableToolOrder reorders a session's tool list so tools it adopted
// mid-turn stay at the end, in adoption order; everything else keeps the
// registry's order. Without this, the next turn would put an adopted tool
// back in its sorted slot, rewriting the tools block from that slot and
// re-reading the conversation behind it. Adopted names that are filtered
// out (connection state, disabled servers) are dropped.
func stableToolOrder(toolList []fantasy.AgentTool, adopted []string) []fantasy.AgentTool {
	if len(adopted) == 0 {
		return toolList
	}
	adoptionIndex := make(map[string]int, len(adopted))
	for i, name := range adopted {
		adoptionIndex[name] = i
	}
	late := make([]fantasy.AgentTool, len(adopted))
	present := make([]bool, len(adopted))
	ordered := make([]fantasy.AgentTool, 0, len(toolList))
	for _, tool := range toolList {
		if i, ok := adoptionIndex[tool.Info().Name]; ok {
			late[i] = tool
			present[i] = true
			continue
		}
		ordered = append(ordered, tool)
	}
	for i, tool := range late {
		if present[i] {
			ordered = append(ordered, tool)
		}
	}
	return ordered
}

// applyCacheBreakpoints marks the prompt-cache boundaries the policy asks
// for. The full layout matches what Anthropic's own clients use: one
// breakpoint on the last system message (covering the system prompt and, in
// providers that serialize them ahead of it, the tool definitions) and one
// on each of the final two messages. The tail breakpoints are what let a
// turn extend the cached prefix instead of rewriting it, so they must land
// on messages that the next turn replays unchanged.
//
// Providers that cache automatically (OpenAI, DeepSeek) ignore these markers
// entirely and get none; Gemini and Qwen get the smaller layouts their
// upstreams actually honour, because a tail marker they do not understand
// writes a fresh cache on every step.
func (a *sessionAgent) applyCacheBreakpoints(messages []fantasy.Message, policy cachePolicy) {
	if len(messages) == 0 || (!policy.System && policy.Tail == 0) {
		return
	}
	lastSystemRoleInx := -1
	systemMarked := false
	for i := range messages {
		if messages[i].Role == fantasy.MessageRoleSystem {
			lastSystemRoleInx = i
			continue
		}
		if policy.System && !systemMarked && lastSystemRoleInx >= 0 {
			messages[lastSystemRoleInx].ProviderOptions = a.getCacheControlOptions()
			systemMarked = true
		}
		if i >= len(messages)-policy.Tail {
			messages[i].ProviderOptions = a.getCacheControlOptions()
		}
	}
	// A prompt made up only of system messages still gets its breakpoint.
	if policy.System && !systemMarked && lastSystemRoleInx >= 0 {
		messages[lastSystemRoleInx].ProviderOptions = a.getCacheControlOptions()
	}
}

func (a *sessionAgent) getCacheControlOptions() fantasy.ProviderOptions {
	if t, _ := strconv.ParseBool(os.Getenv("CRUSH_DISABLE_ANTHROPIC_CACHE")); t {
		return fantasy.ProviderOptions{}
	}
	// fantasy v0.45.2 reads cache control options only from the
	// "anthropic" provider-options key, regardless of which provider
	// instance is calling: bedrock is built on the anthropic client, and
	// vercel and openrouter pass message breakpoints through the same
	// key. The bedrock and vercel entries are kept for forward
	// compatibility in case those providers start reading their own keys.
	return fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
		bedrock.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
		vercel.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
	}
}

// sessionHeaders returns the HTTP headers we use for cache affinity on
// every LLM request for a given session.
//
// We use the session hash is used instead of the raw UUID so the header
// value is deterministic and opaque.
func sessionHeaders(sessionID string) map[string]string {
	hash := session.HashID(sessionID)
	return map[string]string{
		"x-session-id":       hash,
		"x-session-affinity": hash,
	}
}

// shouldCreateEnvSnapshot reports whether the session still needs its one-time
// environment snapshot message (see prompt.SessionEnvSnapshot). Sub-agents
// and agents without a config store (tests, the fetch agent) never get one.
// The hasEnvSnapshot check keeps creation idempotent when a first run is
// retried after persisting the snapshot but failing before the user message.
func (a *sessionAgent) shouldCreateEnvSnapshot(msgs []message.Message) bool {
	return !a.isSubAgent && a.cfg != nil &&
		!hasUserTextMessage(msgs) && !hasEnvSnapshot(msgs)
}

// hasEnvSnapshot reports whether msgs already contains the environment
// snapshot message.
func hasEnvSnapshot(msgs []message.Message) bool {
	for _, msg := range msgs {
		if strings.HasPrefix(msg.Content().Text, prompt.EnvSnapshotPrefix) {
			return true
		}
	}
	return false
}

// createEnvSnapshotMessage persists the environment snapshot as a hidden
// first user message. Hidden keeps it out of the transcript while ToAIMessage
// keeps replaying it to the model verbatim.
func (a *sessionAgent) createEnvSnapshotMessage(ctx context.Context, sessionID string) (message.Message, error) {
	snapshot := prompt.SessionEnvSnapshot(ctx, a.cfg.WorkingDir(), a.now())
	return a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: snapshot, Hidden: true}},
	})
}

func (a *sessionAgent) createUserMessage(ctx context.Context, call SessionAgentCall, modelContext string) (message.Message, error) {
	if call.Report != nil {
		return a.messages.Create(ctx, call.SessionID, message.CreateMessageParams{
			Role:  message.User,
			Parts: []message.ContentPart{*call.Report},
		})
	}
	// Run sends the prompt untrimmed, so the stored text is untrimmed too,
	// or the next turn replays different bytes than this turn sent and
	// misses the provider's prefix cache.
	parts := []message.ContentPart{message.TextContent{Text: call.Prompt, Hidden: call.HiddenUserMessage, Context: modelContext}}
	var attachmentParts []message.ContentPart
	for _, attachment := range call.Attachments {
		attachmentParts = append(attachmentParts, message.BinaryContent{Path: attachment.Ref(), MIMEType: attachment.MimeType, Data: attachment.Content})
	}
	parts = append(parts, attachmentParts...)
	msg, err := a.messages.Create(ctx, call.SessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: parts,
	})
	if err != nil {
		return message.Message{}, fmt.Errorf("failed to create user message: %w", err)
	}
	return msg, nil
}

// preparePrompt replays a session for a caller that does not name a target
// model, leaving every signature alone. Estimates and tests use it; a real
// request calls preparePromptFor so reasoning written by another model
// cannot sink the turn.
func (a *sessionAgent) preparePrompt(msgs []message.Message, supportsImages, supportsAudio bool, attachments ...message.Attachment) ([]fantasy.Message, []fantasy.FilePart) {
	return a.preparePromptFor(msgs, "", "", supportsImages, supportsAudio, attachments...)
}

// preparePromptFor replays a session for a request targeting requestProvider
// and requestModel, dropping the provider-specific reasoning data of
// messages another model wrote.
func (a *sessionAgent) preparePromptFor(msgs []message.Message, requestProvider, requestModel string, supportsImages, supportsAudio bool, attachments ...message.Attachment) ([]fantasy.Message, []fantasy.FilePart) {
	var history []fantasy.Message
	if !a.isSubAgent {
		history = append(history, fantasy.NewUserMessage(
			fmt.Sprintf(
				"<system_reminder>%s</system_reminder>",
				`This is a reminder about the todo list. DO NOT mention this to the user explicitly because they are already aware.
If you are working on tasks that would benefit from a todo list please use the "todos" tool to create one.
If you already have a todo list, update it immediately after each task is implemented: mark the finished task completed and the next task in_progress in the same call. Never leave finished work marked pending or in_progress.
Again do not mention this message to the user.`,
			),
		))
	}
	// Collect all tool call IDs present in assistant messages, then index
	// every tool result by its call ID. Tool results are re-emitted right
	// after the assistant message that requested them instead of at their
	// stored position: messages can be written to a session concurrently
	// (e.g. resuming while a tool is still running), which interleaves
	// user messages between a tool call and its result. LLM APIs require
	// every tool call to be followed by its results before any other
	// message, and strict-adjacency providers (e.g. Kimi, DeepSeek) reject
	// the request otherwise, permanently locking the session.
	knownToolCallIDs := make(map[string]struct{})
	for _, m := range msgs {
		if m.Role != message.Assistant {
			continue
		}
		for _, tc := range m.ToolCalls() {
			knownToolCallIDs[tc.ID] = struct{}{}
		}
	}
	toolResultsByCall := make(map[string]fantasy.ToolResultPart)
	for _, m := range msgs {
		if m.Role != message.Tool {
			continue
		}
		for _, aiMsg := range m.ToAIMessage() {
			for _, part := range aiMsg.Content {
				tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
				if !ok {
					// Tool-role ToAIMessage only emits ToolResultParts today;
					// log so unexpected parts do not vanish silently.
					slog.Warn(
						"Dropping unexpected non-tool-result part from tool message",
						"part_type", fmt.Sprintf("%T", part),
					)
					continue
				}
				if _, known := knownToolCallIDs[tr.ToolCallID]; !known {
					slog.Warn(
						"Dropping orphaned tool result with no matching tool call",
						"tool_call_id", tr.ToolCallID,
					)
					continue
				}
				// One result per call: a real result beats an error, since the
				// usual duplicate is a premature "interrupted" then the output.
				if kept, seen := toolResultsByCall[tr.ToolCallID]; seen {
					_, keptIsError := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](kept.Output)
					slog.Warn("Dropping duplicate tool result", "session_id", m.SessionID,
						"tool_call_id", tr.ToolCallID, "kept_later", keptIsError)
					if !keptIsError {
						continue
					}
				}
				toolResultsByCall[tr.ToolCallID] = tr
			}
		}
	}

	for _, m := range msgs {
		if len(m.Parts) == 0 {
			continue
		}
		// Assistant message without content or tool calls (cancelled before it returned anything).
		if m.Role == message.Assistant && len(m.ToolCalls()) == 0 && m.Content().Text == "" && m.ReasoningContent().String() == "" {
			continue
		}
		// Tool results are emitted right after their assistant message.
		if m.Role == message.Tool {
			continue
		}
		aiMsgs := m.ToAIMessage()
		if m.Role == message.Assistant && foreignReasoning(m.Provider, m.Model, requestProvider, requestModel) {
			if stripped := stripReasoningReplayData(aiMsgs); stripped > 0 {
				slog.Debug(
					"Dropped reasoning replay data written by another model",
					"session_id", m.SessionID,
					"message_id", m.ID,
					"message_provider", m.Provider,
					"message_model", m.Model,
					"request_provider", requestProvider,
					"request_model", requestModel,
					"parts", stripped,
				)
			}
		}
		if !supportsImages {
			for i := range aiMsgs {
				if aiMsgs[i].Role == fantasy.MessageRoleUser {
					aiMsgs[i].Content = filterFileParts(aiMsgs[i].Content)
				}
			}
		}
		history = append(history, aiMsgs...)

		if m.Role == message.Assistant && len(m.ToolCalls()) > 0 {
			// A message whose calls were all provider-executed has no
			// replies to replay, and an empty tool message is invalid.
			if results := toolResultsForCalls(m, toolResultsByCall); len(results.Content) > 0 {
				history = append(history, results)
			}
		}
	}

	var files []fantasy.FilePart
	for _, attachment := range attachments {
		if attachment.IsText() {
			continue
		}
		if attachment.IsAudio() {
			if !supportsAudio {
				continue
			}
		} else if !supportsImages {
			continue
		}
		files = append(files, fantasy.FilePart{
			// Ref matches what createUserMessage stores in
			// BinaryContent.Path and what replay sends back, keeping the
			// request prefix byte-stable for prompt caching.
			Filename:  attachment.Ref(),
			Data:      attachment.Content,
			MediaType: attachment.MimeType,
		})
	}

	return history, files
}

// normalizePromptMessages rewrites in-flight messages so the bytes sent to
// the provider match the form that will be replayed from the session store
// on the next turn. Anything the persistence path normalizes (tool-call
// input repair, media captions) must be applied here too, otherwise the
// replayed prefix diverges from the live one and the provider's prompt
// cache misses on every turn.
func (a *sessionAgent) normalizePromptMessages(messages []fantasy.Message) {
	for mi := range messages {
		msg := &messages[mi]
		for pi := range msg.Content {
			part := msg.Content[pi]
			if tc, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
				if input, changed := sanitizeToolInput(tc.ToolName, tc.ToolCallID, tc.Input); changed {
					tc.Input = input
					msg.Content[pi] = tc
				}
				continue
			}
			tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok {
				continue
			}
			media, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](tr.Output)
			if !ok || media.Text != "" {
				continue
			}
			// convertToToolResult stores this same default caption when the
			// tool returned media without text, so replay carries it too.
			tr.Output = fantasy.ToolResultOutputContentMedia{
				Data:      media.Data,
				MediaType: media.MediaType,
				Text:      fmt.Sprintf("Loaded %s content", media.MediaType),
			}
			msg.Content[pi] = tr
		}
	}
}

// filterDisabledMCPTools removes tools from MCP servers disabled via the
// "Toggle MCPs" dialog. The override set is repository-scoped and shared
// by every session in the repository, including subagent sessions.
// Connections are process-global and left untouched; only the tool list
// changes.
func (a *sessionAgent) filterDisabledMCPTools(ctx context.Context, toolList []fantasy.AgentTool) []fantasy.AgentTool {
	disabledServers, err := a.sessions.MCPDisabledServers(ctx)
	if err != nil {
		slog.Error("Failed to list disabled MCP servers", "error", err)
		return toolList
	}
	if len(disabledServers) == 0 {
		return toolList
	}
	disabled := make(map[string]struct{}, len(disabledServers))
	for _, name := range disabledServers {
		disabled[name] = struct{}{}
	}
	filtered := make([]fantasy.AgentTool, 0, len(toolList))
	for _, t := range toolList {
		if mcpTool, ok := t.(*tools.Tool); ok {
			if _, off := disabled[mcpTool.MCP()]; off {
				continue
			}
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// filterFileParts removes fantasy.FilePart entries from a slice of message
// parts. Used to strip image attachments from historical user messages when
// the current model does not support them.
func filterFileParts(parts []fantasy.MessagePart) []fantasy.MessagePart {
	filtered := make([]fantasy.MessagePart, 0, len(parts))
	for _, part := range parts {
		if _, ok := fantasy.AsMessagePart[fantasy.FilePart](part); ok {
			continue
		}
		filtered = append(filtered, part)
	}
	return filtered
}

func (a *sessionAgent) getSessionMessages(ctx context.Context, session session.Session) ([]message.Message, error) {
	if session.SummaryMessageID != "" && session.SummaryCutMessageID != "" {
		cutMsgs, err := a.messages.ListFromCut(ctx, session.ID, session.SummaryCutMessageID)
		if err != nil {
			return nil, fmt.Errorf("failed to list messages from the compaction cut point: %w", err)
		}
		for i, msg := range cutMsgs {
			if msg.ID != session.SummaryMessageID {
				continue
			}
			// A compacted session sends: the summary (as a user message,
			// since the assistant never literally said it), the verbatim
			// tail the summary was written around, then everything newer.
			out := make([]message.Message, 0, len(cutMsgs))
			summary := cutMsgs[i]
			summary.Role = message.User
			summary = labelCompactedSummary(summary)
			out = append(out, summary)
			out = append(out, cutMsgs[:i]...)
			out = append(out, cutMsgs[i+1:]...)
			return dropDetachedSummaries(out, session.SummaryMessageID), nil
		}
		// The summary or the cut message no longer exists: fall back to
		// the summary-only view rather than re-sending the full
		// transcript.
	}

	// Read only the tail a compacted session actually sends. The full
	// transcript can be tens of megabytes on the single shared connection.
	msgs, err := a.messages.ListFromSummary(ctx, session.ID, session.SummaryMessageID)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}

	if session.SummaryMessageID != "" {
		summaryMsgIndex := -1
		for i, msg := range msgs {
			if msg.ID == session.SummaryMessageID {
				summaryMsgIndex = i
				break
			}
		}
		if summaryMsgIndex != -1 {
			msgs = msgs[summaryMsgIndex:]
			msgs[0].Role = message.User
			msgs[0] = labelCompactedSummary(msgs[0])
		}
	}
	return dropDetachedSummaries(msgs, session.SummaryMessageID), nil
}

// dropDetachedSummaries removes the summaries the session has not adopted:
// one written ahead of the compaction limit, while it is pending or after it
// went unused, sits in the history it summarizes and must not be read as
// part of it.
func dropDetachedSummaries(msgs []message.Message, summaryID string) []message.Message {
	return slices.DeleteFunc(msgs, func(msg message.Message) bool {
		return msg.IsSummaryMessage && msg.ID != summaryID
	})
}

// labelCompactedSummary wraps the summary in the label that tells the
// working model what it is reading: a compacted session replays it as an
// ordinary user message, and nothing else says so. Only the replayed copy is
// labelled, so the transcript and the stats report still read the summary as
// written. The text is fixed, which keeps the replayed bytes stable.
func labelCompactedSummary(msg message.Message) message.Message {
	msg.Parts = slices.Clone(msg.Parts)
	for i, part := range msg.Parts {
		text, ok := part.(message.TextContent)
		if !ok {
			continue
		}
		text.Text = "<compacted_history>\n" +
			"The earlier part of this conversation was compacted. This is its summary. " +
			"The messages after it are kept verbatim.\n\n" +
			text.Text +
			"\n</compacted_history>"
		msg.Parts[i] = text
		break
	}
	return msg
}

// hasUserTextMessage reports whether any user message in msgs contains
// text content (as opposed to only shell commands or other non-text parts).
func hasUserTextMessage(msgs []message.Message) bool {
	for _, msg := range msgs {
		if msg.Role != message.User {
			continue
		}
		for _, part := range msg.Parts {
			if tc, ok := part.(message.TextContent); ok && tc.Text != "" {
				return true
			}
		}
	}
	return false
}

// GenerateTitle generates a session title based on the initial prompt.
func (a *sessionAgent) GenerateTitle(ctx context.Context, sessionID string, userPrompt string) {
	a.titles.begin()
	defer a.titles.end()
	a.generateTitle(ctx, sessionID, userPrompt)
}

// Title attempts are capped so a runaway model cannot spend a session's
// budget on a title. A plain model needs only a handful of tokens, but a
// reasoning model spends most of its budget thinking before it writes the
// title, so the small cap makes every attempt end on FinishReasonLength.
const (
	plainTitleTokens     = int64(40)
	reasoningTitleTokens = int64(512)
)

// titleTokenBudget is the output budget for one title attempt.
func titleTokenBudget(m Model) int64 {
	if !m.CatwalkCfg.CanReason() {
		return plainTitleTokens
	}
	if m.CatwalkCfg.DefaultMaxTokens > reasoningTitleTokens {
		return m.CatwalkCfg.DefaultMaxTokens
	}
	return reasoningTitleTokens
}

// sameModel reports whether two builds address the same provider model.
func sameModel(a, b Model) bool {
	return a.ModelCfg.Provider == b.ModelCfg.Provider &&
		a.ModelCfg.Model == b.ModelCfg.Model
}

// titleAttempt names one model to try for a session title.
type titleAttempt struct {
	name  string
	model Model
}

// titleAttempts is the ordered list of models to try for a title. A large
// model identical to the small one is dropped: retrying it would bill a
// second request for an answer the first one already gave.
func titleAttempts(small, large Model) []titleAttempt {
	attempts := []titleAttempt{{"small", small}}
	if !sameModel(small, large) {
		attempts = append(attempts, titleAttempt{"main", large})
	}
	return attempts
}

// generateTitle is GenerateTitle without tracking, for callers that
// register with a.titles themselves.
func (a *sessionAgent) generateTitle(ctx context.Context, sessionID string, userPrompt string) {
	if userPrompt == "" {
		return
	}
	// A subagent run is never shown as a session of its own, so a title
	// for it would be a billed model request nobody reads.
	if a.isSubAgent {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, titleTimeout)
	defer cancel()

	// The generated title is only applied when the session still carries
	// the title it had when generation started, so a user rename made
	// while generation runs is not clobbered.
	var initialTitle string
	if sess, err := a.sessions.Get(ctx, sessionID); err == nil {
		// Child sessions (teammates, agent tool runs) sit behind their
		// parent and keep the title they were created with.
		if sess.ParentSessionID != "" {
			return
		}
		initialTitle = sess.Title
	} else {
		slog.Warn("Failed to read session for title generation", "session_id", sessionID, "error", err)
	}

	// Ensure the session always gets a title even if every path below
	// fails or the context is cancelled before we finish.
	var titleSaved bool
	defer func() {
		if !titleSaved {
			// WithoutCancel also drops the title timeout's deadline.
			fallbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := a.sessions.SetTitleIfUnchanged(fallbackCtx, sessionID, initialTitle, DefaultSessionName); err != nil {
				slog.Error("Failed to save fallback session title", "error", err)
			}
		}
	}()

	models := a.models()
	smallModel, largeModel := models.Small, models.Large
	systemPromptPrefix := a.systemPromptPrefix.Get()
	titlePrompt := promptOrBuiltin(a.prompts, "title")

	newAgent := func(m fantasy.LanguageModel, p string, tok int64) fantasy.Agent {
		return fantasy.NewAgent(
			m,
			fantasy.WithSystemPrompt(p+"\n /no_think"),
			fantasy.WithMaxOutputTokens(tok),
			fantasy.WithUserAgent(userAgent),
		)
	}

	streamCall := fantasy.AgentStreamCall{
		Prompt:  fmt.Sprintf("Generate a concise title for the following content:\n\n%s\n <think>\n\n</think>", userPrompt),
		Headers: sessionHeaders(sessionID),
		PrepareStep: func(callCtx context.Context, opts fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = opts.Messages
			if systemPromptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{
					fantasy.NewSystemMessage(systemPromptPrefix),
				}, prepared.Messages...)
			}
			return callCtx, prepared, nil
		},
	}

	var resp *fantasy.AgentResult
	var err error
	var model Model
	var success bool
	for _, attempt := range titleAttempts(smallModel, largeModel) {
		tok := titleTokenBudget(attempt.model)
		slog.Info("Generating session title",
			"attempt", attempt.name,
			"provider", attempt.model.ModelCfg.Provider,
			"model", attempt.model.ModelCfg.Model,
			"max_tokens", tok,
		)
		agent := newAgent(attempt.model.Model, titlePrompt, tok)
		resp, err = agent.Stream(ctx, streamCall)
		if err == nil && resp.Response.FinishReason != fantasy.FinishReasonLength {
			model = attempt.model
			slog.Debug("Generated title with " + attempt.name + " model")
			success = true
			break
		}
		if err != nil {
			slog.Error("Error generating title with "+attempt.name+" model; trying next",
				"provider", attempt.model.ModelCfg.Provider,
				"model", attempt.model.ModelCfg.Model,
				"err", err,
			)
		} else {
			slog.Error("Title generation hit token limit with "+attempt.name+" model; trying next",
				"provider", attempt.model.ModelCfg.Provider,
				"model", attempt.model.ModelCfg.Model,
				"max_tokens", tok,
			)
		}
	}
	if !success {
		// The deferred fallback will save the default session name.
		return
	}

	// Clean up title.
	var title string
	title = strings.ReplaceAll(resp.Response.Content.Text(), "\n", " ")

	// Remove thinking tags if present.
	title = thinkTagRegex.ReplaceAllString(title, "")
	title = orphanThinkTagRegex.ReplaceAllString(title, "")

	title = strings.TrimSpace(title)
	if title == "" {
		// LLM returned empty content. Use the prompt itself as a
		// fallback title, truncated to 50 chars, before resorting to
		// the generic default.
		fallback := strings.ReplaceAll(userPrompt, "\n", " ")
		fallback = strings.TrimSpace(fallback)
		if len(fallback) > 50 {
			fallback = ansi.Truncate(fallback, 50, "…")
		}
		title = cmp.Or(fallback, DefaultSessionName)
	}

	// Calculate usage and cost.
	var overrideCost *float64
	for _, step := range resp.Steps {
		stepCost := a.openrouterCost(step.ProviderMetadata)
		if stepCost != nil {
			newCost := *stepCost
			if overrideCost != nil {
				newCost += *overrideCost
			}
			overrideCost = &newCost
		}
	}
	if hyperCost := a.hyperStepsCost(model, resp.Steps); hyperCost != nil {
		overrideCost = hyperCost
	}

	cost := usageCost(model.CatwalkCfg.Pricing, resp.TotalUsage)

	// Use override cost if available (e.g., from OpenRouter or Hyper Prism).
	if overrideCost != nil {
		cost = *overrideCost
	}

	// Skip cost accumulation
	if model.FlatRate {
		cost = 0
	}

	promptTokens := contextTokens(resp.TotalUsage)

	// Record the title request's usage with a SQL-side increment so it
	// cannot be lost to a concurrent session save, and apply the
	// generated title only if the session still has the title it started
	// with (a user rename during generation wins).
	if err := a.sessions.AddSessionUsage(ctx, sessionID, resp.TotalUsage.CacheReadTokens, promptTokens, cost); err != nil {
		slog.Error("Failed to save session title usage", "error", err)
		return
	}
	if err := a.sessions.SetTitleIfUnchanged(ctx, sessionID, initialTitle, title); err != nil {
		slog.Error("Failed to save session title", "error", err)
		return
	}
	titleSaved = true
}

// openRouterCostFromSteps accumulates the OpenRouter-reported cost
// across the steps of a run, or nil when no step reports one.
func (a *sessionAgent) openRouterCostFromSteps(steps []fantasy.StepResult) *float64 {
	var openrouterCost *float64
	for _, step := range steps {
		stepCost := a.openrouterCost(step.ProviderMetadata)
		if stepCost != nil {
			newCost := *stepCost
			if openrouterCost != nil {
				newCost += *openrouterCost
			}
			openrouterCost = &newCost
		}
	}
	return openrouterCost
}

func (a *sessionAgent) openrouterCost(metadata fantasy.ProviderMetadata) *float64 {
	openrouterMetadata, ok := metadata[openrouter.Name]
	if !ok {
		return nil
	}

	opts, ok := openrouterMetadata.(*openrouter.ProviderMetadata)
	if !ok {
		return nil
	}
	return &opts.Usage.Cost
}

// extractPrismModel returns the ID and name of the model that actually
// served the turn, as reported by the Hyper Prism model router headers,
// or empty strings when the turn was not routed through a Prism model.
func extractPrismModel(metadata fantasy.ProviderMetadata) (modelID, modelName string) {
	openaiMeta, ok := metadata[openai.Name]
	if !ok {
		return "", ""
	}
	pm, ok := openaiMeta.(*openai.ProviderMetadata)
	if !ok {
		return "", ""
	}
	_ = pm.ExtraField(hyper.PrismModelIDField, &modelID)
	_ = pm.ExtraField(hyper.PrismModelNameField, &modelName)
	return modelID, modelName
}

// modelDisplayName resolves the display name of a model from the
// current config, falling back to the model ID when no entry exists or
// the entry has no name. The result is persisted on assistant messages
// so the UI keeps showing a meaningful name after the model is renamed
// or removed from the config.
func (a *sessionAgent) modelDisplayName(model config.SelectedModel) string {
	if a.cfg != nil {
		if cfg := a.cfg.Config(); cfg != nil && cfg.Providers != nil {
			if m := cfg.GetModel(model.Provider, model.Model); m != nil && m.Name != "" {
				return m.Name
			}
		}
	}
	return model.Model
}

func (a *sessionAgent) modelForStepCost(model Model, metadata fantasy.ProviderMetadata) Model {
	if model.ModelCfg.Provider != hyper.Name || a.cfg == nil {
		return model
	}
	routedModelID, _ := extractPrismModel(metadata)
	if routedModelID == "" {
		return model
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return model
	}
	routedModel := cfg.GetModel(hyper.Name, routedModelID)
	if routedModel == nil {
		return model
	}
	model.CatwalkCfg = *routedModel
	return model
}

func (a *sessionAgent) hyperStepsCost(model Model, steps []fantasy.StepResult) *float64 {
	if model.ModelCfg.Provider != hyper.Name || len(steps) == 0 {
		return nil
	}
	var cost float64
	for _, step := range steps {
		pricingModel := a.modelForStepCost(model, step.ProviderMetadata)
		cost += usageCost(pricingModel.CatwalkCfg.Pricing, step.Usage)
	}
	return &cost
}

// extractPrismSavings returns the hypercredit and dollar savings from
// routing the turn through the Hyper Prism model router, as reported by
// its savings trailers, or nil when not reported or malformed.
func extractPrismSavings(metadata fantasy.ProviderMetadata) (hypercredits, dollars *float64) {
	openaiMeta, ok := metadata[openai.Name]
	if !ok {
		return nil, nil
	}
	pm, ok := openaiMeta.(*openai.ProviderMetadata)
	if !ok {
		return nil, nil
	}
	return extraFieldFloat(pm, hyper.PrismHypercreditSavingsField), extraFieldFloat(pm, hyper.PrismDollarSavingsField)
}

func extraFieldFloat(pm *openai.ProviderMetadata, key string) *float64 {
	var value string
	if !pm.ExtraField(key, &value) {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		slog.Warn("Could not parse Prism savings", "key", key, "value", value, "error", err)
		return nil
	}
	return &parsed
}

func usageCost(pricing catwalk.Pricing, usage fantasy.Usage) float64 {
	return pricing.CacheCreate/1e6*float64(usage.CacheCreationTokens) +
		pricing.CacheHit/1e6*float64(usage.CacheReadTokens) +
		pricing.Input/1e6*float64(usage.InputTokens) +
		pricing.Output/1e6*float64(usage.OutputTokens)
}

func (a *sessionAgent) updateSessionUsage(model Model, session *session.Session, usage fantasy.Usage, overrideCost *float64, estimated bool) {
	step := a.stepUsageFor(model, *session, usage, overrideCost, estimated)
	session.EstimatedUsage = step.Estimated
	session.PromptTokens = step.PromptTokens
	session.CompletionTokens = step.CompletionTokens
	session.TotalCacheReadTokens += step.CacheReadTokens
	session.TotalContextTokens += step.ContextTokens
	session.Cost += step.Cost
}

// stepUsageFor prices one request's usage and splits it into what a session
// row needs: the context-size snapshot the step reported (falling back to
// the session's current snapshot when the provider gave nothing) and the
// increments for the accumulating columns. Returning values instead of
// mutating lets the per-step write be a single incrementing UPDATE rather
// than a fetch-modify-save, which both costs one less round trip and cannot
// clobber a concurrent sub-agent's totals.
func (a *sessionAgent) stepUsageFor(model Model, sess session.Session, usage fantasy.Usage, overrideCost *float64, estimated bool) session.StepUsage {
	usage = clampUsageCounters(usage)
	estimatedFlag := sess.EstimatedUsage
	if !usageIsZero(usage) {
		estimatedFlag = estimated
	}

	cost := usageCost(model.CatwalkCfg.Pricing, usage)

	if !estimated {
		a.eventTokensUsed(sess.ID, model, usage, cost)
	}

	// Prompt-cache visibility. A low read share means the request prefix
	// changed between turns (see recordPromptFingerprint, which names the
	// message that changed), which is the usual reason caching stops paying
	// off.
	if usage.CacheReadTokens > 0 || usage.CacheCreationTokens > 0 {
		totalInput := usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
		var readPercent int64
		if totalInput > 0 {
			readPercent = usage.CacheReadTokens * 100 / totalInput
		}
		slog.Debug(
			"Prompt cache usage",
			"session_id", sess.ID,
			"provider", model.ModelCfg.Provider,
			"model", model.ModelCfg.Model,
			"cache_read_tokens", usage.CacheReadTokens,
			"cache_write_tokens", usage.CacheCreationTokens,
			"input_tokens", usage.InputTokens,
			"cache_read_percent", readPercent,
		)
	}

	if estimated {
		cost = 0
	} else {
		// Use override cost if available (e.g., from OpenRouter).
		if overrideCost != nil {
			cost = *overrideCost
		}

		// Skip cost accumulation
		if model.FlatRate {
			cost = 0
		}
	}

	promptTokens := contextTokens(usage)
	if promptTokens == 0 {
		promptTokens = sess.PromptTokens
	}
	completionTokens := usage.OutputTokens
	if completionTokens == 0 {
		completionTokens = sess.CompletionTokens
	}

	return session.StepUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		ContextTokens:    contextTokens(usage),
		Cost:             cost,
		Estimated:        estimatedFlag,
	}
}

// tokenUsageCost prices one request's usage with the model's per-token
// rates.
func tokenUsageCost(modelCfg catwalk.Model, usage fantasy.Usage) float64 {
	return modelCfg.Pricing.CacheCreate/1e6*float64(usage.CacheCreationTokens) +
		modelCfg.Pricing.CacheHit/1e6*float64(usage.CacheReadTokens) +
		modelCfg.Pricing.Input/1e6*float64(usage.InputTokens) +
		modelCfg.Pricing.Output/1e6*float64(usage.OutputTokens)
}

// contextTokens returns the size of the prompt the provider processed for
// a step. Providers with prompt caching (Anthropic, Bedrock, Vercel) report
// the prompt as three disjoint buckets: tokens served from cache, tokens
// newly written to the cache, and the uncached remainder. All three occupy
// the context window, so all three count. Providers without cache writes
// leave CacheCreationTokens at zero, and the OpenAI-style providers already
// subtract cached tokens from InputTokens, so nothing is counted twice.
func contextTokens(usage fantasy.Usage) int64 {
	return usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
}

func updateSessionTokenCounters(session *session.Session, usage fantasy.Usage) {
	if usage.OutputTokens != 0 {
		session.CompletionTokens = usage.OutputTokens
	}
	promptTokens := contextTokens(usage)
	if promptTokens != 0 {
		session.PromptTokens = promptTokens
	}
	// Cache totals accumulate across every request of the session, like
	// cost, so the cache hit percentage survives compaction, which only
	// resets the snapshot counters.
	session.TotalCacheReadTokens += usage.CacheReadTokens
	session.TotalContextTokens += promptTokens
}

func summaryCompletionTokens(usage fantasy.Usage, summaryMessage message.Message) int64 {
	if usage.OutputTokens != 0 {
		return usage.OutputTokens
	}
	return approxTokenCount(summaryMessage.Content().Text) + approxTokenCount(summaryMessage.ReasoningContent().String())
}

func (a *sessionAgent) Cancel(sessionID string) {
	// A summary written ahead of the limit has nothing left to compact into
	// once the turn it followed is cancelled. It is stopped before the
	// dispatch lock, because discarding a finished one writes.
	a.cancelBackgroundCompaction(sessionID)

	// Serialize against the dispatch handoff in Run so the accepted ->
	// (cancel-on-entry | queued | active) transition is atomic against
	// this cancel. Every cancel observes at least one of: an active
	// request, an accepted run (recorded as a pending cancel), or a
	// queue entry it then clears. If none of those hold, an idle Escape
	// is a true no-op and must not poison the next prompt.
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()

	// Cancel regular requests. Don't use Take() here - we need the entry to
	// remain in activeRequests so IsBusy() returns true until the goroutine
	// fully completes (including error handling that may access the DB).
	// The defer in processRequest will clean up the entry.
	if ac, ok := a.activeRequests.Get(sessionID); ok && ac != nil {
		slog.Debug("Request cancellation initiated", "session_id", sessionID)
		ac.cancel()
	}

	// Also check for summarize requests.
	if ac, ok := a.activeRequests.Get(sessionID + "-summarize"); ok && ac != nil {
		slog.Debug("Summarize cancellation initiated", "session_id", sessionID)
		ac.cancel()
	}

	// Record a pending cancel only when a dispatched-but-not-yet-active
	// run exists. This catches runs still in the goroutine scheduler or
	// about to enter Run's busy-queue branch, while leaving an idle
	// session untouched. Active and accepted are not mutually exclusive:
	// when a run is active and a follow-up has been accepted, both the
	// cancel above and this pending record fire.
	//
	// Raise the session's cancel mark to the latest accept sequence
	// assigned so far. Every prompt currently accepted-but-not-yet-
	// active has a sequence at or below that value, so one cancel covers
	// all of them; a prompt accepted after this cancel gets a strictly
	// higher sequence and is never poisoned. Using max keeps repeated
	// cancels idempotent while the same prompts are in flight and lets a
	// later cancel extend coverage to prompts accepted since.
	a.acceptedMu.Lock()
	count, ok := a.acceptedRuns.Get(sessionID)
	mark := a.acceptSeqGen
	a.acceptedMu.Unlock()
	if ok && count > 0 {
		slog.Debug("Recording cancel mark for accepted runs", "session_id", sessionID, "count", count, "mark", mark)
		existing, _ := a.cancelMark.Get(sessionID)
		a.cancelMark.Set(sessionID, max(existing, mark))
	}

	if a.QueuedPrompts(sessionID) > 0 {
		slog.Debug("Clearing queued prompts", "session_id", sessionID)
		a.clearQueueAndNotify(sessionID)
	}
}

func (a *sessionAgent) ClearQueue(sessionID string) {
	if a.QueuedPrompts(sessionID) > 0 {
		slog.Debug("Clearing queued prompts", "session_id", sessionID)
		a.clearQueueAndNotify(sessionID)
	}
}

// AddNote keeps text for the session's next prompt, which the agent reads
// with it. Nothing runs and nothing is shown in the chat.
func (a *sessionAgent) AddNote(sessionID, text string) {
	if text == "" {
		return
	}
	a.notesMu.Lock()
	defer a.notesMu.Unlock()
	existing, _ := a.notes.Get(sessionID)
	a.notes.Set(sessionID, append(existing, text))
}

func (a *sessionAgent) restoreNotes(sessionID string, notes []string) {
	if len(notes) == 0 {
		return
	}
	a.notesMu.Lock()
	defer a.notesMu.Unlock()
	later, _ := a.notes.Get(sessionID)
	a.notes.Set(sessionID, append(append([]string{}, notes...), later...))
}

// takeNotes returns and forgets the session's notes, for the prompt about to run.
func (a *sessionAgent) takeNotes(sessionID string) []string {
	a.notesMu.Lock()
	defer a.notesMu.Unlock()
	notes, _ := a.notes.Get(sessionID)
	a.notes.Del(sessionID)
	return notes
}

// CancelAll cancels every run and waits, up to 5s in total, for the runs
// and any in-flight title generation to finish writing.
func (a *sessionAgent) CancelAll() {
	deadline := time.Now().Add(5 * time.Second)
	if a.IsBusy() {
		for key := range a.activeRequests.Seq2() {
			a.Cancel(key) // key is sessionID
		}
		for key := range a.acceptedRuns.Seq2() {
			a.Cancel(key)
		}
		for a.IsBusy() {
			if time.Now().After(deadline) {
				slog.Warn("Gave up waiting for agent runs to cancel; their final state may not be recorded")
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	// Background summaries belong to no active run, so the loop above does
	// not reach them and they have to be stopped on their own.
	for sessionID := range a.bgCompactions.Seq2() {
		a.cancelBackgroundCompaction(sessionID)
	}
	if !a.titles.wait(time.Until(deadline)) {
		slog.Warn("Gave up waiting for session title generation to finish")
	}
}

// IsBusy reports whether any session has an active or accepted run.
func (a *sessionAgent) IsBusy() bool {
	for ac := range a.activeRequests.Seq() {
		if ac != nil {
			return true
		}
	}
	return a.acceptedRuns.Len() > 0
}

// HasActiveTurn ignores accepts so Run, holding its own, doesn't queue behind itself.
func (a *sessionAgent) HasActiveTurn(sessionID string) bool {
	_, busy := a.activeRequests.Get(sessionID)
	return busy
}

// HasPendingWork reports whether sessionID has an active or accepted run.
func (a *sessionAgent) HasPendingWork(sessionID string) bool {
	if a.HasActiveTurn(sessionID) {
		return true
	}
	accepted, _ := a.acceptedRuns.Get(sessionID)
	return accepted > 0
}

func (a *sessionAgent) QueuedPrompts(sessionID string) int {
	l, ok := a.messageQueue.Get(sessionID)
	if !ok {
		return 0
	}
	return len(l)
}

// QueuedPromptsList returns the queued prompts a user sees. Hidden
// continuations are left out, as they are in the transcript, though
// QueuedPrompts still counts them.
func (a *sessionAgent) QueuedPromptsList(sessionID string) []string {
	l, ok := a.messageQueue.Get(sessionID)
	if !ok {
		return nil
	}
	prompts := make([]string, 0, len(l))
	for _, call := range l {
		if !call.HiddenUserMessage {
			prompts = append(prompts, call.Prompt)
		}
	}
	return prompts
}

// EnqueueNotice queues a background completion notice for the next step of
// sessionID. The next request drains it into a hidden user message, so a
// busy turn sees the completion without opening a second turn.
func (a *sessionAgent) EnqueueNotice(sessionID, text string) {
	if sessionID == "" || text == "" {
		return
	}
	a.noticesMu.Lock()
	defer a.noticesMu.Unlock()
	a.notices[sessionID] = append(a.notices[sessionID], text)
}

func (a *sessionAgent) drainNotices(sessionID string) []string {
	a.noticesMu.Lock()
	defer a.noticesMu.Unlock()
	notices := a.notices[sessionID]
	delete(a.notices, sessionID)
	return notices
}

func (a *sessionAgent) SetModels(large Model, small Model) {
	a.modelSwitchMu.Lock()
	a.previousLarge = a.largeModel.Get()
	a.previousSmall = a.smallModel.Get()
	a.modelSwitchPending = true
	a.modelSwitchMu.Unlock()
	a.largeModel.Set(large)
	a.smallModel.Set(small)
}

func (a *sessionAgent) SetTools(tools []fantasy.AgentTool) {
	a.tools.SetSlice(tools)
}

func (a *sessionAgent) SetSystemPrompt(systemPrompt string) {
	a.systemPrompt.Set(systemPrompt)
}

func (a *sessionAgent) SystemPrompt() string {
	return a.systemPrompt.Get() + a.systemPromptSuffix
}

func (a *sessionAgent) Model() Model {
	return a.largeModel.Get()
}

// convertToToolResult converts a fantasy tool result to a message tool result.
func (a *sessionAgent) convertToToolResult(result fantasy.ToolResultContent) message.ToolResult {
	baseResult := message.ToolResult{
		ToolCallID: result.ToolCallID,
		Name:       result.ToolName,
		Metadata:   result.ClientMetadata,
	}

	switch result.Result.GetType() {
	case fantasy.ToolResultContentTypeText:
		if r, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Result); ok {
			baseResult.Content = a.truncateToolOutput(r.Text)
		}
	case fantasy.ToolResultContentTypeError:
		if r, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](result.Result); ok {
			baseResult.Content = a.truncateToolOutput(r.Error.Error())
			baseResult.IsError = true
		}
	case fantasy.ToolResultContentTypeMedia:
		if r, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Result); ok {
			switch {
			case r.Data == "":
				slog.Warn("Tool returned media with no data, storing a note instead",
					"tool", result.ToolName, "tool_call_id", result.ToolCallID)
				baseResult.Content = emptyMediaText(r.Text)
			case !stringext.IsValidBase64(r.Data):
				slog.Warn(
					"Tool returned media with invalid base64 data, discarding image",
					"tool", result.ToolName,
					"tool_call_id", result.ToolCallID,
				)
				baseResult.Content = "Tool returned image data with invalid encoding"
				baseResult.IsError = true
			default:
				content := r.Text
				if content == "" {
					content = fmt.Sprintf("Loaded %s content", r.MediaType)
				}
				baseResult.Content = content
				baseResult.Data = r.Data
				baseResult.MIMEType = r.MediaType
			}
		}
	}

	return baseResult
}

// truncateToolOutput bounds a tool result before it is stored and replayed.
// Oversized text keeps its head and tail and is written in full to a spill
// file under the project data directory, which the view tool can read
// without leaving the workspace. A previous truncation marker is preserved
// so layered bounds do not nest notices.
func (a *sessionAgent) truncateToolOutput(content string) string {
	if content == "" || tools.HasSpillNotice(content) {
		return content
	}
	maxTokens := config.DefaultToolOutputMaxTokens
	spillDir := ""
	if a.cfg != nil {
		if cfg := a.cfg.Config(); cfg != nil {
			maxTokens = cfg.Options.GetToolOutputMaxTokens()
			spillDir = cfg.Options.DataDirectory
		}
	}
	return tools.TruncateForTokens(content, maxTokens, spillDir)
}

// repetitionDetectionEnabled reports whether degenerate streamed output
// should be detected and stopped.
func (a *sessionAgent) repetitionDetectionEnabled() bool {
	if a.cfg == nil {
		return true
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return true
	}
	return cfg.Options.GetRepetitionDetection()
}

// repeatToolRemindersEnabled reports whether advisory repeat-tool reminders
// should be injected before the hard loop stop.
func (a *sessionAgent) repeatToolRemindersEnabled() bool {
	if a.cfg == nil {
		return true
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return true
	}
	return cfg.Options.GetRepeatToolReminders()
}

// maxRequestImages returns the maximum images sent in one request. Zero
// disables the budget.
func (a *sessionAgent) maxRequestImages() int {
	if a.cfg == nil {
		return config.DefaultMaxImages
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return config.DefaultMaxImages
	}
	return cfg.Options.GetMaxImages()
}

// workaroundProviderMediaLimitations converts media content in tool results to
// user messages for providers that don't natively support images in tool results.
//
// Problem: OpenAI, Google, OpenRouter, and other OpenAI-compatible providers
// don't support sending images/media in tool result messages - they only accept
// text in tool results. However, they DO support images in user messages.
//
// If we send media in tool results to these providers, the API returns an error.
//
// Solution: For these providers, we:
//  1. Replace the media in the tool result with a text placeholder
//  2. Inject a user message immediately after with the image as a file attachment
//  3. This maintains the tool execution flow while working around API limitations
//
// Anthropic and Bedrock support images natively in tool results, so we skip
// this workaround for them.
//
// Example transformation:
//
//	BEFORE: [tool result: image data]
//	AFTER:  [tool result: "Image loaded - see attached"], [user: image attachment]
func (a *sessionAgent) workaroundProviderMediaLimitations(messages []fantasy.Message, largeModel Model) []fantasy.Message {
	providerSupportsMedia := largeModel.ModelCfg.Provider == string(catwalk.InferenceProviderAnthropic) ||
		largeModel.ModelCfg.Provider == string(catwalk.InferenceProviderBedrock) ||
		largeModel.ModelCfg.Provider == string(catwalk.InferenceProviderBedrockEurope)

	if providerSupportsMedia {
		return messages
	}

	supportsImages := largeModel.CatwalkCfg.Capabilities.Vision

	convertedMessages := make([]fantasy.Message, 0, len(messages))

	for _, msg := range messages {
		if msg.Role != fantasy.MessageRoleTool {
			convertedMessages = append(convertedMessages, msg)
			continue
		}

		textParts := make([]fantasy.MessagePart, 0, len(msg.Content))
		var mediaFiles []fantasy.FilePart

		for _, part := range msg.Content {
			toolResult, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok {
				textParts = append(textParts, part)
				continue
			}

			if media, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](toolResult.Output); ok {
				if !supportsImages {
					// Model cannot process images. Replace with a text
					// placeholder and skip creating a synthetic user
					// message with FilePart, which would brick the
					// session on text-only models.
					textParts = append(textParts, fantasy.ToolResultPart{
						ToolCallID: toolResult.ToolCallID,
						Output: fantasy.ToolResultOutputContentText{
							Text: "[Image/media content not supported by this model]",
						},
						ProviderOptions: toolResult.ProviderOptions,
					})
					continue
				}

				decoded, err := base64.StdEncoding.DecodeString(media.Data)
				if err != nil {
					slog.Warn("Failed to decode media data", "error", err)
					textParts = append(textParts, part)
					continue
				}

				mediaFiles = append(mediaFiles, fantasy.FilePart{
					Data:      decoded,
					MediaType: media.MediaType,
					Filename:  fmt.Sprintf("tool-result-%s", toolResult.ToolCallID),
				})

				textParts = append(textParts, fantasy.ToolResultPart{
					ToolCallID: toolResult.ToolCallID,
					Output: fantasy.ToolResultOutputContentText{
						Text: "[Image/media content loaded - see attached file]",
					},
					ProviderOptions: toolResult.ProviderOptions,
				})
			} else {
				textParts = append(textParts, part)
			}
		}

		convertedMessages = append(convertedMessages, fantasy.Message{
			Role:    fantasy.MessageRoleTool,
			Content: textParts,
		})

		if len(mediaFiles) > 0 {
			convertedMessages = append(convertedMessages, fantasy.NewUserMessage(
				claudeagent.ToolMediaMessageText,
				mediaFiles...,
			))
		}
	}

	return convertedMessages
}

// buildCompactionPrompt constructs the trailing instruction for a
// compaction request. It is a user message rather than a system prompt so
// the request's system prompt, tools, and message prefix stay
// byte-identical to the live turn, letting providers with prefix caching
// serve the conversation from cache.
func buildCompactionPrompt(body string, sess session.Session, keptMessages, totalMessages int) string {
	var sb strings.Builder
	sb.WriteString(body)
	if sess.SummaryMessageID != "" {
		sb.WriteString("\n\n## Previous Summary\n\n")
		sb.WriteString("The first message in the conversation above is a summary written at an earlier compaction. ")
		sb.WriteString("It is your starting point: preserve everything in it that is still relevant and add everything that happened since it was written. ")
		sb.WriteString("Do not spend space restating what it already says well.")
	}
	if keptMessages > 0 {
		fmt.Fprintf(
			&sb,
			"\n\n## Preserved Tail\n\nThe most recent %d of the conversation's %d messages are preserved verbatim after your summary and will still be readable. Do not re-summarize their contents in detail; concentrate on the earlier conversation.",
			keptMessages, totalMessages,
		)
	}
	if len(sess.Todos) > 0 {
		sb.WriteString("\n\n## Current Todo List\n\n")
		for _, t := range sess.Todos {
			fmt.Fprintf(&sb, "- [%s] %s\n", t.Status, t.Content)
		}
		sb.WriteString("\nInclude these tasks and their statuses in your summary. ")
		sb.WriteString("Instruct the resuming assistant to use the `todos` tool to continue tracking progress on these tasks.")
	}
	return sb.String()
}

func providerRetryLogFields(err *fantasy.ProviderError, delay time.Duration) []any {
	fields := []any{
		"retry_delay", delay.String(),
	}
	if err == nil {
		return fields
	}
	fields = append(fields, "status_code", err.StatusCode)
	if err.Title != "" {
		fields = append(fields, "title", err.Title)
	}
	if err.Message != "" {
		fields = append(fields, "message", err.Message)
	}
	return fields
}

// sanitizeToolInput validates tool call JSON from the provider.
// Malformed input is replaced with an empty object to prevent
// stuck conversations from truncated or malformed model output.
// The second return value indicates whether sanitization occurred.
func sanitizeToolInput(toolName, toolCallID, input string) (string, bool) {
	if !json.Valid([]byte(input)) {
		slog.Warn(
			"Malformed tool call JSON from provider, replacing with empty object",
			"tool", toolName,
			"id", toolCallID,
			"input_len", len(input),
		)
		return "{}", true
	}
	return input, false
}

// signInAgainMessage tells the user how to replace a credential the provider
// refused after a refresh.
func signInAgainMessage(m Model) string {
	name := cmp.Or(m.ProviderName, m.ModelCfg.Provider)
	return fmt.Sprintf("Please sign in to %s again. You can also run \"crush login %s --force\".",
		name, m.ModelCfg.Provider)
}
