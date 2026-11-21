// Package result holds the result line of one eval attempt and the JSONL
// log that stores the lines.
package result

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/eval/catalog"
)

// Task kinds. The pass rate counts both the same, and scores are also
// reported per kind.
const (
	KindPlain     = "plain"
	KindInterface = "interface"
)

// MaxAttempts is the number of attempts after which a key that never exited
// 0 or 2 is final as invalid.
const MaxAttempts = 3

// Tokens are the token counts of one run, by kind.
type Tokens struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
}

// Total returns the sum of every token kind.
func (t Tokens) Total() int64 {
	return t.Input + t.CacheRead + t.CacheWrite + t.Output
}

// Grade is the outcome of grading one run.
type Grade struct {
	Touched           bool `json:"touched"`
	Packages          bool `json:"packages"`
	CompileError      bool `json:"compile_error"`
	ModelTestsRemoved int  `json:"model_tests_removed"`
	// Reason says why a checkout the model broke could not be graded.
	Reason string `json:"reason,omitempty"`
}

// Passed reports whether both test passes succeeded.
func (g Grade) Passed() bool {
	return g.Touched && g.Packages && !g.CompileError
}

// Diff is the size of the change in the run's working tree.
type Diff struct {
	Files   int `json:"files"`
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
}

// Gate is what the turn-end gate did during a run (design 4.3).
type Gate struct {
	Continuations  int `json:"continuations"`
	Claims         int `json:"claims"`
	UnbackedClaims int `json:"unbacked_claims"`
}

// Edits is what the run's edit and multiedit calls did, as `crush run
// --json` reports it: the calls attempted, the refusals split by kind, the
// failures outside those kinds, and the edits applied to a file the session
// did not read first.
type Edits struct {
	Attempted        int `json:"attempted"`
	Refused          int `json:"refused"`
	NotRead          int `json:"not_read"`
	ChangedSinceRead int `json:"changed_since_read"`
	NoMatch          int `json:"no_match"`
	MultipleMatches  int `json:"multiple_matches"`
	Errors           int `json:"errors"`
	UnreadApplied    int `json:"unread_applied"`
}

// Contamination is one tool call whose input named a contamination marker:
// the markers it matched, and a short excerpt of the input. It is what lets
// a flagged run be diagnosed after its session database is gone.
type Contamination struct {
	Markers []string `json:"markers"`
	Excerpt string   `json:"excerpt"`
}

// Result is one attempt of one run. It holds no prompt, answer, file content
// or path from the session, beyond the short contamination excerpts that name
// a marker.
type Result struct {
	Schema     int       `json:"schema"`
	Batch      string    `json:"batch"`
	Suite      string    `json:"suite"`
	Task       string    `json:"task"`
	Kind       string    `json:"kind"`
	Variant    string    `json:"variant"`
	Route      string    `json:"route"`
	Family     string    `json:"family"`
	Rep        int       `json:"rep"`
	Attempt    int       `json:"attempt"`
	Started    time.Time `json:"started"`
	Exit       int       `json:"exit"`
	Reason     string    `json:"reason"`
	Build      string    `json:"build"`
	Model      string    `json:"model"`
	Provider   string    `json:"provider"`
	Steps      int       `json:"steps"`
	ToolCalls  int       `json:"tool_calls"`
	Tokens     Tokens    `json:"tokens"`
	Estimated  bool      `json:"estimated"`
	Cost       float64   `json:"cost"`
	DurationMS int64     `json:"duration_ms"`
	// TextLength is the length of the run's answer text. The text itself
	// never enters the line.
	TextLength         int `json:"text_length"`
	UndeliveredReports int `json:"undelivered_reports"`
	// Gate is what the turn-end gate did: the continuations it added, and
	// the checks the answer claimed but no tool call backed.
	Gate Gate `json:"gate"`
	// Edits is what the run's edit and multiedit calls did.
	Edits           Edits          `json:"edits"`
	TurnEnds        map[string]int `json:"turn_ends"`
	UsageLimited    bool           `json:"usage_limited"`
	Grade           Grade          `json:"grade"`
	Pass            bool           `json:"pass"`
	FalseCompletion bool           `json:"false_completion"`
	Diff            Diff           `json:"diff"`
	Worktrees       int            `json:"worktrees"`
	Contaminated    bool           `json:"contaminated"`
	// ContaminatedMatches counts the tool calls whose input named one of
	// the contamination markers, and ContaminatedEvidence names the
	// markers each of them matched, with a short excerpt of its input.
	ContaminatedMatches  int             `json:"contaminated_matches"`
	ContaminatedEvidence []Contamination `json:"contaminated_evidence,omitempty"`
	ProjectConfig        string          `json:"project_config"`
	// Network is the network mode of the run, jail or host.
	Network string `json:"network"`
	// Catalog is the sha256 of the model catalogs the run's child read from
	// the eval home, so two batches compare only on one catalog.
	Catalog catalog.Fingerprint `json:"catalog"`
	// NetDenied counts the connections the jail's proxy refused, and
	// NetDeniedHosts names them: hosts or host:port, never a path or body.
	NetDenied      int      `json:"net_denied"`
	NetDeniedHosts []string `json:"net_denied_hosts"`
}

// Key identifies a run across its attempts.
type Key struct {
	Batch   string
	Task    string
	Variant string
	Route   string
	Rep     int
}

// Key returns the run key of the line.
func (r Result) Key() Key {
	return Key{Batch: r.Batch, Task: r.Task, Variant: r.Variant, Route: r.Route, Rep: r.Rep}
}

// Final reports whether the attempt ended the run: the model exited on its
// own (0) or stopped short (2), and the attempt was not cut by a usage
// limit.
func (r Result) Final() bool {
	if r.UsageLimited {
		return false
	}
	return r.Exit == 0 || r.Exit == 2
}

// Retryable reports whether a non-final attempt is retried by the batch.
// Usage-limited attempts are not: the route pauses instead.
func (r Result) Retryable() bool {
	if r.Final() || r.UsageLimited {
		return false
	}
	return r.Exit == 1 || r.Exit == 3 || r.Reason == "watchdog" || r.Reason == "bad_output"
}

// Append adds one line to the JSONL file, creating it with mode 0o600 if
// needed. The line is written with a single write, so a crash can only
// truncate it.
func Append(path string, r Result) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	line = append(line, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open results: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("append result: %w", err)
	}
	return f.Close()
}

// ReadFile reads every result line of a JSONL file. A truncated final line
// is skipped and counted; a bad line before the last one is an error.
func ReadFile(path string) ([]Result, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("read results: %w", err)
	}
	rows := strings.Split(string(data), "\n")
	last := -1
	for i, row := range rows {
		if strings.TrimSpace(row) != "" {
			last = i
		}
	}
	var (
		lines   []Result
		ignored int
	)
	for i, row := range rows {
		if strings.TrimSpace(row) == "" {
			continue
		}
		var r Result
		if err := json.Unmarshal([]byte(row), &r); err != nil {
			if i == last {
				ignored++
				continue
			}
			return nil, ignored, fmt.Errorf("parse result line %d: %w", i+1, err)
		}
		lines = append(lines, r)
	}
	return lines, ignored, nil
}

// Final returns the final line for each run key and leaves out keys that are
// still retryable. A key whose third attempt that is not usage-limited still
// did not exit 0 or 2 is final as invalid: its last attempt is returned.
func Final(lines []Result) map[Key]Result {
	final := make(map[Key]Result)
	attempts := make(map[Key]int)
	for _, line := range lines {
		key := line.Key()
		if _, done := final[key]; done {
			continue
		}
		if line.Final() {
			final[key] = line
			continue
		}
		if line.UsageLimited {
			continue
		}
		attempts[key]++
		if attempts[key] >= MaxAttempts {
			final[key] = line
		}
	}
	return final
}
