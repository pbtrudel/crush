package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// BrowserUserAgent is a realistic browser User-Agent for better compatibility.
const BrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var multipleNewlinesRe = regexp.MustCompile(`\n{3,}`)

// FetchURLAndConvert fetches a URL and converts HTML content to markdown.
func FetchURLAndConvert(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Use realistic browser headers for better compatibility.
	req.Header.Set("User-Agent", BrowserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// DuckDuckGo answers its own result pages with a challenge once the
		// machine is blocked, so those answers mean the same block the search
		// backend reports. A missing page or a broken server does not.
		if ddgHost(url) && ddgBlockStatus(resp.StatusCode) {
			markSearchBlocked()
			return "", errSearchRateLimited
		}
		return "", &FetchStatusError{
			Status:     resp.StatusCode,
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}

	maxSize := int64(5 * 1024 * 1024) // 5MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	content, err := decodeResponseBody(body, contentType)
	if err != nil {
		return "", err
	}

	// Convert HTML to markdown for better AI processing.
	if strings.Contains(contentType, "text/html") {
		// Remove noisy elements before conversion.
		cleanedHTML := removeNoisyElements(content)
		markdown, err := ConvertHTMLToMarkdown(cleanedHTML)
		if err != nil {
			return "", fmt.Errorf("failed to convert HTML to markdown: %w", err)
		}
		content = cleanupMarkdown(markdown)
	} else if strings.Contains(contentType, "application/json") || strings.Contains(contentType, "text/json") {
		content = formatJSON(content)
	}

	return content, nil
}

// decodeResponseBody converts a response body to UTF-8. Bodies that are
// already valid UTF-8 are returned unchanged; otherwise the charset from the
// Content-Type header or an HTML meta tag is used to transcode them.
func decodeResponseBody(body []byte, contentType string) (string, error) {
	if utf8.Valid(body) {
		return string(body), nil
	}
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return "", fmt.Errorf("failed to decode response body: %w", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read decoded response body: %w", err)
	}
	if !utf8.Valid(decoded) {
		return "", errors.New("response content is not valid UTF-8")
	}
	return string(decoded), nil
}

// removeNoisyElements removes script, style, nav, header, footer, and other
// noisy elements from HTML to improve content extraction.
func removeNoisyElements(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// If parsing fails, return original content.
		return htmlContent
	}

	// Elements to remove entirely.
	noisyTags := map[string]bool{
		"script":   true,
		"style":    true,
		"nav":      true,
		"header":   true,
		"footer":   true,
		"aside":    true,
		"noscript": true,
		"iframe":   true,
		"svg":      true,
	}

	var removeNodes func(*html.Node)
	removeNodes = func(n *html.Node) {
		var toRemove []*html.Node

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && noisyTags[c.Data] {
				toRemove = append(toRemove, c)
			} else {
				removeNodes(c)
			}
		}

		for _, node := range toRemove {
			n.RemoveChild(node)
		}
	}

	removeNodes(doc)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return htmlContent
	}

	return buf.String()
}

// cleanupMarkdown removes excessive whitespace and blank lines from markdown.
func cleanupMarkdown(content string) string {
	// Collapse multiple blank lines into at most two.
	content = multipleNewlinesRe.ReplaceAllString(content, "\n\n")

	// Remove trailing whitespace from each line.
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	content = strings.Join(lines, "\n")

	// Trim leading/trailing whitespace.
	content = strings.TrimSpace(content)

	return content
}

// ConvertHTMLToMarkdown converts HTML content to markdown format.
func ConvertHTMLToMarkdown(htmlContent string) (string, error) {
	converter := md.NewConverter("", true, nil)

	markdown, err := converter.ConvertString(htmlContent)
	if err != nil {
		return "", err
	}

	return markdown, nil
}

// formatJSON indents content when it is JSON and returns it unchanged
// otherwise. Source bytes are kept, so numbers and key order survive.
func formatJSON(content string) string {
	var buf bytes.Buffer
	if json.Indent(&buf, bytes.TrimSpace([]byte(content)), "", "  ") != nil {
		return content
	}
	return buf.String()
}

// FetchStatusError is a non-200 answer from a fetch. It carries the status so
// a caller can decide what to do next, and its message says what the model
// should try instead of asking for the same URL again.
type FetchStatusError struct {
	Status     int
	RetryAfter string
}

// Error renders the status with the next step for that class of answer.
func (e *FetchStatusError) Error() string {
	return fmt.Sprintf("request failed with status code: %d%s", e.Status, fetchStatusHint(e.Status, e.RetryAfter))
}

// fetchStatusHint renders the next step for a failed status, or an empty
// string for a status the model cannot act on.
func fetchStatusHint(status int, retryAfter string) string {
	switch status {
	case http.StatusNotFound, http.StatusGone:
		return " does not exist; the address was probably guessed. Open the site's index or search instead of guessing another path"
	case http.StatusTooManyRequests:
		if retryAfter != "" {
			return fmt.Sprintf(" rate-limited (retry after %ss); do not retry right away", retryAfter)
		}
		return " rate-limited; do not retry right away"
	case http.StatusServiceUnavailable:
		return " temporarily unavailable; do not retry right away"
	case http.StatusForbidden:
		return " refused access"
	default:
		return ""
	}
}

// ddgHost reports whether a URL belongs to DuckDuckGo, whose search pages
// answer with a bot challenge instead of content once blocked. It is a var so
// a test can stand in a local server.
var ddgHost = isDuckDuckGoHost

// isDuckDuckGoHost reports whether a URL belongs to DuckDuckGo.
func isDuckDuckGoHost(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "duckduckgo.com" || strings.HasSuffix(host, ".duckduckgo.com")
}

// ddgBlockStatus reports the DuckDuckGo answers that mean the machine is
// blocked, rather than that the page is missing or the server failed.
func ddgBlockStatus(status int) bool {
	switch status {
	case http.StatusAccepted, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}
