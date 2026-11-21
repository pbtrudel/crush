package multiturn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/charmbracelet/crush/internal/eval/route"
)

// Transport selects how the four prompts reach the agent. TransportRun sends
// each prompt to its own `crush run` process; TransportServer sends all of
// them to one `crush server`, which is the only way a compaction summary
// written in the background can outlive the turn that started it: the job
// lives in the coordinator's memory, and `crush run` exits at the turn end.
const (
	TransportRun    = "run"
	TransportServer = "server"
)

// serverStartTimeout bounds how long the harness waits for the server to
// answer on its port before treating the run as broken.
const serverStartTimeout = 60 * time.Second

// harnessClientID is the client the driver opens its workspace as. It is a
// fixed UUID because nothing retires it: the workspace has to outlive every
// prompt of the run.
const harnessClientID = "6f9619ff-8b86-d011-b42d-00cf4fc964ff"

// serverTransport drives one session over the HTTP API of a `crush server`
// process it starts itself, so every prompt goes to the same coordinator.
type serverTransport struct {
	bin  string
	dir  string
	args []string
	env  []string
	log  string

	base   string
	client *http.Client
	// stream reads the workspace event stream, whose open connection is what
	// holds this client's claim on the workspace: a client that only posts is
	// released after the create grace and its workspace is torn down under the
	// run (the first smoke lost its workspace 30 s in, with "workspace not
	// found" on every poll).
	stream     *http.Client
	streamStop context.CancelFunc
	cmd        *exec.Cmd
	kill       func() error
	wsID       string
	sessID     string
}

// startServer launches `crush server` in the run's checkout with the run's
// own config and data directories, and waits for it to answer.
func startServer(ctx context.Context, opts Options, src, dataDir, runDir string, env []string) (*serverTransport, error) {
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("pick a server port: %w", err)
	}
	args := []string{
		"--cwd", src, "--data-dir", dataDir,
		"server", "--host", "http://127.0.0.1:" + strconv.Itoa(port),
	}
	s := &serverTransport{
		bin:  opts.BinPath,
		dir:  src,
		args: args,
		env:  env,
		log:  filepath.Join(runDir, "server.log"),
		base: "http://127.0.0.1:" + strconv.Itoa(port),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		stream: &http.Client{},
	}
	if err := s.start(ctx); err != nil {
		return nil, err
	}
	if err := s.waitReady(ctx); err != nil {
		_ = s.close()
		return nil, err
	}
	return s, nil
}

// freePort asks the kernel for an unused loopback port. The listener is
// closed before the server binds it, so a collision is possible in principle
// and impossible in practice on an idle host.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close() //nolint:errcheck // Only used to reserve the port number.
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("unexpected listener address")
	}
	return addr.Port, nil
}

func (s *serverTransport) start(ctx context.Context) error {
	logFile, err := os.OpenFile(s.log, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create server log: %w", err)
	}
	cmd := exec.CommandContext(ctx, s.bin, s.args...)
	cmd.Dir = s.dir
	cmd.Env = s.env
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	kill := processGroup(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start server: %w", err)
	}
	s.cmd = cmd
	s.kill = kill
	return nil
}

// waitReady polls the workspace list until the server answers, and gives up
// early when the process exits.
func (s *serverTransport) waitReady(ctx context.Context) error {
	exited := make(chan error, 1)
	go func() { exited <- s.cmd.Wait() }()
	deadline := time.Now().Add(serverStartTimeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			return fmt.Errorf("server exited before answering: %w%s", err, s.logTail())
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/v1/workspaces", nil)
		if err != nil {
			return err
		}
		resp, err := s.client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("server did not answer within %s%s", serverStartTimeout, s.logTail())
}

// logTail is the end of the server log, so a failed start says why.
func (s *serverTransport) logTail() string {
	data, err := os.ReadFile(s.log)
	if err != nil {
		return ""
	}
	const keep = 600
	if len(data) > keep {
		data = data[len(data)-keep:]
	}
	return "\n" + string(data)
}

// createWorkspace opens the run's checkout as a workspace in yolo mode, which
// is what an unattended run needs: a permission prompt has no client to
// answer it over the API.
func (s *serverTransport) createWorkspace(ctx context.Context, src string) error {
	var ws struct {
		ID string `json:"id"`
	}
	// The server keys a workspace by the client that opened it and rejects
	// one without a client id, so the run names itself with a fixed UUID.
	body := map[string]any{"path": src, "permission_mode": "yolo", "client_id": harnessClientID}
	if err := s.post(ctx, "/v1/workspaces", body, &ws); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	if ws.ID == "" {
		return errors.New("create workspace: no id in response")
	}
	s.wsID = ws.ID
	return nil
}

// initAgent initializes the workspace's agent, which the message endpoint
// refuses to serve before this has run. It asks for the interactive agent on
// purpose: a coordinator initialized non-interactively (what `crush run` uses)
// never starts a background compaction, which is the feature under test.
func (s *serverTransport) initAgent(ctx context.Context) error {
	if err := s.post(ctx, "/v1/workspaces/"+s.wsID+"/agent/init", map[string]any{"interactive": true}, nil); err != nil {
		return fmt.Errorf("initialize agent: %w", err)
	}
	return nil
}

// attach opens the workspace event stream and keeps it open, which is what
// keeps the workspace (and with it the coordinator that owns any background
// summary) alive for the whole run.
func (s *serverTransport) attach(ctx context.Context) error {
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet,
		s.base+"/v1/workspaces/"+s.wsID+"/events?client_id="+harnessClientID, nil)
	if err != nil {
		cancel()
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := s.stream.Do(req)
	if err != nil {
		cancel()
		return fmt.Errorf("open event stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		cancel()
		return fmt.Errorf("open event stream: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	s.streamStop = cancel
	go func() {
		defer resp.Body.Close() //nolint:errcheck // Read until the run ends.
		_, _ = io.Copy(io.Discard, resp.Body)
	}()
	return nil
}

// createSession opens the one session every prompt of this run continues.
func (s *serverTransport) createSession(ctx context.Context) error {
	var sess struct {
		ID string `json:"id"`
	}
	if err := s.post(ctx, "/v1/workspaces/"+s.wsID+"/sessions", map[string]any{}, &sess); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if sess.ID == "" {
		return errors.New("create session: no id in response")
	}
	s.sessID = sess.ID
	return nil
}

// prompt sends one prompt and waits for its turn to end, returning the wall
// time the turn took. Completion is read from the agent session's busy flag:
// every step of a turn writes a finished message, so a message cannot mark a
// turn's end.
func (s *serverTransport) prompt(ctx context.Context, text string, limit time.Duration) (int64, error) {
	started := time.Now()
	body := map[string]any{"session_id": s.sessID, "prompt": text}
	if err := s.post(ctx, "/v1/workspaces/"+s.wsID+"/agent", body, nil); err != nil {
		return 0, fmt.Errorf("send prompt: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if err := s.waitBusy(runCtx); err != nil {
		return time.Since(started).Milliseconds(), err
	}
	for {
		busy, err := s.isBusy(runCtx)
		if err != nil {
			return time.Since(started).Milliseconds(), err
		}
		if !busy {
			return time.Since(started).Milliseconds(), nil
		}
		select {
		case <-runCtx.Done():
			_ = s.post(context.WithoutCancel(runCtx), "/v1/workspaces/"+s.wsID+"/agent/sessions/"+s.sessID+"/cancel", map[string]any{}, nil)
			return time.Since(started).Milliseconds(), fmt.Errorf("turn exceeded %s", limit)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// waitBusy waits for the session to report a run, so the first idle poll does
// not look like a finished turn.
func (s *serverTransport) waitBusy(ctx context.Context) error {
	for {
		busy, err := s.isBusy(ctx)
		if err != nil {
			return err
		}
		if busy {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the run never started: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// isBusy reads the agent session's state.
func (s *serverTransport) isBusy(ctx context.Context) (bool, error) {
	var state struct {
		IsBusy bool `json:"is_busy"`
	}
	if err := s.get(ctx, "/v1/workspaces/"+s.wsID+"/agent/sessions/"+s.sessID, &state); err != nil {
		return false, err
	}
	return state.IsBusy, nil
}

// close stops the server and waits briefly for it to flush.
func (s *serverTransport) close() error {
	if s.streamStop != nil {
		s.streamStop()
	}
	if s.cmd == nil || s.cmd.Process == nil {
		s.client.CloseIdleConnections()
		s.stream.CloseIdleConnections()
		return nil
	}
	if s.kill != nil {
		_ = s.kill()
	}
	_ = s.cmd.Process.Kill()
	_, err := s.cmd.Process.Wait()
	s.client.CloseIdleConnections()
	s.stream.CloseIdleConnections()
	return err
}

func (s *serverTransport) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return err
	}
	return s.do(req, out)
}

func (s *serverTransport) post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req, out)
}

// do performs one request and decodes the response when out is set. Any
// status outside 2xx is an error carrying the body, which is the only place
// the API explains a rejection.
func (s *serverTransport) do(req *http.Request, out any) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // Body is drained below.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s %s: status %d: %s", req.Method, req.URL.Path, resp.StatusCode, bytes.TrimSpace(body))
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", req.Method, req.URL.Path, err)
	}
	return nil
}

// writeServerModels puts the route's models into the run's global config.
// A `crush run` process takes them on its command line, but the server reads
// its models from config, so the server transport has to write them down.
func writeServerModels(path string, r *route.Route) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated config: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode generated config: %w", err)
	}
	main := map[string]any{"provider": r.Provider, "model": r.Model}
	if r.ReasoningEffort != "" {
		main["reasoning_effort"] = r.ReasoningEffort
	}
	doc["models"] = map[string]any{
		"main":  main,
		"small": map[string]any{"provider": r.Provider, "model": r.SmallModel},
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config with models: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config with models: %w", err)
	}
	return nil
}
