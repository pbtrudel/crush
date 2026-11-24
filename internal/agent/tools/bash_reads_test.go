package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellReadPaths(t *testing.T) {
	t.Parallel()

	wd := "/w"
	cases := map[string][]string{
		"cat a.go":                   {"/w/a.go"},
		"sed -n '10,40p' a.go":       {"/w/a.go"},
		"head -50 a.go && tail b.go": {"/w/a.go", "/w/b.go"},
		"nl -ba a.go | sed -n 1,20p": {"/w/a.go"},
		"cd sub && cat a.go":         nil, // Changes directory: not counted.
		"sed -i 's/a/b/' a.go":       nil,
		"sed -n p a.go > a.go":       nil,
		"cat a.go | tee a.go":        nil,
		"cat $FILE":                  nil,
		"grep -n foo a.go":           nil, // Shows matching lines only.
		"cat a.go b.go > out.txt":    nil, // Any redirection: not counted.
		"cat f && cat x > f":         nil, // The redirection is on the second part.
		"cat f | cat > f":            nil,
		"cat a > b && cat c":         nil,
		"cat f |& cat >> f":          nil,
		"cat f && ! cat g":           nil, // A negated part cannot be judged.
		"sed -n 'w out' f":           nil, // Writes the pattern space out.
		"sed -n '1p;w out' f":        nil,
		"sed -n 's/a/b/w out' f":     nil,
		"sed -n 'e touch x' f":       nil,
		"cat a | sed -n 'w a'":       nil,
		"sed -n -e 1p f":             {"/w/f"},
	}
	for cmd, want := range cases {
		require.Equal(t, want, shellReadPaths(cmd, wd), cmd)
	}
}
