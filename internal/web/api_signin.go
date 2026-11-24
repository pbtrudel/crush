package web

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/google/uuid"
)

// keySignInTimeout bounds the provider check a key sign-in waits for. The
// request itself is the user waiting, so a provider that never answers
// must not hold the tab forever.
const keySignInTimeout = 30 * time.Second

// signInStartTimeout bounds a provider's first OAuth call, so a provider
// that never answers cannot hold the sign-in open.
const signInStartTimeout = 30 * time.Second

// signInModelTimeout bounds the model switch a completed sign-in makes. The
// step is detached from the run's context so a cancel cannot fail it, and
// this deadline keeps a workspace that never answers from holding it open.
const signInModelTimeout = 30 * time.Second

// The phases a sign-in moves through. A cancelled state carries no
// sentence: the browser shows its own.
const (
	signInWaiting   = "waiting"
	signInSaving    = "saving"
	signInDone      = "done"
	signInError     = "error"
	signInCancelled = "cancelled"
)

// The two challenge kinds a provider can answer with.
const (
	signInKindDevice  = "device"
	signInKindBrowser = "browser"
)

// The fixed sentences a sign-in failure shows. Provider error text can
// carry a response body, so it never reaches an answer.
const (
	signInStartFailed = "The provider did not start the sign-in. Try again."
	signInWaitFailed  = "The sign-in did not finish. Try again."
	signInSaveFailed  = "Crush could not save the sign-in."
	signInBadCode     = "That code did not work. Copy the whole code or the address the page showed."
	signInPortBusy    = "Another sign-in is using the callback port. Finish or cancel it first."
)

// registerSignInRoutes installs the sign-in routes: the providers a tab can
// sign in to, the API key form and the one OAuth flow the gateway runs.
func (g *Gateway) registerSignInRoutes(api *http.ServeMux) {
	g.route(api, "GET /api/auth/providers", g.handleSignInProviders)
	g.route(api, "POST /api/auth/provider/key", g.handleAPIKeySignIn)
	g.route(api, "POST /api/auth/provider/oauth/start", g.handleOAuthStart)
	g.route(api, "GET /api/auth/provider/oauth/status", g.handleOAuthStatus)
	g.route(api, "POST /api/auth/provider/oauth/cancel", g.handleOAuthCancel)
	g.route(api, "POST /api/auth/provider/oauth/code", g.handleOAuthCode)
}

// knownProviders lists the provider catalog the gateway's seam answers for
// cfg.
func (g *Gateway) knownProviders(cfg *config.Config) ([]catwalk.Provider, error) {
	return g.knownProvidersFn(cfg)
}

// defaultKnownProviders is the real catalog loader. A workspace without a
// config yet has no catalog to load, which the browser shows as unavailable
// rather than a crash.
func defaultKnownProviders(cfg *config.Config) ([]catwalk.Provider, error) {
	if cfg == nil {
		return nil, errors.New("the workspace has no configuration")
	}
	return config.Providers(cfg)
}

// handleSignInProviders lists every provider a tab can sign in to, with the
// methods it offers.
func (g *Gateway) handleSignInProviders(w http.ResponseWriter, _ *http.Request) {
	cfg := g.ws.Config()
	known, err := g.knownProviders(cfg)
	if len(known) == 0 {
		if err != nil {
			slog.Warn("Web sign-in providers unavailable", "error", err)
		}
		writeError(w, http.StatusBadGateway, "providers_unavailable", "The provider list is not available yet.")
		return
	}
	targets := workspace.SignInTargets(cfg, known)
	if targets == nil {
		targets = []workspace.SignInTarget{}
	}
	writeJSON(w, http.StatusOK, map[string][]workspace.SignInTarget{"providers": targets})
}

// keySignInResponse is what a key sign-in answers. The key itself never
// appears in it.
type keySignInResponse struct {
	SignedIn     bool   `json:"signed_in"`
	ModelApplied bool   `json:"model_applied"`
	Reason       string `json:"reason,omitempty"`
}

// handleAPIKeySignIn verifies a provider API key and saves it in one
// request. The key is read from the body only and is never echoed or
// logged, including when the provider rejects it.
func (g *Gateway) handleAPIKeySignIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		Model    *struct {
			Type  string `json:"type"`
			Model string `json:"model"`
		} `json:"model"`
	}
	if !decodeSecretJSON(w, r, &body) {
		return
	}

	cfg := g.ws.Config()
	known, err := g.knownProviders(cfg)
	if err != nil && len(known) == 0 {
		slog.Warn("Web sign-in providers unavailable", "error", err)
		writeError(w, http.StatusBadGateway, "providers_unavailable", "The provider list is not available yet.")
		return
	}
	targets := workspace.SignInTargets(cfg, known)
	idx := slices.IndexFunc(targets, func(t workspace.SignInTarget) bool { return t.ID == body.Provider })
	if idx < 0 {
		writeError(w, http.StatusNotFound, "unknown_provider", "the provider is not one Crush can sign in to")
		return
	}
	target := targets[idx]
	if !slices.Contains(target.Methods, "api_key") {
		writeError(w, http.StatusBadRequest, "api_key_not_supported", "the provider signs in with OAuth only")
		return
	}

	key := strings.TrimSpace(body.APIKey)
	if key == "" || len(key) > maxSecretBytes {
		writeError(w, http.StatusBadRequest, "bad_api_key", "send an API key of at most 4096 bytes")
		return
	}
	// A tab's key is saved literally: a shell expression would be expanded
	// by the resolver, and the TUI is the only place that behaviour stays.
	if strings.ContainsAny(key, "$`") {
		writeError(w, http.StatusBadRequest, "bad_api_key", "the key must not contain a shell expression")
		return
	}

	var modelType config.SelectedModelType
	if body.Model != nil {
		var ok bool
		modelType, ok = selectedModelType(body.Model.Type)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_type", "type must be main or small")
			return
		}
	}

	var provider catwalk.Provider
	for _, p := range known {
		if string(p.ID) == body.Provider {
			provider = p
			break
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), keySignInTimeout)
	defer cancel()
	if err := g.testAPIKeyFn(ctx, provider, key, config.IdentityResolver()); err != nil {
		// Providers echo part of a rejected key in their error text, so the
		// detail stays out of the log as well as the answer.
		slog.Info("Web sign-in key rejected", "provider", body.Provider)
		writeError(w, http.StatusUnprocessableEntity, "key_rejected", "The provider did not accept this key.")
		return
	}

	if err := g.ws.SetProviderAPIKey(config.ScopeGlobal, body.Provider, key); err != nil {
		writeWorkspaceError(w, "save_api_key", err)
		return
	}

	resp := keySignInResponse{SignedIn: true}
	if body.Model != nil {
		resp.ModelApplied, resp.Reason = applySignInModel(r.Context(), g, &signinModel{
			typ:   modelType,
			model: config.SelectedModel{Provider: body.Provider, Model: body.Model.Model},
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// SignInState is the progress of the gateway's sign-in flow, in an answer,
// a frame and the snapshot. It never carries a token or a pasted code.
// expires_at is Unix seconds, and error is one of the fixed sentences.
type SignInState struct {
	FlowID          string `json:"flow_id"`
	Provider        string `json:"provider"`
	ProviderName    string `json:"provider_name"`
	State           string `json:"state"`
	Kind            string `json:"kind,omitempty"`
	UserCode        string `json:"user_code,omitempty"`
	VerificationURL string `json:"verification_url,omitempty"`
	ExpiresAt       int64  `json:"expires_at,omitempty"`
	CodeEntry       bool   `json:"code_entry,omitempty"`
	Error           string `json:"error,omitempty"`
	ModelApplied    bool   `json:"model_applied,omitempty"`
	Imported        bool   `json:"imported,omitempty"`
}

// signinModel is the model a completed sign-in switches to.
type signinModel struct {
	typ   config.SelectedModelType
	model config.SelectedModel
}

// modelTarget is the model a start request asks to apply.
type modelTarget struct {
	Type  string `json:"type"`
	Model string `json:"model"`
}

// signinRun is one running sign-in. The gateway holds at most one, so a
// second start can free the first flow's callback port.
type signinRun struct {
	id     string
	flow   signin.Flow
	ctx    context.Context
	cancel context.CancelFunc
	model  *signinModel
	once   sync.Once
	mu     sync.Mutex
	state  SignInState
}

// applySignInModel switches the model a sign-in chose, once its credential
// is saved. A key sign-in and an OAuth sign-in share it, so both apply the
// model the same way.
func applySignInModel(ctx context.Context, g *Gateway, m *signinModel) (bool, string) {
	if m == nil {
		return false, ""
	}
	if err := workspace.SelectModel(ctx, g.ws, m.typ, m.model); err != nil {
		reason := signInModelReason(err)
		if reason == "model_failed" {
			slog.Warn("Web sign-in model not applied", "provider", m.model.Provider, "reason", reason)
		}
		return false, reason
	}
	g.announceModel()
	return true, ""
}

// signInErrorClass names a sign-in failure for the log. Provider errors can
// embed a response body, so a log line carries this class and never the
// error value.
func signInErrorClass(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, syscall.EADDRINUSE):
		return "port_busy"
	case errors.As(err, &netErr):
		return "network"
	}
	return "provider"
}

// signInIsFinal reports whether a phase ends the run.
func signInIsFinal(state string) bool {
	switch state {
	case signInDone, signInError, signInCancelled:
		return true
	}
	return false
}

// newSignInFlow creates the flow a sign-in runs. The seam lets tests and
// webfake run a scripted provider.
func (g *Gateway) newSignInFlow(providerID string) (signin.Flow, error) {
	return g.signInFlowFn(providerID)
}

// signInState returns the current or last sign-in state, or nil when no
// sign-in has started.
func (g *Gateway) signInState() *SignInState {
	g.signinMu.Lock()
	run := g.signin
	g.signinMu.Unlock()
	if run == nil {
		return nil
	}
	st := run.view()
	return &st
}

// handleOAuthStart starts a provider's sign-in and answers its first
// challenge. One flow runs per gateway: a start stops the running one,
// whose tabs then see it cancelled. The token never reaches an answer.
func (g *Gateway) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string       `json:"provider"`
		Model    *modelTarget `json:"model"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	cfg := g.ws.Config()
	known, err := g.knownProviders(cfg)
	if err != nil && len(known) == 0 {
		slog.Warn("Web sign-in providers unavailable", "error", err)
		writeError(w, http.StatusBadGateway, "providers_unavailable", "The provider list is not available yet.")
		return
	}
	targets := workspace.SignInTargets(cfg, known)
	idx := slices.IndexFunc(targets, func(t workspace.SignInTarget) bool { return t.ID == body.Provider })
	if idx < 0 {
		writeError(w, http.StatusNotFound, "unknown_provider", "the provider is not one Crush can sign in to")
		return
	}
	target := targets[idx]
	if !slices.Contains(target.Methods, "oauth") {
		writeError(w, http.StatusBadRequest, "oauth_not_supported", "the provider signs in with an API key")
		return
	}
	model, ok := newSignInModel(body.Provider, body.Model)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_type", "type must be main or small")
		return
	}
	// Copilot first reuses a login made in VS Code, as the terminal's model
	// picker does, so a user who already signed in there skips the device
	// code. A start replaces the running flow either way (decision 11).
	if body.Provider == string(catwalk.InferenceProviderCopilot) && !target.Configured {
		g.signinMu.Lock()
		if g.signinClosed {
			g.signinMu.Unlock()
			writeError(w, http.StatusServiceUnavailable, "shutting_down", "Crush is shutting down")
			return
		}
		g.stopRunLocked(g.signin)
		g.signinMu.Unlock()
		// ImportCopilot takes no context and may make a network call.
		if _, imported := g.ws.ImportCopilot(); imported {
			st := g.recordImportedSignIn(r.Context(), body.Provider, target.Name, model)
			writeJSON(w, http.StatusOK, st)
			return
		}
	}

	g.signinMu.Lock()
	if g.signinClosed {
		g.signinMu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "shutting_down", "Crush is shutting down")
		return
	}
	// One flow per gateway: the callback ports allow one browser flow
	// anyway, and the old tabs learn why their dialog stopped.
	g.stopRunLocked(g.signin)
	flow, err := g.newSignInFlow(body.Provider)
	if err != nil {
		g.signinMu.Unlock()
		if errors.Is(err, signin.ErrUnsupported) {
			writeError(w, http.StatusBadRequest, "oauth_not_supported", "the provider signs in with an API key")
			return
		}
		slog.Warn("Web sign-in did not start", "provider", body.Provider, "reason", signInErrorClass(err))
		writeError(w, http.StatusBadGateway, "signin_start_failed", signInStartFailed)
		return
	}
	ctx, cancel := context.WithCancel(g.ctx)
	id := uuid.NewString()
	run := &signinRun{
		id:     id,
		flow:   flow,
		ctx:    ctx,
		cancel: cancel,
		model:  model,
		state: SignInState{
			FlowID:       id,
			Provider:     body.Provider,
			ProviderName: flow.Name(),
		},
	}
	g.signinMu.Unlock()

	// The flow outlives this request: the browser is sent elsewhere and
	// the token arrives later.
	startCtx, startCancel := context.WithTimeout(run.ctx, signInStartTimeout)
	challenge, err := flow.Start(startCtx)
	startCancel()
	if err != nil {
		run.cancel()
		flow.Close()
		slog.Warn("Web sign-in did not start", "provider", body.Provider, "reason", signInErrorClass(err))
		if errors.Is(err, syscall.EADDRINUSE) {
			writeError(w, http.StatusConflict, "signin_busy", signInPortBusy)
			return
		}
		writeError(w, http.StatusBadGateway, "signin_start_failed", signInStartFailed)
		return
	}

	st := run.waiting(challenge, g.now().Unix())
	g.signinMu.Lock()
	if g.signinClosed {
		g.signinMu.Unlock()
		run.cancel()
		flow.Close()
		writeError(w, http.StatusServiceUnavailable, "shutting_down", "Crush is shutting down")
		return
	}
	// A start that began while this one waited for the provider may have
	// adopted its flow first; one flow per gateway means it stops now.
	g.stopRunLocked(g.signin)
	g.signin = run
	// Add under the lock after the signinClosed check, so Shutdown can
	// never miss a goroutine started after it began waiting.
	g.signinWG.Add(1)
	// The first challenge is published before the wait goroutine starts,
	// so a fallback challenge can never reach a tab before it.
	g.setStateLocked(run, st)
	g.signinMu.Unlock()
	go g.waitSignIn(run, challenge)
	writeJSON(w, http.StatusOK, st)
}

// handleOAuthStatus answers the current or last sign-in, so a tab that
// reloads mid-flow picks it up again.
func (g *Gateway) handleOAuthStatus(w http.ResponseWriter, _ *http.Request) {
	st := g.signInState()
	if st == nil {
		writeError(w, http.StatusNotFound, "no_flow", "no sign-in has started")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleOAuthCancel stops the running sign-in and frees a browser flow's
// callback listener. The cancelled state stays readable as the last one.
func (g *Gateway) handleOAuthCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FlowID string `json:"flow_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	g.signinMu.Lock()
	run := g.signin
	if run == nil || run.id != body.FlowID || !run.running() {
		g.signinMu.Unlock()
		writeError(w, http.StatusNotFound, "no_flow", "no such sign-in is running")
		return
	}
	g.stopRunLocked(run)
	g.signinMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// handleOAuthCode hands a code pasted back from the provider's page to the
// flow. The code is read from the body only, is never echoed, and is never
// logged, not even when the provider refuses it.
func (g *Gateway) handleOAuthCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FlowID string `json:"flow_id"`
		Code   string `json:"code"`
	}
	if !decodeSecretJSON(w, r, &body) {
		return
	}
	code := strings.TrimSpace(body.Code)
	if code == "" || len(code) > maxSecretBytes {
		writeError(w, http.StatusBadRequest, "bad_code", "send a code of at most 4096 bytes")
		return
	}
	g.signinMu.Lock()
	run := g.signin
	if run == nil || run.id != body.FlowID {
		g.signinMu.Unlock()
		writeError(w, http.StatusNotFound, "no_flow", "no such sign-in is running")
		return
	}
	// Code entry is a capability of the flow, not live state: it is on
	// only while a browser challenge is in force, so a device challenge,
	// a device fallback or a run already saving its token answers 409
	// instead of a rejected code.
	if !run.codeEntry() {
		g.signinMu.Unlock()
		writeError(w, http.StatusConflict, "code_entry_unavailable", "This sign-in does not take a code.")
		return
	}
	if g.signinClosed {
		g.signinMu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "shutting_down", "Crush is shutting down")
		return
	}
	g.signinWG.Add(1)
	g.signinMu.Unlock()
	// The exchange talks to the provider, so it runs after the answer.
	w.WriteHeader(http.StatusAccepted)
	go g.submitSignInCode(run, code)
}

// submitSignInCode finishes a browser flow with a pasted code. The flow's
// error quotes the code, so a refusal is logged without it.
func (g *Gateway) submitSignInCode(run *signinRun, code string) {
	defer g.signinWG.Done()
	tok, err := run.flow.SubmitCode(run.ctx, code)
	if err != nil {
		if run.ctx.Err() != nil {
			return
		}
		if errors.Is(err, signin.ErrNoBrowserFlow) {
			// The flow fell back to a device challenge after the tab was
			// told the code was accepted. The device flow is still
			// running, so the code is dropped rather than reported as
			// refused.
			slog.Debug("Web sign-in code arrived with no browser flow", "provider", run.providerID())
			return
		}
		// A refused code ends the run: the listener is freed and the
		// wait goroutine stops, so a retry starts clean and a token the
		// flow still delivered cannot land on top of the error.
		g.signinMu.Lock()
		g.endRunLocked(run, signInBadCode)
		g.signinMu.Unlock()
		slog.Info("Web sign-in code refused", "provider", run.providerID())
		return
	}
	if tok == nil {
		return
	}
	g.completeSignIn(run, tok)
}

// waitSignIn follows the flow until a token arrives or it fails, publishing
// every transition. A cancelled context was published by the cancel path.
func (g *Gateway) waitSignIn(run *signinRun, c signin.Challenge) {
	defer g.signinWG.Done()
	for {
		tok, next, err := run.flow.Wait(run.ctx, c)
		if err != nil {
			if run.ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("Web sign-in did not finish", "provider", run.providerID(), "reason", signInErrorClass(err))
			// A failed wait ends the run: the context is cancelled and the
			// listener freed, so a retry finds the callback port free.
			g.signinMu.Lock()
			g.endRunLocked(run, signInWaitFailed)
			g.signinMu.Unlock()
			return
		}
		if next != nil {
			c = *next
			g.publishSignIn(run, run.waiting(c, g.now().Unix()))
			continue
		}
		if tok != nil {
			g.completeSignIn(run, tok)
		}
		return
	}
}

// completeSignIn saves the token once and applies the model the tab chose.
// The token never reaches a frame, an answer or a log line.
func (g *Gateway) completeSignIn(run *signinRun, tok *oauth.Token) {
	run.once.Do(func() {
		// A run cancelled or replaced before the save keeps nothing: a
		// stale token must not be written, and the saving phase must not
		// land after the cancel.
		g.signinMu.Lock()
		if run.ctx.Err() != nil || g.signin != run {
			g.signinMu.Unlock()
			slog.Debug("Web sign-in token arrived after the run ended", "provider", run.providerID())
			return
		}
		g.setStateLocked(run, run.phase(signInSaving, ""))
		g.signinMu.Unlock()
		provider := run.providerID()
		if err := g.ws.SetProviderAPIKey(config.ScopeGlobal, provider, tok); err != nil {
			slog.Warn("Web sign-in could not be saved", "provider", provider, "reason", signInErrorClass(err))
			g.signinMu.Lock()
			g.finishRunLocked(run, run.phase(signInError, signInSaveFailed))
			g.signinMu.Unlock()
			return
		}
		// The model step is not tied to the run: a cancel that raced the
		// save cannot fail it now the credential is stored, and the
		// deadline keeps a workspace that never answers from holding the
		// step open.
		modelCtx, cancelModel := context.WithTimeout(context.WithoutCancel(run.ctx), signInModelTimeout)
		applied, _ := applySignInModel(modelCtx, g, run.model)
		cancelModel()
		st := run.phase(signInDone, "")
		st.ModelApplied = applied
		g.signinMu.Lock()
		g.finishRunLocked(run, st)
		g.signinMu.Unlock()
	})
}

// stopRunLocked stops a run: its context ends, a browser flow frees its
// callback listener, and every tab sees it cancelled unless it had already
// finished. The caller holds signinMu. The run stays the last state, so a
// tab that reloads still reads the outcome.
func (g *Gateway) stopRunLocked(run *signinRun) {
	if run == nil {
		return
	}
	run.cancel()
	run.flow.Close()
	st, stopped := run.stopped()
	if stopped && g.signin == run {
		g.hub.publishJSON(frameSignIn, st)
	}
}

// endRunLocked ends a waiting run with the sentence of a failed phase: its
// context is cancelled and its listener freed before the error reaches a
// tab. The caller holds signinMu.
func (g *Gateway) endRunLocked(run *signinRun, sentence string) {
	run.cancel()
	run.flow.Close()
	st, ended := run.failed(sentence)
	if ended && g.signin == run {
		g.hub.publishJSON(frameSignIn, st)
	}
}

// finishRunLocked ends a run with the phase it reached: its context is
// cancelled and its listener freed before the phase reaches a tab, so no
// goroutine outlives the outcome. The caller holds signinMu.
func (g *Gateway) finishRunLocked(run *signinRun, st SignInState) {
	run.cancel()
	run.flow.Close()
	g.setStateLocked(run, st)
}

// setStateLocked records a run's state and tells every tab, unless a newer
// run replaced it. The caller holds signinMu.
func (g *Gateway) setStateLocked(run *signinRun, st SignInState) {
	run.mu.Lock()
	run.state = st
	run.mu.Unlock()
	if g.signin == run {
		g.hub.publishJSON(frameSignIn, st)
	}
}

// publishSignIn records a run's state and tells every tab about it, unless
// a newer run replaced it.
func (g *Gateway) publishSignIn(run *signinRun, st SignInState) {
	g.signinMu.Lock()
	defer g.signinMu.Unlock()
	g.setStateLocked(run, st)
}

// view returns a copy of the run's state.
func (r *signinRun) view() SignInState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// providerID is the provider the run signs in to.
func (r *signinRun) providerID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Provider
}

// codeEntry reports whether the run takes a pasted code now: only a
// waiting browser challenge does.
func (r *signinRun) codeEntry() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.State == signInWaiting && r.state.CodeEntry
}

// running reports whether the run still waits for the user.
func (r *signinRun) running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.State == signInWaiting
}

// waiting applies a challenge to the run's state. Code entry is on only for
// a browser challenge whose flow can exchange a pasted code, and a device
// code's expiry is measured from the gateway clock, not the wall one, so
// webfake can date a flow from WEBFAKE_CLOCK.
func (r *signinRun) waiting(c signin.Challenge, now int64) SignInState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.state
	st.State = signInWaiting
	st.Error = ""
	st.Kind = signInKindBrowser
	st.UserCode = c.UserCode
	st.VerificationURL = c.VerificationURL
	st.ExpiresAt = 0
	st.CodeEntry = false
	st.ModelApplied = false
	if c.UserCode != "" {
		st.Kind = signInKindDevice
	} else {
		st.CodeEntry = r.flow.CodeEntry()
	}
	if c.ExpiresIn > 0 {
		st.ExpiresAt = now + int64(c.ExpiresIn)
	}
	return st
}

// phase returns the run's state moved to another phase. A final phase takes
// no pasted code and carries only the sentence it is given.
func (r *signinRun) phase(state, sentence string) SignInState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.state
	st.State = state
	st.Error = sentence
	if signInIsFinal(state) {
		st.CodeEntry = false
	}
	return st
}

// failed moves a waiting run to error with sentence and reports whether it
// was still waiting, with the state to publish. A run that already finished
// keeps its phase.
func (r *signinRun) failed(sentence string) (SignInState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.State != signInWaiting {
		return r.state, false
	}
	r.state.State = signInError
	r.state.Error = sentence
	r.state.CodeEntry = false
	return r.state, true
}

// stopped marks a waiting run cancelled and reports whether it was still
// waiting, with the state to publish. A run already saving a token is not
// stopped: the sign-in did finish.
func (r *signinRun) stopped() (SignInState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.State != signInWaiting {
		return r.state, false
	}
	r.state.State = signInCancelled
	r.state.Error = ""
	r.state.CodeEntry = false
	return r.state, true
}

// recordImportedSignIn records a sign-in that finished by importing an
// existing Copilot login. No flow runs, so the last state sits on a flow
// with nothing to wait for and nothing to close, and status and the
// snapshot keep answering it. ImportCopilot saved the token already.
func (g *Gateway) recordImportedSignIn(ctx context.Context, providerID, providerName string, model *signinModel) SignInState {
	applied, _ := applySignInModel(ctx, g, model)
	runCtx, cancel := context.WithCancel(g.ctx)
	id := uuid.NewString()
	st := SignInState{
		FlowID:       id,
		Provider:     providerID,
		ProviderName: providerName,
		State:        signInDone,
		ModelApplied: applied,
		Imported:     true,
	}
	run := &signinRun{
		id:     id,
		flow:   importedFlow{name: providerName},
		ctx:    runCtx,
		cancel: cancel,
		model:  model,
		state:  st,
	}
	g.signinMu.Lock()
	defer g.signinMu.Unlock()
	// A start that raced this one is replaced too, as any start replaces
	// the running flow.
	g.stopRunLocked(g.signin)
	g.signin = run
	g.setStateLocked(run, st)
	return st
}

// importedFlow is the flow of a sign-in that finished by importing an
// existing login: nothing runs, so it never waits, takes a code or holds a
// listener.
type importedFlow struct{ name string }

func (f importedFlow) Name() string { return f.name }

func (importedFlow) Start(context.Context) (signin.Challenge, error) {
	return signin.Challenge{}, nil
}

func (importedFlow) Wait(context.Context, signin.Challenge) (*oauth.Token, *signin.Challenge, error) {
	return nil, nil, nil
}

func (importedFlow) CodeEntry() bool { return false }

func (importedFlow) SubmitCode(context.Context, string) (*oauth.Token, error) {
	return nil, signin.ErrNoBrowserFlow
}

func (importedFlow) Close() {}

// newSignInModel parses the model a start request asks to apply. A nil
// target means no switch; a bad type is a client bug.
func newSignInModel(providerID string, t *modelTarget) (*signinModel, bool) {
	if t == nil {
		return nil, true
	}
	typ, ok := selectedModelType(t.Type)
	if !ok {
		return nil, false
	}
	return &signinModel{typ: typ, model: config.SelectedModel{Provider: providerID, Model: t.Model}}, true
}

// signInModelReason maps a failed model switch to the code a tab shows.
func signInModelReason(err error) string {
	switch {
	case errors.Is(err, workspace.ErrAgentBusy):
		return "agent_busy"
	case errors.Is(err, workspace.ErrAgentNotInitialized):
		return "agent_starting"
	case errors.Is(err, workspace.ErrModelUnavailable):
		return "model_unavailable"
	}
	return "model_failed"
}

// testAPIKey is the default provider check: the catalog entry becomes the
// provider config the TUI's key dialog tests, so both clients ask the same
// question. endpoint resolves the catalog's api_endpoint, which is a shell
// template rather than user input. It ignores ctx: TestConnection applies
// its own 5 s limit.
func testAPIKey(_ context.Context, p catwalk.Provider, key string, r, endpoint config.VariableResolver) error {
	pc := providerTestConfig(p, key, endpoint)
	return pc.TestConnection(r)
}

// providerTestConfig builds the provider config a catalog entry's key is
// tested with. The endpoint is catalog data and resolves through the
// workspace resolver. An endpoint that does not resolve, or that still
// names a variable after resolving (client/server mode resolves nothing),
// leaves BaseURL empty so TestConnection uses the provider's default URL.
// The key stays as typed.
func providerTestConfig(p catwalk.Provider, key string, endpoint config.VariableResolver) config.ProviderConfig {
	var baseURL string
	if p.APIEndpoint != "" && endpoint != nil {
		if resolved, err := endpoint.ResolveValue(p.APIEndpoint); err == nil && !strings.HasPrefix(resolved, "$") {
			baseURL = resolved
		}
	}
	return config.ProviderConfig{
		ID:      string(p.ID),
		Name:    p.Name,
		APIKey:  key,
		Type:    p.Type,
		BaseURL: baseURL,
	}
}
