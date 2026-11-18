package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestReads(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	view := func(session, id, name, input string) {
		log.step(session, newBuild, message.ToolCall{ID: id, Name: name, Input: input})
	}
	view("s1", "v1", "view", `{"file_path":"a.go"}`)
	// Pages through a.go: re-reads that set an offset or a limit.
	view("s1", "v2", "read", `{"file_path":"a.go","offset":200,"limit":100}`)
	view("s1", "v3", "view", `{"file_path":"a.go","offset":300}`)
	view("s1", "v4", "view", `{"file_path":"b.go","limit":50}`)
	// A re-read whose previous read was of another file: not paging.
	view("s1", "v5", "view", `{"file_path":"a.go","limit":400}`)
	// A re-read without an offset or a limit: not paging.
	view("s1", "v6", "view", `{"file_path":"a.go"}`)
	// The multi-file form; c.go was not read before.
	view("s1", "v7", "view", `{"file_paths":["b.go","c.go"],"limit":10}`)
	// Another session starts with nothing read.
	log.session("s2", "title")
	view("s2", "v8", "view", `{"file_path":"a.go","offset":10}`)

	reads := log.report(Options{}).Reads
	require.Equal(t, 8, reads.Calls)
	require.Equal(t, 4, reads.WithLimit)
	require.InDelta(t, 0.5, reads.LimitShare, 1e-9)
	// Lines requested: 200, 100, 200, 50, 400, 200, 10, 200.
	require.Equal(t, 200, reads.MedianLines)
	require.Equal(t, 5, reads.ReReads)
	require.Equal(t, 2, reads.Paging)
}

func TestAPathRepeatedInsideOneCallIsNotAReRead(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	view := func(session, id, name, input string) {
		log.step(session, newBuild, message.ToolCall{ID: id, Name: name, Input: input})
	}
	// The same path twice in one call names no earlier call's path, so
	// only the later call is a re-read.
	view("s1", "v1", "view", `{"file_paths":["a.go","a.go"]}`)
	view("s1", "v2", "view", `{"file_path":"a.go"}`)

	reads := log.report(Options{}).Reads
	require.Equal(t, 2, reads.Calls)
	require.Equal(t, 1, reads.ReReads)
}

func TestPercentileIsNearestRank(t *testing.T) {
	t.Parallel()

	require.Zero(t, percentile(nil, 50))
	require.Equal(t, 7, percentile([]int{7}, 90))
	require.Equal(t, 2, percentile([]int{4, 1, 3, 2}, 50))
	require.Equal(t, 4, percentile([]int{4, 1, 3, 2}, 90))
	require.Equal(t, 1, percentile([]int{4, 1, 3, 2}, 0))
}
