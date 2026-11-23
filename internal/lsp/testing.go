package lsp

import (
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// NewForTest returns a client with initialized diagnostic storage and
// no transport, for tests that need to publish diagnostics without
// starting a language server. Feed it with [HandleDiagnostics].
func NewForTest(name string) *Client {
	return &Client{
		name:        name,
		diagnostics: csync.NewVersionedMap[protocol.DocumentURI, []protocol.Diagnostic](),
		openFiles:   csync.NewMap[string, *OpenFileInfo](),
	}
}
