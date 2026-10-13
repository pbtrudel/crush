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
	typing      []string
	reactions   []string
	renames     []string
	gwURL       string
	gwErr       error
	createErr   error
}

type fakePost struct {
	channelID string
	messageID string
	content   string
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

func (f *fakeAPI) createMessage(ctx context.Context, channelID, content string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	f.nextID++
	id := "msg-" + string(rune('a'+f.nextID%26)) + time.Now().Format("150405.000000")
	f.posts = append(f.posts, fakePost{channelID: channelID, messageID: id, content: content})
	return id, nil
}

func (f *fakeAPI) editMessage(ctx context.Context, channelID, messageID, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, fakePost{channelID: channelID, messageID: messageID, content: content})
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
	catalog    []catwalk.Provider

	busy   bool
	queued []string
	yolo   bool
	agent  string
	dir    string

	runs        []string
	runAttached int
	cancels     int
	summarizes  int
	modelUpdate int
	preferred   []config.SelectedModel

	grants        []permission.PermissionRequest
	grantSessions []permission.PermissionRequest
	denies        []permission.PermissionRequest

	answers  []question.Answer
	answerOK bool
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
		catalog: []catwalk.Provider{
			{
				ID:   catwalk.InferenceProvider("openai"),
				Name: "OpenAI",
				Models: []catwalk.Model{
					{ID: "gpt-4o", Name: "GPT-4o"},
					{ID: "gpt-5", Name: "GPT-5"},
				},
			},
			{
				ID:   catwalk.InferenceProvider("anthropic"),
				Name: "Anthropic",
				Models: []catwalk.Model{
					{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
				},
			},
		},
		dir:      "/home/user/project",
		agent:    config.AgentCoder,
		answerOK: true,
	}
	env.cfg.Providers.Set("openai", config.ProviderConfig{ID: "openai"})
	return env
}

func (e *fakeEnv) catalogFn(cfg *config.Config) ([]catwalk.Provider, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.catalog, nil
}

func (e *fakeEnv) AgentRun(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.runs = append(e.runs, prompt)
	e.runAttached += len(attachments)
	return nil
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
