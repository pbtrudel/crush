//go:build unix

package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDiscoverRefusesSpecialAndOversizeSkills(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"fifo-skill", "zero-skill", "big-skill", "ok-skill"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o755))
	}
	require.NoError(t, unix.Mkfifo(filepath.Join(dir, "fifo-skill", SkillFileName), 0o644))
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(dir, "zero-skill", SkillFileName)))
	big := "---\nname: big-skill\ndescription: Big.\n---\n" + strings.Repeat("a", MaxSkillFileBytes)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big-skill", SkillFileName), []byte(big), 0o644))
	ok := "---\nname: ok-skill\ndescription: Fine.\n---\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok-skill", SkillFileName), []byte(ok), 0o644))

	type result struct {
		skills []*Skill
		states []*SkillState
	}
	done := make(chan result, 1)
	go func() {
		skills, states := DiscoverWithStates([]string{dir})
		done <- result{skills, states}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("skill discovery hung on a special file")
	}
	require.Len(t, got.skills, 1)
	require.Equal(t, "ok-skill", got.skills[0].Name)
	errored := 0
	for _, state := range got.states {
		if state.State == StateError {
			errored++
		}
	}
	require.Equal(t, 3, errored, "each refused file is a diagnostic")

	read := make(chan error, 1)
	fifo := filepath.Join(dir, "fifo-skill", SkillFileName)
	go func() {
		_, _, err := ReadContent([]*Skill{{Name: "fifo-skill", SkillFilePath: fifo}}, nil, "", fifo)
		read <- err
	}()
	select {
	case err := <-read:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ReadContent hung on a FIFO")
	}
}
