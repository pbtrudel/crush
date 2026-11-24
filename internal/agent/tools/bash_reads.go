package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// shellReadCommands are the commands a read through bash may consist of. Each
// one prints file contents to stdout; a command outside the set could also
// write, so a step that uses one is not counted.
var shellReadCommands = map[string]struct{}{
	"cat":  {},
	"head": {},
	"tail": {},
	"nl":   {},
	"sed":  {},
}

// shellReadPaths returns the absolute paths of the files a command only
// reads, so an inspection through bash counts as a read for the edit guard
// the way view does. It answers nil whenever the command could write: a
// redirection, a background or negated statement, a command outside
// shellReadCommands, a word that is not fully literal, a directory change, or
// a parse error. It fails closed at every step; a missed read costs one
// refusal, while a read a writing command never made would silence the
// guard.
func shellReadPaths(command, workingDir string) []string {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) == 0 {
		return nil
	}

	var paths []string
	if !collectReadPaths(file.Stmts, &paths) {
		return nil
	}

	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workingDir, path)
		}
		// A directory is not a file the edit guard tracks; a path that does
		// not exist yet is harmless to record.
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			continue
		}
		out = append(out, filepath.Clean(path))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// collectReadPaths walks the statements of a parsed command, appending the
// file operands it only reads. It reports false as soon as the command could
// write, so a partial list is never returned.
func collectReadPaths(stmts []*syntax.Stmt, paths *[]string) bool {
	for _, stmt := range stmts {
		if !collectReadPathsFromStmt(stmt, paths) {
			return false
		}
	}
	return true
}

// collectReadPathsFromStmt handles one statement at any depth. The checks run
// on every statement, not just the top level: `cat f && cat x > f` and
// `cat f | cat > f` put the redirection on an inner statement, which would
// otherwise hide a write behind the reads beside it.
func collectReadPathsFromStmt(stmt *syntax.Stmt, paths *[]string) bool {
	if stmt == nil || stmt.Cmd == nil {
		return false
	}
	// A redirection may rewrite one of the files, and a detached
	// statement outlives the run that would record the read.
	if len(stmt.Redirs) > 0 || stmt.Background || stmt.Coprocess || stmt.Negated || stmt.Disown {
		return false
	}
	// A chain of read-only commands stays read-only, so a pipeline is judged
	// by its parts.
	if bin, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
		switch bin.Op {
		case syntax.AndStmt, syntax.OrStmt, syntax.Pipe, syntax.PipeAll:
			return collectReadPathsFromStmt(bin.X, paths) && collectReadPathsFromStmt(bin.Y, paths)
		default:
			return false
		}
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return false
	}
	return collectReadPathsFromCall(call, paths)
}

// collectReadPathsFromCall handles one command word and its arguments. Only a
// fully literal argv qualifies: an expansion could name a different file than
// the text suggests, which is how a read of one file would cover a write to
// another.
func collectReadPathsFromCall(call *syntax.CallExpr, paths *[]string) bool {
	if len(call.Assigns) > 0 || len(call.Args) == 0 {
		return false
	}
	argv, ok := literalArgs(call.Args)
	if !ok || len(argv) == 0 {
		return false
	}

	name := filepath.Base(argv[0])
	if _, known := shellReadCommands[name]; !known {
		return false
	}
	args := argv[1:]
	if name == "sed" {
		return collectSedReadPaths(args, paths)
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			// A flag that takes its value in the next word would otherwise
			// make that value look like a file name.
			if takesSeparateValue(name, arg) {
				i++
			}
			continue
		}
		*paths = append(*paths, arg)
	}
	return true
}

// separatedValueFlags are the flags of the read-only commands that read
// their value from the next word rather than from the same one.
var separatedValueFlags = map[string][]string{
	"head": {"-n", "-c", "--lines", "--bytes"},
	"tail": {"-n", "-c", "--lines", "--bytes"},
	"nl":   {"-b", "-d", "-f", "-h", "-i", "-l", "-n", "-s", "-v", "-w"},
}

// takesSeparateValue reports whether a flag of command takes its value in the
// next word.
func takesSeparateValue(command, flag string) bool {
	if strings.Contains(flag, "=") {
		return false
	}
	for _, known := range separatedValueFlags[command] {
		if flag == known {
			return true
		}
		if !strings.HasPrefix(known, "--") && strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") {
			// A cluster such as -bn ends with a flag that takes a value.
			if strings.HasSuffix(flag, known[1:]) {
				return true
			}
		}
	}
	return false
}

// printOnlySedScript matches a sed command that only prints lines: an
// optional line number or $, an optional range, and p. Every other sed
// command can write (w, W, e), substitute (s, y), or decide from a pattern
// this analysis does not model, so it is refused.
var printOnlySedScript = regexp.MustCompile(`^\s*(\$|\d+)?(,(\$|\d+))?p\s*$`)

// isPrintOnlySedScript reports whether every ;-separated part of the script
// only prints lines.
func isPrintOnlySedScript(script string) bool {
	parts := strings.Split(script, ";")
	for _, part := range parts {
		if !printOnlySedScript.MatchString(part) {
			return false
		}
	}
	return len(parts) > 0
}

// collectSedReadPaths handles sed, which only prints when it is asked to.
// Without -n its output is the rewritten text, and -i rewrites the files
// themselves; an -e expression takes the next word as the script, so that
// word is not a file.
func collectSedReadPaths(args []string, paths *[]string) bool {
	printsOnly := false
	var scripts, files []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			files = append(files, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") {
			switch arg {
			case "--quiet", "--silent":
				printsOnly = true
			case "--expression":
				// The value of --expression is the script, not a file.
				if i+1 >= len(args) {
					return false
				}
				i++
				scripts = append(scripts, args[i])
			default:
				return false
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			for j := 1; j < len(arg); j++ {
				switch arg[j] {
				case 'n':
					printsOnly = true
				case 'e':
					// A short -e takes the next word as the script. When it
					// ends the cluster the word is the next argument.
					if j != len(arg)-1 {
						scripts = append(scripts, arg[j+1:])
						j = len(arg)
						continue
					}
					if i+1 >= len(args) {
						return false
					}
					i++
					scripts = append(scripts, args[i])
				case 'i', 'f', 'l', 's', 'y', 'w', 'W', 'r', 'R', 'x', 'E':
					// These rewrite the files, read a script from one, or
					// change what the script means.
					return false
				default:
					return false
				}
			}
			continue
		}
		files = append(files, arg)
	}

	// Without -n sed prints every line whether the script prints or not,
	// which is a full read only when no other command is involved; keeping
	// the -n requirement is the simpler, safer rule.
	if !printsOnly {
		return false
	}

	if len(scripts) == 0 {
		// The first operand is the script, not a file.
		if len(files) == 0 {
			return false
		}
		scripts = append(scripts, files[0])
		files = files[1:]
	}
	for _, script := range scripts {
		if !isPrintOnlySedScript(script) {
			return false
		}
	}
	*paths = append(*paths, files...)
	return true
}
