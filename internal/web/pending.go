package web

import (
	"sync"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
)

// pending remembers unresolved prompts so the browser can answer by ID
// and so snapshots can restore open dialogs.
type pending struct {
	mu     sync.Mutex
	perms  map[string]permission.PermissionRequest
	order  []string
	qs     map[string]question.Request
	qorder []string
}

func newPending() *pending {
	return &pending{
		perms: make(map[string]permission.PermissionRequest),
		qs:    make(map[string]question.Request),
	}
}

func (p *pending) observe(ev any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch e := ev.(type) {
	case pubsub.Event[permission.PermissionRequest]:
		if _, ok := p.perms[e.Payload.ID]; !ok {
			p.order = append(p.order, e.Payload.ID)
		}
		p.perms[e.Payload.ID] = e.Payload
	case pubsub.Event[permission.PermissionNotification]:
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
	case pubsub.Event[notify.RunComplete]:
		// The run that asked is over, so nothing it asked can be answered
		// any more. Leaving these entries behind would keep dialogs open
		// across reloads.
		p.forgetSessionLocked(e.Payload.SessionID)
	}
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
