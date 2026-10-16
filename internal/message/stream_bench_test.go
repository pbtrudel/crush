package message

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

// newBenchService mirrors newTestService for benchmarks: a fresh SQLite
// backstore plus one session to attach messages to.
func newBenchService(b *testing.B, opts ...ServiceOption) (Service, string) {
	b.Helper()
	conn, err := db.Connect(b.Context(), b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	q := db.New(conn)
	sessions := session.NewService(q, conn)
	sess, err := sessions.Create(b.Context(), "bench")
	if err != nil {
		b.Fatal(err)
	}
	return NewService(q, opts...), sess.ID
}

// BenchmarkStreamAppendText measures the per-delta cost of accumulating
// streamed text into the live assistant message and handing it to
// Service.Update with no SQL writes in the loop (debounce far in the
// future). This is the hot path: it runs once per streamed token.
func BenchmarkStreamAppendText(b *testing.B) {
	const delta = "the quick brown fox " // ~20 bytes per token
	svc, sessionID := newBenchService(b, WithDebounce(time.Hour))
	ctx := context.Background()

	msg, err := svc.Create(ctx, sessionID, CreateMessageParams{Role: Assistant})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		msg.AppendContent(delta)
		if err := svc.Update(ctx, msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStreamFlush measures the cost of one debounced flush tick —
// marshal of every part + SQL write + publish — against a message that
// has accumulated ~30 KB of streamed text.
func BenchmarkStreamFlush(b *testing.B) {
	// Debounce 0 makes every Update a synchronous flush: the loop below
	// measures exactly one marshal + SQL write + publish per iteration.
	svc, sessionID := newBenchService(b, WithDebounce(0))
	ctx := context.Background()

	msg, err := svc.Create(ctx, sessionID, CreateMessageParams{Role: Assistant})
	if err != nil {
		b.Fatal(err)
	}
	var sb strings.Builder
	for sb.Len() < 30_000 {
		sb.WriteString("the quick brown fox ")
	}
	msg.AppendContent(sb.String())
	if err := svc.Update(ctx, msg); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		msg.AppendContent("x")
		if err := svc.Update(ctx, msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAppendOnly isolates the cost of merging a delta into the live
// message at a realistic accumulated size (no Update, no SQL).
func BenchmarkAppendOnly(b *testing.B) {
	var sb strings.Builder
	for sb.Len() < 30_000 {
		sb.WriteString("the quick brown fox ")
	}
	msg := Message{Role: Assistant}
	msg.AppendContent(sb.String())

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		msg.AppendContent("the quick brown fox ")
	}
}

// BenchmarkUpdateNoFlush isolates Clone + pending bookkeeping: the work
// Service.Update does per token when the debounce timer never fires.
func BenchmarkUpdateNoFlush(b *testing.B) {
	svc, sessionID := newBenchService(b, WithDebounce(time.Hour))
	ctx := context.Background()
	msg, err := svc.Create(ctx, sessionID, CreateMessageParams{Role: Assistant})
	if err != nil {
		b.Fatal(err)
	}
	var sb strings.Builder
	for sb.Len() < 30_000 {
		sb.WriteString("the quick brown fox ")
	}
	msg.AppendContent(sb.String())

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		msg.AppendContent("x")
		if err := svc.Update(ctx, msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMarshalOnly isolates part serialization for a 30 KB message.
func BenchmarkMarshalOnly(b *testing.B) {
	var sb strings.Builder
	for sb.Len() < 30_000 {
		sb.WriteString("the quick brown fox ")
	}
	parts := []ContentPart{TextContent{Text: sb.String()}}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := marshalParts(parts); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWriteOnly isolates the SQL UPDATE itself (marshal + write, no
// publish, no debounce bookkeeping).
func BenchmarkWriteOnly(b *testing.B) {
	svc, sessionID := newBenchService(b, WithDebounce(time.Hour))
	ctx := context.Background()
	msg, err := svc.Create(ctx, sessionID, CreateMessageParams{Role: Assistant})
	if err != nil {
		b.Fatal(err)
	}
	var sb strings.Builder
	for sb.Len() < 30_000 {
		sb.WriteString("the quick brown fox ")
	}
	msg.Parts = []ContentPart{TextContent{Text: sb.String()}}

	s := svc.(*service)
	// A single pending state across iterations is the streaming steady
	// state: the first write primes the seal, the rest reuse it.
	p := &pendingState{}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := s.write(ctx, p, msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWriteCold is the pre-seal baseline: a fresh encoder per flush
// re-escapes the whole document every tick.
func BenchmarkWriteCold(b *testing.B) {
	svc, sessionID := newBenchService(b, WithDebounce(time.Hour))
	ctx := context.Background()
	msg, err := svc.Create(ctx, sessionID, CreateMessageParams{Role: Assistant})
	if err != nil {
		b.Fatal(err)
	}
	var sb strings.Builder
	for sb.Len() < 30_000 {
		sb.WriteString("the quick brown fox ")
	}
	msg.Parts = []ContentPart{TextContent{Text: sb.String()}}

	s := svc.(*service)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		// Fresh state per write: the shape of a message that is only ever
		// flushed once (user, tool, summary rows).
		if err := s.write(ctx, &pendingState{}, msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPublishOnly isolates broker fan-out cost for one message
// snapshot with a single attached subscriber, which is what the TUI adds.
func BenchmarkPublishOnly(b *testing.B) {
	svc, sessionID := newBenchService(b, WithDebounce(time.Hour))
	ctx := context.Background()
	msg, err := svc.Create(ctx, sessionID, CreateMessageParams{Role: Assistant})
	if err != nil {
		b.Fatal(err)
	}
	var sb strings.Builder
	for sb.Len() < 30_000 {
		sb.WriteString("the quick brown fox ")
	}
	msg.Parts = []ContentPart{TextContent{Text: sb.String()}}

	s := svc.(*service)
	sub := s.Subscribe(ctx)
	go func() {
		for range sub {
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s.Publish(pubsub.UpdatedEvent, msg)
	}
}

// BenchmarkSealEncode isolates encoding cost: warm seal against a
// growing document versus the unsealed marshal, with no SQL in the loop.
func BenchmarkSealEncode(b *testing.B) {
	var sb strings.Builder
	for sb.Len() < 30_000 {
		sb.WriteString("the quick brown fox ")
	}
	var seal partsSeal
	parts := []ContentPart{TextContent{Text: sb.String()}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		parts[0] = TextContent{Text: sb.String()}
		if _, err := seal.encode(nil, parts); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSealEncodeGrowing is the streaming shape: every flush adds a
// delta to the growing text part.
func BenchmarkSealEncodeGrowing(b *testing.B) {
	var seal partsSeal
	msg := Message{Role: Assistant}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.AppendContent("the quick brown fox ")
		if _, err := seal.encode(nil, msg.Parts); err != nil {
			b.Fatal(err)
		}
	}
}
