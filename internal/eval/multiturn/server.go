package multiturn

import (
	"bytes"
	"context"
	"database/sql"
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

	"github.com/charmbracelet/crush/internal/db"
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
	// dataDir is the run's data directory, which the workspace must open.
	dataDir string

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
		bin:     opts.BinPath,
		dir:     src,
		args:    args,
		env:     env,
		log:     filepath.Join(runDir, "server.log"),
		dataDir: dataDir,
		base:    "http://127.0.0.1:" + strconv.Itoa(port),
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
	// The data directory and environment travel with the request because the
	// workspace, not the server process, opens the store and runs the tools:
	// without data_dir the session lands in the checkout's .crush and the
	// driver reads an empty database.
	body := map[string]any{
		"path":            src,
		"permission_mode": "yolo",
		"client_id":       harnessClientID,
		"data_dir":        s.dataDir,
		"env":             s.env,
	}
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

// prompt sends one prompt and waits for its turn to settle. It returns the
// wall time to settle and, separately, the time at which the agent session
// stopped reporting busy.
//
// The busy flag alone cannot mark a turn's end: the first smoke showed it
// clearing while the turn-end compaction was still writing its summary, so
// the next prompt was posted mid-compaction and the turns overlapped. A turn
// is settled once the session is idle, the prompt's user row and an
// assistant row after it are stored, and no assistant row (summary rows
// included) is still unfinished, on three polls in a row. Waiting for summary
// rows means a background summary finishes before the next prompt; that is
// what a user's think time between prompts would hide, and the notes report
// it as such.
func (s *serverTransport) prompt(ctx context.Context, text string, limit time.Duration) (wall, idle int64, err error) {
	before, err := s.lastRow(ctx)
	if err != nil {
		return 0, 0, err
	}
	started := time.Now()
	body := map[string]any{"session_id": s.sessID, "prompt": text}
	if err := s.post(ctx, "/v1/workspaces/"+s.wsID+"/agent", body, nil); err != nil {
		return 0, 0, fmt.Errorf("send prompt: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	stable := 0
	for {
		busy, err := s.isBusy(runCtx)
		if err != nil {
			return time.Since(started).Milliseconds(), idle, err
		}
		if busy {
			idle, stable = 0, 0
		} else {
			if idle == 0 {
				idle = time.Since(started).Milliseconds()
			}
			done, err := s.settled(runCtx, before)
			if err != nil {
				return time.Since(started).Milliseconds(), idle, err
			}
			if done {
				stable++
			} else {
				stable = 0
			}
			if stable >= 3 {
				return time.Since(started).Milliseconds(), idle, nil
			}
		}
		select {
		case <-runCtx.Done():
			_ = s.post(context.WithoutCancel(runCtx), "/v1/workspaces/"+s.wsID+"/agent/sessions/"+s.sessID+"/cancel", map[string]any{}, nil)
			return time.Since(started).Milliseconds(), idle, fmt.Errorf("turn exceeded %s", limit)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// lastRow is the highest message rowid stored so far, the mark a prompt's own
// rows are found after. Without a store (the stub test) it is zero.
func (s *serverTransport) lastRow(ctx context.Context) (int64, error) {
	if s.dataDir == "" {
		return 0, nil
	}
	conn, err := db.ConnectReadOnly(ctx, filepath.Join(s.dataDir, "crush.db"))
	if err != nil {
		return 0, err
	}
	defer conn.Close() //nolint:errcheck // Read-only connection.
	var last sql.NullInt64
	if err := conn.QueryRowContext(ctx, `select max(rowid) from messages`).Scan(&last); err != nil {
		return 0, err
	}
	return last.Int64, nil
}

// settled reports whether the prompt sent after row before has its user row,
// an assistant row after that, and no assistant row left unfinished.
func (s *serverTransport) settled(ctx context.Context, before int64) (bool, error) {
	if s.dataDir == "" {
		return true, nil
	}
	conn, err := db.ConnectReadOnly(ctx, filepath.Join(s.dataDir, "crush.db"))
	if err != nil {
		return false, err
	}
	defer conn.Close() //nolint:errcheck // Read-only connection.
	var user sql.NullInt64
	if err := conn.QueryRowContext(ctx,
		`select min(rowid) from messages where session_id = ? and role = 'user' and rowid > ?`,
		s.sessID, before).Scan(&user); err != nil {
		return false, err
	}
	if !user.Valid {
		return false, nil
	}
	var answered, open int
	if err := conn.QueryRowContext(ctx,
		`select count(*) from messages where session_id = ? and role = 'assistant' and rowid > ? and finished_at is not null`,
		s.sessID, user.Int64).Scan(&answered); err != nil {
		return false, err
	}
	if err := conn.QueryRowContext(ctx,
		`select count(*) from messages where session_id = ? and role = 'assistant' and finished_at is null`,
		s.sessID).Scan(&open); err != nil {
		return false, err
	}
	return answered > 0 && open == 0, nil
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
