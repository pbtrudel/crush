package app

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
)

// gateClaims counts the checks the session's last answer claims to have run
// and the ones no tool call backs (design 4.3). The question the gate asks
// invites invented verification, so each backticked command in the answer's
// Verified block is matched against what the turn actually ran.
func gateClaims(rows []message.Message, sessionID string) (claims, unbacked int) {
	succeeded := map[string]bool{}
	for _, row := range rows {
		if row.SessionID != sessionID || row.Role != message.Tool {
			continue
		}
		for _, result := range row.ToolResults() {
			succeeded[result.ToolCallID] = !result.IsError
		}
	}

	var (
		lastText string
		commands []string
		called   []string
	)
	for _, row := range rows {
		if row.SessionID != sessionID || row.Role != message.Assistant || row.IsSummaryMessage {
			continue
		}
		if text := row.Content().Text; strings.TrimSpace(text) != "" {
			lastText = text
		}
		for _, call := range row.ToolCalls() {
			if !succeeded[call.ID] {
				continue
			}
			if call.Name == tools.BashToolName {
				commands = append(commands, toolCommand(call.Input))
				continue
			}
			called = append(called, call.Name)
		}
	}

	answers := verifiedClaims(lastText)
	for _, claim := range answers {
		if !claimBacked(claim, commands, called) {
			unbacked++
		}
	}
	return len(answers), unbacked
}

// toolCommand reads the command of a bash call's JSON input.
func toolCommand(input string) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &parsed); err != nil {
		return ""
	}
	return parsed.Command
}

// verifiedClaims reads the backticked command of every "- " line in an
// answer's Verified block. A missing block and "Verified: none" hold no
// claim.
func verifiedClaims(text string) []string {
	var claims []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if strings.EqualFold(trimmed, "Verified:") {
				inBlock = true
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		if claim := backticked(trimmed); claim != "" {
			claims = append(claims, claim)
		}
	}
	return claims
}

// backticked returns the first backticked span of a line, or "".
func backticked(line string) string {
	start := strings.Index(line, "`")
	if start < 0 {
		return ""
	}
	end := strings.Index(line[start+1:], "`")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(line[start+1 : start+1+end])
}

// claimBacked reports whether a claim names a command the turn ran or a tool
// it called. A command matches when it equals the claim, whitespace aside, or
// contains it.
func claimBacked(claim string, commands, called []string) bool {
	target := collapseSpace(claim)
	if target == "" {
		return false
	}
	for _, command := range commands {
		command = collapseSpace(command)
		if command != "" && strings.Contains(command, target) {
			return true
		}
	}
	// A claim may name a tool instead of a command, such as
	// "lsp_diagnostics on the package".
	for _, name := range called {
		if name != "" && strings.Contains(target, name) {
			return true
		}
	}
	return false
}

// collapseSpace reduces every run of whitespace to one space.
func collapseSpace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// gateRowCount counts the harness rows the turn-end gate wrote in the
// session, which is how many times the prompt was continued.
func gateRowCount(rows []message.Message, sessionID string) int {
	count := 0
	for _, row := range rows {
		if row.SessionID != sessionID || row.Role != message.User {
			continue
		}
		if row.Content().Hidden && strings.HasPrefix(row.Content().Text, gateOpenTag) {
			count++
		}
	}
	return count
}
