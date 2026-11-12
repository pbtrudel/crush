package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Copilot Fusion tiers. The Standard tier asks the router for a single model,
// while High and Max ask for a multi-phase plan.
const (
	fusionTierStandard = "standard"
	fusionTierHigh     = "high"
	fusionTierMax      = "max"
)

// fusionAPIVersion is the oldest Copilot API version that serves the routing
// endpoints. The router rejects older versions with old_api_version.
const fusionAPIVersion = "2026-08-01"

// fusionPlanVersion is the plan format the router returns.
const fusionPlanVersion = "2"

// fusionStep is one phase of a Fusion plan.
type fusionStep struct {
	Role            string `json:"role"`
	ModelID         string `json:"model_id"`
	ReasoningEffort string `json:"reasoning_effort"`
}

// fusionPlan is the router's answer for one turn.
type fusionPlan struct {
	// Pattern is solo, cascade, or critique.
	Pattern string
	Steps   []fusionStep
}

// fusionRouter resolves the plan for a turn.
type fusionRouter interface {
	route(ctx context.Context, tier, prompt string) (*fusionPlan, error)
}

// copilotFusionRouter calls the Copilot routing endpoints. The credentials are
// resolved per request so an OAuth token refreshed mid-session is picked up.
type copilotFusionRouter struct {
	resolveBaseURL func() (string, error)
	resolveAPIKey  func() (string, error)
	headers        map[string]string
	client         *http.Client
}

func (r copilotFusionRouter) route(ctx context.Context, tier, prompt string) (*fusionPlan, error) {
	if tier == fusionTierStandard {
		return r.routeStandard(ctx, prompt)
	}
	return r.routeFusion(ctx, tier, prompt)
}

// routeStandard asks the auto router for the single best model for the turn.
func (r copilotFusionRouter) routeStandard(ctx context.Context, prompt string) (*fusionPlan, error) {
	body := map[string]any{
		"prompt":              prompt,
		"has_image":           false,
		"multi_turn":          map[string]any{"routing_intent": "anchor", "turns_since_anchor": 0},
		"hydra_rl_multi_turn": map[string]any{},
		"fusion":              true,
	}
	var response struct {
		SessionToken  string `json:"session_token"`
		SelectedModel struct {
			ID string `json:"id"`
		} `json:"selected_model"`
	}
	if err := r.post(ctx, "/auto", body, &response); err != nil {
		return nil, err
	}
	if response.SelectedModel.ID == "" {
		return nil, fmt.Errorf("fusion: auto router returned no model")
	}
	return &fusionPlan{
		Pattern: "solo",
		Steps: []fusionStep{{
			Role:            "generation",
			ModelID:         response.SelectedModel.ID,
			ReasoningEffort: fusionReasoningBucket(response.SessionToken),
		}},
	}, nil
}

// routeFusion asks the Fusion router for a plan for the turn.
func (r copilotFusionRouter) routeFusion(ctx context.Context, tier, prompt string) (*fusionPlan, error) {
	mode := "hydrafusion"
	if tier == fusionTierMax {
		mode = "hydrafusion-max"
	}
	body := map[string]any{
		"prompt":       prompt,
		"fusion_mode":  mode,
		"plan_version": fusionPlanVersion,
	}
	var response struct {
		Pattern string       `json:"fusion_pattern"`
		Steps   []fusionStep `json:"steps"`
	}
	if err := r.post(ctx, "/model/fusion", body, &response); err != nil {
		return nil, err
	}
	if len(response.Steps) == 0 {
		return nil, fmt.Errorf("fusion: router returned an empty plan")
	}
	pattern := response.Pattern
	if pattern == "" {
		pattern = "solo"
	}
	return &fusionPlan{Pattern: pattern, Steps: response.Steps}, nil
}

func (r copilotFusionRouter) post(ctx context.Context, path string, body any, out any) error {
	baseURL, err := r.resolveBaseURL()
	if err != nil {
		return fmt.Errorf("fusion: resolving base URL: %w", err)
	}
	apiKey, err := r.resolveAPIKey()
	if err != nil {
		return fmt.Errorf("fusion: resolving API key: %w", err)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	for key, value := range r.headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-GitHub-Api-Version", fusionAPIVersion)

	client := r.client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fusion: routing request failed: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("fusion: reading routing response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fusion: routing request failed: %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("fusion: decoding routing response: %w", err)
	}
	return nil
}

// fusionReasoningBucket reads the reasoning bucket the auto router encoded in
// its session token. The token is a JWT whose payload is plain base64 JSON.
func fusionReasoningBucket(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		ReasoningBucket string `json:"reasoning_bucket"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.ReasoningBucket
}
