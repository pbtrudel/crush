package web

import (
	"encoding/json"

	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/wire"
	"github.com/charmbracelet/crush/internal/workspace"
)

// encode turns a workspace event into a frame type and its wire payload.
// Client/server mode delivers LSP events as workspace.LSPEvent, which the
// shared wire package does not know, so they are mapped here. Values that
// are not wire events (TUI-only messages such as util.InfoMsg, or
// workspace.HostThemeEvent) are skipped without logging: wire.Encode
// reports them as unrecognized instead of warning like wire.Wrap.
func encode(ev any) (string, json.RawMessage, bool) {
	if e, ok := ev.(pubsub.Event[workspace.LSPEvent]); ok {
		raw, err := json.Marshal(pubsub.Event[proto.LSPEvent]{
			Type: e.Type,
			Payload: proto.LSPEvent{
				Type:            proto.LSPEventType(e.Payload.Type),
				Name:            e.Payload.Name,
				State:           e.Payload.State,
				Error:           e.Payload.Error,
				DiagnosticCount: e.Payload.DiagnosticCount,
			},
		})
		if err != nil {
			return "", nil, false
		}
		return pubsub.PayloadTypeLSPEvent, raw, true
	}
	p, ok := wire.Encode(ev)
	if !ok || p == nil {
		return "", nil, false
	}
	if p.Type == pubsub.PayloadTypeMessage {
		return p.Type, withoutAttachmentData(p.Payload), true
	}
	return p.Type, p.Payload, true
}

// withoutAttachmentData removes the base64 attachment bytes from a message
// payload, keeping the path and MIME type the browser renders. The gateway
// sends whole frames, keeps them in its replay ring and resends them in
// snapshots, so repeated copies of attachment bytes would cost memory for
// data the page never shows. The wire package keeps them for the
// client/server path, which does carry attachments.
func withoutAttachmentData(payload json.RawMessage) json.RawMessage {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(payload, &env); err != nil {
		return payload
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(env["payload"], &msg); err != nil {
		return payload
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(msg["parts"], &parts); err != nil {
		return payload
	}
	changed := false
	for _, part := range parts {
		if string(part["type"]) != `"binary"` {
			continue
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(part["data"], &data); err != nil {
			continue
		}
		if _, ok := data["Data"]; !ok {
			continue
		}
		delete(data, "Data")
		trimmed, err := json.Marshal(data)
		if err != nil {
			return payload
		}
		part["data"] = trimmed
		changed = true
	}
	if !changed {
		return payload
	}
	trimmedParts, err := json.Marshal(parts)
	if err != nil {
		return payload
	}
	msg["parts"] = trimmedParts
	out, err := json.Marshal(msg)
	if err != nil {
		return payload
	}
	env["payload"] = out
	out, err = json.Marshal(env)
	if err != nil {
		return payload
	}
	return out
}

// innerPayload returns only the entity of a wire event, so snapshot
// entries have exactly the shape of live event payloads.
func innerPayload(ev any) json.RawMessage {
	_, raw, ok := encode(ev)
	if !ok {
		return nil
	}
	var inner struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &inner); err != nil {
		return nil
	}
	return inner.Payload
}

// entities returns the inner payload of every entity, in order. The event
// type is irrelevant to the inner payload, so created is used.
func entities[T any](xs []T) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(xs))
	for _, x := range xs {
		if raw := innerPayload(pubsub.Event[T]{Type: pubsub.CreatedEvent, Payload: x}); raw != nil {
			out = append(out, raw)
		}
	}
	return out
}
