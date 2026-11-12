package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func testFusionRouter(t *testing.T, handler http.HandlerFunc) copilotFusionRouter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return copilotFusionRouter{
		resolveBaseURL: func() (string, error) { return srv.URL, nil },
		resolveAPIKey:  func() (string, error) { return "test-token", nil },
		headers:        map[string]string{"Copilot-Integration-Id": "test"},
	}
}

func TestFusionRouter_Standard(t *testing.T) {
	t.Parallel()

	token := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"reasoning_bucket":"medium"}`)) + ".sig"
	router := testFusionRouter(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/auto", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		require.Equal(t, fusionAPIVersion, r.Header.Get("X-GitHub-Api-Version"))
		require.Equal(t, "test", r.Header.Get("Copilot-Integration-Id"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, "fix the bug", payload["prompt"])
		require.Equal(t, true, payload["fusion"])

		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_token":  token,
			"selected_model": map[string]any{"id": "gpt-6.1-terra"},
		})
	})

	plan, err := router.route(context.Background(), fusionTierStandard, "fix the bug")
	require.NoError(t, err)
	require.Equal(t, "solo", plan.Pattern)
	require.Len(t, plan.Steps, 1)
	require.Equal(t, "gpt-6.1-terra", plan.Steps[0].ModelID)
	require.Equal(t, "medium", plan.Steps[0].ReasoningEffort)
}

func TestFusionRouter_HighAndMax(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		tier string
		mode string
	}{
		{fusionTierHigh, "hydrafusion"},
		{fusionTierMax, "hydrafusion-max"},
	} {
		t.Run(tc.tier, func(t *testing.T) {
			t.Parallel()

			router := testFusionRouter(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/model/fusion", r.URL.Path)
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var payload map[string]any
				require.NoError(t, json.Unmarshal(body, &payload))
				require.Equal(t, tc.mode, payload["fusion_mode"])
				require.Equal(t, fusionPlanVersion, payload["plan_version"])

				_ = json.NewEncoder(w).Encode(map[string]any{
					"fusion_pattern": "cascade",
					"steps": []map[string]any{
						{"role": "draft", "model_id": "claude-opus-5.5", "reasoning_effort": "xhigh"},
						{"role": "escalation", "model_id": "gpt-6-sol", "reasoning_effort": "high"},
						{"role": "repair", "model_id": "gpt-6-astra", "reasoning_effort": "xhigh"},
					},
				})
			})

			plan, err := router.route(context.Background(), tc.tier, "debug this")
			require.NoError(t, err)
			require.Equal(t, "cascade", plan.Pattern)
			require.Len(t, plan.Steps, 3)
			require.Equal(t, "gpt-6-astra", plan.Steps[2].ModelID)
		})
	}
}

func TestFusionRouter_Error(t *testing.T) {
	t.Parallel()

	router := testFusionRouter(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"old_api_version"}`))
	})

	_, err := router.route(context.Background(), fusionTierMax, "x")
	require.ErrorContains(t, err, "old_api_version")
}

func TestFusionReasoningBucket(t *testing.T) {
	t.Parallel()

	token := "h." + base64.RawURLEncoding.EncodeToString([]byte(`{"reasoning_bucket":"high"}`)) + ".s"
	require.Equal(t, "high", fusionReasoningBucket(token))
	require.Empty(t, fusionReasoningBucket("not-a-jwt"))
}
