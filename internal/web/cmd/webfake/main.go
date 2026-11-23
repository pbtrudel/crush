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

	"github.com/charmbracelet/crush/internal/web"
	"github.com/charmbracelet/crush/internal/web/webtest"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fake := webtest.NewFake()
	fake.OnRun = webtest.Scripted(fake)
	if wd, err := os.Getwd(); err == nil {
		fake.WorkDir = wd
	}
	// webfake has no TUI, so it plays the terminal's part for mode switches:
	// the request comes back through the channel and the fake applies it.
	modeRequests := make(chan bool, 8)
	g, err := web.Start(ctx, web.Options{
		Workspace:            fake,
		OnInputModeRequested: func(plan bool) { modeRequests <- plan },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "webfake:", err)
		os.Exit(1)
	}
	go func() {
		for plan := range modeRequests {
			g.SetInputMode(plan)
		}
	}()
	fmt.Println(g.LaunchURL())
	if u, err := url.Parse(g.URL()); err == nil {
		fmt.Fprintf(os.Stderr, "Dev server: CRUSH_WEB_DEV_PORT=%s npm --prefix web run dev, then open http://localhost:5173/#t=%s\n",
			u.Port(), strings.TrimPrefix(g.LaunchURL(), g.URL()+"/#t="))
	}
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = g.Shutdown(sctx)
}
