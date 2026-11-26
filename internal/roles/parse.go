package roles

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxDescriptionBytes = 200
	MaxBodyBytes        = 8 << 10
)

var (
	namePattern   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	reservedNames = []string{"coder", "task", "plan"}
)

// frontmatter is a role file's header. Pointer fields tell an overlay
// which fields the file sets.
type frontmatter struct {
	Name        string    `yaml:"name"`
	Description *string   `yaml:"description"`
	Tools       *toolList `yaml:"tools"`
	Model       *string   `yaml:"model"`
	Provider    *string   `yaml:"provider"`
	Reasoning   *string   `yaml:"reasoning"`
}

// toolList accepts a comma-separated string or a YAML list.
type toolList []string

func (t *toolList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*t = trimTools(strings.Split(node.Value, ","))
	case yaml.SequenceNode:
		var items []string
		if err := node.Decode(&items); err != nil {
			return err
		}
		*t = trimTools(items)
	default:
		return errors.New("tools must be a comma-separated string or a list")
	}
	return nil
}

func trimTools(items []string) toolList {
	out := toolList{}
	for _, item := range items {
		if name := strings.TrimSpace(item); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// definition is one parsed file before it is resolved against the role of
// the same name below it.
type definition struct {
	fm     frontmatter
	body   string
	path   string
	source Source
}

// overlay reports whether the file inherits from the role below it. A file
// without a body is an overlay.
func (d definition) overlay() bool {
	return d.body == ""
}

func parse(content []byte, path string, source Source) (definition, error) {
	header, body, err := splitFrontmatter(string(content))
	if err != nil {
		return definition{}, err
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(header), &fm); err != nil {
		return definition{}, fmt.Errorf("parsing frontmatter: %w", err)
	}
	def := definition{fm: fm, body: strings.TrimSpace(body), path: path, source: source}
	return def, def.validate()
}

func (d definition) validate() error {
	stem := strings.TrimSuffix(filepath.Base(d.path), ".md")
	switch {
	case !namePattern.MatchString(d.fm.Name):
		return fmt.Errorf("name %q must be lower-kebab-case", d.fm.Name)
	case slices.Contains(reservedNames, d.fm.Name):
		return fmt.Errorf("name %q is reserved", d.fm.Name)
	case d.fm.Name != stem:
		return fmt.Errorf("name %q must match the file name %q", d.fm.Name, stem)
	case len(d.body) > MaxBodyBytes:
		return fmt.Errorf("body is %d bytes, more than %d", len(d.body), MaxBodyBytes)
	case d.fm.Tools != nil && len(*d.fm.Tools) == 0:
		return errors.New("tools is empty; omit it to allow every tool")
	}
	if d.fm.Description == nil {
		if d.overlay() {
			return nil
		}
		return errors.New("description is required")
	}
	desc := strings.TrimSpace(*d.fm.Description)
	switch {
	case desc == "":
		return errors.New("description is empty")
	case strings.Contains(desc, "\n"):
		return errors.New("description must be one line")
	case len(desc) > MaxDescriptionBytes:
		return fmt.Errorf("description is %d bytes, more than %d", len(desc), MaxDescriptionBytes)
	}
	return nil
}

// apply resolves d against base, the role of the same name below it, or
// nil when there is none. A replacement ignores base; an overlay takes
// every field it leaves out from base, and treats model, provider and
// reasoning as one group.
func (d definition) apply(base *Role) (Role, error) {
	if !d.overlay() {
		r := Role{
			Name:        d.fm.Name,
			Description: trimmed(d.fm.Description),
			Model:       trimmed(d.fm.Model),
			Provider:    trimmed(d.fm.Provider),
			Reasoning:   trimmed(d.fm.Reasoning),
			Body:        d.body,
			Source:      d.source,
			Path:        d.path,
		}
		if d.fm.Tools != nil {
			r.Tools = slices.Clone([]string(*d.fm.Tools))
		}
		return r, nil
	}
	if base == nil {
		return Role{}, errors.New("an overlay (a file with no body) needs a role of the same name below it")
	}
	r := *base
	r.Tools = slices.Clone(base.Tools)
	r.Source, r.Path = d.source, d.path
	if d.fm.Description != nil {
		r.Description = trimmed(d.fm.Description)
	}
	if d.fm.Tools != nil {
		r.Tools = slices.Clone([]string(*d.fm.Tools))
	}
	if d.fm.Model != nil || d.fm.Provider != nil || d.fm.Reasoning != nil {
		r.Model, r.Provider, r.Reasoning = trimmed(d.fm.Model), trimmed(d.fm.Provider), trimmed(d.fm.Reasoning)
	}
	return r, nil
}

func trimmed(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// splitFrontmatter extracts the YAML header and the body. It mirrors the
// skills parser: a BOM is stripped and line endings are normalized first.
func splitFrontmatter(content string) (header, body string, err error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	lines := strings.Split(content, "\n")
	start := slices.IndexFunc(lines, func(line string) bool {
		return strings.TrimSpace(line) != ""
	})
	if start == -1 || strings.TrimSpace(lines[start]) != "---" {
		return "", "", errors.New("no YAML frontmatter found")
	}
	end := slices.IndexFunc(lines[start+1:], func(line string) bool {
		return strings.TrimSpace(line) == "---"
	})
	if end == -1 {
		return "", "", errors.New("unclosed YAML frontmatter")
	}
	end += start + 1
	return strings.Join(lines[start+1:end], "\n"), strings.Join(lines[end+1:], "\n"), nil
}
