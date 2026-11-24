// Command webfake serves the web mode UI against a scripted fake
// workspace, for Playwright tests and frontend development.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/web"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/workspace"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fake := webtest.NewFake()
	fake.OnRun = webtest.Scripted(fake)
	if wd, err := os.Getwd(); err == nil {
		fake.WorkDir = wd
	}
	clock := time.Now()
	if v := os.Getenv("WEBFAKE_CLOCK"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			fmt.Fprintln(os.Stderr, "webfake: invalid WEBFAKE_CLOCK:", err)
			os.Exit(1)
		}
		clock = t
	}
	teamDir, err := os.MkdirTemp("", "webfake-team-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "webfake:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(teamDir)
	if err := webtest.SeedHolodeck(fake, teamDir, clock); err != nil {
		fmt.Fprintln(os.Stderr, "webfake:", err)
		os.RemoveAll(teamDir)
		os.Exit(1)
	}
	// webfake has no TUI, so it plays the terminal's part for mode switches:
	// the request comes back through the channel and the fake applies it.
	modeRequests := make(chan bool, 8)
	opts := web.Options{
		Workspace:            fake,
		AgentTeamsDir:        teamDir,
		OnInputModeRequested: func(plan bool) { modeRequests <- plan },
	}
	// The m3 scenario carries the M3 seeds and the plan handoff the terminal
	// would confirm; the default scenario stays exactly as it was.
	planRequests := make(chan struct{}, 8)
	m3 := os.Getenv("WEBFAKE_SCENARIO") == "m3"
	if m3 {
		if err := webtest.SeedM3(fake, clock); err != nil {
			fmt.Fprintln(os.Stderr, "webfake:", err)
			os.RemoveAll(teamDir)
			os.Exit(1)
		}
		opts.LoadCommands = func(*config.Config) ([]commands.CustomCommand, error) {
			return webtest.M3Commands(), nil
		}
		opts.OnPlanHandoff = func(string, bool) { planRequests <- struct{}{} }
	}
	// The terminal has no agent until it has a model, which the browser
	// shows as the starting state on every route that needs one.
	if os.Getenv("WEBFAKE_AGENT_STARTING") == "1" {
		fake.SetReadyErr(workspace.ErrAgentNotInitialized)
	}
	g, err := web.Start(ctx, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "webfake:", err)
		os.RemoveAll(teamDir)
		os.Exit(1)
	}
	go func() {
		for plan := range modeRequests {
			g.SetInputMode(plan)
		}
	}()
	if m3 {
		go func() {
			for range planRequests {
				g.SetPlanReady("")
			}
		}()
	}
	if os.Getenv("WEBFAKE_PLAN_READY") == "1" {
		// A ready plan only reaches the browser in plan mode, which the
		// terminal's mode switch would have set.
		g.SetInputMode(true)
		g.SetPlanReady(fake.CurrentSessionID())
	}
	fmt.Println(g.LaunchURL())
	if u, err := url.Parse(g.URL()); err == nil {
		fmt.Fprintf(os.Stderr, "Dev server: CRUSH_WEB_DEV_PORT=%s npm --prefix web run dev, then open http://localhost:5173/#t=%s\n",
			u.Port(), strings.TrimPrefix(g.LaunchURL(), g.URL()+"/#t="))
	}
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := g.Shutdown(sctx); err != nil {
		fmt.Fprintln(os.Stderr, "webfake: shutdown:", err)
	}
}
