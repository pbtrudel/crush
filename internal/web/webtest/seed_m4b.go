package webtest

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/skills"
)

// SeedM4b adds the M4b surfaces on top of the M4 seeds. The tab's display
// settings start on the Holodeck theme unless WEBFAKE_THEME names the one
// a screenshot needs; the runs of other seeds extend this function.
func SeedM4b(f *Fake, done <-chan struct{}, clock time.Time) (M4Seams, error) {
	seams, err := SeedM4(f, done, clock)
	if err != nil {
		return M4Seams{}, err
	}
	theme, err := m4bTheme()
	if err != nil {
		return M4Seams{}, err
	}
	if f.Cfg.Options == nil {
		f.Cfg.Options = &config.Options{}
	}
	f.Cfg.Options.WebDisplay = &config.WebDisplayOptions{Theme: theme}
	// The m4b scenario carries a channel-capable MCP server, a skill the
	// global config disables, and a terminal that answers automation.
	if f.MCP == nil {
		f.MCP = make(map[string]mcp.ClientInfo)
	}
	f.MCP["discord"] = mcp.ClientInfo{
		Name:           "discord",
		State:          mcp.StateConnected,
		Counts:         mcp.Counts{Tools: 3},
		ChannelCapable: true,
	}
	f.AddDiscoveredSkill(skills.CatalogEntry{
		ID:            "crush-config",
		Name:          "crush-config",
		Description:   "Read and change Crush's own configuration",
		Label:         "project:crush-config",
		Source:        skills.SourceProject,
		UserInvocable: true,
	})
	f.Cfg.Options.DisabledSkills = append(f.Cfg.Options.DisabledSkills, "crush-config")
	seams.DockerMCPAvailable = func() bool { return true }
	seams.Automation = m4bAutomation
	return seams, nil
}

// m4bAutomation answers the two automation commands the m4b scenario runs,
// with the replies its tests expect.
func m4bAutomation(_ context.Context, _, name, args string) (string, error) {
	switch name {
	case "goal":
		return "Goal set: " + args, nil
	case "loop":
		return "Loops: 2 active", nil
	}
	return "", fmt.Errorf("unknown automation command %q", name)
}

// m4bTheme reads the theme a run starts on. Every other display field keeps
// its default, which is what an absent web_display block means.
func m4bTheme() (string, error) {
	switch theme := os.Getenv("WEBFAKE_THEME"); theme {
	case "", "holodeck":
		return "holodeck", nil
	case "solid":
		return "solid", nil
	default:
		return "", fmt.Errorf("invalid WEBFAKE_THEME: %q", theme)
	}
}
