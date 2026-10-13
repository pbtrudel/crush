package browser

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenURLDoesNotLaunchBrowserDuringTests pins the guard the package
// exists for: under `go test` pkg/browser must be short-circuited, never
// reaching the OS openers (xdg-open and friends) — the real browser may
// otherwise launch for every login/SSO code path that happens to be
// exercised by the suite. The fake PATH opener writes a marker file; the
// test fails if it ever appears.
func TestOpenURLDoesNotLaunchBrowserDuringTests(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "browser-opened")
	stub := filepath.Join(dir, "xdg-open")
	stubScript := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	t.Run("guarded under go test", func(t *testing.T) {
		if err := OpenURL("https://example.test/auth"); err != nil {
			t.Fatalf("OpenURL under go test must silently succeed, got: %v", err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("browser opener ran during go test")
		}
	})
}
