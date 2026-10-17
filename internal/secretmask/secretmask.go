// Package secretmask detects well-known credential formats in text and
// replaces them with a masked form. Masking is a rendering concern only:
// the original text is neither stored nor sent differently.
package secretmask

import "regexp"

// prefixLen is how many leading characters of a masked secret stay
// visible. Four characters are enough to identify the token family
// (sk-, ghp_, AKIA, xoxb, ...) without exposing the secret.
const prefixLen = 4

// secretPattern pairs a compiled pattern with a fixed replacement. When
// replacement is empty the matched secret keeps a short prefix; used for
// token formats that are recognized by their prefix. A non-empty
// replacement is used for matches that should be hidden wholesale.
type secretPattern struct {
	re          *regexp.Regexp
	replacement string
}

// patterns lists the credential formats worth masking. The word boundary
// in front of each prefix keeps words that merely end in the prefix's
// letters (like "task-abcdefghijklmnopqrstuvwx", which contains "sk-")
// from being mistaken for tokens. Length thresholds are generous: a real
// token is never shorter, and a near miss stays readable rather than
// being silently hidden.
var patterns = []secretPattern{
	{
		// Private key blocks span multiple lines and have no prefix
		// worth keeping, so they are replaced wholesale.
		re:          regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
		replacement: "[redacted private key]",
	},
	{
		re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
	},
	{
		re: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),
	},
	{
		re: regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{20,}`),
	},
	{
		re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	},
	{
		re: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	},
	{
		re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`),
	},
	{
		re: regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	},
	{
		re: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),
	},
	{
		re: regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35}\b`),
	},
	{
		re: regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}`),
	},
	{
		re: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`),
	},
	{
		re: regexp.MustCompile(`\btvly-[A-Za-z0-9]{20,}`),
	},
}

// Mask returns s with every detected secret replaced by a masked form.
// Text without secrets is returned unchanged.
func Mask(s string) string {
	for _, p := range patterns {
		if !p.re.MatchString(s) {
			continue
		}
		if p.replacement != "" {
			s = p.re.ReplaceAllLiteralString(s, p.replacement)
			continue
		}
		s = p.re.ReplaceAllStringFunc(s, maskPrefix)
	}
	return s
}

// maskPrefix keeps a short identifying prefix of the match and hides the
// rest behind an ellipsis.
func maskPrefix(match string) string {
	runes := []rune(match)
	if len(runes) <= prefixLen {
		return "…"
	}
	return string(runes[:prefixLen]) + "…"
}
