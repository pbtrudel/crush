package web

import (
	"testing"

	"github.com/charmbracelet/crush/internal/logtest"
)

func captureLogs(t *testing.T) *logtest.Buffer { return logtest.Capture(t) }
