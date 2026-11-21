package web

import (
	"sync"

	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
)

// pending remembers unresolved prompts so the browser can answer by ID
// and so snapshots can restore open dialogs.
type pending struct {
	mu    sync.Mutex
	perms map[string]permission.PermissionRequest
	order []string
	q     *question.Request
}

func newPending() *pending {
	return &pending{perms: make(map[string]permission.PermissionRequest)}
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
		q := e.Payload
		p.q = &q
	case pubsub.Event[question.Notification]:
		if p.q != nil && p.q.ID == e.Payload.BatchID {
			p.q = nil
		}
	}
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

func (p *pending) question() *question.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.q == nil {
		return nil
	}
	q := *p.q
	return &q
}

func (p *pending) forget(id string) {
	p.mu.Lock()
	delete(p.perms, id)
	p.compact()
	p.mu.Unlock()
}
