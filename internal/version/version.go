package version

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// Build-time parameters set via -ldflags.

var (
	Version = "devel"
	Commit  = "unknown"
	// BuildID is a unique identifier for this build. For release builds it
	// equals Commit; for development builds (go run / go build without
	// ldflags) it is derived from the executable's modification time, which
	// changes on every recompilation.
	BuildID = ""
)

// A user may install crush using `go install github.com/charmbracelet/crush@latest`.
// without -ldflags, in which case the version above is unset. As a workaround
// we use the embedded build version that *is* set when using `go install` (and
// is only set for `go install` and not for `go build`).
func init() {
	info, ok := debug.ReadBuildInfo()
	if ok {
		mainVersion := info.Main.Version
		if mainVersion != "" && mainVersion != "(devel)" {
			Version = mainVersion
		}
	}

	// Derive BuildID when not set via ldflags.
	if BuildID == "" {
		BuildID = deriveBuildID()
	}
}

// deriveBuildID uses the running executable's modification time as a unique
// build fingerprint. This changes on every recompilation (including `go run`),
// making it reliable for detecting stale servers during development.
func deriveBuildID() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return "unknown"
	}
	return strconv.FormatInt(fi.ModTime().UnixNano(), 36)
}

// BuildStamp names the running build for the session log. A release
// version is also a git tag, which names its commit. A modified checkout
// or an unversioned build is not, so the build fingerprint is appended to
// tell two such builds apart. When no fingerprint could be read, the bare
// version is returned rather than a made-up one.
func BuildStamp() string {
	return buildStamp(Version, BuildID)
}

func buildStamp(version, buildID string) string {
	if buildID == "" || buildID == "unknown" {
		return version
	}
	if version == "devel" || strings.HasSuffix(version, "+dirty") {
		return version + "." + buildID
	}
	return version
}
