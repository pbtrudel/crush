package pinentry

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestParsePS(t *testing.T) {
	t.Parallel()

	out := []byte(`    1     0 /sbin/launchd
  338     1 /usr/libexec/logd
 1337   338 /opt/homebrew/bin/pinentry-curses
 1442     1 gpg
 2499     1 gpg-agent
 3000  1442 /usr/bin/Google Chrome Helper
bad line here
 1443    no gpg
`)

	procs := parsePS(out)
	require.Len(t, procs, 6)
	require.Equal(t, Proc{PID: 1, PPID: 0, Name: "launchd", Path: "/sbin/launchd"}, procs[0])
	require.Equal(t, Proc{PID: 1337, PPID: 338, Name: "pinentry-curses", Path: "/opt/homebrew/bin/pinentry-curses"}, procs[2])
	// Bare names (Linux ps style) carry no path.
	require.Equal(t, Proc{PID: 1442, PPID: 1, Name: "gpg"}, procs[3])
	// gpg-agent must not be confused with gpg.
	require.Equal(t, Proc{PID: 2499, PPID: 1, Name: "gpg-agent"}, procs[4])
	// Commands with spaces keep the full remainder as their name.
	require.Equal(t, Proc{PID: 3000, PPID: 1442, Name: "Google Chrome Helper", Path: "/usr/bin/Google Chrome Helper"}, procs[5])
}

func TestClassify(t *testing.T) {
	t.Parallel()

	handover, ctrlL, gpg := classify([]Proc{{PID: 1, Name: "pinentry-curses"}, ownProc(2, "gpg")}, nil)
	require.True(t, handover)
	require.True(t, ctrlL)
	require.True(t, gpg)

	handover, ctrlL, gpg = classify([]Proc{{PID: 1, Name: "pinentry-tty"}}, nil)
	require.True(t, handover)
	require.False(t, ctrlL, "pinentry-tty reads raw bytes; Ctrl-L would corrupt the passphrase")
	require.False(t, gpg)

	handover, ctrlL, _ = classify([]Proc{{PID: 1, Name: "pinentry"}}, nil)
	require.True(t, handover)
	require.True(t, ctrlL, "an unresolvable plain pinentry defaults to the curses behavior")

	// GUI pinentry flavors never need the terminal.
	handover, ctrlL, gpg = classify([]Proc{{PID: 1, Name: "pinentry-mac"}, {PID: 2, Name: "pinentry-qt"}}, nil)
	require.False(t, handover)
	require.False(t, ctrlL)
	require.False(t, gpg)

	// gpg-agent is not gpg.
	handover, ctrlL, gpg = classify([]Proc{{PID: 1, Name: "gpg-agent"}}, nil)
	require.False(t, handover)
	require.False(t, ctrlL)
	require.False(t, gpg)
}

func TestClassifyRequiresOwnTerminal(t *testing.T) {
	t.Parallel()

	curses := []Proc{{PID: 1, Name: "pinentry-curses"}}
	ours := func(int) bool { return true }
	foreign := func(int) bool { return false }

	handover, ctrlL, _ := classify(curses, ours)
	require.True(t, handover, "a dialog holding our terminal takes it")
	require.True(t, ctrlL)

	handover, ctrlL, _ = classify(curses, foreign)
	require.False(t, handover, "a dialog on another terminal must not take ours")
	require.False(t, ctrlL)

	// Only a hold of our own terminal counts among several dialogs.
	handover, ctrlL, _ = classify([]Proc{{PID: 1, Name: "pinentry-curses"}, {PID: 2, Name: "pinentry-tty"}}, func(pid int) bool {
		return pid == 2
	})
	require.True(t, handover)
	require.False(t, ctrlL, "only the pinentry-tty holds our terminal")

	// GUI flavors never take the terminal, whatever they hold.
	handover, ctrlL, _ = classify([]Proc{{PID: 1, Name: "pinentry-mac"}}, ours)
	require.False(t, handover)
	require.False(t, ctrlL)

	// An unidentifiable terminal keeps the name-only match.
	handover, ctrlL, _ = classify(curses, nil)
	require.True(t, handover)
	require.True(t, ctrlL)
}

func TestResolveFlavor(t *testing.T) {
	t.Parallel()

	t.Run("direct names", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, flavorCurses, resolveFlavor(Proc{Name: "pinentry-curses"}))
		require.Equal(t, flavorTTY, resolveFlavor(Proc{Name: "pinentry-tty"}))
		require.Equal(t, flavorGUI, resolveFlavor(Proc{Name: "pinentry-mac"}))
	})

	t.Run("symlink to curses", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		real := filepath.Join(dir, "pinentry-curses")
		require.NoError(t, os.WriteFile(real, []byte(""), 0o755))
		link := filepath.Join(dir, "pinentry")
		require.NoError(t, os.Symlink(real, link))
		require.Equal(t, flavorCurses, resolveFlavor(Proc{PID: 1, Name: "pinentry", Path: link}))
	})

	t.Run("alternatives chain to gui", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		real := filepath.Join(dir, "pinentry-qt")
		require.NoError(t, os.WriteFile(real, []byte(""), 0o755))
		alt := filepath.Join(dir, "alt-pinentry")
		require.NoError(t, os.Symlink(real, alt))
		link := filepath.Join(dir, "pinentry")
		require.NoError(t, os.Symlink(alt, link))
		require.Equal(t, flavorGUI, resolveFlavor(Proc{PID: 1, Name: "pinentry", Path: link}))
	})

	t.Run("real binary defaults to curses", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		real := filepath.Join(dir, "pinentry")
		require.NoError(t, os.WriteFile(real, []byte(""), 0o755))
		require.Equal(t, flavorCurses, resolveFlavor(Proc{PID: 1, Name: "pinentry", Path: real}))
	})

	t.Run("unresolvable defaults to curses", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, flavorCurses, resolveFlavor(Proc{PID: 1, Name: "pinentry"}))
	})
}

func newTestService() *Service {
	s := NewService()
	s.reopenWindow = 300 * time.Millisecond
	s.touchGrace = 800 * time.Millisecond
	s.touchMax = 3 * time.Minute
	return s
}

func tickAll(s *Service, start time.Time, procs []Proc, interval time.Duration, n int) time.Time {
	now := start
	for range n {
		s.tick(now, procs)
		now = now.Add(interval)
	}
	return now
}

func TestWatcherPassphraseFlow(t *testing.T) {
	t.Parallel()

	s := newTestService()
	now := time.Now()
	pin := []Proc{{PID: 10, Name: "pinentry-curses"}, ownProc(20, "gpg")}

	// Dialog appears: terminal handover requested, dialog redraws on
	// Ctrl-L.
	s.tick(now, pin)
	require.Equal(t, Event{Active: true, CtrlLRedraw: true}, s.last)

	// Dialog closes and gpg finishes immediately: keep the handover for
	// the reopen window, then release.
	now = now.Add(100 * time.Millisecond)
	s.tick(now, nil)
	require.Equal(t, Event{Active: true, CtrlLRedraw: true}, s.last, "reopen window should keep the handover")

	tickAll(s, now, nil, 100*time.Millisecond, 4)
	require.Equal(t, Event{}, s.last)
	require.Equal(t, stateIdle, s.state)
}

func TestWatcherDialogReopenDebounce(t *testing.T) {
	t.Parallel()

	s := newTestService()
	now := time.Now()
	pin := []Proc{{PID: 10, Name: "pinentry"}}

	s.tick(now, pin)
	require.Equal(t, Event{Active: true, CtrlLRedraw: true}, s.last)

	// The dialog disappears and a new one (passphrase retry) appears
	// within the reopen window: no event flapping.
	now = now.Add(100 * time.Millisecond)
	s.tick(now, nil)
	now = now.Add(100 * time.Millisecond)
	s.tick(now, pin)
	require.Equal(t, Event{Active: true, CtrlLRedraw: true}, s.last, "no release while a follow-up dialog opens")
	require.Equal(t, stateDialog, s.state)
}

func TestWatcherGUIPinentryIgnored(t *testing.T) {
	t.Parallel()

	// A "pinentry" dispatcher resolving to a GUI flavor must not trigger
	// a terminal handover.
	dir := t.TempDir()
	real := filepath.Join(dir, "pinentry-mac")
	require.NoError(t, os.WriteFile(real, []byte(""), 0o755))
	link := filepath.Join(dir, "pinentry")
	require.NoError(t, os.Symlink(real, link))

	s := newTestService()
	s.tick(time.Now(), []Proc{{PID: 10, Name: "pinentry", Path: link}})
	require.Equal(t, Event{}, s.last)
	require.Equal(t, stateIdle, s.state)
}

func TestWatcherTouchAfterPassphrase(t *testing.T) {
	t.Parallel()

	s := newTestService()
	now := time.Now()
	pinAndGPG := []Proc{{PID: 10, Name: "pinentry-curses"}, ownProc(20, "gpg")}
	gpgOnly := []Proc{ownProc(20, "gpg")}

	// Passphrase dialog with gpg waiting behind it.
	s.tick(now, pinAndGPG)
	require.Equal(t, Event{Active: true, CtrlLRedraw: true}, s.last)

	// Dialog closes, gpg keeps running: handover released after the
	// reopen window, no touch hint yet.
	now = now.Add(100 * time.Millisecond)
	s.tick(now, gpgOnly)
	now = tickAll(s, now, gpgOnly, 100*time.Millisecond, 4)
	require.Equal(t, Event{}, s.last, "handover released, touch hint still within grace")

	// After the grace period gpg is still waiting: touch hint.
	tickAll(s, now, gpgOnly, 100*time.Millisecond, 10)
	require.Equal(t, Event{TouchPending: true}, s.last)

	// Touch happened, gpg exited: hint cleared.
	s.tick(now, nil)
	require.Equal(t, Event{}, s.last)
	require.Equal(t, stateIdle, s.state)
}

func TestWatcherTouchWithCachedCredentials(t *testing.T) {
	t.Parallel()

	s := newTestService()
	now := time.Now()
	gpgOnly := []Proc{ownProc(20, "gpg2")}

	// gpg starts waiting without ever showing a dialog (cached
	// passphrase). No hint before the grace period.
	now = tickAll(s, now, gpgOnly, 100*time.Millisecond, 7)
	require.Equal(t, Event{}, s.last)

	// After the grace period with gpg still waiting: touch hint.
	tickAll(s, now, gpgOnly, 100*time.Millisecond, 2)
	require.Equal(t, Event{TouchPending: true}, s.last)
}

func TestWatcherTouchExpiry(t *testing.T) {
	t.Parallel()

	s := newTestService()
	s.touchGrace = 100 * time.Millisecond
	s.touchMax = 500 * time.Millisecond
	now := time.Now()
	gpgOnly := []Proc{ownProc(20, "gpg")}

	now = tickAll(s, now, gpgOnly, 100*time.Millisecond, 2)
	require.Equal(t, Event{TouchPending: true}, s.last)

	// Past the maximum wait, assume gpg is stuck on something else and
	// drop the hint.
	tickAll(s, now, gpgOnly, 100*time.Millisecond, 5)
	require.Equal(t, Event{}, s.last)
}

func TestWatcherDialogReappearsDuringTouchWait(t *testing.T) {
	t.Parallel()

	s := newTestService()
	s.touchGrace = 100 * time.Millisecond
	now := time.Now()
	gpgOnly := []Proc{ownProc(20, "gpg")}
	pinAndGPG := []Proc{{PID: 10, Name: "pinentry-tty"}, ownProc(20, "gpg")}

	// Touch wait detected without a dialog.
	now = tickAll(s, now, gpgOnly, 100*time.Millisecond, 2)
	require.Equal(t, Event{TouchPending: true}, s.last)

	// A dialog appears after all (e.g. confirm dialog): hand over the
	// terminal and clear the touch hint. pinentry-tty does not support
	// Ctrl-L redraw.
	s.tick(now, pinAndGPG)
	require.Equal(t, Event{Active: true}, s.last)
}

func TestWatcherReleasePrecedesTouchHint(t *testing.T) {
	t.Parallel()

	s := newTestService()
	s.reopenWindow = 100 * time.Millisecond
	s.touchGrace = 100 * time.Millisecond
	now := time.Now()
	pinAndGPG := []Proc{{PID: 10, Name: "pinentry-curses"}, ownProc(20, "gpg")}
	gpgOnly := []Proc{ownProc(20, "gpg")}

	s.tick(now, pinAndGPG)
	require.Equal(t, Event{Active: true, CtrlLRedraw: true}, s.last)

	// The dialog disappears; the closing window starts on this tick.
	now = now.Add(20 * time.Millisecond)
	s.tick(now, gpgOnly)
	require.Equal(t, Event{Active: true, CtrlLRedraw: true}, s.last)

	// A slow poll makes the reopen window and the touch grace expire in
	// the same tick. The handover release must still be published on its
	// own before the touch hint, or a subscriber waiting for the release
	// never sees it.
	now = now.Add(300 * time.Millisecond)
	s.tick(now, gpgOnly)
	require.Equal(t, Event{}, s.last, "release is published even when the grace expires in the same tick")

	s.tick(now.Add(20*time.Millisecond), gpgOnly)
	require.Equal(t, Event{TouchPending: true}, s.last)
}

func TestTrackCommand(t *testing.T) {
	t.Parallel()

	s := newTestService()
	done := s.TrackCommand()
	require.Equal(t, 1, s.tracked)
	done()
	require.Equal(t, 0, s.tracked)
	// The returned function is idempotent.
	done()
	require.Equal(t, 0, s.tracked)
}

func TestServicePublishesOnStart(t *testing.T) {
	t.Parallel()

	s := newTestService()
	s.pollInterval = 5 * time.Millisecond
	var calls int
	s.lister = func(context.Context) ([]Proc, error) {
		calls++
		if calls > 2 {
			return []Proc{{PID: 10, Name: "pinentry-curses"}}, nil
		}
		return nil, nil
	}

	ctx := t.Context()
	s.Start(ctx)

	sub := s.Subscribe(ctx)
	done := s.TrackCommand()
	defer done()

	select {
	case ev := <-sub:
		require.Equal(t, pubsub.UpdatedEvent, ev.Type)
		require.Equal(t, Event{Active: true, CtrlLRedraw: true}, ev.Payload)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for pinentry event")
	}
}

func TestServiceSleepsWithoutTrackedCommands(t *testing.T) {
	t.Parallel()

	s := newTestService()
	s.pollInterval = time.Millisecond
	s.lister = func(context.Context) ([]Proc, error) {
		return []Proc{{PID: 10, Name: "pinentry-curses"}}, nil
	}

	ctx := t.Context()
	s.Start(ctx)

	sub := s.Subscribe(ctx)
	select {
	case ev := <-sub:
		t.Fatalf("unexpected event without tracked commands: %+v", ev.Payload)
	case <-time.After(50 * time.Millisecond):
	}

	// Once a command runs, the watcher wakes up and notices the dialog.
	done := s.TrackCommand()
	defer done()
	select {
	case ev := <-sub:
		require.Equal(t, Event{Active: true, CtrlLRedraw: true}, ev.Payload)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for pinentry event")
	}
}

// ownProc describes a process this test process started, as a process
// started by a Crush command is.
func ownProc(pid int, name string) Proc {
	return Proc{PID: pid, PPID: os.Getpid(), Name: name}
}

func TestClassifyCountsOnlyOurGPG(t *testing.T) {
	t.Parallel()
	parent := os.Getpid()

	// A gpg started by Crush counts.
	_, _, gpg := classify([]Proc{ownProc(20, "gpg")}, nil)
	require.True(t, gpg)

	// A deeper descendant of Crush counts too.
	_, _, gpg = classify([]Proc{
		{PID: 30, PPID: parent, Name: "sh"},
		{PID: 31, PPID: 30, Name: "gpg2"},
	}, nil)
	require.True(t, gpg)

	// A gpg belonging to another program or another Crush instance does
	// not count: it cannot be waiting for our key.
	_, _, gpg = classify([]Proc{{PID: 40, PPID: parent + 1, Name: "gpg"}}, nil)
	require.False(t, gpg)

	// A parent missing from the snapshot cannot be attributed to Crush.
	_, _, gpg = classify([]Proc{{PID: 41, PPID: 999999, Name: "gpg"}}, nil)
	require.False(t, gpg)

	// A malformed snapshot with a parent cycle terminates.
	_, _, gpg = classify([]Proc{{PID: 42, PPID: 42, Name: "gpg"}}, nil)
	require.False(t, gpg)
}

func TestWatcherTouchRequiresOurGPG(t *testing.T) {
	t.Parallel()

	s := newTestService()
	s.touchGrace = 100 * time.Millisecond
	now := time.Now()
	foreign := []Proc{{PID: 20, PPID: os.Getpid() + 1, Name: "gpg"}}

	// Another program's gpg is never a security key wait of ours.
	now = tickAll(s, now, foreign, 100*time.Millisecond, 5)
	require.Equal(t, Event{}, s.last)
	require.Equal(t, stateIdle, s.state)

	// A gpg started by Crush still is.
	tickAll(s, now, []Proc{ownProc(20, "gpg")}, 100*time.Millisecond, 2)
	require.Equal(t, Event{TouchPending: true}, s.last)
}

// waitUntil polls cond for up to five seconds.
func waitUntil(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// startFakeHolding starts a fake process holding path open for the rest of
// the test and returns its PID.
func startFakeHolding(t *testing.T, path string) int {
	t.Helper()
	exe := buildFakeProc(t, "fakeproc.go", "fakeproc")
	cmd := exec.CommandContext(t.Context(), exe, "30", path)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func TestWatcherRequiresOwnTerminal(t *testing.T) {
	t.Parallel()

	ttyPath := standinTerminal(t)
	otherPath := standinTerminal(t)
	owns := pathOwns(t, ttyPath)
	foreignOwnsOther := pathOwns(t, otherPath)

	// A dialog on another terminal must not take ours.
	foreign := startFakeHolding(t, otherPath)
	require.True(t, waitUntil(t, func() bool { return foreignOwnsOther(foreign) }), "the fake holds the other terminal")
	s := newTestService()
	s.owns = owns
	s.tick(time.Now(), []Proc{{PID: foreign, Name: "pinentry-curses"}})
	require.Equal(t, Event{}, s.last)
	require.Equal(t, stateIdle, s.state)

	// The same dialog holding our terminal is a handover.
	pid := startFakeHolding(t, ttyPath)
	require.True(t, waitUntil(t, func() bool { return owns(pid) }), "the fake holds our terminal")
	s = newTestService()
	s.owns = owns
	s.tick(time.Now(), []Proc{{PID: pid, Name: "pinentry-curses"}})
	require.Equal(t, Event{Active: true, CtrlLRedraw: true}, s.last)
}

// devZero and devFull stand in for two different terminals. They are
// character devices, as a terminal is, and neither is stdin of the fakes.
const (
	devZero = "/dev/zero"
	devFull = "/dev/full"
)

func TestOwnsTerminalUsesOpenDescriptors(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("descriptor ownership is read through /proc/<pid>/fd")
	}
	for _, dev := range []string{devZero, devFull} {
		if _, err := os.Stat(dev); err != nil {
			t.Skipf("%s stands in for a terminal device", dev)
		}
	}

	ref, ok := terminalRefAt(devZero)
	require.True(t, ok, "%s is a character device", devZero)
	fullRef, ok := terminalRefAt(devFull)
	require.True(t, ok, "%s is a character device", devFull)

	ours := startFakeHolding(t, devZero)
	require.True(t, waitUntil(t, func() bool { return ownsTerminal(ours, ref) }), "a process holding the device owns it")

	foreign := startFakeHolding(t, devFull)
	require.True(t, waitUntil(t, func() bool { return ownsTerminal(foreign, fullRef) }), "the fake holds the other device")
	require.False(t, ownsTerminal(foreign, ref), "another device is not our terminal")

	// Descriptors that cannot be read are never ours.
	require.False(t, ownsTerminal(1<<30, ref))
}

func TestDefaultOwnsFallsBackWithoutTerminal(t *testing.T) {
	t.Parallel()
	if _, ok := ownTerminalRef(); ok {
		t.Skip("stdin is a terminal here; the fallback cannot be exercised")
	}
	require.Nil(t, defaultOwns(), "an unidentifiable terminal keeps the name-only match")
}

// buildFakeProc builds the given helper source from testdata under the
// given binary name. The name must not be one a pinentry watcher reacts to
// ("pinentry", "pinentry-curses", "gpg", ...): a live Crush on the same
// machine would act on it. Tests that need such a name report it through
// [renamedProcs] instead.
func buildFakeProc(t *testing.T, source, name string) string {
	t.Helper()

	goBin, err := exec.LookPath("go")
	require.NoError(t, err)

	dst := filepath.Join(t.TempDir(), name)
	out, err := exec.CommandContext(t.Context(), goBin, "build", "-o", dst, "testdata/"+source).CombinedOutput()
	require.NoError(t, err, "go build: %s", out)
	return dst
}

// standinTerminal creates a file that stands in for a terminal. Tests bind
// the watcher's ownership check to it instead of to the real terminal, so
// running the tests can never make a live Crush hand its terminal over.
func standinTerminal(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "standin-terminal")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	return path
}

// pathOwns returns an ownership check that reports a process as owning the
// stand-in terminal when it holds path open. It replaces the platform
// ownership check in tests, which inspect open descriptors through /proc.
func pathOwns(t *testing.T, path string) func(pid int) bool {
	t.Helper()
	want, err := os.Stat(path)
	require.NoError(t, err)
	if _, err := os.ReadDir("/proc/self/fd"); err != nil {
		t.Skip("open descriptors are not inspectable without /proc/<pid>/fd")
	}
	return func(pid int) bool {
		dir := fmt.Sprintf("/proc/%d/fd", pid)
		ents, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, e := range ents {
			got, err := os.Stat(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			if os.SameFile(got, want) {
				return true
			}
		}
		return false
	}
}

// renamedProcs rewrites the names of specific processes in a process table
// snapshot. Tests use it so the fakes they spawn never appear under a name
// a live Crush watcher reacts to, while the snapshot the watcher under test
// sees still carries a pinentry or gpg name.
type renamedProcs struct {
	mu    sync.Mutex
	names map[int]string
}

// newRenamedProcs returns an empty renaming.
func newRenamedProcs() *renamedProcs {
	return &renamedProcs{names: map[int]string{}}
}

// set reports pid under name in the snapshots the wrapper lister returns.
func (r *renamedProcs) set(pid int, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names[pid] = name
}

// lister wraps a process lister with the renaming.
func (r *renamedProcs) lister(base Lister) Lister {
	return func(ctx context.Context) ([]Proc, error) {
		procs, err := base(ctx)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		for i := range procs {
			if name, ok := r.names[procs[i].PID]; ok {
				procs[i].Name = name
			}
		}
		return procs, nil
	}
}

func TestServiceDetectsRealProcess(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("process name emulation relies on ps(1)")
	}

	// The fakes hold a stand-in terminal open and the watcher's ownership
	// check is bound to that stand-in, so no test process ever claims the
	// terminal of the developer running the tests.
	ttyPath := standinTerminal(t)
	fakePinentry := buildFakeProc(t, "fakeproc.go", "fakepinentry")
	fakeGPG := buildFakeProc(t, "fakeproc.go", "fakegpg")
	names := newRenamedProcs()

	s := newTestService()
	s.lister = names.lister(psLister)
	s.owns = pathOwns(t, ttyPath)
	s.pollInterval = 20 * time.Millisecond
	s.reopenWindow = 50 * time.Millisecond
	s.touchGrace = 100 * time.Millisecond

	ctx := t.Context()
	s.Start(ctx)
	sub := s.Subscribe(ctx)

	expectEvent := func(want Event) {
		t.Helper()
		for {
			select {
			case ev := <-sub:
				if ev.Payload == want {
					return
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("timed out waiting for event %+v", want)
			}
		}
	}

	done := s.TrackCommand()
	defer done()

	// A "pinentry-curses" process holding the terminal appears while gpg
	// runs: handover.
	gpgCmd := exec.CommandContext(ctx, fakeGPG, "30")
	require.NoError(t, gpgCmd.Start())
	defer func() { _ = gpgCmd.Process.Kill() }()
	names.set(gpgCmd.Process.Pid, "gpg")
	pinCmd := exec.CommandContext(ctx, fakePinentry, "30", ttyPath)
	require.NoError(t, pinCmd.Start())
	names.set(pinCmd.Process.Pid, "pinentry-curses")

	expectEvent(Event{Active: true, CtrlLRedraw: true})

	// The dialog closes but gpg keeps waiting: handover released, then
	// the security key touch hint appears.
	require.NoError(t, pinCmd.Process.Kill())
	_ = pinCmd.Wait()
	expectEvent(Event{})
	expectEvent(Event{TouchPending: true})

	// gpg exits: hint cleared.
	require.NoError(t, gpgCmd.Process.Kill())
	_ = gpgCmd.Wait()
	expectEvent(Event{})
}
