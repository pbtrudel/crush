package message

import (
	"slices"
	"strings"
	"testing"
)

// The seal is an optimization only: every encoding it produces MUST be
// byte-identical to marshalParts. These tests drive the streaming
// mutation sequences the agent actually performs and assert equality at
// every step, so a stale or wrongly-reused cache is a test failure, not
// a corrupted session row.

func encodeEqual(t *testing.T, seal *partsSeal, parts []ContentPart) {
	t.Helper()
	want, err := marshalParts(parts)
	if err != nil {
		t.Fatalf("marshalParts: %v", err)
	}
	got, err := seal.encode(nil, parts)
	if err != nil {
		t.Fatalf("seal.encode: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("seal.encode != marshalParts\n got: %s\nwant: %s", trunc(string(got)), trunc(string(want)))
	}
}

func trunc(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func TestPartsSeal_StreamedTextMatchesMarshal(t *testing.T) {
	t.Parallel()

	var seal partsSeal
	var msg Message
	msg.Role = Assistant

	for range 40 {
		msg.AppendContent("hello world <b>&amp;</b> \"quoted\" \\slash\\")
		encodeEqual(t, &seal, msg.Parts)
	}
}

// A streamed reasoning section keeps growing at index 0 while later
// parts are appended after it, so the growing part is NOT the last part.
// The seal must still reuse it.
func TestPartsSeal_GrowingPartIsNotLast(t *testing.T) {
	t.Parallel()

	var seal partsSeal
	var msg Message
	msg.Role = Assistant

	msg.AppendReasoningContent("thinking about ")
	encodeEqual(t, &seal, msg.Parts)
	msg.AppendReasoningContent("the problem")
	encodeEqual(t, &seal, msg.Parts)
	msg.AppendContent("answer part one ")
	encodeEqual(t, &seal, msg.Parts)

	msg.AddToolCall(ToolCall{ID: "call_1", Name: "bash", Input: "{\"comma"})
	encodeEqual(t, &seal, msg.Parts)
	msg.AppendToolCallInput("call_1", "nd\": true}")
	encodeEqual(t, &seal, msg.Parts)
	msg.FinishToolCall("call_1")
	encodeEqual(t, &seal, msg.Parts)

	// Text keeps streaming after a tool call: index 1 grows while index 2
	// is already sealed.
	msg.AppendContent("answer part two")
	encodeEqual(t, &seal, msg.Parts)

	msg.AddFinish(FinishReasonEndTurn, "", "")
	encodeEqual(t, &seal, msg.Parts)
}

// Escaping must match the stdlib for the characters that are easy to get
// wrong in a hand-rolled encoder, including runes split across deltas.
func TestPartsSeal_AdversarialContent(t *testing.T) {
	t.Parallel()

	chunks := []string{
		"a\u2028b\u2029c",           // line/paragraph separators
		"\x00\x01\x1f",              // control characters
		"tab\nnewline\r",            // named escapes
		"e\u0301 and 🚀 emoji",       // combining marks, astral plane
		"<script>alert(1)</script>", // HTML escapes
		"back\\slash \"quote\"",
		"\xff\xfe invalid utf-8 → ok",
		"split: " + "\u00e9", // precomposed
	}

	for _, chunk := range chunks {
		var seal partsSeal
		var msg Message
		msg.Role = Assistant
		// Split each chunk one byte at a time to exercise incomplete
		// rune boundaries between flushes.
		for i := range len(chunk) {
			msg.AppendContent(string(chunk[i : i+1]))
			encodeEqual(t, &seal, msg.Parts)
		}
	}
}

// Non-append mutations (Hidden flip, streamed-content reset, re-added
// finish) must fall back cleanly and still match.
func TestPartsSeal_NonAppendMutationsFallBack(t *testing.T) {
	t.Parallel()

	var seal partsSeal
	var msg Message
	msg.Role = Assistant
	msg.AppendContent("streaming text")
	encodeEqual(t, &seal, msg.Parts)

	msg.ResetStreamedContent()
	encodeEqual(t, &seal, msg.Parts)

	msg.AppendContent("after reset")
	encodeEqual(t, &seal, msg.Parts)

	msg.Parts = []ContentPart{TextContent{Text: "prefix", Hidden: true}}
	encodeEqual(t, &seal, msg.Parts)

	// Same text, different Hidden: tail bytes differ, cache must not be
	// reused naively.
	msg.Parts = []ContentPart{TextContent{Text: "prefix", Hidden: false}}
	encodeEqual(t, &seal, msg.Parts)

	// Binary and image parts are never cached; make sure they still
	// encode through the seal.
	msg.Parts = append(msg.Parts,
		BinaryContent{Path: "/tmp/x", MIMEType: "image/png", Data: []byte{0x89, 'P', 0x00, 0xff}},
		ImageURLContent{URL: "https://example.com/a.png", Detail: "auto"},
		CompactionStats{TokensBefore: 1, SavedTokens: 2},
		ShellCommand{Command: "ls", Output: "a\nb", ExitCode: 0},
	)
	encodeEqual(t, &seal, msg.Parts)
}

// Mixed message: streamed text followed by a big immutable tool-result
// style part, then more streaming.
func TestPartsSeal_InterleavedImmutableParts(t *testing.T) {
	t.Parallel()

	var seal partsSeal
	var msg Message
	msg.Role = Assistant
	msg.AddToolResult(ToolResult{ToolCallID: "call_1", Content: strings.Repeat("x", 5000)})
	encodeEqual(t, &seal, msg.Parts)
	msg.AppendContent("tail ")
	encodeEqual(t, &seal, msg.Parts)
	msg.AppendContent("tail more")
	encodeEqual(t, &seal, msg.Parts)
}
