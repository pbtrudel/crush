package web

import (
	"cmp"
	"reflect"
	"sync"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pinentry"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/sshaskpass"
)

// SecurePrompt is a GPG or SSH prompt waiting for an answer, as the
// browser shows it. It never carries a secret.
type SecurePrompt struct {
	Source  string   `json:"source"`
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
	Retry   string   `json:"retry,omitempty"`
}

// AWSSSO is a running AWS SSO refresh, as the browser shows it. A tab
// opened mid-refresh renders the progress dialog from the snapshot
// instead of waiting for a notification it missed.
type AWSSSO struct {
	Command string `json:"command"`
	URL     string `json:"url,omitempty"`
}

const (
	sourcePinentry = "pinentry"
	sourceSSH      = "ssh"
)

func fromPinentry(r pinentry.PromptRequest) SecurePrompt {
	title := "GPG passphrase"
	if r.Kind == pinentry.KindPIN {
		title = "Security key PIN"
	}
	return SecurePrompt{
		Source: sourcePinentry, ID: r.ID, Kind: string(r.Kind), Title: title,
		Message: r.Prompt, Details: r.Key.Lines(), Retry: r.Error,
	}
}

func fromSSH(r sshaskpass.PromptRequest) SecurePrompt {
	r = sshaskpass.Describe(r)
	p := SecurePrompt{
		Source: sourceSSH, ID: r.ID, Kind: string(r.Kind),
		Title: cmp.Or(r.Title, "SSH"), Message: cmp.Or(r.Message, r.Prompt),
	}
	if r.KeyInfo != "" {
		p.Details = []string{r.KeyInfo}
	}
	return p
}

// pending remembers unresolved prompts so the browser can answer by ID
// and so snapshots can restore open dialogs.
type pending struct {
	mu     sync.Mutex
	perms  map[string]permission.PermissionRequest
	order  []string
	qs     map[string]question.Request
	qorder []string
	secure []SecurePrompt
	sso    *AWSSSO
}

func newPending() *pending {
	return &pending{
		perms: make(map[string]permission.PermissionRequest),
		qs:    make(map[string]question.Request),
	}
}

func (p *pending) observe(ev any) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch e := ev.(type) {
	case pubsub.Event[permission.PermissionRequest]:
		if _, ok := p.perms[e.Payload.ID]; !ok {
			p.order = append(p.order, e.Payload.ID)
		}
		p.perms[e.Payload.ID] = e.Payload
	case pubsub.Event[permission.PermissionNotification]:
		// An empty tool call id would match every request that lacks one.
		if e.Payload.ToolCallID == "" {
			break
		}
		for id, req := range p.perms {
			if req.ToolCallID == e.Payload.ToolCallID {
				delete(p.perms, id)
			}
		}
		p.compact()
	case pubsub.Event[question.Request]:
		if _, ok := p.qs[e.Payload.ID]; !ok {
			p.qorder = append(p.qorder, e.Payload.ID)
		}
		p.qs[e.Payload.ID] = e.Payload
	case pubsub.Event[question.Notification]:
		delete(p.qs, e.Payload.BatchID)
		p.compactQuestions()
	case pubsub.Event[notify.Notification]:
		// A refresh changes no secure prompt, so it publishes no frame of
		// its own; the notification already reaches the tabs as an
		// agent_event.
		p.followAWSSSOLocked(e.Payload)
	case pubsub.Event[notify.RunComplete]:
		// The run that asked is over, so nothing it asked can be answered
		// any more. Leaving these entries behind would keep dialogs open
		// across reloads.
		p.forgetSessionLocked(e.Payload.SessionID)
	case pubsub.Event[pinentry.PromptRequest]:
		return p.upsertSecureLocked(fromPinentry(e.Payload))
	case pubsub.Event[pinentry.Notification]:
		return p.removeSecureLocked(sourcePinentry, e.Payload.RequestID)
	case pubsub.Event[sshaskpass.PromptRequest]:
		return p.upsertSecureLocked(fromSSH(e.Payload))
	case pubsub.Event[sshaskpass.Notification]:
		return p.removeSecureLocked(sourceSSH, e.Payload.RequestID)
	}
	return false
}

// followAWSSSOLocked folds an AWS SSO notification into the refresh state.
// A non-empty command replaces it, a non-empty URL fills it in, and a
// result clears it whatever it says, so a failed refresh leaves no stale
// dialog behind. The caller holds the lock.
func (p *pending) followAWSSSOLocked(n notify.Notification) {
	switch n.Type {
	case notify.TypeAWSSSOAuth:
		if n.AWSSOCommand == "" && n.AWSSOURL == "" && p.sso == nil {
			return
		}
		if p.sso == nil {
			p.sso = &AWSSSO{}
		}
		if n.AWSSOCommand != "" {
			p.sso.Command = n.AWSSOCommand
		}
		if n.AWSSOURL != "" {
			p.sso.URL = n.AWSSOURL
		}
	case notify.TypeAWSSSOAuthResult:
		p.sso = nil
	}
}

// upsertSecureLocked adds a prompt or replaces the entry with the same
// source and id in place, keeping arrival order. The caller holds the lock.
func (p *pending) upsertSecureLocked(prompt SecurePrompt) bool {
	for i := range p.secure {
		if p.secure[i].Source != prompt.Source || p.secure[i].ID != prompt.ID {
			continue
		}
		if reflect.DeepEqual(p.secure[i], prompt) {
			return false
		}
		p.secure[i] = prompt
		return true
	}
	p.secure = append(p.secure, prompt)
	return true
}

// removeSecureLocked drops one prompt. The caller holds the lock.
func (p *pending) removeSecureLocked(source, id string) bool {
	for i := range p.secure {
		if p.secure[i].Source == source && p.secure[i].ID == id {
			p.secure = append(p.secure[:i], p.secure[i+1:]...)
			return true
		}
	}
	return false
}

// forgetSessionLocked drops the permissions and question of a session whose
// run has finished. The caller holds the lock.
func (p *pending) forgetSessionLocked(sessionID string) {
	for id, req := range p.perms {
		if req.SessionID == sessionID {
			delete(p.perms, id)
		}
	}
	p.compact()
	for id, q := range p.qs {
		if q.SessionID == sessionID {
			delete(p.qs, id)
		}
	}
	p.compactQuestions()
}

func (p *pending) compact() {
	kept := p.order[:0]
	for _, id := range p.order {
		if _, ok := p.perms[id]; ok {
			kept = append(kept, id)
		}
	}
	p.order = kept
}

func (p *pending) compactQuestions() {
	kept := p.qorder[:0]
	for _, id := range p.qorder {
		if _, ok := p.qs[id]; ok {
			kept = append(kept, id)
		}
	}
	p.qorder = kept
}

func (p *pending) permission(id string) (permission.PermissionRequest, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	req, ok := p.perms[id]
	return req, ok
}

func (p *pending) permissions() []permission.PermissionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]permission.PermissionRequest, 0, len(p.order))
	for _, id := range p.order {
		out = append(out, p.perms[id])
	}
	return out
}

// question returns the batch with the given ID, if it is still pending.
func (p *pending) question(id string) (question.Request, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	q, ok := p.qs[id]
	return q, ok
}

// questions returns every pending batch in arrival order.
func (p *pending) questions() []question.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]question.Request, 0, len(p.qorder))
	for _, id := range p.qorder {
		out = append(out, p.qs[id])
	}
	return out
}

func (p *pending) forget(id string) {
	p.mu.Lock()
	delete(p.perms, id)
	p.compact()
	p.mu.Unlock()
}

// forgetQuestion drops one pending batch. Other batches stay answerable.
func (p *pending) forgetQuestion(id string) {
	p.mu.Lock()
	delete(p.qs, id)
	p.compactQuestions()
	p.mu.Unlock()
}

// securePrompts returns the open GPG and SSH prompts in arrival order.
// The copy is never nil.
func (p *pending) securePrompts() []SecurePrompt {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]SecurePrompt, len(p.secure))
	copy(out, p.secure)
	return out
}

// securePrompt returns the open prompt with the given source and id.
func (p *pending) securePrompt(source, id string) (SecurePrompt, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, prompt := range p.secure {
		if prompt.Source == source && prompt.ID == id {
			return prompt, true
		}
	}
	return SecurePrompt{}, false
}

// awsSSO returns a copy of the running refresh, or nil when none runs.
func (p *pending) awsSSO() *AWSSSO {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sso == nil {
		return nil
	}
	out := *p.sso
	return &out
}

// forgetSecure drops one prompt and reports whether it was open.
func (p *pending) forgetSecure(source, id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.removeSecureLocked(source, id)
}

// interacts reports whether a permission or a question for sessionID still
// waits for an answer.
func (p *pending) interacts(sessionID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, req := range p.perms {
		if req.SessionID == sessionID {
			return true
		}
	}
	for _, batch := range p.qs {
		if batch.SessionID == sessionID {
			return true
		}
	}
	return false
}
