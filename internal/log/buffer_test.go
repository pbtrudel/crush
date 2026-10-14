package log

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type captureHandler struct {
	mu      sync.Mutex
	level   slog.Level
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

func TestBufferHandlerReplaysRecordsInOrder(t *testing.T) {
	t.Parallel()

	buf := newBufferHandler()
	src := slog.New(buf)
	src.Warn("provider missing key", "provider", "openai")
	src.Info("loaded config")

	target := &captureHandler{level: slog.LevelInfo}
	buf.replay(slog.New(target))

	require.Len(t, target.records, 2)
	require.Equal(t, slog.LevelWarn, target.records[0].Level)
	require.Equal(t, "provider missing key", target.records[0].Message)
	require.Equal(t, slog.LevelInfo, target.records[1].Level)
	require.Equal(t, "loaded config", target.records[1].Message)
}

func TestBufferHandlerReplayRespectsTargetLevel(t *testing.T) {
	t.Parallel()

	buf := newBufferHandler()
	src := slog.New(buf)
	src.Debug("debug detail")
	src.Warn("warning kept")

	target := &captureHandler{level: slog.LevelWarn}
	buf.replay(slog.New(target))

	require.Len(t, target.records, 1)
	require.Equal(t, "warning kept", target.records[0].Message)
}

func TestBufferHandlerReplayEmptiesBuffer(t *testing.T) {
	t.Parallel()

	buf := newBufferHandler()
	slog.New(buf).Warn("once")

	target := &captureHandler{level: slog.LevelInfo}
	buf.replay(slog.New(target))
	buf.replay(slog.New(target))

	require.Len(t, target.records, 1, "records must not replay twice")
}

func TestBufferHandlerWithAttrsAndGroup(t *testing.T) {
	t.Parallel()

	buf := newBufferHandler()
	h := buf.
		WithAttrs([]slog.Attr{slog.String("component", "config")}).
		WithGroup("load")
	src := slog.New(h)
	src.Warn("merged", "extra", "value")

	target := &captureHandler{level: slog.LevelInfo}
	buf.replay(slog.New(target))

	require.Len(t, target.records, 1)
	attrs := map[string]string{}
	target.records[0].Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	require.Equal(t, "config", attrs["component"], "attrs added before WithGroup stay at the parent scope")
	require.Equal(t, "value", attrs["load.extra"], "attrs added after WithGroup are prefixed")
}
