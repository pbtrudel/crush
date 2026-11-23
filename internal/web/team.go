package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

// teamPollInterval matches the TUI sidebar's refresh (teamRefreshInterval).
const teamPollInterval = time.Second

// TeamTask is one shared task as the task board shows it.
type TeamTask struct {
	ID          string               `json:"id"`
	Revision    int                  `json:"revision"`
	Subject     string               `json:"subject"`
	Description string               `json:"description"`
	Status      agentteam.TaskStatus `json:"status"`
	OwnerID     string               `json:"owner_id,omitempty"`
	OwnerName   string               `json:"owner_name,omitempty"`
	BlockedBy   []string             `json:"blocked_by"`
	Ready       bool                 `json:"ready"`
	SessionID   string               `json:"session_id,omitempty"`
}

// TeamState is the workspace's Agent Team roster and live task board. The
// inbox stays out: it can hold large messages the browser never shows.
type TeamState struct {
	TeamID  string             `json:"team_id"`
	Members []agentteam.Member `json:"members"`
	Tasks   []TeamTask         `json:"tasks"`
}

func emptyTeam() TeamState {
	return TeamState{Members: []agentteam.Member{}, Tasks: []TeamTask{}}
}

// teamStateFrom trims the durable Team record to what the browser shows.
// Inactive teammates stay on the roster so the stage can dim them, and
// deleted tasks are dropped.
func teamStateFrom(s agentteam.State) TeamState {
	out := emptyTeam()
	out.TeamID = s.TeamID
	if s.Members != nil {
		out.Members = s.Members
	}
	for _, t := range agentteam.TaskViews(s) {
		if t.Status == agentteam.TaskDeleted {
			continue
		}
		blocked := t.BlockedBy
		if blocked == nil {
			blocked = []string{}
		}
		out.Tasks = append(out.Tasks, TeamTask{
			ID: t.ID, Revision: t.Revision, Subject: t.Subject, Description: t.Description,
			Status: t.Status, OwnerID: t.OwnerID, OwnerName: t.OwnerName,
			BlockedBy: blocked, Ready: t.Ready, SessionID: t.SessionID,
		})
	}
	return out
}

// teamWatcher rereads team.json and agents.json and reports what changed.
type teamWatcher struct {
	statePath, runsPath string

	mu         sync.Mutex
	team       TeamState
	runs       []agentrun.Run
	teamRaw    []byte
	runsRaw    []byte
	warned     bool
	runsWarned bool
}

func newTeamWatcher(dir string) *teamWatcher {
	w := &teamWatcher{team: emptyTeam(), runs: []agentrun.Run{}}
	if dir != "" {
		w.statePath = filepath.Join(dir, "team.json")
		w.runsPath = filepath.Join(dir, "agents.json")
	}
	return w
}

// poll returns the payloads that changed since the last poll, nil for an
// unchanged one. A file that fails to load keeps the last good state: the
// writer replaces it atomically, so a failure is a real problem, and blanking
// the board would only make it flicker.
func (w *teamWatcher) poll() (team, runs json.RawMessage) {
	if w.statePath == "" {
		return nil, nil
	}
	// Read both files before taking the lock: a snapshot asks current for
	// the last good state while a poll is running, and a slow disk must
	// not hold it up.
	state, stateErr := agentteam.LoadState(w.statePath)
	list, runsErr := agentrun.LoadErr(w.runsPath)

	w.mu.Lock()
	defer w.mu.Unlock()
	if stateErr != nil {
		if !w.warned {
			slog.Warn("Web mode could not read the team state", "error", stateErr)
			w.warned = true
		}
	} else {
		w.warned = false
		next := teamStateFrom(state)
		if raw, err := json.Marshal(next); err == nil && !bytes.Equal(raw, w.teamRaw) {
			w.team, w.teamRaw, team = next, raw, raw
		}
	}
	if runsErr != nil {
		if !w.runsWarned {
			slog.Warn("Web mode could not read the agent runs", "error", runsErr)
			w.runsWarned = true
		}
		return team, nil
	}
	w.runsWarned = false
	if list == nil {
		list = []agentrun.Run{}
	}
	if raw, err := json.Marshal(list); err == nil && !bytes.Equal(raw, w.runsRaw) {
		w.runs, w.runsRaw, runs = list, raw, raw
	}
	return team, runs
}

// current returns the last good team state and runs for a snapshot.
func (w *teamWatcher) current() (TeamState, []agentrun.Run) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.team, w.runs
}

// watchTeam publishes the team and agent run frames whenever their payload
// bytes change. It returns when the gateway shuts down.
func (g *Gateway) watchTeam(interval time.Duration) {
	defer close(g.teamDone)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-t.C:
			team, runs := g.team.poll()
			if team != nil {
				g.hub.publish(frameTeamState, team)
			}
			if runs != nil {
				g.hub.publish(frameAgentRuns, runs)
			}
		}
	}
}

// teamDir locates the directory holding team.json and agents.json. Empty
// means no config, so there is no team to watch.
func teamDir(opts Options) string {
	if opts.AgentTeamsDir != "" {
		return opts.AgentTeamsDir
	}
	cfg := opts.Workspace.Config()
	if cfg == nil || cfg.Options == nil || cfg.Options.DataDirectory == "" {
		return ""
	}
	return filepath.Join(cfg.Options.DataDirectory, "agent-teams")
}

func (g *Gateway) handleGetSession(w http.ResponseWriter, r *http.Request) {
	s, err := g.ws.GetSession(r.Context(), r.PathValue("sid"))
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "unknown_session", "no such session")
			return
		}
		writeInternal(w, "get_session", err)
		return
	}
	writeJSON(w, http.StatusOK, innerPayload(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: s}))
}

func (g *Gateway) handleTeamMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	switch {
	case body.SessionID == "":
		writeError(w, http.StatusBadRequest, "missing_session", "session_id is required")
		return
	case strings.TrimSpace(body.Text) == "":
		writeError(w, http.StatusBadRequest, "empty_message", "message is empty")
		return
	case len(body.Text) > agentteam.DefaultMaxMessageBytes:
		writeError(w, http.StatusBadRequest, "message_too_large", "message is over 64 KiB")
		return
	}
	if err := g.ws.TeamSendMessage(r.Context(), body.SessionID, r.PathValue("member"), body.Text); err != nil {
		writeTeamError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleTeamInterrupt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.SessionID == "" {
		writeError(w, http.StatusBadRequest, "missing_session", "session_id is required")
		return
	}
	if err := g.ws.TeamInterrupt(r.Context(), body.SessionID, r.PathValue("member")); err != nil {
		writeTeamError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeTeamError answers a Team action failure. A disabled team and a
// refusal are the caller's to fix, so they keep their meaning across the
// wire; anything else is a workspace failure whose text may name a socket
// or a path and stays in the log.
func writeTeamError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agent.ErrAgentTeamsDisabled):
		writeError(w, http.StatusConflict, "teams_disabled", "Agent Teams is not enabled")
	case agentteam.ErrorCode(err) != "":
		// Team errors are written for the model and name only the member
		// and the rule it broke, so they are safe to show.
		writeError(w, http.StatusConflict, "team_refused", err.Error())
	default:
		writeInternal(w, "team_failed", err)
	}
}
