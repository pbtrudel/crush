package task

import (
	"bytes"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ReplaceBlock replaces the top-level block key of the YAML file at path with
// block, leaving the rest of the file byte-for-byte unchanged. A file without
// the block gets it at the end.
func ReplaceBlock(path, key string, block []byte) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := splitLines(data)
	start, end := blockSpan(lines, key)
	if start < 0 {
		start, end = len(lines), len(lines)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines = append(lines, "\n")
			start, end = len(lines), len(lines)
		}
	}
	var out bytes.Buffer
	for _, line := range lines[:start] {
		out.WriteString(line)
	}
	out.Write(block)
	for _, line := range lines[end:] {
		out.WriteString(line)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// splitLines splits a file into lines that keep their trailing newline.
func splitLines(data []byte) []string {
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// blockSpan returns the line range of the file's top-level key block, or -1
// when the file has none. The block ends after its last indented line: a
// blank line or a column-0 comment after it stays outside, and a column-0
// comment belongs to the block only when more indented content follows it.
func blockSpan(lines []string, key string) (start, end int) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "")), &doc); err != nil {
		return -1, -1
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return -1, -1
	}
	mapping := doc.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		start = mapping.Content[i].Line - 1
		end = start + 1
		for i := start + 1; i < len(lines); i++ {
			line := lines[i]
			switch {
			case strings.TrimSpace(line) == "", line[0] == '#':
				// A blank line or a column-0 comment is part of the block
				// only when an indented line follows it.
			case line[0] == ' ' || line[0] == '\t':
				end = i + 1
			default:
				return start, end
			}
		}
		return start, end
	}
	return -1, -1
}
