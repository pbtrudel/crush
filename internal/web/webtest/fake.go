// Package webtest provides a scriptable fake workspace for web gateway
// tests, Playwright smoke tests and frontend development.
package webtest

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pinentry"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/charmbracelet/crush/internal/sshaskpass"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/tidwall/sjson"
)

// ErrSessionNotFound is returned by GetSession for unknown IDs. It wraps
// sql.ErrNoRows, which is what the real session service returns.
var ErrSessionNotFound = fmt.Errorf("session not found: %w", sql.ErrNoRows)

// subscriber is one SubscribeEvents channel. mu serialises Emit's send
// against the cleanup close so an event can never race with close(ch).
type subscriber struct {
	ch     chan any
	done   <-chan struct{}
	mu     sync.Mutex
	closed bool
}

// Fake implements workspace.Workspace for tests. Methods not overridden
// panic through the nil embedded interface, which flags unexpected calls.
type Fake struct {
	workspace.Workspace

	// subsMu guards subs and is never taken by the getters the gateway
	// polls, so a blocked Emit cannot stall CurrentSessionID.
	subsMu sync.Mutex
	subs   map[*subscriber]struct{}

	mu          sync.Mutex
	current     string
	sessions    map[string]session.Session
	messages    map[string][]message.Message
	mode        permission.PermissionMode
	queued      map[string][]string
	busy        map[string]bool
	history     map[string][]history.File
	diagnostics map[string]lsp.DiagnosticCounts
	teamErr     error
	seq         int
	// skillCatalog is the visible skill set; skillContent and
	// skillDisabled back ReadSkill and the toggles. skillDiscovered is the
	// rest of the discovered set, which only ListAllSkills reports.
	skillCatalog    []skills.CatalogEntry
	skillDiscovered []skills.CatalogEntry
	skillContent    map[string]string
	skillDisabled   map[string]bool
	// skillsErr, when set, makes both skill list calls fail; allSkillsErr
	// makes only ListAllSkills fail. Set them with the setters.
	skillsErr    error
	allSkillsErr error
	// mcpPrompts is the prompt catalog; mcpPromptText is keyed by
	// "<client>:<prompt>" and holds what GetMCPPrompt returns.
	mcpPrompts    []commands.MCPPrompt
	mcpPromptText map[string]string
	// sessionSeq hands out CreateSession IDs. A monotonic counter keeps
	// them unique even after a session is deleted.
	sessionSeq int

	// decisions and answers carry resolutions to a running scripted
	// agent, so a test or webfake can play the user's part.
	decisions chan permission.PermissionNotification
	answers   chan []question.Answer

	// WorkDir is what WorkingDir returns; webfake points it at the repo
	// so @ completion has real files.
	WorkDir string
	// LSP and MCP are what LSPGetStates and MCPGetStates return. Set them
	// before the gateway starts, and LSPDiagnostics likewise: it is what
	// LSPGetDiagnosticCounts answers.
	LSP            map[string]workspace.LSPClientInfo
	MCP            map[string]mcp.ClientInfo
	LSPDiagnostics map[string]lsp.DiagnosticCounts
	// MCPDisabled and MCPEnabled are the repository-scoped overrides, and
	// MCPPending the servers awaiting OAuth. Set them before the gateway
	// starts.
	MCPDisabled []string
	MCPEnabled  []string
	MCPPending  []mcp.PendingAuthServer
	// AuthURL is what MCPAuthURL answers while a flow runs.
	AuthURL string
	// mcpOverridesErr makes both override reads fail; reloadErr makes
	// ReloadConfig fail; authErr is what MCPAuthenticate returns once
	// released. The auth flow blocks on authRelease, or on ctx unless
	// authIgnoreCtx. Set them with the setters.
	mcpOverridesErr error
	reloadErr       error
	authErr         error
	authRelease     chan struct{}
	authIgnoreCtx   bool
	// configErr makes the config writes and the global toggles fail.
	// configGate, when set, holds every config write until it closes, so a
	// test can prove two writes never overlap. Set them with the setters.
	configErr  error
	configGate <-chan struct{}
	// Calls is guarded by mu; read it through CallLog.
	Calls   []string
	Granted []string
	Answers [][]question.Answer
	// AnswerBatches records the batch each answer named.
	AnswerBatches []string
	// pinentryOpen and sshOpen hold the prompt ids a tab can answer;
	// lastSecret is the answer the last respond method delivered, which
	// LastSecret returns.
	pinentryOpen map[string]bool
	sshOpen      map[string]bool
	lastSecret   string
	Resolve      bool
	OnRun        func(ctx context.Context, sessionID, prompt string, att []message.Attachment) error
	// OnSummarize, OnAsk and OnShell play a scripted agent for the browser
	// action routes. A nil OnSummarize succeeds, a nil OnAsk falls back to
	// AskAnswer, and a nil OnShell returns an empty shell response.
	OnSummarize func(ctx context.Context, sessionID string) error
	OnAsk       func(ctx context.Context, sessionID, question string) (string, error)
	OnShell     func(ctx context.Context, sessionID, command string, onProgress func(string), isFirst bool) (proto.ShellCommandResponse, error)
	// OnLSPStart, when set, runs after LSPStart records its call, so a test
	// can observe the context a detached start was given.
	OnLSPStart func(ctx context.Context, path string)
	// AskAnswer is what AgentAsk returns when OnAsk is nil.
	AskAnswer string
	// NoteErr, when set, makes AgentAddNote fail.
	NoteErr error
	// readyErr is what AgentReadyErr returns; set it with SetReadyErr.
	readyErr error
	// Cfg is what Config returns, so a test can give the gateway the
	// provider and model set it reads. A model switch swaps in a copy, so
	// a reader of a published config never sees its models change.
	Cfg *config.Config
	// catalog is the provider catalog SetProviderAPIKey takes models from.
	catalog []catwalk.Provider
	// copilotImport is what ImportCopilot reports; set it with
	// SetCopilotImport.
	copilotImport *oauth.Token
	// providerKeyErr makes SetProviderAPIKey fail; set it with
	// SetProviderKeyErr.
	providerKeyErr error
	// agentBusy is what AgentIsBusy reports; set it with SetAgentBusy.
	agentBusy bool
	// agentModel is what AgentModel returns; set it with SetAgentModel.
	agentModel workspace.AgentModel
	// agentModelGate holds one AgentModel read; set it with
	// SetAgentModelGate.
	agentModelGate *agentModelGate
	// listMessagesGate holds one ListMessages read; set it with
	// SetListMessagesGate.
	listMessagesGate *agentModelGate
	// initializePrompt is what InitializePrompt answers; set it with
	// SetInitializePrompt.
	initializePrompt string
}

// agentModelGate is one held AgentModel read.
type agentModelGate struct {
	entered chan<- struct{}
	release <-chan struct{}
}

// NewFake returns a fake with one current session "s1".
func NewFake() *Fake {
	f := &Fake{
		subs:          make(map[*subscriber]struct{}),
		sessions:      map[string]session.Session{"s1": {ID: "s1", Title: "Holodeck test"}},
		messages:      make(map[string][]message.Message),
		queued:        make(map[string][]string),
		busy:          make(map[string]bool),
		history:       make(map[string][]history.File),
		diagnostics:   make(map[string]lsp.DiagnosticCounts),
		pinentryOpen:  make(map[string]bool),
		sshOpen:       make(map[string]bool),
		skillContent:  make(map[string]string),
		skillDisabled: make(map[string]bool),
		mcpPromptText: make(map[string]string),
		current:       "s1",
		sessionSeq:    1,
		decisions:     make(chan permission.PermissionNotification, 16),
		answers:       make(chan []question.Answer, 16),
		WorkDir:       "/tmp/project",
		Resolve:       true,
	}
	return f
}

func (f *Fake) record(call string) {
	f.mu.Lock()
	f.Calls = append(f.Calls, call)
	f.mu.Unlock()
}

// Emit delivers ev to every SubscribeEvents channel. Subscribers are
// copied under subsMu and then released, so a blocked Emit never holds
// the lock CurrentSessionID and the other getters need. It waits for a
// full subscriber buffer, but gives up on a subscriber whose context
// ends.
func (f *Fake) Emit(ev any) {
	f.subsMu.Lock()
	subs := make([]*subscriber, 0, len(f.subs))
	for s := range f.subs {
		subs = append(subs, s)
	}
	f.subsMu.Unlock()
	for _, s := range subs {
		s.mu.Lock()
		if !s.closed {
			select {
			case s.ch <- ev:
			case <-s.done:
			}
		}
		s.mu.Unlock()
	}
}

// SetCurrent changes the session the fake terminal is viewing.
func (f *Fake) SetCurrent(id string) {
	f.mu.Lock()
	f.current = id
	f.mu.Unlock()
}

// SetCurrentSession is how both the terminal and the gateway report the
// session they are viewing. The fake has one shared view, like the real
// in-process workspace.
func (f *Fake) SetCurrentSession(_ context.Context, sessionID string) error {
	f.SetCurrent(sessionID)
	return nil
}

// AddMessage stores a message so ListMessages returns it.
func (f *Fake) AddMessage(m message.Message) {
	f.mu.Lock()
	f.messages[m.SessionID] = append(f.messages[m.SessionID], m)
	f.mu.Unlock()
}

// AddSession stores a session, such as a child session with a parent.
func (f *Fake) AddSession(s session.Session) {
	f.mu.Lock()
	f.sessions[s.ID] = s
	f.mu.Unlock()
}

// AddHistory stores one file version for ListSessionHistory.
func (f *Fake) AddHistory(file history.File) {
	f.mu.Lock()
	f.history[file.SessionID] = append(f.history[file.SessionID], file)
	f.mu.Unlock()
}

// SubscribeEvents registers a subscriber before it returns, so no event
// emitted after registration can be lost. The channel closes when ctx is
// done.
func (f *Fake) SubscribeEvents(ctx context.Context) <-chan any {
	s := &subscriber{ch: make(chan any, 1024), done: ctx.Done()}
	f.subsMu.Lock()
	f.subs[s] = struct{}{}
	f.subsMu.Unlock()
	go func() {
		<-ctx.Done()
		f.subsMu.Lock()
		delete(f.subs, s)
		f.subsMu.Unlock()
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.ch)
		}
		s.mu.Unlock()
	}()
	return s.ch
}

// CloseSubscribers closes every SubscribeEvents channel, the way a
// workspace that shut down does.
func (f *Fake) CloseSubscribers() {
	f.subsMu.Lock()
	subs := make([]*subscriber, 0, len(f.subs))
	for s := range f.subs {
		subs = append(subs, s)
		delete(f.subs, s)
	}
	f.subsMu.Unlock()
	for _, s := range subs {
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.ch)
		}
		s.mu.Unlock()
	}
}

func (f *Fake) CurrentSessionID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

func (f *Fake) WorkingDir() string { return f.WorkDir }

// Resolver returns the identity resolver: a fake never expands a $(...)
// in a test key as a shell command.
func (f *Fake) Resolver() config.VariableResolver { return config.IdentityResolver() }

// SetCatalog gives SetProviderAPIKey the provider catalog it takes models
// from.
func (f *Fake) SetCatalog(providers []catwalk.Provider) {
	f.mu.Lock()
	f.catalog = append([]catwalk.Provider(nil), providers...)
	f.mu.Unlock()
}

// SetCopilotImport drives ImportCopilot.
func (f *Fake) SetCopilotImport(tok *oauth.Token) {
	f.mu.Lock()
	f.copilotImport = tok
	f.mu.Unlock()
}

// SetProviderKeyErr makes SetProviderAPIKey fail, the way a config write
// can. nil restores success.
func (f *Fake) SetProviderKeyErr(err error) {
	f.mu.Lock()
	f.providerKeyErr = err
	f.mu.Unlock()
}

// SetProviderAPIKey records the mutation without the value and marks the
// provider configured with the catalog's models. The key itself is never
// stored.
func (f *Fake) SetProviderAPIKey(scope config.Scope, providerID string, apiKey any) error {
	kind := "key"
	if _, ok := apiKey.(*oauth.Token); ok {
		kind = "oauth"
	}
	f.record("SetProviderAPIKey:" + scope.String() + ":" + providerID + ":" + kind)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.providerKeyErr != nil {
		return f.providerKeyErr
	}
	f.Cfg = withProviderKey(f.Cfg, providerID, f.catalog)
	return nil
}

// ImportCopilot reports the token SetCopilotImport set. A token marks
// Copilot configured with the catalog's models, as the real import saves
// the login to the config.
func (f *Fake) ImportCopilot() (*oauth.Token, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.copilotImport == nil {
		return nil, false
	}
	f.Calls = append(f.Calls, "ImportCopilot")
	f.Cfg = withProviderKey(f.Cfg, string(catwalk.InferenceProviderCopilot), f.catalog)
	return f.copilotImport, true
}

// Config returns the test's config, or nil: the fake has no data
// directory, so the gateway's team watcher stays off unless a test sets
// Options.AgentTeamsDir.
func (f *Fake) Config() *config.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Cfg
}

func (f *Fake) GitBranch(context.Context) (string, error) { return "main", nil }

func (f *Fake) AgentModel() workspace.AgentModel {
	f.mu.Lock()
	// The gate is claimed under the same lock as the read, so exactly one
	// caller blocks and the others still see the model set before it.
	gate := f.agentModelGate
	f.agentModelGate = nil
	model := f.agentModel
	f.mu.Unlock()
	if gate != nil {
		close(gate.entered)
		<-gate.release
	}
	return model
}

// SetAgentModel replaces what AgentModel returns, as a terminal model
// switch would.
func (f *Fake) SetAgentModel(m workspace.AgentModel) {
	f.mu.Lock()
	f.agentModel = m
	f.mu.Unlock()
}

// SetAgentModelGate holds the next AgentModel read until release closes,
// the way a stalled workspace holds one. entered signals the held read.
func (f *Fake) SetAgentModelGate(entered chan<- struct{}, release <-chan struct{}) {
	f.mu.Lock()
	f.agentModelGate = &agentModelGate{entered: entered, release: release}
	f.mu.Unlock()
}

// SetListMessagesGate holds the next ListMessages call until release is
// closed, reporting on entered first. It is one-shot: later reads pass
// through, so a test only gates the snapshot it means to hold.
func (f *Fake) SetListMessagesGate(entered chan<- struct{}, release <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listMessagesGate = &agentModelGate{entered: entered, release: release}
}

// SetAgentBusy makes AgentIsBusy report busy, as a running turn would.
func (f *Fake) SetAgentBusy(busy bool) {
	f.mu.Lock()
	f.agentBusy = busy
	f.mu.Unlock()
}

// SetInitializePrompt sets what InitializePrompt answers.
func (f *Fake) SetInitializePrompt(text string) {
	f.mu.Lock()
	f.initializePrompt = text
	f.mu.Unlock()
}

func (f *Fake) InitializePrompt() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.initializePrompt, nil
}

func (f *Fake) MarkProjectInitialized() error {
	f.record("MarkProjectInitialized")
	return nil
}

func (f *Fake) EnableDockerMCP(context.Context) error {
	f.record("EnableDockerMCP")
	return nil
}

func (f *Fake) DisableDockerMCP() error {
	f.record("DisableDockerMCP")
	return nil
}

// UpdatePreferredModel records a model choice and replaces the selected
// model, the way the config store does, so the model info a handler sends
// back already describes the new model.
func (f *Fake) UpdatePreferredModel(scope config.Scope, typ config.SelectedModelType, m config.SelectedModel) error {
	f.record("UpdatePreferredModel:" + scope.String() + ":" + string(typ) + ":" + m.Provider + "/" + m.Model)
	f.mu.Lock()
	f.Cfg = withModel(f.Cfg, typ, m)
	f.mu.Unlock()
	return nil
}

// UpdateAgentModel points the agent model at the config's main model, the
// way the real workspace rebuilds the agent after a switch.
func (f *Fake) UpdateAgentModel(context.Context) error {
	f.record("UpdateAgentModel")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Cfg == nil {
		return nil
	}
	selected, ok := f.Cfg.Models[config.SelectedModelTypeMain]
	if !ok {
		return nil
	}
	m := workspace.AgentModel{ModelCfg: selected}
	if model := catalogModel(f.Cfg, selected); model != nil {
		m.CatwalkCfg = *model
	}
	f.agentModel = m
	return nil
}

// catalogModel returns the catalog entry of a selected model, or nil when
// the config has no providers (a reload in flight).
func catalogModel(cfg *config.Config, selected config.SelectedModel) *catwalk.Model {
	if cfg.Providers == nil {
		return nil
	}
	return cfg.GetModel(selected.Provider, selected.Model)
}

// withModel copies cfg with one selected model replaced. The copy keeps a
// published config's model map immutable, so reads of it never race the
// switch.
func withModel(cfg *config.Config, typ config.SelectedModelType, m config.SelectedModel) *config.Config {
	if cfg == nil {
		cfg = &config.Config{}
	}
	clone := *cfg
	clone.Models = maps.Clone(cfg.Models)
	if clone.Models == nil {
		clone.Models = make(map[config.SelectedModelType]config.SelectedModel)
	}
	clone.Models[typ] = m
	return &clone
}

// withProviderKey copies cfg with provider id marked configured, taking
// the models from the matching catalog entry. The credential value stays
// out of the fake config.
func withProviderKey(cfg *config.Config, id string, catalog []catwalk.Provider) *config.Config {
	if cfg == nil {
		cfg = &config.Config{}
	}
	clone := *cfg
	providers := csync.NewMap[string, config.ProviderConfig]()
	if cfg.Providers != nil {
		for name, pc := range cfg.Providers.Seq2() {
			providers.Set(name, pc)
		}
	}
	pc, _ := providers.Get(id)
	pc.ID = id
	if pc.Name == "" {
		pc.Name = id
		for _, p := range catalog {
			if string(p.ID) == id {
				pc.Name = cmp.Or(p.Name, id)
				break
			}
		}
	}
	if len(pc.Models) == 0 {
		for _, p := range catalog {
			if string(p.ID) == id {
				pc.Models = append([]catwalk.Model(nil), p.Models...)
				break
			}
		}
	}
	providers.Set(id, pc)
	clone.Providers = providers
	clone.Models = maps.Clone(cfg.Models)
	return &clone
}

func (f *Fake) AgentIsBusy() bool {
	f.record("AgentIsBusy")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agentBusy
}

func (f *Fake) AgentReadyErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readyErr
}

// SetReadyErr makes AgentReadyErr report err. nil means the agent is
// ready.
func (f *Fake) SetReadyErr(err error) {
	f.mu.Lock()
	f.readyErr = err
	f.mu.Unlock()
}

func (f *Fake) LSPGetStates() map[string]workspace.LSPClientInfo { return f.LSP }

func (f *Fake) LSPGetDiagnosticCounts(name string) lsp.DiagnosticCounts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.LSPDiagnostics[name]
}

func (f *Fake) LSPStart(ctx context.Context, path string) {
	f.record("LSPStart:" + path)
	f.mu.Lock()
	onStart := f.OnLSPStart
	f.mu.Unlock()
	if onStart != nil {
		onStart(ctx, path)
	}
}

func (f *Fake) LSPStopAll(context.Context) {
	f.record("LSPStopAll")
}

func (f *Fake) ReloadConfig(context.Context) error {
	f.record("ReloadConfig")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reloadErr
}

// SetReloadErr makes ReloadConfig fail, the way a busy agent or a config
// that does not parse does.
func (f *Fake) SetReloadErr(err error) {
	f.mu.Lock()
	f.reloadErr = err
	f.mu.Unlock()
}

// SetConfigErr makes SetConfigField, RemoveConfigField and the global
// toggles fail, the way a full disk or an unwritable file does.
func (f *Fake) SetConfigErr(err error) {
	f.mu.Lock()
	f.configErr = err
	f.mu.Unlock()
}

// SetConfigGate holds every config write until gate closes, so a test can
// check that two writes never overlap.
func (f *Fake) SetConfigGate(gate <-chan struct{}) {
	f.mu.Lock()
	f.configGate = gate
	f.mu.Unlock()
}

// SetConfigField records the write and applies it to Cfg the way the real
// store writes the file and reloads it. Only options.* keys reach the
// config: the fake has no other section a test writes.
func (f *Fake) SetConfigField(scope config.Scope, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return f.writeConfig("SetConfigField", scope, key, string(raw), value)
}

// RemoveConfigField records the removal and deletes the key from Cfg,
// which succeeds for an absent key exactly as the real store does.
func (f *Fake) RemoveConfigField(scope config.Scope, key string) error {
	return f.writeConfig("RemoveConfigField", scope, key, "", nil)
}

// writeConfig shares the record, the gate, the failure and the apply
// between the two config writes; value is nil for a removal.
func (f *Fake) writeConfig(method string, scope config.Scope, key, raw string, value any) error {
	call := method + ":" + scope.String() + ":" + key
	if value != nil {
		call += ":" + raw
	}
	f.record(call)
	f.mu.Lock()
	gate, err := f.configGate, f.configErr
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if err != nil {
		return err
	}
	path, ok := strings.CutPrefix(key, "options.")
	if !ok {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Cfg == nil {
		f.Cfg = &config.Config{}
	}
	if f.Cfg.Options == nil {
		f.Cfg.Options = &config.Options{}
	}
	options, err := json.Marshal(f.Cfg.Options)
	if err != nil {
		return err
	}
	if value == nil {
		options, err = sjson.DeleteBytes(options, path)
	} else {
		options, err = sjson.SetBytes(options, path, value)
	}
	if err != nil {
		return err
	}
	var opts config.Options
	if err := json.Unmarshal(options, &opts); err != nil {
		return err
	}
	f.Cfg.Options = &opts
	return nil
}

// SetDiagnostics sets what LSPDiagnosticsByFile returns.
func (f *Fake) SetDiagnostics(m map[string]lsp.DiagnosticCounts) {
	f.mu.Lock()
	f.diagnostics = maps.Clone(m)
	f.mu.Unlock()
}

func (f *Fake) LSPDiagnosticsByFile(context.Context) map[string]lsp.DiagnosticCounts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.diagnostics)
}

// SetTeamErr sets the error both team calls return.
func (f *Fake) SetTeamErr(err error) {
	f.mu.Lock()
	f.teamErr = err
	f.mu.Unlock()
}

func (f *Fake) TeamSendMessage(_ context.Context, sessionID, member, text string) error {
	f.record("TeamSendMessage:" + sessionID + ":" + member + ":" + text)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.teamErr
}

func (f *Fake) TeamInterrupt(_ context.Context, sessionID, member string) error {
	f.record("TeamInterrupt:" + sessionID + ":" + member)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.teamErr
}

func (f *Fake) MCPGetStates() map[string]mcp.ClientInfo { return f.MCP }

func (f *Fake) MCPStartServer(_ context.Context, name string) error {
	f.record("MCPStartServer:" + name)
	return nil
}

func (f *Fake) RefreshMCPTools(_ context.Context, name string) {
	f.record("RefreshMCPTools:" + name)
}

func (f *Fake) MCPRefreshPrompts(_ context.Context, name string) {
	f.record("MCPRefreshPrompts:" + name)
}

func (f *Fake) MCPRefreshResources(_ context.Context, name string) {
	f.record("MCPRefreshResources:" + name)
}

func (f *Fake) MCPSetServerDisabled(_ context.Context, name string, disabled bool) error {
	f.record("MCPSetServerDisabled:" + name + ":" + strconv.FormatBool(disabled))
	return nil
}

func (f *Fake) MCPSetServerConfigDisabled(_ context.Context, name string, disabled bool) error {
	f.record("MCPSetServerConfigDisabled:" + name + ":" + strconv.FormatBool(disabled))
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configErr
}

func (f *Fake) MCPServersDisabled(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mcpOverridesErr != nil {
		return nil, f.mcpOverridesErr
	}
	return append([]string(nil), f.MCPDisabled...), nil
}

func (f *Fake) MCPServersEnabled(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mcpOverridesErr != nil {
		return nil, f.mcpOverridesErr
	}
	return append([]string(nil), f.MCPEnabled...), nil
}

// SetMCPOverridesErr makes both repository override reads fail, as a
// session store that cannot answer would.
func (f *Fake) SetMCPOverridesErr(err error) {
	f.mu.Lock()
	f.mcpOverridesErr = err
	f.mu.Unlock()
}

func (f *Fake) MCPPendingAuth() []mcp.PendingAuthServer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mcp.PendingAuthServer(nil), f.MCPPending...)
}

// SetMCPPending replaces the servers awaiting OAuth, the way the real flow
// moves a server out of the pending state as soon as it starts.
func (f *Fake) SetMCPPending(pending []mcp.PendingAuthServer) {
	f.mu.Lock()
	f.MCPPending = append([]mcp.PendingAuthServer(nil), pending...)
	f.mu.Unlock()
}

func (f *Fake) MCPAuthURL(string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.AuthURL
}

// MCPAuthenticate plays the blocking OAuth flow: it ends when its context
// does or when the release channel closes, and then returns the configured
// error. A nil release channel returns at once.
func (f *Fake) MCPAuthenticate(ctx context.Context, name string) error {
	f.record("MCPAuthenticate:" + name)
	f.mu.Lock()
	release, ignoreCtx, authErr := f.authRelease, f.authIgnoreCtx, f.authErr
	f.mu.Unlock()
	if release == nil {
		return authErr
	}
	if ignoreCtx {
		<-release
	} else {
		select {
		case <-ctx.Done():
			f.record("MCPAuthenticateCanceled:" + name)
			return ctx.Err()
		case <-release:
		}
	}
	// Released while the context was still live: the flow outlived its
	// request, which is what a detached caller needs.
	f.record("MCPAuthenticateReleased:" + name + ":" + strconv.FormatBool(ctx.Err() == nil))
	return authErr
}

// SetMCPAuthRelease makes MCPAuthenticate block until ch closes.
func (f *Fake) SetMCPAuthRelease(ch chan struct{}) {
	f.mu.Lock()
	f.authRelease = ch
	f.mu.Unlock()
}

// SetMCPAuthIgnoreCtx makes the blocked flow wait for the release channel
// even after its context ends, the way a command that ignores cancellation
// does.
func (f *Fake) SetMCPAuthIgnoreCtx(ignore bool) {
	f.mu.Lock()
	f.authIgnoreCtx = ignore
	f.mu.Unlock()
}

// SetMCPAuthErr makes a released flow fail.
func (f *Fake) SetMCPAuthErr(err error) {
	f.mu.Lock()
	f.authErr = err
	f.mu.Unlock()
}

// AddSkill adds a visible skill and the content ReadSkill returns for it.
func (f *Fake) AddSkill(entry skills.CatalogEntry, content string) {
	f.mu.Lock()
	f.skillCatalog = append(f.skillCatalog, entry)
	f.skillContent[entry.ID] = content
	f.mu.Unlock()
}

// SetSkillsErr makes ListSkills fail, the way a broken catalog does.
func (f *Fake) SetSkillsErr(err error) {
	f.mu.Lock()
	f.skillsErr = err
	f.mu.Unlock()
}

func (f *Fake) ListSkills(context.Context) ([]skills.CatalogEntry, error) {
	f.record("ListSkills")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.skillsErr != nil {
		return nil, f.skillsErr
	}
	return append([]skills.CatalogEntry(nil), f.skillCatalog...), nil
}

// AddDiscoveredSkill adds a skill to the discovered set without making it
// active: ListAllSkills reports it, ListSkills does not.
func (f *Fake) AddDiscoveredSkill(entry skills.CatalogEntry) {
	f.mu.Lock()
	f.skillDiscovered = append(f.skillDiscovered, entry)
	f.mu.Unlock()
}

// SetAllSkillsErr makes only ListAllSkills fail, the way an older server
// without the full-set route would.
func (f *Fake) SetAllSkillsErr(err error) {
	f.mu.Lock()
	f.allSkillsErr = err
	f.mu.Unlock()
}

func (f *Fake) ListAllSkills(context.Context) ([]skills.CatalogEntry, error) {
	f.record("ListAllSkills")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.skillsErr != nil {
		return nil, f.skillsErr
	}
	if f.allSkillsErr != nil {
		return nil, f.allSkillsErr
	}
	out := append([]skills.CatalogEntry(nil), f.skillCatalog...)
	return append(out, f.skillDiscovered...), nil
}

func (f *Fake) ReadSkill(_ context.Context, skillID string) ([]byte, skills.SkillReadResult, error) {
	f.record("ReadSkill:" + skillID)
	f.mu.Lock()
	defer f.mu.Unlock()
	content, ok := f.skillContent[skillID]
	if !ok {
		return nil, skills.SkillReadResult{}, fmt.Errorf("read skill %s: %w", skillID, skills.ErrSkillNotFound)
	}
	var result skills.SkillReadResult
	for _, entry := range f.skillCatalog {
		if entry.ID == skillID {
			result = skills.SkillReadResult{Name: entry.Name, Description: entry.Description, Source: entry.Source}
			break
		}
	}
	return []byte(content), result, nil
}

func (f *Fake) ReloadSkills(context.Context) error {
	f.record("ReloadSkills")
	return nil
}

func (f *Fake) SkillsDisabled(context.Context) ([]string, error) {
	f.record("SkillsDisabled")
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for name, disabled := range f.skillDisabled {
		if disabled {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

// SkillsEnabled reports the repository-scoped enable overrides the way the
// session store records them: only for skills the config disabled.
func (f *Fake) SkillsEnabled(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for name, disabled := range f.skillDisabled {
		if !disabled && f.configDisabled(name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

// configDisabled reports whether the config's disabled list names a skill.
// The caller holds f.mu.
func (f *Fake) configDisabled(name string) bool {
	if f.Cfg == nil || f.Cfg.Options == nil {
		return false
	}
	return slices.Contains(f.Cfg.Options.DisabledSkills, name)
}

func (f *Fake) SkillSetDisabled(_ context.Context, name string, disabled bool) error {
	f.record("SkillSetDisabled:" + name + ":" + strconv.FormatBool(disabled))
	f.mu.Lock()
	f.skillDisabled[name] = disabled
	f.mu.Unlock()
	return nil
}

func (f *Fake) SkillSetConfigDisabled(_ context.Context, name string, disabled bool) error {
	f.record("SkillSetConfigDisabled:" + name + ":" + strconv.FormatBool(disabled))
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configErr
}

// AddMCPPrompt adds a prompt to the catalog and the text GetMCPPrompt
// returns for it.
func (f *Fake) AddMCPPrompt(p commands.MCPPrompt, text string) {
	f.mu.Lock()
	f.mcpPrompts = append(f.mcpPrompts, p)
	f.mcpPromptText[p.ClientID+":"+p.PromptID] = text
	f.mu.Unlock()
}

func (f *Fake) ListMCPPrompts(context.Context) ([]commands.MCPPrompt, error) {
	f.record("ListMCPPrompts")
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]commands.MCPPrompt(nil), f.mcpPrompts...), nil
}

func (f *Fake) GetMCPPrompt(clientID, promptID string, _ map[string]string) (string, error) {
	f.record("GetMCPPrompt:" + clientID + ":" + promptID)
	f.mu.Lock()
	defer f.mu.Unlock()
	text, ok := f.mcpPromptText[clientID+":"+promptID]
	if !ok {
		return "", fmt.Errorf("mcp prompt %s:%s not found", clientID, promptID)
	}
	return text, nil
}

func (f *Fake) PermissionMode() permission.PermissionMode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

func (f *Fake) PermissionSetMode(m permission.PermissionMode) {
	f.mu.Lock()
	f.mode = m
	f.mu.Unlock()
	f.record("PermissionSetMode")
}

func (f *Fake) GetSession(_ context.Context, id string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return session.Session{}, ErrSessionNotFound
	}
	return s, nil
}

// ListSessions answers newest updated first, with the ID breaking ties,
// the same order the real session service and the gateway use.
func (f *Fake) ListSessions(context.Context) ([]session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]session.Session, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b session.Session) int {
		return cmp.Or(cmp.Compare(b.UpdatedAt, a.UpdatedAt), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func (f *Fake) CreateSession(_ context.Context, title string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionSeq++
	id := "s" + strconv.Itoa(f.sessionSeq)
	s := session.Session{ID: id, Title: title}
	f.sessions[id] = s
	return s, nil
}

// SessionRename updates the title only, then publishes the same Updated
// event the real session service publishes so a tab sees the rename.
func (f *Fake) SessionRename(_ context.Context, id, title string) (session.Session, error) {
	f.record("SessionRename:" + id + ":" + title)
	f.mu.Lock()
	s, ok := f.sessions[id]
	if ok {
		s.Title = title
		f.sessions[id] = s
	}
	f.mu.Unlock()
	if !ok {
		return session.Session{}, ErrSessionNotFound
	}
	f.Emit(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: s})
	return s, nil
}

// DeleteSession removes the session and publishes the Deleted event, like
// the real session service.
func (f *Fake) DeleteSession(_ context.Context, id string) error {
	f.record("DeleteSession:" + id)
	f.mu.Lock()
	s, ok := f.sessions[id]
	if ok {
		delete(f.sessions, id)
	}
	f.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	f.Emit(pubsub.Event[session.Session]{Type: pubsub.DeletedEvent, Payload: s})
	return nil
}

// SetSessionBusy makes AgentIsSessionBusy report id as busy, as a running
// agent would.
func (f *Fake) SetSessionBusy(id string, busy bool) {
	f.mu.Lock()
	f.busy[id] = busy
	f.mu.Unlock()
}

// SetSessionQueued makes AgentQueuedPromptsList report prompts for id, as a
// tab that queued a second prompt behind a running turn would. An empty
// slice clears the queue.
func (f *Fake) SetSessionQueued(id string, prompts []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(prompts) == 0 {
		delete(f.queued, id)
		return
	}
	f.queued[id] = append([]string(nil), prompts...)
}

func (f *Fake) ListMessages(ctx context.Context, id string) ([]message.Message, error) {
	f.mu.Lock()
	gate := f.listMessagesGate
	f.listMessagesGate = nil
	messages := append([]message.Message(nil), f.messages[id]...)
	f.mu.Unlock()
	if gate != nil {
		gate.entered <- struct{}{}
		select {
		case <-gate.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return messages, nil
}

func (f *Fake) ListChildSessions(_ context.Context, parentID string) ([]session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []session.Session
	for _, s := range f.sessions {
		if s.ParentSessionID == parentID {
			out = append(out, s)
		}
	}
	if out == nil {
		out = []session.Session{}
	}
	slices.SortFunc(out, func(a, b session.Session) int {
		return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func (f *Fake) ListSessionHistory(_ context.Context, sessionID string) ([]history.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]history.File(nil), f.history[sessionID]...), nil
}

func (f *Fake) ListSessionHistoryMeta(_ context.Context, sessionID string) ([]history.FileMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	files := f.history[sessionID]
	metas := make([]history.FileMeta, len(files))
	for i, file := range files {
		metas[i] = history.FileMeta{
			ID:         file.ID,
			SessionID:  file.SessionID,
			Path:       file.Path,
			Version:    file.Version,
			CreatedAt:  file.CreatedAt,
			UpdatedAt:  file.UpdatedAt,
			Size:       int64(len(file.Content)),
			ToolCallID: file.ToolCallID,
		}
	}
	return metas, nil
}

func (f *Fake) GetHistoryVersion(_ context.Context, id string) (history.File, error) {
	f.record("GetHistoryVersion:" + id)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, files := range f.history {
		for _, file := range files {
			if file.ID == id {
				return file, nil
			}
		}
	}
	return history.File{}, fmt.Errorf("history version %s: %w", id, sql.ErrNoRows)
}

func (f *Fake) AgentRun(ctx context.Context, sessionID, prompt string, att ...message.Attachment) error {
	f.record("AgentRun:" + sessionID + ":" + prompt)
	if f.OnRun != nil {
		return f.OnRun(ctx, sessionID, prompt, att)
	}
	return nil
}

func (f *Fake) AgentCancel(sessionID string) {
	f.record("AgentCancel:" + sessionID)
}

func (f *Fake) AgentIsSessionBusy(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy[id]
}

func (f *Fake) AgentQueuedPromptsList(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queued[id]...)
}

func (f *Fake) AgentClearQueue(id string) {
	f.mu.Lock()
	delete(f.queued, id)
	f.mu.Unlock()
	f.record("AgentClearQueue:" + id)
}

func (f *Fake) AgentSummarize(ctx context.Context, sessionID string) error {
	f.record("AgentSummarize:" + sessionID)
	if f.OnSummarize != nil {
		return f.OnSummarize(ctx, sessionID)
	}
	return nil
}

func (f *Fake) AgentAsk(ctx context.Context, sessionID, question string) (string, error) {
	f.record("AgentAsk:" + sessionID + ":" + question)
	if f.OnAsk != nil {
		return f.OnAsk(ctx, sessionID, question)
	}
	return f.AskAnswer, nil
}

func (f *Fake) AgentRunShellCommand(ctx context.Context, sessionID, command string, _ int, onProgress func(string), isFirst bool) (proto.ShellCommandResponse, error) {
	f.record("AgentRunShellCommand:" + sessionID + ":" + command + ":" + strconv.FormatBool(isFirst))
	if f.OnShell != nil {
		return f.OnShell(ctx, sessionID, command, onProgress, isFirst)
	}
	return proto.ShellCommandResponse{}, nil
}

func (f *Fake) AgentAddNote(_ context.Context, sessionID, text string) error {
	f.record("AgentAddNote:" + sessionID + ":" + text)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.NoteErr
}

// resolvePermission publishes the outcome the UI would publish and hands
// it to a running scripted agent. It reports whether the fake answered.
func (f *Fake) resolvePermission(p permission.PermissionRequest, granted bool) bool {
	if !f.Resolve {
		return false
	}
	n := permission.PermissionNotification{SessionID: p.SessionID, ToolCallID: p.ToolCallID, Granted: granted, Denied: !granted}
	f.Emit(pubsub.Event[permission.PermissionNotification]{Type: pubsub.UpdatedEvent, Payload: n})
	select {
	case f.decisions <- n:
	default:
	}
	return true
}

func (f *Fake) PermissionGrant(p permission.PermissionRequest) bool {
	f.record("PermissionGrant:" + p.ID)
	return f.resolvePermission(p, true)
}

func (f *Fake) PermissionGrantPersistent(p permission.PermissionRequest) bool {
	f.record("PermissionGrantPersistent:" + p.ID)
	return f.resolvePermission(p, true)
}

func (f *Fake) PermissionDeny(p permission.PermissionRequest) bool {
	f.record("PermissionDeny:" + p.ID)
	return f.resolvePermission(p, false)
}

func (f *Fake) QuestionAnswer(batchID string, a []question.Answer) bool {
	f.mu.Lock()
	f.Answers = append(f.Answers, a)
	f.AnswerBatches = append(f.AnswerBatches, batchID)
	f.mu.Unlock()
	f.record("QuestionAnswer")
	if !f.Resolve {
		return false
	}
	select {
	case f.answers <- a:
	default:
	}
	return true
}

func (f *Fake) QuestionCancel(batchID string) bool {
	f.record("QuestionCancel:" + batchID)
	return f.Resolve
}

// AddPinentryPrompt opens a GPG prompt and publishes the request event.
func (f *Fake) AddPinentryPrompt(req pinentry.PromptRequest) {
	f.mu.Lock()
	f.pinentryOpen[req.ID] = true
	f.mu.Unlock()
	f.Emit(pubsub.Event[pinentry.PromptRequest]{Type: pubsub.CreatedEvent, Payload: req})
}

// PinentryRespond answers an open GPG prompt. The call log names the id
// only; the secret stays out of it.
func (f *Fake) PinentryRespond(id, secret string) bool {
	f.record("PinentryRespond:" + id)
	f.mu.Lock()
	if !f.pinentryOpen[id] {
		f.mu.Unlock()
		return false
	}
	delete(f.pinentryOpen, id)
	f.lastSecret = secret
	f.mu.Unlock()
	f.Emit(pubsub.Event[pinentry.Notification]{Type: pubsub.UpdatedEvent, Payload: pinentry.Notification{RequestID: id}})
	return true
}

// PinentryCancel dismisses an open GPG prompt.
func (f *Fake) PinentryCancel(id string) bool {
	f.record("PinentryCancel:" + id)
	f.mu.Lock()
	if !f.pinentryOpen[id] {
		f.mu.Unlock()
		return false
	}
	delete(f.pinentryOpen, id)
	f.mu.Unlock()
	f.Emit(pubsub.Event[pinentry.Notification]{Type: pubsub.UpdatedEvent, Payload: pinentry.Notification{RequestID: id}})
	return true
}

// AddSSHPrompt opens an SSH prompt and publishes the request event.
func (f *Fake) AddSSHPrompt(req sshaskpass.PromptRequest) {
	f.mu.Lock()
	f.sshOpen[req.ID] = true
	f.mu.Unlock()
	f.Emit(pubsub.Event[sshaskpass.PromptRequest]{Type: pubsub.CreatedEvent, Payload: req})
}

// SSHRespond answers an open SSH prompt. The call log names the id only;
// the secret stays out of it.
func (f *Fake) SSHRespond(id, secret string) bool {
	f.record("SSHRespond:" + id)
	f.mu.Lock()
	if !f.sshOpen[id] {
		f.mu.Unlock()
		return false
	}
	delete(f.sshOpen, id)
	f.lastSecret = secret
	f.mu.Unlock()
	f.Emit(pubsub.Event[sshaskpass.Notification]{Type: pubsub.UpdatedEvent, Payload: sshaskpass.Notification{RequestID: id}})
	return true
}

// SSHCancel dismisses an open SSH prompt.
func (f *Fake) SSHCancel(id string) bool {
	f.record("SSHCancel:" + id)
	f.mu.Lock()
	if !f.sshOpen[id] {
		f.mu.Unlock()
		return false
	}
	delete(f.sshOpen, id)
	f.mu.Unlock()
	f.Emit(pubsub.Event[sshaskpass.Notification]{Type: pubsub.UpdatedEvent, Payload: sshaskpass.Notification{RequestID: id}})
	return true
}

// ResolveFromTerminal closes a prompt the way the TUI would, without
// delivering a secret.
func (f *Fake) ResolveFromTerminal(source, id string) {
	f.mu.Lock()
	switch source {
	case "pinentry":
		delete(f.pinentryOpen, id)
	case "ssh":
		delete(f.sshOpen, id)
	}
	f.mu.Unlock()
	switch source {
	case "pinentry":
		f.Emit(pubsub.Event[pinentry.Notification]{Type: pubsub.UpdatedEvent, Payload: pinentry.Notification{RequestID: id}})
	case "ssh":
		f.Emit(pubsub.Event[sshaskpass.Notification]{Type: pubsub.UpdatedEvent, Payload: sshaskpass.Notification{RequestID: id}})
	}
}

// LastSecret returns the answer the last respond method delivered.
func (f *Fake) LastSecret() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastSecret
}

// nextID returns a deterministic, per-fake unique ID. The scripted agent
// uses it so fixture and smoke-test output stays stable.
func (f *Fake) nextID(prefix string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	return prefix + "-" + strconv.Itoa(f.seq)
}

// CallLog returns a copy of the recorded calls.
func (f *Fake) CallLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Calls...)
}

// FakeFlow is a signin.Flow for tests and webfake. Wait returns Next once,
// at once, when it is set; otherwise it blocks until Finish or the context
// ends, then returns Token or WaitErr.
type FakeFlow struct {
	FlowName     string // Name; "Fake provider" when empty
	HasCodeEntry bool   // CodeEntry
	Challenge    signin.Challenge
	StartErr     error
	Next         *signin.Challenge
	Token        *oauth.Token
	WaitErr      error
	CodeOK       string // SubmitCode accepts only this code

	release  chan struct{}
	finish   sync.Once
	mu       sync.Mutex
	nextUsed bool
	closed   bool
}

// NewFakeFlow returns a flow whose Wait blocks until Finish, and registers
// Finish in t.Cleanup so no test leaves a goroutine waiting.
func NewFakeFlow(t testing.TB) *FakeFlow {
	t.Helper()
	f := &FakeFlow{release: make(chan struct{})}
	t.Cleanup(f.Finish)
	return f
}

// NewFakeFlowFor returns a flow whose Wait blocks until done closes, for
// callers without a *testing.T, such as webfake. Finish still releases it.
func NewFakeFlowFor(done <-chan struct{}) *FakeFlow {
	f := &FakeFlow{release: make(chan struct{})}
	go func() {
		select {
		case <-done:
			f.Finish()
		case <-f.release:
		}
	}()
	return f
}

// Finish completes the flow, the way a token arriving would.
func (f *FakeFlow) Finish() {
	f.finish.Do(func() {
		f.mu.Lock()
		if f.release == nil {
			f.release = make(chan struct{})
		}
		release := f.release
		f.mu.Unlock()
		close(release)
	})
}

// Closed reports whether the gateway closed the flow.
func (f *FakeFlow) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// Name is the provider name the dialog shows.
func (f *FakeFlow) Name() string {
	if f.FlowName == "" {
		return "Fake provider"
	}
	return f.FlowName
}

// Start returns Challenge, or StartErr.
func (f *FakeFlow) Start(context.Context) (signin.Challenge, error) {
	if f.StartErr != nil {
		return signin.Challenge{}, f.StartErr
	}
	return f.Challenge, nil
}

// Wait returns Next once when it is set. Otherwise it blocks until Finish
// or the context ends.
func (f *FakeFlow) Wait(ctx context.Context, _ signin.Challenge) (*oauth.Token, *signin.Challenge, error) {
	f.mu.Lock()
	if f.Next != nil && !f.nextUsed {
		f.nextUsed = true
		next := f.Next
		f.mu.Unlock()
		return nil, next, nil
	}
	release := f.release
	f.mu.Unlock()
	select {
	case <-release:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	return f.Token, nil, f.WaitErr
}

// CodeEntry is the flow's code entry capability.
func (f *FakeFlow) CodeEntry() bool { return f.HasCodeEntry }

// SubmitCode accepts only CodeOK. Its error quotes the code, so a gateway
// that logged or forwarded the text fails the test.
func (f *FakeFlow) SubmitCode(_ context.Context, code string) (*oauth.Token, error) {
	if f.CodeOK != "" && code == f.CodeOK {
		return f.Token, nil
	}
	return nil, fmt.Errorf("invalid code %q", code)
}

// Close records that the flow's listener was released.
func (f *FakeFlow) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

var _ signin.Flow = (*FakeFlow)(nil)
