package webtest

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/crush/internal/config"
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
	return seams, nil
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
