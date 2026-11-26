// Package roles loads agent role definitions: a named prompt with a tool
// allow-list and an optional model, from built-ins embedded in the binary
// and markdown files on disk.
package roles

import (
	"fmt"
	"slices"
	"strings"
)

// Source says where a role definition came from.
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceGlobal  Source = "global"
	SourceProject Source = "project"
)

// Role is one resolved role definition.
type Role struct {
	Name        string
	Description string
	// Tools holds the allow-list as written: exact tool names, or a prefix
	// ending in "*". Nil allows every tool.
	Tools     []string
	Model     string
	Provider  string
	Reasoning string
	Body      string
	Source    Source
	Path      string
}

// Dir is one directory of role files and the source it counts as.
type Dir struct {
	Path   string
	Source Source
}

// Diagnostic is one problem found while loading role files.
type Diagnostic struct {
	Path    string
	Message string
}

func (d Diagnostic) String() string {
	return d.Path + ": " + d.Message
}

// Allows reports whether the role lets an agent call the named tool.
func (r Role) Allows(tool string) bool {
	if r.Tools == nil {
		return true
	}
	for _, pattern := range r.Tools {
		if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
			if strings.HasPrefix(tool, prefix) {
				return true
			}
			continue
		}
		if pattern == tool {
			return true
		}
	}
	return false
}

// ReadOnly reports whether the role allows none of writeTools. A role with
// no allow-list can write.
func (r Role) ReadOnly(writeTools []string) bool {
	if r.Tools == nil {
		return false
	}
	return !slices.ContainsFunc(writeTools, r.Allows)
}

// PinsModel reports whether the role sets any field of its model group.
func (r Role) PinsModel() bool {
	return r.Model != "" || r.Provider != "" || r.Reasoning != ""
}

// Block renders the text a role adds to an agent's first message. It
// depends only on the role, so every agent with the same role sends the
// same bytes and siblings share them in the provider's cache.
func Block(r Role) string {
	allowed := "all tools"
	if r.Tools != nil {
		names := slices.Clone(r.Tools)
		slices.Sort(names)
		allowed = strings.Join(names, ", ")
	}
	return fmt.Sprintf("## Your role: %s\n\n%s\n\nAllowed tools: %s\nTeam coordination tools are always available.\n\n---\n\n", r.Name, r.Body, allowed)
}

// Catalog renders one line per role, sorted by name, for the delegation
// tool descriptions. The access marker comes from the resolved allow-list,
// not the description, so an overlay that widens a read-only role cannot
// claim to be read-only. A project role that pins a model says so, so a
// cloned repository cannot move spend to another model unnoticed.
func Catalog(list []Role, writeTools []string) string {
	sorted := slices.Clone(list)
	slices.SortFunc(sorted, func(a, b Role) int { return strings.Compare(a.Name, b.Name) })
	var b strings.Builder
	for _, r := range sorted {
		access := "can write"
		if r.ReadOnly(writeTools) {
			access = "read-only"
		}
		origin := string(r.Source) + ", " + access
		if r.Source == SourceProject && r.PinsModel() {
			origin += ", pins model"
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", r.Name, origin, r.Description)
	}
	return b.String()
}
