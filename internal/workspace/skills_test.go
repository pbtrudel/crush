package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
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
			ID:          "/skills/crush-config/SKILL.md",
			Name:        "crush-config",
			Description: "disabled in the config",
			Source:      string(skills.SourceUser),
		}})
	}))
	defer srv.Close()

	w := newTestClientWorkspace(t, srv)
	entries, err := w.ListAllSkills(t.Context())
	require.NoError(t, err)
	require.Equal(t, []skills.CatalogEntry{{
		ID:          "/skills/crush-config/SKILL.md",
		Name:        "crush-config",
		Description: "disabled in the config",
		Source:      skills.SourceUser,
	}}, entries)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "/v1/workspaces/ws-1/skills/all", gotPath)
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
