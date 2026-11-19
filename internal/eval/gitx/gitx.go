// Package gitx runs git for the eval runner. Every command runs with a
// minimal environment and with the repository's own fsmonitor and hooks
// turned off: the checkout belongs to the model, so a file it writes could
// otherwise run code and read the runner's secrets.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// hardening are the settings no checkout may override: a repository-local
// core.fsmonitor or hook is code the model can write.
var hardening = []string{"-c", "core.fsmonitor=", "-c", "core.hooksPath=" + os.DevNull}

// Env is the environment of every git command: PATH, an empty HOME and no
// user or system configuration. Nothing else of the parent survives, so a
// fsmonitor or hook cannot read an API key from it.
func Env() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	}
	if dir := emptyHome(); dir != "" {
		env = append(env, "HOME="+dir)
	}
	return env
}

var (
	homeOnce sync.Once
	homeDir  string
)

// emptyHome is the HOME every git command runs with, created once. When it
// cannot be created the commands run without HOME, and the fixed
// GIT_CONFIG_GLOBAL keeps the user's config out either way.
func emptyHome() string {
	homeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "crush-eval-git-home-")
		if err != nil {
			return
		}
		_ = os.Chmod(dir, 0o700)
		homeDir = dir
	})
	return homeDir
}

// Command builds one git command in dir with Env and the hardening.
func Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	return WithEnv(ctx, dir, Env(), args...)
}

// WithEnv builds one git command in dir with env, which must come from Env,
// and the hardening. It is for a caller that adds to Env, as the checkout
// does with its fixed identity.
func WithEnv(ctx context.Context, dir string, env []string, args ...string) *exec.Cmd {
	full := append([]string{"-C", dir}, hardening...)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = env
	return cmd
}

// Output runs one git command in dir and returns its trimmed output.
func Output(ctx context.Context, dir string, args ...string) (string, error) {
	return OutputWith(ctx, dir, Env(), args...)
}

// OutputWith runs one git command in dir with env, which must come from
// Env, and returns its trimmed output.
func OutputWith(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := WithEnv(ctx, dir, env, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Bytes runs one git command in dir and returns its standard output.
func Bytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := Command(ctx, dir, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Stdin runs one git command in dir with stdin and returns its trimmed
// output.
func Stdin(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	cmd := Command(ctx, dir, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
