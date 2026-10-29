package agent

import (
	"strings"
	"unicode"
)

const (
	repetitionWindowRunes    = 4096
	repetitionMaxRun         = 200
	repetitionPatternMin     = 128
	repetitionPatternCheckAt = 64
)

// repetitionDetector watches streamed text for degenerate output: a long
// run of the same character, or a short pattern repeated many times. It is
// intentionally conservative — whitespace, punctuation, and digits are
// exempt because long runs of them are legitimate in code and formatting.
type repetitionDetector struct {
	window           []rune
	total            int
	lastPatternCheck int
	detected         bool
	safeRunes        int
}

func newRepetitionDetector() *repetitionDetector {
	return &repetitionDetector{window: make([]rune, 0, repetitionWindowRunes)}
}

// Observe appends a streamed delta and reports whether it completes a
// degenerate repetition. safeRunes is the number of runes before the
// repeated run, so the caller can trim the stored message back to useful
// content.
func (d *repetitionDetector) Observe(delta string) (safeRunes int, detected bool) {
	if d.detected {
		return d.safeRunes, true
	}
	runes := []rune(delta)
	d.total += len(runes)
	d.window = append(d.window, runes...)
	if len(d.window) > repetitionWindowRunes {
		d.window = d.window[len(d.window)-repetitionWindowRunes:]
	}

	if count, r, ok := trailingSameRune(d.window); ok && count >= repetitionMaxRun && !safeRepetitionRune(r) {
		d.detected = true
		d.safeRunes = max(d.total-count, 0)
		return d.safeRunes, true
	}

	if d.total-d.lastPatternCheck >= repetitionPatternCheckAt {
		d.lastPatternCheck = d.total
		if repeats, size, ok := trailingRepeatedPattern(d.window); ok && repeats >= repetitionPatternMin {
			pattern := d.window[len(d.window)-size:]
			if !safeRepetitionPattern(pattern) {
				d.detected = true
				d.safeRunes = max(d.total-size*repeats, 0)
				return d.safeRunes, true
			}
		}
	}
	return 0, false
}

// trailingSameRune returns the length of the run of identical runes at the
// end of window.
func trailingSameRune(window []rune) (int, rune, bool) {
	if len(window) == 0 {
		return 0, 0, false
	}
	last := window[len(window)-1]
	count := 0
	for i := len(window) - 1; i >= 0 && window[i] == last; i-- {
		count++
	}
	return count, last, true
}

// trailingRepeatedPattern finds a short pattern (2..8 runes) repeated at
// least repetitionPatternMin times at the end of window.
func trailingRepeatedPattern(window []rune) (repeats, size int, ok bool) {
	for size = 2; size <= 8; size++ {
		need := size * repetitionPatternMin
		if len(window) < need {
			continue
		}
		start := len(window) - need
		pattern := window[start : start+size]
		matches := true
		for i := start; i < len(window); i++ {
			if window[i] != pattern[(i-start)%size] {
				matches = false
				break
			}
		}
		if matches {
			return repetitionPatternMin, size, true
		}
	}
	return 0, 0, false
}

// safeRepetitionRune reports whether a long run of r is common enough in
// ordinary code and formatting that it should not stop the stream.
func safeRepetitionRune(r rune) bool {
	if unicode.IsSpace(r) || unicode.IsDigit(r) {
		return true
	}
	return strings.ContainsRune("-=*#/\\_.~+|:;,()[]{}<>`'\"", r)
}

// safeRepetitionPattern reports whether every rune in the pattern is exempt
// from repetition detection.
func safeRepetitionPattern(pattern []rune) bool {
	for _, r := range pattern {
		if !safeRepetitionRune(r) {
			return false
		}
	}
	return true
}
