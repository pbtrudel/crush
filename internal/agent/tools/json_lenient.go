package tools

import "strings"

// trimTrailingCommas drops commas that sit right before a closing bracket,
// which some models emit inside a stringified JSON payload. Commas inside
// strings and escaped quotes are left untouched.
func trimTrailingCommas(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == ']' || s[j] == '}') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}
