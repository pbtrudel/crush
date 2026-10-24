package discord

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
)

// fakeAPI records every Discord call for assertions.
type fakeAPI struct {
	mu sync.Mutex

	nextID      int
	threadNames []string
	threadID    string
	posts       []fakePost
	edits       []fakePost
	deletes     []string
	typing      []string
	reactions   []string
	renames     []string
	callbacks   []fakeCallback
	updates     []fakeUpdate
	modals      []fakeModal
	commands    []applicationCommand
	commandsErr error
	gwURL       string
	gwErr       error
	createErr   error
	updateErr   error
}

type fakePost struct {
	channelID  string
	messageID  string
	content    string
	components []discordComponent
}

// fakeCallback records one interaction callback.
type fakeCallback struct {
	interactionID string
	token         string
	content       string
	ephemeral     bool
}

// fakeUpdate records one type-7 (update message) interaction callback.
type fakeUpdate struct {
	interactionID string
	token         string
	content       string
	components    []discordComponent
}

// fakeModal records one type-9 (modal) interaction callback.
type fakeModal struct {
	interactionID string
	token         string
	modal         discordModal
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{threadID: "thread-1", gwURL: "ws://gateway.test"}
}

func (f *fakeAPI) createThread(ctx context.Context, channelID, name string) (createdThread, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadNames = append(f.threadNames, name)
	return createdThread{ID: f.threadID, GuildID: "guild-1"}, nil
}

func (f *fakeAPI) createMessage(ctx context.Context, channelID, content string, components ...discordComponent) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	f.nextID++
	id := "msg-" + string(rune('a'+f.nextID%26)) + time.Now().Format("150405.000000")
	f.posts = append(f.posts, fakePost{channelID: channelID, messageID: id, content: content, components: components})
	return id, nil
}

func (f *fakeAPI) editMessage(ctx context.Context, channelID, messageID, content string, components ...discordComponent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, fakePost{channelID: channelID, messageID: messageID, content: content, components: components})
	return nil
}

func (f *fakeAPI) deleteMessage(ctx context.Context, channelID, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, messageID)
	return nil
}

func (f *fakeAPI) triggerTyping(ctx context.Context, channelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typing = append(f.typing, channelID)
	return nil
}

func (f *fakeAPI) addReaction(ctx context.Context, channelID, messageID, emoji string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactions = append(f.reactions, messageID+"/"+emoji)
	return nil
}

func (f *fakeAPI) editChannelName(ctx context.Context, channelID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renames = append(f.renames, name)
	return nil
}

func (f *fakeAPI) gatewayURL(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gwURL, f.gwErr
}

func (f *fakeAPI) respondInteraction(ctx context.Context, interactionID, token, content string, ephemeral bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callbacks = append(f.callbacks, fakeCallback{
		interactionID: interactionID,
		token:         token,
		content:       content,
		ephemeral:     ephemeral,
	})
	return nil
}

func (f *fakeAPI) updateInteraction(ctx context.Context, interactionID, token, content string, components []discordComponent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates = append(f.updates, fakeUpdate{
		interactionID: interactionID,
		token:         token,
		content:       content,
		components:    components,
	})
	return nil
}

func (f *fakeAPI) respondModal(ctx context.Context, interactionID, token string, modal discordModal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modals = append(f.modals, fakeModal{
		interactionID: interactionID,
		token:         token,
		modal:         modal,
	})
	return nil
}

// modalsSnapshot returns a copy of the recorded modal callbacks.
func (f *fakeAPI) modalsSnapshot() []fakeModal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeModal(nil), f.modals...)
}

func (f *fakeAPI) ensureApplicationCommands(ctx context.Context, guildID string, cmds []applicationCommand) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append([]applicationCommand(nil), cmds...)
	return f.commandsErr
}

// callbacksSnapshot returns a copy of the recorded interaction callbacks.
func (f *fakeAPI) callbacksSnapshot() []fakeCallback {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCallback(nil), f.callbacks...)
}

// updatesSnapshot returns a copy of the recorded update-message callbacks.
func (f *fakeAPI) updatesSnapshot() []fakeUpdate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeUpdate(nil), f.updates...)
}

// messageContent returns the latest recorded content for a message id,
// looking at edits first and posts second.
func (f *fakeAPI) messageContent(messageID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.edits) - 1; i >= 0; i-- {
		if f.edits[i].messageID == messageID {
			return f.edits[i].content
		}
	}
	for i := len(f.posts) - 1; i >= 0; i-- {
		if f.posts[i].messageID == messageID {
			return f.posts[i].content
		}
	}
	return ""
}

// messageComponents returns the latest recorded components for a message
// id, looking at edits first and posts second.
func (f *fakeAPI) messageComponents(messageID string) []discordComponent {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.edits) - 1; i >= 0; i-- {
		if f.edits[i].messageID == messageID {
			return f.edits[i].components
		}
	}
	for i := len(f.posts) - 1; i >= 0; i-- {
		if f.posts[i].messageID == messageID {
			return f.posts[i].components
		}
	}
	return nil
}

// hasPostContaining reports whether any posted message contains sub.
func (f *fakeAPI) hasPostContaining(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.posts {
		if strings.Contains(p.content, sub) {
			return true
		}
	}
	return false
}

// hasEditContaining reports whether any edit contains sub.
func (f *fakeAPI) hasEditContaining(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.edits {
		if strings.Contains(e.content, sub) {
			return true
		}
	}
	return false
}

// hasContentContaining reports whether any post or edit contains sub.
func (f *fakeAPI) hasContentContaining(sub string) bool {
	return f.hasPostContaining(sub) || f.hasEditContaining(sub)
}

// latestEditContaining returns the content of the most recent edit
// containing sub, or "".
func (f *fakeAPI) latestEditContaining(sub string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	content := ""
	for _, e := range f.edits {
		if strings.Contains(e.content, sub) {
			content = e.content
		}
	}
	return content
}

// lastPostContent returns the content of the most recent post.
func (f *fakeAPI) lastPostContent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) == 0 {
		return ""
	}
	return f.posts[len(f.posts)-1].content
}

// hasDelete reports whether a delete was recorded for messageID.
func (f *fakeAPI) hasDelete(messageID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.deletes {
		if id == messageID {
			return true
		}
	}
	return false
}

// postsSnapshot returns a copy of the recorded posts.
func (f *fakeAPI) postsSnapshot() []fakePost {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakePost(nil), f.posts...)
}

// lastPostID returns the id of the most recent post.
func (f *fakeAPI) lastPostID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) == 0 {
		return ""
	}
	return f.posts[len(f.posts)-1].messageID
}

// reactionCount counts recorded reactions.
func (f *fakeAPI) reactionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reactions)
}

// fakeEnv is a scriptable commandEnv.
type fakeEnv struct {
	mu sync.Mutex

	session    session.Session
	sessionErr error
	cfg        *config.Config

	busy   bool
	queued []string
	yolo   bool
	agent  string
	dir    string

	runs        []string
	runAttached int
	runErr      error
	asks        []string
	askAnswer   string
	askErr      error
	cancels     int
	summarizes  int
	modelUpdate int
	preferred   []config.SelectedModel

	grants        []permission.PermissionRequest
	grantSessions []permission.PermissionRequest
	denies        []permission.PermissionRequest

	answers  []question.Answer
	answerOK bool

	// CreateSession scripting for /clear.
	newSession   session.Session
	createErr    error
	createCalls  int
	createTitles []string
}

func newFakeEnv() *fakeEnv {
	env := &fakeEnv{
		session: session.Session{
			ID:               "sess-1",
			Title:            "Test Session",
			PromptTokens:     1200,
			CompletionTokens: 340,
		},
		cfg: &config.Config{
			Providers: csync.NewMap[string, config.ProviderConfig](),
			Models: map[config.SelectedModelType]config.SelectedModel{
				config.SelectedModelTypeLarge: {Provider: "openai", Model: "gpt-4o"},
			},
			Options: &config.Options{
				DataDirectory: filepath.Join(os.TempDir(), "crush-discord-tests"),
			},
		},
		dir:      "/home/user/project",
		agent:    config.AgentCoder,
		answerOK: true,
	}
	env.cfg.Providers.Set("openai", config.ProviderConfig{
		ID:   "openai",
		Name: "OpenAI",
		Models: []catwalk.Model{
			{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
			{ID: "gpt-5", Name: "GPT-5", ContextWindow: 256000},
			{ID: "gpt-5-canary", Name: "GPT-5 Canary", ContextWindow: 400000},
		},
	})
	env.cfg.Providers.Set("neuralwatt", config.ProviderConfig{
		ID:   "neuralwatt",
		Name: "Neuralwatt",
		Models: []catwalk.Model{
			{ID: "nw-flash", Name: "Neuralwatt Flash", ContextWindow: 1000000},
			{ID: "deepseek-v4.1-flash-canary", Name: "DeepSeek V4.1 Flash 1017 (canary)", ContextWindow: 1000000},
		},
	})
	env.cfg.Providers.Set("acme", config.ProviderConfig{
		ID:   "acme",
		Name: "Acme",
		Models: []catwalk.Model{
			{ID: "acme-1", Name: "Acme One", ContextWindow: 8000},
		},
	})
	return env
}

func (e *fakeEnv) AgentRun(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.runs = append(e.runs, prompt)
	e.runAttached += len(attachments)
	return e.runErr
}

func (e *fakeEnv) AgentAsk(ctx context.Context, sessionID, question string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.asks = append(e.asks, question)
	return e.askAnswer, e.askErr
}

// asksSnapshot returns a copy of the recorded side questions.
func (e *fakeEnv) asksSnapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.asks...)
}

func (e *fakeEnv) AgentCancel(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancels++
}

func (e *fakeEnv) AgentIsSessionBusy(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.busy
}

func (e *fakeEnv) AgentQueuedPrompts(sessionID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.queued)
}

func (e *fakeEnv) AgentQueuedPromptsList(sessionID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.queued...)
}

func (e *fakeEnv) AgentSetMain(agentID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.agent = agentID
	return nil
}

func (e *fakeEnv) AgentSummarize(ctx context.Context, sessionID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.summarizes++
	return nil
}

func (e *fakeEnv) UpdateAgentModel(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.modelUpdate++
	return nil
}

func (e *fakeEnv) UpdatePreferredModel(scope config.Scope, modelType config.SelectedModelType, model config.SelectedModel) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.preferred = append(e.preferred, model)
	if e.cfg.Models == nil {
		e.cfg.Models = map[config.SelectedModelType]config.SelectedModel{}
	}
	e.cfg.Models[modelType] = model
	return nil
}

func (e *fakeEnv) GetSession(ctx context.Context, sessionID string) (session.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sessionErr != nil {
		return session.Session{}, e.sessionErr
	}
	return e.session, nil
}

func (e *fakeEnv) CreateSession(ctx context.Context, title string) (session.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.createCalls++
	e.createTitles = append(e.createTitles, title)
	if e.createErr != nil {
		return session.Session{}, e.createErr
	}
	return e.newSession, nil
}

func (e *fakeEnv) PermissionGrant(perm permission.PermissionRequest) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.grants = append(e.grants, perm)
	return true
}

func (e *fakeEnv) PermissionGrantPersistent(perm permission.PermissionRequest) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.grantSessions = append(e.grantSessions, perm)
	return true
}

func (e *fakeEnv) PermissionDeny(perm permission.PermissionRequest) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.denies = append(e.denies, perm)
	return true
}

func (e *fakeEnv) PermissionSetSkipRequests(skip bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.yolo = skip
}

func (e *fakeEnv) PermissionSkipRequests() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.yolo
}

func (e *fakeEnv) QuestionAnswer(responses []question.Answer) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.answers = append(e.answers, responses...)
	return e.answerOK
}

// answersSnapshot returns a copy of the recorded question answers.
func (e *fakeEnv) answersSnapshot() []question.Answer {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]question.Answer(nil), e.answers...)
}

func (e *fakeEnv) Config() *config.Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

func (e *fakeEnv) WorkingDir() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dir
}

// snapshot helpers for assertions.
func (e *fakeEnv) runCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.runs)
}

func (e *fakeEnv) lastRun() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.runs) == 0 {
		return ""
	}
	return e.runs[len(e.runs)-1]
}

func (e *fakeEnv) cancelCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cancels
}

func (e *fakeEnv) summarizeCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.summarizes
}

func (e *fakeEnv) grantCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.grants)
}

func (e *fakeEnv) setBusy(busy bool, queued ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.busy = busy
	e.queued = queued
}
