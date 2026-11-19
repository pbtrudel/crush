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
	"runtime"
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

// Root is the GOROOT the resolver names for env, or the empty string when the
// toolchain does not resolve. One environment resolves its toolchain once and
// hands the root to PathWithRoot and ValueWithRoot: every value then comes
// from the same toolchain, and go runs once per value, not once more to
// resolve the toolchain each time.
func Root(env []string, resolve Resolver) string {
	root, err := resolve(env)
	if err != nil {
		return ""
	}
	return root
}

// Path returns the PATH of env with the bin directory of the toolchain env
// selects first, so a child that runs go runs the go the parent runs. A
// toolchain that does not resolve leaves the PATH alone: the child then runs
// the go its PATH names, as it did before.
func Path(env []string, resolve Resolver) string {
	return PathWithRoot(env, Root(env, resolve))
}

// PathWithRoot is Path for a GOROOT the caller resolved once. An empty root
// leaves the PATH alone.
func PathWithRoot(env []string, root string) string {
	path := value(env, "PATH")
	if root == "" {
		return path
	}
	bin := filepath.Join(root, "bin")
	if path == "" {
		return bin
	}
	return bin + string(os.PathListSeparator) + path
}

// Value returns the value name has in env, or the value the toolchain env
// selects reports for it when env does not set it. That go runs with env and
// with its own bin directory first on PATH, so it reads the user's go env
// file: a child whose HOME is its own empty directory would otherwise find
// neither cache, and an offline module lookup would fail.
func Value(env []string, name string, resolve Resolver) string {
	return ValueWithRoot(env, name, Root(env, resolve))
}

// ValueWithRoot is Value for a GOROOT the caller resolved once.
func ValueWithRoot(env []string, name, root string) string {
	if set := value(env, name); set != "" {
		return set
	}
	cmdEnv := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			cmdEnv = append(cmdEnv, kv)
		}
	}
	cmdEnv = append(cmdEnv, "PATH="+PathWithRoot(env, root))
	cmd := exec.CommandContext(context.Background(), Go(cmdEnv), "env", name)
	cmd.Env = cmdEnv
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Go is the go binary that the PATH of env names first. exec resolves a bare
// command name with the environment of the calling process, not with the
// environment the child gets, so a caller that puts the selected toolchain
// first on the child's PATH must name the binary to run it.
func Go(env []string) string {
	return goBinary(env, runtime.GOOS)
}

// goBinary is Go for one GOOS: it looks for the name go has there and checks
// the executable bit only where the system keeps one.
func goBinary(env []string, goos string) string {
	name := goName(goos)
	for _, dir := range filepath.SplitList(value(env, "PATH")) {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if goos == "windows" || info.Mode().Perm()&0o111 != 0 {
			return path
		}
	}
	return name
}

// goName is the name of the go binary on goos.
func goName(goos string) string {
	if goos == "windows" {
		return "go.exe"
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
