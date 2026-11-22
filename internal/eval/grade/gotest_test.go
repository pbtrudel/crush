package grade

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunGoTest parses the exit status of go test against the events it
// reported. The fake go binaries are found through PATH, so these tests
// cannot run in parallel with each other.
func TestGoTest(t *testing.T) {
	tests := []struct {
		name         string
		script       string
		wantCompile  bool
		wantFailed   bool
		wantNoFailed bool
	}{
		{
			name: "events without a fail event",
			script: `#!/bin/sh
echo '{"Action":"start","Package":"p"}'
echo '{"Action":"output","Package":"p","Output":"panic: boom\n"}'
exit 1
`,
			wantFailed: true,
		},
		{
			name:        "no events",
			script:      "#!/bin/sh\necho 'no required module' 1>&2\nexit 1\n",
			wantCompile: true,
		},
		{
			name:         "passing package",
			script:       "#!/bin/sh\necho '{\"Action\":\"pass\",\"Package\":\"p\"}'\nexit 0\n",
			wantNoFailed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "go"), []byte(tt.script), 0o700))
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			out, err := GoTest(context.Background(), dir, []string{"PATH=" + dir}, []string{"test", "-json", "./p"}, false)
			require.NoError(t, err)
			require.Equal(t, tt.wantCompile, out.Compile, "compile")
			require.Equal(t, tt.wantFailed, out.PackageFailed, "package failed")
			if tt.wantNoFailed {
				require.False(t, out.Compile)
				require.False(t, out.PackageFailed)
				require.Empty(t, out.Failed)
			}
		})
	}
}
