//go:build !linux

package netjail

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// Wrap reports that the jail needs Linux: only a Linux user namespace can
// be created without privilege, and only Linux has the network namespace
// the jail relies on.
func Wrap(cmd *exec.Cmd, sock string, port int, masks ...Mask) error {
	return errors.New("network jail needs Linux")
}

// WrapBound reports that the jail needs Linux.
func WrapBound(cmd *exec.Cmd, sock string, port int, bind Bind, masks ...Mask) error {
	return errors.New("network jail needs Linux")
}

// Preflight reports that the jail needs Linux.
func Preflight() error {
	return errors.New("network jail needs Linux; use --network host")
}

// KillGroup is a no-op where the jail does not exist.
func KillGroup(cmd *exec.Cmd) {}

// Main reports that the jail needs Linux. It never runs a child.
func Main(args []string) int {
	fmt.Fprintln(os.Stderr, "Network jail needs Linux")
	return 2
}
