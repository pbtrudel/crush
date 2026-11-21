package clientserverrace_test

import (
	"slices"
	"testing"
)

func TestChildEnvDropsProviderCredentials(t *testing.T) {
	got := childEnv([]string{
		"PATH=/usr/bin",
		"TMPDIR=/tmp",
		"LANG=C.UTF-8",
		"NEURALWATT_API_KEY=secret",
		"ANTHROPIC_API_KEY=secret",
		"GITHUB_TOKEN=secret",
		"AWS_ACCESS_KEY_ID=secret",
		"CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=0",
	})

	want := []string{"PATH=/usr/bin", "TMPDIR=/tmp", "LANG=C.UTF-8"}
	if !slices.Equal(got, want) {
		t.Fatalf("childEnv() = %q, want %q", got, want)
	}
}
