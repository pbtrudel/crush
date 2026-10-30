package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/permission"
)

const reviewSystemPrompt = `You are a security reviewer for an AI coding agent. You decide whether a tool call that requires user approval is safe to run automatically.

Rules:
- APPROVE only when the call is clearly safe and narrowly scoped: read-only inspection, or a write confined to the project that is consistent with the task.
- DENY when the call is destructive, irreversible, exfiltrates data, touches credentials, or reaches outside the project without a clear reason.
- ASK when you are unsure or the call needs context you do not have.

Respond with exactly one word: APPROVE, DENY, or ASK.`

const reviewTimeout = 30 * time.Second

// ReviewToolCall asks the small model whether a permission request is
// safe to approve automatically. It returns ReviewAsk on any error so
// the user still gets the final say.
func (c *coordinator) ReviewToolCall(ctx context.Context, req permission.PermissionRequest) (permission.ReviewDecision, error) {
	model := c.subAgentModels().Small
	if model.Model == nil {
		return permission.ReviewAsk, nil
	}

	ctx, cancel := context.WithTimeout(ctx, reviewTimeout)
	defer cancel()

	agent := fantasy.NewAgent(
		model.Model,
		fantasy.WithSystemPrompt(reviewSystemPrompt),
		fantasy.WithMaxOutputTokens(16),
		fantasy.WithUserAgent(userAgent),
	)
	resp, err := agent.Stream(ctx, fantasy.AgentStreamCall{
		Prompt: fmt.Sprintf(
			"Tool: %s\nAction: %s\nPath: %s\nDescription: %s\nParameters: %v",
			req.ToolName, req.Action, req.Path, req.Description, req.Params,
		),
	})
	if err != nil {
		return permission.ReviewAsk, err
	}
	return parseReviewDecision(resp.Response.Content.Text()), nil
}

// parseReviewDecision extracts a decision from the reviewer's reply. It
// defaults to asking the user when the reply is ambiguous.
func parseReviewDecision(s string) permission.ReviewDecision {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch {
	case strings.Contains(s, "APPROVE"):
		return permission.ReviewApprove
	case strings.Contains(s, "DENY"):
		return permission.ReviewDeny
	default:
		return permission.ReviewAsk
	}
}
