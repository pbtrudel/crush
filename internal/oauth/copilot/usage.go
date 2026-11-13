package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"
)

const usageBaseURL = "https://api.github.com"

// FetchCreditsUsed returns the AI credits consumed in the current billing
// cycle, as reported by GitHub for the given GitHub OAuth token.
func FetchCreditsUsed(ctx context.Context, githubToken string) (int, error) {
	return fetchCreditsUsed(ctx, usageBaseURL, githubToken)
}

func fetchCreditsUsed(ctx context.Context, baseURL, githubToken string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/copilot_internal/user", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "token "+githubToken)
	for k, v := range Headers() {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("copilot usage request failed: status %d", resp.StatusCode)
	}

	var body struct {
		QuotaSnapshots struct {
			PremiumInteractions *struct {
				CreditsUsed float64 `json:"credits_used"`
			} `json:"premium_interactions"`
		} `json:"quota_snapshots"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decode copilot usage: %w", err)
	}
	if body.QuotaSnapshots.PremiumInteractions == nil {
		return 0, errors.New("copilot usage has no premium_interactions snapshot")
	}
	return int(math.Round(body.QuotaSnapshots.PremiumInteractions.CreditsUsed)), nil
}
