package tools

import (
	"runtime"
	"slices"
	"strings"
)

// safeCommands lists command prefixes that are genuinely read-only and may
// therefore run without a permission prompt. Commands that can write files,
// execute child commands, or dump environment variables (e.g. kill,
// timeout, nohup, env, git branch) must not be listed: prefix matching
// cannot tell `git branch` (a listing) from `git branch -D` (a deletion).
var safeCommands = []string{
	// Bash builtins and core utils
	"cal",
	"date",
	"df",
	"du",
	"echo",
	"free",
	"groups",
	"hostname",
	"id",
	"ls",
	"ps",
	"pwd",
	"top",
	"type",
	"uname",
	"uptime",
	"whatis",
	"whereis",
	"which",
	"whoami",

	// Git
	"git blame",
	"git config --get",
	"git config --list",
	"git describe",
	"git diff",
	"git grep",
	"git log",
	"git ls-files",
	"git ls-remote",
	"git rev-parse",
	"git shortlog",
	"git show",
	"git status",
}

// chainingMetacharacters lists shell characters that enable command
// chaining, substitution, or redirection. Any command containing one of
// these is never classified as safe read-only, even when it starts with a
// safe prefix: `ls > ~/.bashrc` writes a file, and `ls & rm -rf /` runs a
// second command. Quoting is not parsed, so a metacharacter inside a quoted
// argument (e.g. `grep "a>b" f`) forces a permission prompt too; a false
// prompt is safer than a missed write.
var chainingMetacharacters = []string{
	";",
	"&",
	"|",
	"$(",
	"`",
	"<",
	">",
	"\n",
}

// containsCommandChaining reports whether s contains shell metacharacters
// that enable command chaining or substitution.
func containsCommandChaining(s string) bool {
	return slices.ContainsFunc(chainingMetacharacters, func(c string) bool {
		return strings.Contains(s, c)
	})
}

// unsafeReadOnlyFlags lists option substrings that make an otherwise
// read-only command write files or execute other programs: `git diff
// --output=<file>` truncates the file, `--ext-diff` runs an external diff
// driver, and `-O`/`--open-files-in-pager` hands content to a pager
// command. Prefix matching cannot see into option values, so any
// occurrence forces the permission prompt. The `-O` check is
// case-sensitive so read-only short flags like `git grep -o` are not
// caught.
var unsafeReadOnlyFlags = []string{
	"--output",
	"--ext-diff",
	"--open-files-in-pager",
}

// isSafeReadOnlyCommand reports whether cmd is a read-only command that
// can run without a permission prompt. Commands containing shell
// metacharacters or side-effecting flags are never considered safe, and
// only known read-only command prefixes qualify. The prefix must be
// followed by a space, a dash, or the end of the string so that "lsof"
// does not match "ls".
func isSafeReadOnlyCommand(cmd string) bool {
	if containsCommandChaining(cmd) {
		return false
	}
	cmdLower := strings.ToLower(cmd)
	if slices.ContainsFunc(unsafeReadOnlyFlags, func(f string) bool {
		return strings.Contains(cmdLower, f)
	}) {
		return false
	}
	if strings.Contains(cmd, "-O") {
		return false
	}
	for _, safe := range safeCommands {
		if strings.HasPrefix(cmdLower, safe) {
			if len(cmdLower) == len(safe) || cmdLower[len(safe)] == ' ' || cmdLower[len(safe)] == '-' {
				return true
			}
		}
	}
	return false
}

func init() {
	if runtime.GOOS == "windows" {
		safeCommands = append(
			safeCommands,
			// Windows-specific commands
			"ipconfig",
			"nslookup",
			"ping",
			"systeminfo",
			"tasklist",
			"where",
		)
	}
}
