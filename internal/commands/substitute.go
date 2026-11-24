package commands

// Substitute replaces each $NAME placeholder in content with its argument
// in a single pass. A value is inserted as is and never scanned again, so
// an argument that contains "$OTHER" cannot pull in another argument, and
// the result does not depend on map order. Placeholders without an
// argument are left in place.
func Substitute(content string, args map[string]string) string {
	return namedArgPattern.ReplaceAllStringFunc(content, func(m string) string {
		if v, ok := args[m[1:]]; ok {
			return v
		}
		return m
	})
}

// PromptText builds the prompt a command sends for args. A skill sends its
// loaded-skill block, exactly as the TUI does; a custom command has its
// placeholders filled in.
func (c CustomCommand) PromptText(args map[string]string) string {
	if c.Skill != nil {
		return c.Skill.FormatInvocation()
	}
	return Substitute(c.Content, args)
}
