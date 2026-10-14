package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainsCommandChaining(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"plain ls", "ls -la", false},
		{"plain echo", "echo hello world", false},
		{"plain pwd", "pwd", false},
		{"plain git status", "git status", false},
		{"ls with redirect", "ls > /tmp/out", true},
		{"ls with append redirect", "echo hi >> /tmp/out", true},
		{"ls with input redirect", "ls < /tmp/in", true},
		{"ls with pipe", "ls | grep foo", true},
		{"ls with double ampersand", "ls && echo done", true},
		{"ls with semicolon", "ls; echo done", true},
		{"ls with pipe pipe", "ls || echo fail", true},
		{"ls with backticks", "ls `echo foo`", true},
		{"ls with subshell", "ls $(echo foo)", true},
		{"ls with background ampersand", "ls & echo done", true},
		{"multi-line command", "ls\nrm -rf /", true},
		{"rm -rf with && ls (rm first)", "rm -rf / && ls", true},
		{"redirect with ampersand gt", "ls &> /dev/null", true},
		{"redirect with gt ampersand", "ls >& /dev/null", true},
		{"simple kill", "kill 1234", false},
		{"kill with pipe", "kill 1234 | echo foo", true},
		{"git log", "git log --oneline", false},
		{"git log with pipe", "git log | head", true},
		{"empty string", "", false},
		{"dollar sign in argument", "echo $HOME", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := containsCommandChaining(tt.input)
			assert.Equal(t, tt.expected, got, "containsCommandChaining(%q)", tt.input)
		})
	}
}

func TestIsSafeReadOnlyCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"plain ls", "ls -la", true},
		{"plain echo", "echo hello", true},
		{"git status", "git status --short", true},
		{"git log", "git log --oneline -5", true},
		{"git diff", "git diff HEAD", true},
		{"git config get", "git config --get core.name", true},
		{"git config get all", "git config --get-all core.name", true},
		{"git config list", "git config --list", true},
		{"redirect output", "ls > /tmp/out", false},
		{"redirect append", "echo hi >> ~/.bashrc", false},
		{"redirect input", "ls < /etc/hosts", false},
		{"chained", "ls && echo done", false},
		{"background", "ls & echo done", false},
		{"env dump", "env", false},
		{"printenv", "printenv", false},
		{"set dump", "set", false},
		{"time wrapper", "time rm -rf /", false},
		{"kill", "kill -9 1", false},
		{"killall", "killall node", false},
		{"nice", "nice ls", false},
		{"nohup", "nohup ls", false},
		{"timeout", "timeout 5 ls", false},
		{"unset", "unset FOO", false},
		{"git branch delete", "git branch -D feature", false},
		{"git tag delete", "git tag -d v1", false},
		{"git remote add", "git remote add other url", false},
		{"prefix without boundary", "lsof", false},
		{"rm", "rm -rf /", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, isSafeReadOnlyCommand(tt.input), "isSafeReadOnlyCommand(%q)", tt.input)
		})
	}
}
