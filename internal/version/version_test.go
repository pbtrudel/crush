package version

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildStamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		buildID string
		want    string
	}{
		{name: "release", version: "0.100.0-fork.100", buildID: "c71dec5e", want: "0.100.0-fork.100"},
		{name: "modified checkout", version: "v0.100.0-fork.100+dirty", buildID: "mx7k2", want: "v0.100.0-fork.100+dirty.mx7k2"},
		{name: "unversioned", version: "devel", buildID: "mx7k2", want: "devel.mx7k2"},
		{name: "clean pseudo-version", version: "v0.100.1-0.20261117120000-c71dec5e1234", buildID: "mx7k2", want: "v0.100.1-0.20261117120000-c71dec5e1234"},
		{name: "fingerprint unreadable on unversioned", version: "devel", buildID: "unknown", want: "devel"},
		{name: "fingerprint missing on modified checkout", version: "v0.100.0-fork.100+dirty", buildID: "", want: "v0.100.0-fork.100+dirty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, buildStamp(tt.version, tt.buildID))
		})
	}
}

func TestBuildStampUsesTheRunningBuild(t *testing.T) {
	t.Parallel()

	require.Equal(t, buildStamp(Version, BuildID), BuildStamp())
}
