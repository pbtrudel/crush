package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/stretchr/testify/require"
)

// TestListAllSkillsRoute covers GET /v1/workspaces/{id}/skills/all: the
// route answers the whole discovered set, including skills the config
// disabled, while /skills keeps answering the active set.
func TestListAllSkillsRoute(t *testing.T) {
	t.Parallel()

	h := newE2EHarness(t)
	active := &skills.Skill{Name: "go-tests", Description: "Runs the tests.", SkillFilePath: "/skills/go-tests/SKILL.md"}
	disabled := &skills.Skill{Name: "crush-config", SkillFilePath: "/skills/crush-config/SKILL.md"}
	h.workspace.Skills = skills.NewManager([]*skills.Skill{active, disabled}, []*skills.Skill{active}, nil)

	get := func(path string) []proto.SkillInfo {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.httpSrv.URL+path, nil)
		require.NoError(t, err)
		rsp, err := h.httpSrv.Client().Do(req)
		require.NoError(t, err)
		defer rsp.Body.Close()
		require.Equal(t, http.StatusOK, rsp.StatusCode)
		var out []proto.SkillInfo
		require.NoError(t, json.NewDecoder(rsp.Body).Decode(&out))
		return out
	}

	all := get("/v1/workspaces/" + h.workspace.ID + "/skills/all")
	require.Len(t, all, 2)
	require.Equal(t, "go-tests", all[0].Name)
	require.Equal(t, "crush-config", all[1].Name)

	visible := get("/v1/workspaces/" + h.workspace.ID + "/skills")
	require.Len(t, visible, 1, "the active route still hides a disabled skill")
	require.Equal(t, "go-tests", visible[0].Name)
}
