package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// BenchmarkReadUnderWriteLoad measures the latency of the snapshot reads
// the TUI performs (list sessions, list a session's messages) while the
// message service hammers the same single-writer connection at its ~30 Hz
// debounce rate. If reads stall, splitting readers onto their own
// connection is worth its risk; if they do not, it is not.
func BenchmarkReadUnderWriteLoad(b *testing.B) {
	ctx := context.Background()
	dataDir := b.TempDir()
	conn, err := Connect(ctx, dataDir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = Release(dataDir) })

	q := New(conn)
	sess, err := q.CreateSession(ctx, CreateSessionParams{ID: uuid.New().String(), Title: "bench"})
	if err != nil {
		b.Fatal(err)
	}
	body := `[{"type":"text","data":{"text":"` + strings.Repeat("payload ", 300) + `"}}]`
	for range 200 {
		if _, err := q.CreateMessage(ctx, CreateMessageParams{
			ID: uuid.New().String(), SessionID: sess.ID, Role: "assistant",
			Parts: body, Model: sql.NullString{String: "m", Valid: true},
			Provider: sql.NullString{String: "p", Valid: true},
		}); err != nil {
			b.Fatal(err)
		}
	}

	stop := make(chan struct{})
	writes := make(chan time.Duration, 1024)
	go func() {
		for {
			select {
			case <-stop:
				close(writes)
				return
			default:
			}
			start := time.Now()
			_, _ = q.UpdateSession(ctx, UpdateSessionParams{
				ID: sess.ID, Title: sess.Title, PromptTokens: 10, CompletionTokens: 10,
				TotalCacheReadTokens: 10, TotalContextTokens: 10, Cost: 0.1,
			})
			select {
			case writes <- time.Since(start):
			default:
			}
			time.Sleep(33 * time.Millisecond)
		}
	}()

	b.ResetTimer()
	var worst time.Duration
	for range b.N {
		start := time.Now()
		if _, err := q.ListSessions(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := q.ListMessagesBySession(ctx, sess.ID); err != nil {
			b.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed > worst {
			worst = elapsed
		}
	}
	b.ReportMetric(float64(worst.Microseconds()), "worst-read-µs")
	close(stop)

	var writeTotal time.Duration
	var writeCount int
	for d := range writes {
		writeTotal += d
		writeCount++
	}
	if writeCount > 0 {
		b.ReportMetric(float64(writeTotal.Microseconds())/float64(writeCount), "avg-write-µs")
	}
}
