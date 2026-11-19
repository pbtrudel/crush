package shell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestJQ_CtxCancel verifies that handleJQ polls ctx during iteration and
// returns ctx.Err() (not an interp.ExitStatus) when the context is
// cancelled. This is what lets hook timeouts interrupt long-running jq
// filters rather than waiting for the iterator to terminate naturally.
func TestJQ_CtxCancel(t *testing.T) {
	t.Parallel()

	// `range(N)` generates a large stream of values. With a slurped input
	// the filter produces all N values in sequence; ctx cancellation
	// between values should short-circuit the loop.
	const filter = "range(10000000)"
	stdin := strings.NewReader("null\n")

	ctx, cancel := context.WithCancel(t.Context())
	// Cancel almost immediately so we catch the next iteration check.
	cancel()

	err := handleJQ(ctx, []string{"jq", filter}, stdin, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected ctx cancel error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// errWriteLimit is returned by cancelAfterWriter once too many writes have
// happened. It only fires when the ctx poll in handleJQ's value loop is
// gone, and it turns a multi-second drain of range(100000000) into an
// immediate failure.
var errWriteLimit = errors.New("write limit exceeded")

// cancelAfterWriter counts Write calls and cancels ctx once a fixed number
// of them have happened, i.e. only after jq has already produced output.
// Cancellation is requested by write progress rather than by a deadline, so
// the test never depends on how long the process gets to run.
type cancelAfterWriter struct {
	cancel context.CancelFunc
	after  int
	limit  int
	writes int
}

func (w *cancelAfterWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.after {
		w.cancel()
	}
	if w.writes > w.limit {
		return 0, errWriteLimit
	}
	return len(p), nil
}

// TestJQ_CtxCancel_DuringFilter verifies cancellation mid-stream: ctx is
// cancelled after jq has started producing output, and the loop must
// observe the cancel on the next iteration rather than running to
// completion. The cancel comes from the writer, so the invariant is the
// number of writes that follow it, not elapsed wall-clock time.
func TestJQ_CtxCancel_DuringFilter(t *testing.T) {
	t.Parallel()

	const (
		// writeValue emits two Writes per value with -c (the marshalled
		// bytes, then "\n"), so cancelling on the 100th write lands after
		// jq has emitted 50 values.
		cancelAfter = 100
		writeLimit  = cancelAfter + 1000
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := &cancelAfterWriter{
		cancel: cancel,
		after:  cancelAfter,
		limit:  writeLimit,
	}
	err := handleJQ(
		ctx, []string{"jq", "-c", "range(100000000)"},
		strings.NewReader("null\n"), w, io.Discard,
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v after %d writes", err, w.writes)
	}
	// The loop polls ctx at the top of every iteration, so at most the
	// in-flight value (two Writes) is written after the cancelling one.
	if w.writes > cancelAfter+2 {
		t.Fatalf("loop kept writing after cancel: %d writes (cancel at %d)", w.writes, cancelAfter)
	}
}

// errReadLimit is returned by cancelAfterReader once too many Read calls
// have happened. Like errWriteLimit it only fires when the ctx poll that
// should stop the read is gone, which keeps that failure fast.
var errReadLimit = errors.New("read limit exceeded")

// cancelAfterReader serves fixed-size chunks from a source of a given size
// and cancels ctx once a fixed number of Read calls have happened.
// Cancellation is triggered by read progress, so io.ReadAll has already
// consumed part of the source when it is requested, and the assertion does
// not depend on scheduling. The source is never materialized, so the test
// costs nothing in proportion to its size.
type cancelAfterReader struct {
	remaining int
	chunk     int
	cancel    context.CancelFunc
	after     int
	limit     int
	reads     int
}

func (r *cancelAfterReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == r.after {
		r.cancel()
	}
	if r.reads > r.limit {
		return 0, errReadLimit
	}
	n := min(len(p), min(r.chunk, r.remaining))
	if n == 0 {
		return 0, io.EOF
	}
	for i := range p[:n] {
		p[i] = 'a'
	}
	r.remaining -= n
	return n, nil
}

// TestJQ_CtxCancel_MidReadAll verifies that ctx cancellation observed
// *during* io.ReadAll, after several chunks have already been consumed,
// short-circuits the read via ctxReader, rather than draining the whole
// source. This is the guarantee the hook runner relies on when it feeds a
// large bytes.Reader payload.
//
// The reader requests cancellation after a fixed number of Read calls, so
// the test asserts on read progress instead of elapsed time: a regression
// that stops polling ctx shows up as reads continuing past the cancel
// point, not as a slow return.
func TestJQ_CtxCancel_MidReadAll(t *testing.T) {
	t.Parallel()

	const (
		size        = 64 * 1024 * 1024 // 64 MiB
		chunk       = 512
		cancelAfter = 5
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	reader := &cancelAfterReader{
		remaining: size,
		chunk:     chunk,
		cancel:    cancel,
		after:     cancelAfter,
		limit:     cancelAfter + 1000,
	}

	err := handleJQ(ctx, []string{"jq", "-R", "."}, reader, io.Discard, io.Discard)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v after %d reads", err, reader.reads)
	}
	// ctxReader polls before every Read, so the first Read attempted after
	// the cancelling one returns ctx.Err() and the source is not read
	// again. Allow one Read of slack for io.ReadAll's own bookkeeping.
	if reader.reads > cancelAfter+1 {
		t.Fatalf("kept reading after cancel: %d reads (cancel at %d)", reader.reads, cancelAfter)
	}
	// Sanity check: we should have been cancelled mid-stream, not before
	// any reads happened. If remaining == size, cancel fired so early that
	// nothing was consumed: that's a fast-fail path, not the mid-read
	// guarantee we want to verify.
	consumed := size - reader.remaining
	if consumed == 0 {
		t.Fatal("reader was never read from; test did not exercise mid-ReadAll cancel")
	}
	if consumed >= size {
		t.Fatal("reader was fully drained; cancel was not observed mid-read")
	}
}

// failOnReadReader fails the test if Read is ever called. It proves that a
// code path short-circuited before touching the input, without relying on
// wall-clock timing.
type failOnReadReader struct {
	t *testing.T
}

func (r failOnReadReader) Read(p []byte) (int, error) {
	r.t.Error("Read called; outer guard did not short-circuit before io.ReadAll")
	return 0, io.EOF
}

// TestJQ_CtxCancel_PreCancel verifies the fast-fail path: a ctx already
// cancelled before handleJQ is called returns context.Canceled
// immediately via the outer-loop guard, never entering io.ReadAll.
// Complements TestJQ_CtxCancel_MidReadAll.
//
// The invariant — not wall-clock timing — is what proves the guard fired:
// the input reader fails the test if it is ever read from. A previous
// version asserted a 100ms ceiling, which measured scheduler latency rather
// than the guard and flaked on contended Windows CI runners under -race.
func TestJQ_CtxCancel_PreCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := handleJQ(ctx, []string{"jq", "-R", "."},
		failOnReadReader{t: t},
		io.Discard, io.Discard)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestJQ_Success confirms the ctx-aware refactor did not regress the
// success path.
func TestJQ_Success(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := handleJQ(
		t.Context(),
		[]string{"jq", "-c", ".a"},
		strings.NewReader(`{"a":1}`),
		&stdout, io.Discard,
	)
	if err != nil {
		t.Fatalf("handleJQ returned error: %v", err)
	}
	if got := stdout.String(); got != "1\n" {
		t.Fatalf("stdout = %q, want %q", got, "1\n")
	}
}
