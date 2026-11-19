package pinentry

import "strings"

// lsofMentions reports whether the output of `lsof -Fn` names path. It is
// kept platform independent so the parsing is covered on every platform;
// only the BSD builds that own the terminal through lsof call it.
func lsofMentions(out []byte, path string) bool {
	for line := range strings.Lines(string(out)) {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), "n")
		if ok && name == path {
			return true
		}
	}
	return false
}
