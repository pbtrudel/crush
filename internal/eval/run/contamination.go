package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// marker is one string design 3.7 scans tool-call input for.
type marker struct {
	text string
	// command is the pattern of a marker that names a command. It matches
	// only a whole command, so "gh pr" no longer matches inside "through
	// prefix". A nil command makes the marker a substring.
	command *regexp.Regexp
	// own is a path that is cut out of the text before the marker is
	// matched. The child names its own run directory all the time, so
	// naming it is not contact with another batch; every other path under
	// the batches is.
	own string
	// needs are the repositories the whole tool call must also name, any
	// of them. A bare "gh pr create" is the recipe the checkout's own bash
	// tool description shows, not a lookup at the fork.
	needs []string
}

// commandPattern compiles the pattern of a command marker: the words of the
// marker joined by any run of whitespace, at the start of the text or after
// a separator, and followed by the end of the text or a separator. "/" is a
// separator, so "/usr/bin/gh pr" matches, while "-" and "_" join the words
// of a name, so "gh-pr" and "gh_pr" do not match.
func commandPattern(text string) *regexp.Regexp {
	words := strings.Fields(text)
	for i, word := range words {
		words[i] = regexp.QuoteMeta(word)
	}
	return regexp.MustCompile(`(^|[^a-z0-9_.-])` + strings.Join(words, `\s+`) + `($|[^a-z0-9_-])`)
}

// contaminationMarkers are the strings design 3.7 scans tool calls for: the
// task's merge commit, the fork's pull request and repository URLs, the gh
// commands that name it, git's object reader, the suite path, and the host
// paths a jailed run hides: the repository, the checkout the tasks come from,
// the worktrees beside it and the eval home's batches.
func contaminationMarkers(tk *task.Task, paths Paths) []marker {
	repos := auditRepos(tk.Source.Repo, paths.Remotes)
	markers := []marker{
		{text: "pbtrudel/crush/pull"},
		{text: "github.com/pbtrudel/crush"},
		{text: "gh pr", command: commandPattern("gh pr"), needs: repos},
		{text: "gh api", command: commandPattern("gh api"), needs: repos},
		{text: "git cat-file", command: commandPattern("git cat-file")},
	}
	if tk.Source.Merge != "" {
		markers = append(markers, marker{text: tk.Source.Merge})
	}
	seen := make(map[string]bool, len(markers))
	for _, dir := range []string{paths.Suite, paths.Repo, paths.Checkout, paths.Worktrees} {
		markers = appendPathMarker(markers, seen, dir, "")
	}
	// Only the run's own directory is cut out of the batch marker: it names
	// the neutral path a jailed run gives the child, or the child's own run
	// directory in a run on the host. Any other path under the batches counts,
	// the host path of the run's own directory included: once the child works
	// at the neutral path, nothing it is handed names the batch.
	markers = appendPathMarker(markers, seen, paths.Batches, childOwn(paths))
	for i, m := range markers {
		markers[i].text = strings.ToLower(m.text)
		markers[i].own = strings.ToLower(m.own)
		for j, name := range m.needs {
			m.needs[j] = strings.ToLower(name)
		}
	}
	return markers
}

// childOwn is the directory the child names as its own: the jail's neutral
// path for a jailed run, the run's host directory otherwise. Empty when the
// run has none to cut.
func childOwn(paths Paths) string {
	if paths.Jail != "" {
		return absPath(paths.Jail)
	}
	return absPath(paths.Own)
}

// upstreamRepo is the upstream repository this fork tracks: a pull request
// there can hold the fix a task asks for, so a gh command that names it
// counts. It is the constant fallback when the task and the checkout name no
// other repository.
const upstreamRepo = "charmbracelet/crush"

// auditRepos are the repositories a gh command may name and still count: the
// task's repository, the checkout's remotes, and the upstream repository.
func auditRepos(fork string, remotes []string) []string {
	var repos []string
	seen := make(map[string]bool, len(remotes)+2)
	for _, name := range append([]string{fork}, append(remotes, upstreamRepo)...) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		repos = append(repos, name)
	}
	return repos
}

// appendPathMarker adds dir, as an absolute path, as a marker once. It skips
// an empty path and one that another marker already names.
func appendPathMarker(markers []marker, seen map[string]bool, dir, own string) []marker {
	abs := absPath(dir)
	if abs == "" || seen[abs] {
		return markers
	}
	seen[abs] = true
	return append(markers, marker{text: abs, own: own})
}

// absPath is dir as an absolute path, or empty when it has none to give.
func absPath(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return abs
}

// Matches opens the session database at path and describes every tool call
// whose input names one of the task's contamination markers: the markers and
// a short, redacted excerpt of the input. The excerpt is the only text the
// result line keeps.
func Matches(path string, tk *task.Task, paths Paths) ([]result.Contamination, error) {
	return scanDatabase(path, contaminationMarkers(tk, paths))
}

// scanDatabase opens the run's own session database read-only and collects
// the tool calls whose input names one of the markers.
func scanDatabase(path string, markers []marker) ([]result.Contamination, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat session database: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open session database: %w", err)
	}
	defer db.Close() //nolint:errcheck // Read-only.
	rows, err := db.QueryContext(context.Background(), "SELECT parts FROM messages")
	if err != nil {
		return nil, fmt.Errorf("query session messages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // Read-only.

	var found []result.Contamination
	for rows.Next() {
		var parts string
		if err := rows.Scan(&parts); err != nil {
			return nil, err
		}
		found = append(found, contaminatedCalls(parts, markers)...)
	}
	return found, rows.Err()
}

// contaminatedCalls collects the tool calls of one message whose input names
// a marker: design 3.7 counts tool calls, not messages.
func contaminatedCalls(parts string, markers []marker) []result.Contamination {
	var list []struct {
		Type string `json:"type"`
		Data struct {
			Input string `json:"input"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(parts), &list) != nil {
		return nil
	}
	var found []result.Contamination
	for _, part := range list {
		if part.Type != "tool_call" || part.Data.Input == "" {
			continue
		}
		if names := matchedMarkers(part.Data.Input, markers); len(names) > 0 {
			found = append(found, result.Contamination{
				Markers: names,
				Excerpt: excerpt(part.Data.Input, names),
			})
		}
	}
	return found
}

// matchedMarkers is the markers one tool call's input names, in the order of
// the marker list. A marker that needs the repository's name is reported only
// when the same input names it too.
func matchedMarkers(input string, markers []marker) []string {
	texts := inputTexts(input)
	var names []string
	for _, m := range markers {
		if m.text == "" {
			continue
		}
		if len(m.needs) > 0 {
			named := false
			for _, need := range m.needs {
				for _, text := range texts {
					if strings.Contains(text, need) {
						named = true
						break
					}
				}
			}
			if !named {
				continue
			}
		}
		for _, text := range texts {
			if m.matches(text) {
				names = append(names, m.text)
				break
			}
		}
	}
	return names
}

// maxExcerpt is how much of a contaminated tool call's input the result line
// keeps. It is enough to name the command, and small enough to leave a
// results.jsonl readable.
const maxExcerpt = 200

// redactions are the shapes of a credential an excerpt must not keep: a
// bearer or basic authorization, credentials in a URL or a curl command, a
// named credential, and the token formats of the providers the suite runs.
var redactions = []struct {
	pattern *regexp.Regexp
	replace string
}{
	{regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/-]{8,}=*`), "${1}[redacted]"},
	{regexp.MustCompile(`(?i)(authorization\s*:\s*basic\s+)[a-z0-9+/=._~-]{4,}`), "${1}[redacted]"},
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`), "${1}[redacted]@"},
	{regexp.MustCompile(`(?i)((?:^|\s)(?:-u|--user)[= ]\s*)[^\s:]+:[^\s]+`), "${1}[redacted]"},
	{regexp.MustCompile(`(?i)(["']?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|secret|password|passwd)["']?\s*[:=]\s*["']?)[^\s"',}]{6,}`), "${1}[redacted]"},
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{16,}`), "[redacted]"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{16,}`), "[redacted]"},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), "[redacted]"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35,}`), "[redacted]"},
	{regexp.MustCompile(`\b(?:sk|xox[baprs])-[A-Za-z0-9_-]{16,}`), "[redacted]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16,}`), "[redacted]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), "[redacted]"},
}

// excerpt is about maxExcerpt characters of one contaminated tool call's
// input, centred on the first marker it matched, with anything that looks
// like a credential replaced.
func excerpt(input string, markers []string) string {
	text := input
	for _, redaction := range redactions {
		text = redaction.pattern.ReplaceAllString(text, redaction.replace)
	}
	if len(text) <= maxExcerpt {
		return text
	}
	start := firstMatch(text, markers) - maxExcerpt/2
	start = min(max(start, 0), len(text)-maxExcerpt)
	// Both ends move back to a rune boundary, so the excerpt stays valid
	// UTF-8 and never splits a character.
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	end := start + maxExcerpt
	for end > start && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[start:end]
}

// firstMatch is where the first of the matched markers appears in text, or 0
// when none of them is found: a command marker matches a whole command, so
// its words may not sit in the text exactly as the marker writes them.
func firstMatch(text string, markers []string) int {
	lower := strings.ToLower(text)
	at, found := 0, false
	for _, m := range markers {
		if m == "" {
			continue
		}
		i := strings.Index(lower, m)
		if i < 0 || (found && i >= at) {
			continue
		}
		at, found = i, true
	}
	return at
}

// cutOwn removes the run's own directory from text, where text names a path
// inside it. An occurrence only counts when the end of the text or a
// separator follows it, and never when "/.." follows, which climbs back out
// of the run into another directory: "pr-0010-r1" is a prefix of the sibling
// "pr-0010-r10".
func cutOwn(text, own string) string {
	if own == "" {
		return text
	}
	var out strings.Builder
	for {
		i := strings.Index(text, own)
		if i < 0 {
			out.WriteString(text)
			return out.String()
		}
		rest := text[i+len(own):]
		ends := rest == "" ||
			(os.IsPathSeparator(rest[0]) && !strings.HasPrefix(rest, string(os.PathSeparator)+".."))
		if ends {
			out.WriteString(text[:i])
		} else {
			out.WriteString(text[:i+len(own)])
		}
		text = rest
	}
}

// inputTexts is the text a marker is matched against. A tool call's input is
// the JSON object of its arguments, so its string values are decoded and
// matched one by one: an escaped newline or the closing quote of the object
// then reads as the separator it stands for. An input that is not a JSON
// object is matched as it is.
func inputTexts(input string) []string {
	var object map[string]any
	if json.Unmarshal([]byte(input), &object) != nil {
		return []string{strings.ToLower(input)}
	}
	var texts []string
	collectStrings(object, &texts)
	return texts
}

// collectStrings appends every string value of value to texts.
func collectStrings(value any, texts *[]string) {
	switch v := value.(type) {
	case string:
		*texts = append(*texts, strings.ToLower(v))
	case map[string]any:
		for _, child := range v {
			collectStrings(child, texts)
		}
	case []any:
		for _, child := range v {
			collectStrings(child, texts)
		}
	}
}

// matches reports whether text names the marker. A command marker must name a
// whole command; every other marker matches anywhere. The run's own directory
// is cut out first, so a tool call that names its own checkout matches
// nothing.
func (m marker) matches(text string) bool {
	if m.own != "" {
		text = cutOwn(text, m.own)
	}
	if m.command == nil {
		return strings.Contains(text, m.text)
	}
	return m.command.MatchString(text)
}
