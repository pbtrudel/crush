package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepetitionDetector(t *testing.T) {
	t.Parallel()

	t.Run("long run of CJK is detected", func(t *testing.T) {
		t.Parallel()
		d := newRepetitionDetector()
		safe, detected := d.Observe("answer: " + strings.Repeat("哈", repetitionMaxRun+10))
		require.True(t, detected)
		require.Equal(t, len([]rune("answer: ")), safe)
	})

	t.Run("same character separated by spaces is detected", func(t *testing.T) {
		t.Parallel()
		d := newRepetitionDetector()
		_, detected := d.Observe(strings.Repeat(strings.Repeat("姩", 57)+" ", 12))
		require.True(t, detected)
	})

	t.Run("long run of dashes is allowed", func(t *testing.T) {
		t.Parallel()
		d := newRepetitionDetector()
		_, detected := d.Observe(strings.Repeat("-", repetitionMaxRun+100))
		require.False(t, detected)
	})

	t.Run("long run of spaces is allowed", func(t *testing.T) {
		t.Parallel()
		d := newRepetitionDetector()
		_, detected := d.Observe(strings.Repeat(" ", repetitionMaxRun+100))
		require.False(t, detected)
	})

	t.Run("repeated short pattern is detected", func(t *testing.T) {
		t.Parallel()
		d := newRepetitionDetector()
		_, detected := d.Observe(strings.Repeat("哈喽", repetitionPatternMin+10))
		require.True(t, detected)
	})

	t.Run("normal prose is not detected", func(t *testing.T) {
		t.Parallel()
		d := newRepetitionDetector()
		_, detected := d.Observe(strings.Repeat("The quick brown fox jumps over the lazy dog. ", 100))
		require.False(t, detected)
	})

	t.Run("detection is sticky", func(t *testing.T) {
		t.Parallel()
		d := newRepetitionDetector()
		_, detected := d.Observe(strings.Repeat("x", repetitionMaxRun+1))
		require.True(t, detected)
		_, detected = d.Observe("more")
		require.True(t, detected)
	})
}
