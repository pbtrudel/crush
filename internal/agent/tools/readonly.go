package tools

// readOnlyCommands extend safeCommands with forms that only read or wait.
// They stay out of the permission allow list because they can read any file
// the user can (cat, grep) or tie up the session (sleep), which is a
// question for permissions; whether a step could have finished a task is a
// narrower one, and these cannot.
var readOnlyCommands = []safeCommand{
	{argv: []string{"cat"}, allowOperands: true},
	{argv: []string{"cut"}, allowOperands: true},
	{argv: []string{"diff"}, allowOperands: true},
	// -C compiles the magic database named by -m, which is a write.
	{argv: []string{"file"}, denyFlags: []string{"-C", "--compile"}, allowOperands: true},
	// find writes or runs programs only through these actions.
	{
		argv: []string{"find"},
		denyFlags: []string{
			"-delete", "-exec", "-execdir", "-ok", "-okdir",
			"-fls", "-fprint", "-fprint0", "-fprintf",
		},
		allowOperands: true,
	},
	{argv: []string{"grep"}, allowOperands: true},
	{argv: []string{"head"}, allowOperands: true},
	{argv: []string{"pgrep"}, allowOperands: true},
	// --pre runs a program on every searched file.
	{argv: []string{"rg"}, denyFlags: []string{"--pre"}, allowOperands: true},
	{argv: []string{"sleep"}, allowOperands: true},
	// -o writes the sorted output to a file.
	{argv: []string{"sort"}, denyFlags: []string{"-o", "--output"}, allowOperands: true},
	{argv: []string{"stat"}, allowOperands: true},
	{argv: []string{"tail"}, allowOperands: true},
	// A second operand is an output file, which uniq writes.
	{argv: []string{"uniq"}, allowOperands: false},
	{argv: []string{"wc"}, allowOperands: true},
}

// IsReadOnlyCommand reports whether a shell command only reads or waits, so
// running it cannot have finished a task. It uses the same parser and fails
// closed the same way as the permission check: anything it cannot classify,
// including an unknown command, a redirect or an assignment, is not
// read-only.
func IsReadOnlyCommand(command string) bool {
	return isSafeReadOnly(command, readOnlyCommands)
}
