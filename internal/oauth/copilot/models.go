package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ModelLimits struct {
	MaxPromptTokens        int64 `json:"max_prompt_tokens"`
	MaxContextWindowTokens int64 `json:"max_context_window_tokens"`
	MaxOutputTokens        int64 `json:"max_output_tokens"`
}

func FetchModelLimits(ctx context.Context, baseURL, apiKey string, headers map[string]string) (map[string]ModelLimits, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-GitHub-Api-Version", "2026-08-01")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot models: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 4<<20 {
		return nil, fmt.Errorf("copilot models: response too large")
	}
	var payload struct {
		Data []struct {
			ID           string `json:"id"`
			Capabilities struct {
				Limits ModelLimits `json:"limits"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Data == nil {
		return nil, fmt.Errorf("copilot models: missing data")
	}
	limits := make(map[string]ModelLimits, len(payload.Data))
	for _, model := range payload.Data {
		if model.ID != "" {
			limits[model.ID] = model.Capabilities.Limits
		}
	}
	return limits, nil
}
