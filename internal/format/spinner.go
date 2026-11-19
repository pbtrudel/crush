package format

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/anim"
	"github.com/charmbracelet/x/ansi"
)

// Spinner wraps the bubbles spinner for non-interactive mode
type Spinner struct {
	done        chan struct{}
	prog        *tea.Program
	interrupted atomic.Bool
}

type model struct {
	cancel      context.CancelFunc
	anim        *anim.Anim
	interrupted *atomic.Bool
}

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(anim.FrameInterval(), func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd  { return tick() }
func (m model) View() tea.View { return tea.NewView(m.anim.Render()) }

// Update implements tea.Model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			// The terminal is in raw mode here, so Ctrl-C is a key and the
			// process never sees SIGINT. Remember that the user asked to
			// stop so the caller can end the way a signal would.
			m.interrupted.Store(true)
			m.cancel()
			return m, tea.Quit
		}
	case tickMsg:
		m.anim.Advance()
		return m, tick()
	}
	return m, nil
}

// NewSpinner creates a new spinner with the given message
func NewSpinner(ctx context.Context, cancel context.CancelFunc, animSettings anim.Settings) *Spinner {
	s := &Spinner{done: make(chan struct{}, 1)}
	m := model{
		anim:        anim.New(animSettings),
		cancel:      cancel,
		interrupted: &s.interrupted,
	}

	s.prog = tea.NewProgram(m, tea.WithOutput(os.Stderr), tea.WithContext(ctx))

	return s
}

// Start begins the spinner animation
func (s *Spinner) Start() {
	go func() {
		defer close(s.done)
		_, err := s.prog.Run()
		// ensures line is cleared
		fmt.Fprint(os.Stderr, ansi.EraseEntireLine)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, tea.ErrInterrupted) {
			fmt.Fprintf(os.Stderr, "Error running spinner: %v\n", err)
		}
	}()
}

// Interrupted reports whether the user stopped the run from the spinner, with
// Ctrl-C or Esc. The spinner runs with the terminal in raw mode, so those keys
// arrive as key presses and the process never receives SIGINT; a caller that
// sees this true should end the run as if it had.
func (s *Spinner) Interrupted() bool { return s.interrupted.Load() }

// Stop ends the spinner animation
func (s *Spinner) Stop() {
	s.prog.Quit()
	<-s.done
}
