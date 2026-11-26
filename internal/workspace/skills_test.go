package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/client"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/stretchr/testify/require"
)

// TestClientWorkspaceListAllSkills pins the client-mode path: the SDK asks
// the server for a workspace's whole discovered set, including the skills
// the config disabled, and converts the answer back to catalog entries.
func TestClientWorkspaceListAllSkills(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		mu.Unlock()
		_ = json.NewEncoder(w).Encode([]proto.SkillInfo{{
			ID:            "/skills/crush-config/SKILL.md",
			Name:          "crush-config",
			Description:   "disabled in the config",
			Label:         "user",
			Source:        string(skills.SourceUser),
			UserInvocable: true,
		}})
	}))
	defer srv.Close()

	w := newTestClientWorkspace(t, srv)
	entries, err := w.ListAllSkills(t.Context())
	require.NoError(t, err)
	require.Equal(t, []skills.CatalogEntry{{
		ID:            "/skills/crush-config/SKILL.md",
		Name:          "crush-config",
		Description:   "disabled in the config",
		Label:         "user",
		Source:        skills.SourceUser,
		UserInvocable: true,
	}}, entries)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "/v1/workspaces/ws-1/skills/all", gotPath)
}

// TestClientWorkspaceListSkillsRoundTripsEveryField pins the visible-list
// conversion field by field: the browser and the command palette must see
// the same skill the TUI does, including the label, the source and whether
// it is user-invocable.
func TestClientWorkspaceListSkillsRoundTripsEveryField(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		mu.Unlock()
		_ = json.NewEncoder(w).Encode([]proto.SkillInfo{{
			ID:            "/skills/go-tests/SKILL.md",
			Name:          "go-tests",
			Description:   "Runs the tests.",
			Label:         "project",
			Source:        string(skills.SourceProject),
			UserInvocable: true,
		}})
	}))
	defer srv.Close()

	w := newTestClientWorkspace(t, srv)
	entries, err := w.ListSkills(t.Context())
	require.NoError(t, err)
	require.Equal(t, []skills.CatalogEntry{{
		ID:            "/skills/go-tests/SKILL.md",
		Name:          "go-tests",
		Description:   "Runs the tests.",
		Label:         "project",
		Source:        skills.SourceProject,
		UserInvocable: true,
	}}, entries)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "/v1/workspaces/ws-1/skills", gotPath)
}

// TestClientWorkspaceSkillsMapLostLink pins the two link states the
// gateway branches on: a workspace the server forgot reads as
// ErrWorkspaceGone, and a request that never reached the server reads as
// ErrServerUnreachable, for both skill lists.
func TestClientWorkspaceSkillsMapLostLink(t *testing.T) {
	t.Parallel()

	t.Run("workspace gone", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"workspace not found"}`))
		}))
		defer srv.Close()

		w := newTestClientWorkspace(t, srv)
		_, err := w.ListAllSkills(t.Context())
		require.ErrorIs(t, err, ErrWorkspaceGone)
		_, err = w.ListSkills(t.Context())
		require.ErrorIs(t, err, ErrWorkspaceGone)
	})

	t.Run("server unreachable", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		w := newTestClientWorkspace(t, srv)
		srv.Close()

		_, err := w.ListAllSkills(t.Context())
		require.ErrorIs(t, err, ErrServerUnreachable)
		_, err = w.ListSkills(t.Context())
		require.ErrorIs(t, err, ErrServerUnreachable)
	})
}

// TestClientWorkspaceSkillsKeepCallerCancellation pins that a caller that
// gave up, or whose deadline passed, keeps that error. The gateway maps
// ErrServerUnreachable to a 503 "reconnecting" answer, which would tell
// the browser to retry a request the caller already abandoned.
func TestClientWorkspaceSkillsKeepCallerCancellation(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	// Cleanup, not defer: the parallel subtests run after this function
	// returns.
	t.Cleanup(srv.Close)

	w := newTestClientWorkspace(t, srv)

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := w.ListAllSkills(ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ErrServerUnreachable)

		_, err = w.ListSkills(ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ErrServerUnreachable)
	})

	t.Run("deadline exceeded", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()

		_, err := w.ListAllSkills(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.NotErrorIs(t, err, ErrServerUnreachable)

		_, err = w.ListSkills(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.NotErrorIs(t, err, ErrServerUnreachable)
	})
}

// TestClientWorkspaceListAllSkillsOlderServer pins the fallback path: a
// server without the full-set route answers a bare 404, which reads as
// unsupported while the visible-list route keeps working.
func TestClientWorkspaceListAllSkillsOlderServer(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/workspaces/ws-1/skills/all" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]proto.SkillInfo{{ID: "a", Name: "go-tests"}})
	}))
	defer srv.Close()

	w := newTestClientWorkspace(t, srv)
	_, err := w.ListAllSkills(t.Context())
	require.ErrorIs(t, err, client.ErrUnsupported)
	require.NotErrorIs(t, err, ErrWorkspaceGone)

	entries, err := w.ListSkills(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "go-tests", entries[0].Name)
}

// TestClientWorkspaceSkillsServerErrorStaysGeneric pins that a server
// failure is neither a gone workspace nor a lost link: the gateway can
// still fall back to the visible set and answer the real refusal.
func TestClientWorkspaceSkillsServerErrorStaysGeneric(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"catalog unavailable"}`))
	}))
	defer srv.Close()

	w := newTestClientWorkspace(t, srv)
	_, err := w.ListAllSkills(t.Context())
	require.ErrorContains(t, err, "catalog unavailable")
	require.NotErrorIs(t, err, ErrWorkspaceGone)
	require.NotErrorIs(t, err, ErrServerUnreachable)

	_, err = w.ListSkills(t.Context())
	require.ErrorContains(t, err, "catalog unavailable")
	require.NotErrorIs(t, err, ErrWorkspaceGone)
	require.NotErrorIs(t, err, ErrServerUnreachable)
}

// TestAppWorkspaceListAllSkillsIncludesDisabled pins the in-process path:
// a skill the config disabled is missing from ListSkills but present in
// ListAllSkills, so the browser can offer to re-enable it.
func TestAppWorkspaceListAllSkillsIncludesDisabled(t *testing.T) {
	t.Parallel()

	active := &skills.Skill{Name: "go-tests", Description: "Runs the tests.", SkillFilePath: "/skills/go-tests/SKILL.md"}
	disabled := &skills.Skill{Name: "crush-config", SkillFilePath: "/skills/crush-config/SKILL.md"}
	mgr := skills.NewManager([]*skills.Skill{active, disabled}, []*skills.Skill{active}, nil)
	w := NewAppWorkspace(&app.App{Skills: mgr}, nil)

	all, err := w.ListAllSkills(t.Context())
	require.NoError(t, err)
	names := make([]string, len(all))
	for i, entry := range all {
		names[i] = entry.Name
	}
	require.Equal(t, []string{"go-tests", "crush-config"}, names)

	visible, err := w.ListSkills(t.Context())
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.Equal(t, "go-tests", visible[0].Name)
}
