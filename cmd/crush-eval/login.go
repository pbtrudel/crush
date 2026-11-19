package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/route"
)

const loginUsage = `usage:
  crush-eval login <route>`

// loginCmd signs a route in once, with CRUSH_GLOBAL_DATA set to the eval
// home, so the token lands there and never in the user's own data directory.
// It is the only command that runs crush on the terminal.
func loginCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, loginUsage)
		return 2
	}

	h, err := home.Default()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := h.Ensure(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	rt, err := route.Load(h.RoutesDir(), flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	bin, err := exec.LookPath("crush")
	if err != nil {
		fmt.Fprintf(stderr, "find the crush binary: %v\n", err)
		return 1
	}
	cmd, err := rt.LoginCommand(context.Background(), h, bin, os.Environ())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
