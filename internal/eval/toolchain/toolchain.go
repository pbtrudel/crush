// Package toolchain finds the go toolchain an environment selects, so a
// child process that runs go uses the version go.mod asks for instead of
// whatever go comes first on its own PATH.
package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Resolver returns the GOROOT of the go toolchain that env selects.
type Resolver func(env []string) (string, error)

// GOROOT is the GOROOT of the toolchain env selects: go reads the go.mod of
// the working directory and switches to the toolchain it names.
func GOROOT(env []string) (string, error) {
	out, err := output(env, "env", "GOROOT")
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", errors.New("go env GOROOT: no value")
	}
	return out, nil
}

// Version is the version of the toolchain env selects, as `go version`
// prints it after the command's name, for the batch record.
func Version(env []string) (string, error) {
	out, err := output(env, "version")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) < 3 {
		return "", fmt.Errorf("go version: unexpected output %q", out)
	}
	return fields[2], nil
}

// Path returns the PATH of env with the bin directory of the toolchain env
// selects first, so a child that runs go runs the go the parent runs. A
// toolchain that does not resolve leaves the PATH alone: the child then runs
// the go its PATH names, as it did before.
func Path(env []string, resolve Resolver) string {
	path := value(env, "PATH")
	root, err := resolve(env)
	if err != nil || root == "" {
		return path
	}
	bin := filepath.Join(root, "bin")
	if path == "" {
		return bin
	}
	return bin + string(os.PathListSeparator) + path
}

// Go is the go binary that the PATH of env names first. exec resolves a bare
// command name with the environment of the calling process, not with the
// environment the child gets, so a caller that puts the selected toolchain
// first on the child's PATH must name the binary to run it.
func Go(env []string) string {
	for _, dir := range filepath.SplitList(value(env, "PATH")) {
		path := filepath.Join(dir, "go")
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return path
		}
	}
	return "go"
}

// output runs one go command with env and returns its trimmed output.
func output(env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "go", args...)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// value returns the value of key in a KEY=VALUE environment.
func value(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}
