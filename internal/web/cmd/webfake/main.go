// Command webfake serves the web mode UI against a scripted fake
// workspace, for Playwright tests and frontend development.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/pinentry"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/sshaskpass"
	"github.com/charmbracelet/crush/internal/web"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/workspace"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fake := webtest.NewFake()
	fake.OnRun = webtest.Scripted(fake)
	// The workspace root is the checkout, so a dev session browses the repo
	// and @ completion sees real files. A screenshot run pins it instead:
	// the HUD labels the tab with the basename of this path, so an unpinned
	// root would make every baseline depend on the folder name.
	if dir := os.Getenv("WEBFAKE_PROJECT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "webfake: project dir:", err)
		}
		fake.WorkDir = dir
	} else if wd, err := os.Getwd(); err == nil {
		fake.WorkDir = wd
	}
	clock := time.Now()
	// now pins the gateway's sign-in timestamps to the seeded clock, so a
	// Playwright run sees a stable expires_at.
	var now func() time.Time
	if v := os.Getenv("WEBFAKE_CLOCK"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			fmt.Fprintln(os.Stderr, "webfake: invalid WEBFAKE_CLOCK:", err)
			os.Exit(1)
		}
		clock = t
		now = func() time.Time { return clock }
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
		Now:                  now,
	}
	// The m3 scenario carries the M3 seeds and the plan handoff the terminal
	// would confirm; the m4 scenario adds the sign-in and secure prompt
	// surfaces on top of the same seeds. The default scenario stays exactly
	// as it was.
	planRequests := make(chan struct{}, 8)
	scenario := os.Getenv("WEBFAKE_SCENARIO")
	m3 := scenario == "m3"
	m4 := scenario == "m4"
	// signinDone releases a flow still waiting when the server shuts down.
	signinDone := make(chan struct{})
	defer close(signinDone)
	var seams webtest.M4Seams
	if m4 {
		seams, err = webtest.SeedM4(fake, signinDone, clock)
		if err != nil {
			fmt.Fprintln(os.Stderr, "webfake:", err)
			os.RemoveAll(teamDir)
			os.Exit(1)
		}
		opts.KnownProviders = seams.KnownProviders
		opts.TestAPIKey = seams.TestAPIKey
	}
	if m3 {
		if err := webtest.SeedM3(fake, clock); err != nil {
			fmt.Fprintln(os.Stderr, "webfake:", err)
			os.RemoveAll(teamDir)
			os.Exit(1)
		}
	}
	if m3 || m4 {
		opts.LoadCommands = func(*config.Config) ([]commands.CustomCommand, error) {
			return webtest.M3Commands(), nil
		}
		opts.OnPlanHandoff = func(string, bool) { planRequests <- struct{}{} }
	}
	if m4 {
		ms, ok := signinMS()
		if !ok {
			os.RemoveAll(teamDir)
			os.Exit(1)
		}
		opts.SignInFlow = signInFlow(seams.SignInFlow, ms)
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
	if m3 || m4 {
		go func() {
			for range planRequests {
				g.SetPlanReady("")
			}
		}()
	}
	if m4 {
		if kind := os.Getenv("WEBFAKE_SECURE_PROMPT"); kind != "" {
			go playSecurePrompt(ctx, fake, kind)
		}
		if os.Getenv("WEBFAKE_AWS_SSO") == "1" {
			go playAWSSSO(ctx, fake)
		}
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

// signinMS reads WEBFAKE_SIGNIN_MS and returns -1 when it is unset. An
// invalid value reports false after printing, so the caller can remove the
// team directory before exiting.
func signinMS() (int, bool) {
	v := os.Getenv("WEBFAKE_SIGNIN_MS")
	if v == "" {
		return -1, true
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms < 0 {
		fmt.Fprintln(os.Stderr, "webfake: invalid WEBFAKE_SIGNIN_MS:", v)
		return 0, false
	}
	return ms, true
}

// signInFlow wraps the scripted flows with WEBFAKE_SIGNIN_MS: a device flow
// completes on its own after that many milliseconds, the way a user
// finishing the approval in a browser would. A negative value leaves the
// flow waiting for a cancel.
func signInFlow(seams func(string) (signin.Flow, error), ms int) func(string) (signin.Flow, error) {
	if ms < 0 {
		return seams
	}
	return func(providerID string) (signin.Flow, error) {
		flow, err := seams(providerID)
		if err != nil {
			return nil, err
		}
		if ff, ok := flow.(*webtest.FakeFlow); ok && ff.Challenge.UserCode != "" {
			time.AfterFunc(time.Duration(ms)*time.Millisecond, ff.Finish)
		}
		return flow, nil
	}
}

// playSecurePrompt opens the prompt WEBFAKE_SECURE_PROMPT names, 300 ms
// after start or after WEBFAKE_PROMPT_AFTER_MS when that is set, and answers
// it from the terminal after WEBFAKE_PROMPT_ELSEWHERE_MS when that is set.
func playSecurePrompt(ctx context.Context, fake *webtest.Fake, kind string) {
	ev, ok := webtest.SecurePromptFromEnv(kind)
	if !ok {
		fmt.Fprintln(os.Stderr, "webfake: unknown WEBFAKE_SECURE_PROMPT:", kind)
		return
	}
	after := 300
	if v := os.Getenv("WEBFAKE_PROMPT_AFTER_MS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			fmt.Fprintln(os.Stderr, "webfake: invalid WEBFAKE_PROMPT_AFTER_MS:", v)
			return
		}
		after = n
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(after) * time.Millisecond):
	}
	switch req := ev.(type) {
	case pinentry.PromptRequest:
		fake.AddPinentryPrompt(req)
	case sshaskpass.PromptRequest:
		fake.AddSSHPrompt(req)
	}
	ms := 0
	if v := os.Getenv("WEBFAKE_PROMPT_ELSEWHERE_MS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			fmt.Fprintln(os.Stderr, "webfake: invalid WEBFAKE_PROMPT_ELSEWHERE_MS:", v)
			return
		}
		ms = n
	}
	if ms == 0 {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(ms) * time.Millisecond):
	}
	switch req := ev.(type) {
	case pinentry.PromptRequest:
		fake.PinentryRespond(req.ID, "webfake-answer")
	case sshaskpass.PromptRequest:
		// The terminal answers every touch and confirm prompt with "yes".
		fake.SSHRespond(req.ID, "yes")
	}
}

// playAWSSSO runs one AWS SSO refresh: the command first, then its
// verification URL 200 ms later. Nothing resolves it, so the card stays
// open for the reader.
func playAWSSSO(ctx context.Context, fake *webtest.Fake) {
	emit := func(n notify.Notification) {
		fake.Emit(pubsub.Event[notify.Notification]{Type: pubsub.CreatedEvent, Payload: n})
	}
	emit(notify.Notification{
		Type: notify.TypeAWSSSOAuth, ProviderID: "amazon-bedrock",
		AWSSOCommand: "aws sso login --profile dev",
	})
	select {
	case <-ctx.Done():
		return
	case <-time.After(200 * time.Millisecond):
	}
	emit(notify.Notification{
		Type: notify.TypeAWSSSOAuth, ProviderID: "amazon-bedrock",
		AWSSOCommand: "aws sso login --profile dev",
		AWSSOURL:     "https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD",
	})
}
