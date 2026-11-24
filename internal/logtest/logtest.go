// Package logtest captures the default logger's output in tests, so a
// test can prove a secret never reached a log line.
package logtest

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
)

// Buffer collects log output written from several goroutines.
type Buffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Capture routes the default logger into a buffer at debug level for the
// rest of the test. Tests that call it must not run in parallel.
func Capture(t testing.TB) *Buffer {
	t.Helper()
	b := &Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return b
}
