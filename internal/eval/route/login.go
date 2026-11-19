package route

import (
	"context"
	"os/exec"

	"github.com/charmbracelet/crush/internal/eval/home"
)

// LoginCommand returns the `crush login <provider>` command for the
// route, with the scrubbed child environment. CRUSH_GLOBAL_DATA points at
// the eval home, so the token lands there and never in the user's own
// data directory. The caller runs it.
func (r *Route) LoginCommand(ctx context.Context, h *home.Home, crushBin string, parent []string) (*exec.Cmd, error) {
	env, err := r.ChildEnv(h, h.ConfigDir(), parent)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, crushBin, "login", r.Provider)
	cmd.Env = env
	return cmd, nil
}
