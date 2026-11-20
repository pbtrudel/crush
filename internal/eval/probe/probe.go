// Package probe measures whether a route's model recalls a value it noted in
// its own reasoning, with and without extra request fields. It runs on the
// host, outside the network jail, and prints nothing but counts and short
// answers.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"strings"
	"time"

	hyperp "github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/oauth/copilot"
)

const (
	// Nonce is the fixed value the probe hides in the reasoning of an
	// earlier assistant turn.
	Nonce = "A7F3-B2Q9"
	// maxAnswer is the longest answer a report keeps, in characters.
	maxAnswer = 40
	// maxErrorBody is how much of a refused response the error keeps.
	maxErrorBody = 200
	// maxAnswerBody is how much of a response body the probe reads.
	maxAnswerBody = 1 << 20
)

// Options describe one recall probe.
type Options struct {
	Route *route.Route
	Home  *home.Home
	// Extra is merged into the request body of the second pass: the field
	// a reasoning flag or a provider quirk adds.
	Extra map[string]any
	// Reps is the number of requests per pass. 0 means 1.
	Reps int
	// Client sends the requests. Nil means a client with a two-minute
	// timeout.
	Client *http.Client
}

// Report is the outcome of one probe.
type Report struct {
	// Reps is the number of requests per pass.
	Reps int
	// Without and With are the short answers of each pass, in order.
	Without, With []string
	// RecalledWithout and RecalledWith count the answers holding the nonce.
	RecalledWithout, RecalledWith int
}

// Run sends the probe: reps requests as Crush builds the request, then reps
// with extra merged into the body, and reports each pass's short answers.
func Run(ctx context.Context, opts Options) (Report, error) {
	reps := opts.Reps
	if reps < 1 {
		reps = 1
	}
	url, header, err := endpoint(ctx, opts.Home, opts.Route)
	if err != nil {
		return Report{}, err
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	report := Report{Reps: reps}
	for _, pass := range []struct {
		extra   map[string]any
		answers *[]string
	}{
		{nil, &report.Without},
		{opts.Extra, &report.With},
	} {
		for range reps {
			answer, err := ask(ctx, client, url, header, opts.Route.Model, pass.extra)
			if err != nil {
				return Report{}, err
			}
			*pass.answers = append(*pass.answers, answer)
		}
	}
	report.RecalledWithout = recalled(report.Without)
	report.RecalledWith = recalled(report.With)
	return report, nil
}

// requestBody is one non-streaming chat request holding a user turn, an
// assistant turn whose reasoning holds the nonce, and a user turn asking for
// it. The extra fields of the second pass are merged over it.
func requestBody(model string, extra map[string]any) map[string]any {
	body := map[string]any{
		"model":  model,
		"stream": false,
		"messages": []map[string]any{
			{"role": "user", "content": "Think of a codeword and keep it in mind."},
			{"role": "assistant", "content": "OK", "reasoning_content": Nonce},
			{"role": "user", "content": "Which codeword did you keep in mind? Reply with the codeword only."},
		},
	}
	maps.Copy(body, extra)
	return body
}

// ask sends one request and returns its answer, cut to a short line.
func ask(ctx context.Context, client *http.Client, url string, header http.Header, model string, extra map[string]any) (string, error) {
	raw, err := json.Marshal(requestBody(model, extra))
	if err != nil {
		return "", fmt.Errorf("encode the probe request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("build the probe request: %w", err)
	}
	req.Header = header.Clone()
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send the probe request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // Read below.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBody))
	if err != nil {
		return "", fmt.Errorf("read the probe answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("probe request failed: %s: %s", resp.Status, short(string(data), maxErrorBody))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("parse the probe answer: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("the probe answer holds no choices")
	}
	return short(out.Choices[0].Message.Content, maxAnswer), nil
}

// recalled counts the answers that hold the nonce.
func recalled(answers []string) int {
	count := 0
	for _, answer := range answers {
		if strings.Contains(strings.ToUpper(answer), Nonce) {
			count++
		}
	}
	return count
}

// short collapses an answer to one line of at most max characters.
func short(s string, max int) string {
	runes := []rune(strings.Join(strings.Fields(s), " "))
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}

// endpoint resolves where the probe sends its request and the headers Crush
// sends with it. An expired OAuth token is renewed on the host, where the
// token endpoint is reachable, and stored as Crush stores it. Nothing here
// prints a credential.
func endpoint(ctx context.Context, h *home.Home, r *route.Route) (string, http.Header, error) {
	base := r.ProviderBaseURL()
	if base == "" {
		return "", nil, fmt.Errorf("route %s: provider %s has no base_url", r.Name, r.Provider)
	}
	url := strings.TrimSuffix(base, "/") + "/chat/completions"
	header := http.Header{}

	if r.Provider == route.ProviderCopilot {
		for name, value := range copilot.Headers() {
			header.Set(name, value)
		}
	}
	if key := resolveValue(r.ProviderConfig["api_key"]); key != "" {
		header.Set("Authorization", "Bearer "+key)
		for name, value := range extraHeaders(r) {
			header.Set(name, resolveValue(value))
		}
		return url, header, nil
	}

	if r.Provider == hyperp.Name {
		// Hyper serves chat completions one path below its catalog
		// endpoint, which Crush ships.
		url = strings.TrimSuffix(hyperp.Embedded().APIEndpoint, "/") + "/chat/completions"
	} else if r.Provider != route.ProviderCopilot {
		return "", nil, fmt.Errorf("route %s: api_key does not resolve to a value", r.Name)
	}
	token, err := accessToken(ctx, h, r.Provider)
	if err != nil {
		return "", nil, fmt.Errorf("route %s: %w", r.Name, err)
	}
	header.Set("Authorization", "Bearer "+token)
	return url, header, nil
}

// accessToken is the provider's stored access token, renewed and stored
// again when it has expired so a probe is never a day away from a 401.
func accessToken(ctx context.Context, h *home.Home, provider string) (string, error) {
	token, err := route.StoredToken(h, provider)
	if err != nil {
		return "", err
	}
	if token.IsExpired() {
		fresh, err := route.DefaultRefreshToken(ctx, provider, token.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("refresh the %s token: %w", provider, err)
		}
		if err := route.StoreToken(h, provider, fresh); err != nil {
			return "", err
		}
		token = fresh
	}
	return token.AccessToken, nil
}

// extraHeaders are the provider headers a route adds, as a header name to a
// possibly unresolved value.
func extraHeaders(r *route.Route) map[string]any {
	headers, _ := r.ProviderConfig["extra_headers"].(map[string]any)
	return headers
}

// resolveValue resolves a $NAME or ${NAME} reference from the environment,
// as Crush resolves provider config, and returns any other value as it is.
func resolveValue(value any) string {
	s, _ := value.(string)
	if !strings.HasPrefix(s, "$") {
		return s
	}
	name := strings.TrimPrefix(s, "$")
	name = strings.TrimSuffix(strings.TrimPrefix(name, "{"), "}")
	return os.Getenv(name)
}
