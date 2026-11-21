package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsReadOnlyCommand_Allowed covers the commands that cannot change the
// working tree or the machine, so a step that ran one cannot have finished
// a task.
func TestIsReadOnlyCommand_Allowed(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"git status",
		"git log --oneline -5",
		"git diff origin/main...HEAD",
		"git show HEAD:go.mod",
		"ls -la internal",
		"cat go.mod",
		"grep -rn todoReminder internal",
		"rg -n 'TODO' internal",
		"head -20 README.md",
		"tail -n 5 go.sum",
		"wc -l internal/agent/*.go",
		"pgrep -f crush",
		"ps aux",
		"du -sh .",
		"df -h",
		"find . -name '*.go'",
		"sort go.sum",
		"diff a.txt b.txt",
		"cut -d: -f1 go.sum",
		"file go.mod",
		"stat -c %s go.mod",
		"uniq",
		"uniq -c",
		// Waiting is not progress either.
		"sleep 5",
		// A chain is judged by its parts.
		"ls && git status",
		"cat go.mod | grep module",
	} {
		assert.True(t, IsReadOnlyCommand(command), "expected %q to be read-only", command)
	}
}

// TestIsReadOnlyCommand_Denied covers the commands that can finish a task,
// including the ones that look read-only and are not.
func TestIsReadOnlyCommand_Denied(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"go build ./...",
		"go test ./internal/agent/...",
		"git commit -m 'x'",
		"git push",
		"rm -rf internal",
		"mv a b",
		"make lint",
		// A redirect turns a read-only command into a file write.
		"ls > out.txt",
		"echo hi >> ~/.bashrc",
		// find is read-only until it is not.
		"find . -delete",
		"find . -exec rm {} ;",
		// uniq takes a second operand to write, and file compiles the
		// magic database it is given.
		"uniq in.txt out.txt",
		"file -C -m magic",
		"file --compile -m magic",
		// Both flags name a program and run it.
		"sort --compress-program=gzip f",
		"rg --hostname-bin=/tmp/evil x",
		// Every spelling of an output flag has to be caught, not just the
		// one this list happens to spell out.
		"sort -o f x",
		"sort -of x",
		"sort --output=f x",
		"find . -fprint f",
		`find . -ok rm {} \;`,
		"rg --pre=cmd x",
		// A chain is only as read-only as its writer, however it is joined.
		"cat go.mod | tee out.txt",
		"ls && go build ./...",
		"git status | sh",
		// An unknown command still counts as progress, and so does
		// anything the classifier cannot parse.
		"",
		"ls && go build ./...",
		"sleep 1 && go test ./...",
		"FOO=1 ls",
	} {
		assert.False(t, IsReadOnlyCommand(command), "expected %q not to be read-only", command)
	}
}
