// Package browser wraps github.com/pkg/browser so that `go test` never
// opens a real browser: pkg/browser has no seam and launches xdg-open (and
// whatever that resolves to) for every login, OAuth and SSO code path the
// suite may execute, popping a browser window on every test run.
package browser

import (
	"testing"

	pkgbrowser "github.com/pkg/browser"
)

// Testing is a seam for tests that need to pin [OpenURL]/[OpenFile]
// test-time behavior.
var Testing = testing.Testing

// OpenURL opens the given URL in the user's default browser, except under
// `go test`, where it reports success without side effects.
func OpenURL(url string) error {
	if Testing() {
		return nil
	}
	return pkgbrowser.OpenURL(url)
}

// OpenFile opens the given file in the user's default browser, except under
// `go test`, where it reports success without side effects.
func OpenFile(path string) error {
	if Testing() {
		return nil
	}
	return pkgbrowser.OpenFile(path)
}
