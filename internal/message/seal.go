package message

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// partsSeal caches the JSON encoding of a message's parts across debounced
// flushes, so a streamed turn re-escapes only the text that arrived since
// the last flush instead of re-escaping the whole document on every tick.
//
// The invariant is absolute: encode's output is byte-identical to
// marshalParts for the same parts. Everything uncertain falls back to a
// fresh encode of the part — a changed sibling field, a replaced or
// reordered part, a value that is not an append-only extension of the
// cached one, or a delta that does not end on a complete rune. A stale or
// wrong cache can therefore cost time but can never corrupt a stored
// message.
//
// Why per part rather than "seal everything but the last part": the
// streaming callbacks merge each text delta into the existing TextContent
// part wherever it sits, so during a tool-using turn the growing part is
// usually NOT the last one (reasoning, then text, then tool calls).
type partsSeal struct {
	entries []sealEntry
}

// sealEntry is the cached encoding of one part whose value grows by
// appending. head and tail are the static bytes around the growing value,
// raw is the field value those bytes cover, and esc is its JSON encoding.
type sealEntry struct {
	head []byte
	tail []byte
	raw  string
	esc  []byte
}

// sealSentinel is a pure-alphanumeric marker planted in the growing field
// to locate it inside the marshaled part. Its shape guarantees it survives
// JSON escaping verbatim.
const sealSentinel = "CrushSealSentinel0123456789"

// growingSpec describes a part whose single string field only ever grows
// by appending during a stream.
type growingSpec struct {
	value func(ContentPart) string
	with  func(ContentPart, string) ContentPart
}

func growingOf(part ContentPart) (growingSpec, bool) {
	switch part.(type) {
	case TextContent:
		return growingSpec{
			value: func(p ContentPart) string { return p.(TextContent).Text },
			with: func(p ContentPart, v string) ContentPart {
				c := p.(TextContent)
				c.Text = v
				return c
			},
		}, true
	case ReasoningContent:
		return growingSpec{
			value: func(p ContentPart) string { return p.(ReasoningContent).Thinking },
			with: func(p ContentPart, v string) ContentPart {
				c := p.(ReasoningContent)
				c.Thinking = v
				return c
			},
		}, true
	case ToolCall:
		return growingSpec{
			value: func(p ContentPart) string { return p.(ToolCall).Input },
			with: func(p ContentPart, v string) ContentPart {
				c := p.(ToolCall)
				c.Input = v
				return c
			},
		}, true
	}
	return growingSpec{}, false
}

func (s *partsSeal) reset() { s.entries = s.entries[:0] }

// encode appends the JSON array encoding of parts to dst, reusing cached
// escaped prefixes where it can prove they are still exact.
func (s *partsSeal) encode(dst []byte, parts []ContentPart) ([]byte, error) {
	if len(s.entries) != len(parts) {
		s.entries = make([]sealEntry, len(parts))
	}

	dst = append(dst, '[')
	for i, part := range parts {
		if i > 0 {
			dst = append(dst, ',')
		}
		var err error
		dst, err = s.encodePart(dst, i, part)
		if err != nil {
			return nil, err
		}
	}
	return append(dst, ']'), nil
}

// encodePart writes one part wrapper. Parts whose growing field cannot be
// reused fall through to a plain marshal of the whole part.
func (s *partsSeal) encodePart(dst []byte, i int, part ContentPart) ([]byte, error) {
	full := func() ([]byte, error) {
		typ, ok := partTypeOf(part)
		if !ok {
			return nil, fmt.Errorf("unknown part type: %T", part)
		}
		b, err := json.Marshal(partWrapper{Type: typ, Data: part})
		if err != nil {
			return nil, err
		}
		return append(dst, b...), nil
	}

	g, grows := growingOf(part)
	if !grows {
		return full()
	}
	typ, ok := partTypeOf(part)
	if !ok {
		return nil, fmt.Errorf("unknown part type: %T", part)
	}

	// Locate the growing value inside the part by planting a sentinel in
	// it. The probe costs one small marshal of the part minus its growing
	// text, and it removes any need to hand-replicate the field layout or
	// omitempty rules of the part struct.
	probe, err := json.Marshal(partWrapper{Type: typ, Data: g.with(part, sealSentinel)})
	if err != nil {
		return nil, err
	}
	at := bytes.Index(probe, []byte(sealSentinel))
	if at < 0 || bytes.Contains(probe[at+len(sealSentinel):], []byte(sealSentinel)) {
		// Ambiguous or absent marker: nothing to reuse.
		return full()
	}
	head, tail := probe[:at], probe[at+len(sealSentinel):]

	value := g.value(part)
	e := &s.entries[i]
	reusable := len(e.head) > 0 &&
		bytes.Equal(e.head, head) &&
		bytes.Equal(e.tail, tail) &&
		len(value) >= len(e.raw) &&
		value[:len(e.raw)] == e.raw

	if !reusable {
		// One escape covers the whole value: the output uses it directly,
		// and the cache takes it minus any bytes a truncated trailing
		// sequence contributes.
		esc, err := escapeJSONString(value)
		if err != nil {
			return nil, err
		}
		e.head = append(e.head[:0], head...)
		e.tail = append(e.tail[:0], tail...)
		if trunc := truncatedRuneLen(value); trunc == 0 {
			e.raw, e.esc = value, esc
		} else {
			oneoff, err := escapeJSONString(value[len(value)-trunc:])
			if err != nil {
				return nil, err
			}
			e.raw = value[:len(value)-trunc]
			e.esc = esc[:len(esc)-len(oneoff)]
		}
		dst = append(dst, head...)
		dst = append(dst, esc...)
		return append(dst, tail...), nil
	}

	// Append-only: escape just the bytes that arrived since the last
	// flush. A delta can end inside a rune, so those trailing bytes are
	// escaped for this flush but left out of the cache.
	delta := value[len(e.raw):]
	trunc := truncatedRuneLen(delta)
	if stable := len(delta) - trunc; stable > 0 {
		escaped, err := escapeJSONString(delta[:stable])
		if err != nil {
			return nil, err
		}
		e.esc = append(e.esc, escaped...)
		e.raw = value[:len(value)-trunc]
	}
	var trailing []byte
	if trunc > 0 {
		trailing, err = escapeJSONString(delta[len(delta)-trunc:])
		if err != nil {
			return nil, err
		}
	}

	dst = append(dst, head...)
	dst = append(dst, e.esc...)
	dst = append(dst, trailing...)
	return append(dst, tail...), nil
}

// escapeJSONString returns the JSON encoding of s without its surrounding
// quotes. Encoding the value with the stdlib keeps escaping rules —
// HTML-unsafe runes, U+2028/2029, control bytes, replacement of invalid
// input — exactly in sync with marshalParts.
func escapeJSONString(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return b[1 : len(b)-1], nil
}

// truncatedRuneLen reports how many bytes at the end of s form an
// incomplete UTF-8 sequence. Those bytes cannot be cached: json escapes a
// truncated sequence as one U+FFFD per byte today, but the append that
// completes it turns the same bytes into the real rune, so a cached
// escape would be wrong. Bytes that are simply invalid (a stray lead
// byte with no valid continuation, or 0xF8-0xFF) can never be completed
// and are safe to cache.
func truncatedRuneLen(s string) int {
	if s == "" {
		return 0
	}
	if r, size := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || size != 1 {
		return 0
	}
	// The last rune did not decode. Walk back at most UTFMax-1 bytes to
	// the lead byte and compare the sequence it promises against what is
	// actually present.
	for i := len(s) - 1; i >= 0 && len(s)-i <= utf8.UTFMax; i-- {
		b := s[i]
		if utf8.RuneStart(b) {
			need := 1
			switch {
			case b >= 0xC0 && b <= 0xDF:
				need = 2
			case b >= 0xE0 && b <= 0xEF:
				need = 3
			case b >= 0xF0 && b <= 0xF7:
				need = 4
			default:
				return 0
			}
			if avail := len(s) - i; avail < need {
				return avail
			}
			return 0
		}
	}
	return 0
}

// partTypeOf maps a content part to its wire type tag.
func partTypeOf(part ContentPart) (partType, bool) {
	switch part.(type) {
	case ReasoningContent:
		return reasoningType, true
	case TextContent:
		return textType, true
	case ImageURLContent:
		return imageURLType, true
	case BinaryContent:
		return binaryType, true
	case ToolCall:
		return toolCallType, true
	case ToolResult:
		return toolResultType, true
	case Finish:
		return finishType, true
	case ShellCommand:
		return shellCommandType, true
	case CompactionStats:
		return compactionStatsType, true
	}
	return "", false
}
