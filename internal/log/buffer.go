package log

import (
	"context"
	"log/slog"
	"slices"
	"sync"
)

// bufferState is the shared record store behind every derived
// bufferHandler from a single newBufferHandler call.
type bufferState struct {
	mu      sync.Mutex
	records []slog.Record
}

// bufferHandler is a slog.Handler that keeps records in memory so they can
// be replayed once the real log handlers are installed. It is used to
// retain startup diagnostics logged before the log file path (which comes
// from the config) is known.
type bufferHandler struct {
	state *bufferState
	attrs []slog.Attr
	group string
}

func newBufferHandler() *bufferHandler {
	return &bufferHandler{state: &bufferState{}}
}

func (b *bufferHandler) Enabled(context.Context, slog.Level) bool { return true }

func (b *bufferHandler) Handle(_ context.Context, r slog.Record) error {
	buffered := r.Clone()
	if b.group != "" {
		prefixed := slog.NewRecord(buffered.Time, buffered.Level, buffered.Message, buffered.PC)
		buffered.Attrs(func(a slog.Attr) bool {
			prefixed.AddAttrs(slog.Attr{Key: b.group + a.Key, Value: a.Value})
			return true
		})
		buffered = prefixed
	}
	if len(b.attrs) > 0 {
		buffered.AddAttrs(b.attrs...)
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	b.state.records = append(b.state.records, buffered)
	return nil
}

func (b *bufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return b
	}
	next := &bufferHandler{
		state: b.state,
		group: b.group,
		attrs: append(slices.Clip(b.attrs), b.prefix(attrs)...),
	}
	return next
}

func (b *bufferHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return b
	}
	next := *b
	next.group = b.group + name + "."
	return &next
}

// prefix qualifies attrs with the current group. Nested slog groups inside
// values are not expanded; early startup logs are flat key-value pairs, so
// prefixing is sufficient here.
func (b *bufferHandler) prefix(attrs []slog.Attr) []slog.Attr {
	if b.group == "" {
		return attrs
	}
	prefixed := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		prefixed[i] = slog.Attr{Key: b.group + attr.Key, Value: attr.Value}
	}
	return prefixed
}

// replay flushes the buffered records to the given logger, honoring its
// level configuration, and empties the buffer.
func (b *bufferHandler) replay(l *slog.Logger) {
	b.state.mu.Lock()
	records := b.state.records
	b.state.records = nil
	b.state.mu.Unlock()

	ctx := context.Background()
	handler := l.Handler()
	for _, r := range records {
		if handler.Enabled(ctx, r.Level) {
			_ = handler.Handle(ctx, r)
		}
	}
}
