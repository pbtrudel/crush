package tools

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/shell"
)

type BashParams struct {
	Description         string `json:"description,omitempty" description:"A brief description of what the command does, try to keep it under 30 characters or so"`
	Command             string `json:"command" description:"The command to execute"`
	WorkingDir          string `json:"working_dir,omitempty" description:"The working directory to execute the command in (defaults to current directory)"`
	RunInBackground     bool   `json:"run_in_background,omitempty" description:"Set to true (boolean) to run this command in the background. Use job_output to read the output later."`
	AutoBackgroundAfter int    `json:"auto_background_after,omitempty" description:"Seconds to wait before automatically moving the command to a background job (default: 60)"`
}

type BashPermissionsParams struct {
	Description         string `json:"description"`
	Command             string `json:"command"`
	WorkingDir          string `json:"working_dir"`
	RunInBackground     bool   `json:"run_in_background"`
	AutoBackgroundAfter int    `json:"auto_background_after"`
}

type BashResponseMetadata struct {
	StartTime        int64  `json:"start_time"`
	EndTime          int64  `json:"end_time"`
	Output           string `json:"output"`
	Description      string `json:"description"`
	WorkingDirectory string `json:"working_directory"`
	Background       bool   `json:"background,omitempty"`
	ShellID          string `json:"shell_id,omitempty"`
	// ExitCode is set for every command the tool waited for and that
	// finished, zero included. Interrupted marks one that was aborted,
	// which has no exit code.
	ExitCode    *int `json:"exit_code,omitempty"`
	Interrupted bool `json:"interrupted,omitempty"`
}

const (
	BashToolName = "bash"

	DefaultAutoBackgroundAfter = 60 // Commands taking longer automatically become background jobs
	MaxOutputLength            = 30000
	BashNoOutput               = "no output"
	// cancelledJobWait bounds how long a cancelled call waits for its job to
	// end, so a write a detached child makes after the kill still lands
	// before the scope check that follows.
	cancelledJobWait = 10 * time.Second
)

//go:embed bash.md.tpl
var bashDescriptionTmpl []byte

var bashDescriptionTpl = template.Must(
	template.New("bashDescription").
		Parse(string(bashDescriptionTmpl)),
)

type bashDescriptionData struct {
	BannedCommands  string
	MaxOutputLength int
	Attribution     config.Attribution
	RgAvailable     bool
	GhAvailable     bool
}

// defaultBlockedCommands is the built-in dangerous-command list. It is a
// default, not a hardcoded policy: the `permissions` config block can add
// to it and take away from it (see resolveBlockedCommands).
var defaultBlockedCommands = []string{
	// Network/Download tools
	"alias",
	"aria2c",
	"axel",
	"chrome",
	"curl",
	"curlie",
	"firefox",
	"http-prompt",
	"httpie",
	"links",
	"lynx",
	"nc",
	"safari",
	"scp",
	"ssh",
	"telnet",
	"w3m",
	"wget",
	"xh",

	// System administration
	"doas",
	"su",
	"sudo",

	// Indirect execution: these run a command name the static check can't
	// see, so whatever they wrap would never be flagged on its own.
	"env",
	"eval",
	"nohup",
	"xargs",

	// Package managers
	"apk",
	"apt",
	"apt-cache",
	"apt-get",
	"dnf",
	"dpkg",
	"emerge",
	"home-manager",
	"makepkg",
	"opkg",
	"pacman",
	"paru",
	"pkg",
	"pkg_add",
	"pkg_delete",
	"portage",
	"rpm",
	"yay",
	"yum",
	"zypper",

	// System modification
	"at",
	"batch",
	"chkconfig",
	"crontab",
	"fdisk",
	"mkfs",
	"mount",
	"parted",
	"service",
	"systemctl",
	"umount",

	// Network configuration
	"firewall-cmd",
	"ifconfig",
	"ip",
	"iptables",
	"netstat",
	"pfctl",
	"route",
	"ufw",
}

// modelIDPlaceholder is the literal token the commit-attribution instructions
// tell the model to write. The bash tool expands it to the live model ID when
// the command runs, so commits record the model that produced them while the
// tool description stays identical across model switches (a changing tool
// description would invalidate the provider's prompt cache).
const modelIDPlaceholder = "<MODEL>"

// resolveBlockedCommands returns the effective dangerous-command list:
// the built-in defaults plus perms.BlockedCommands, minus
// perms.AllowedCommands.
//
// The built-in order is preserved and user additions are appended. The list is
// embedded verbatim in the tool description, so it has to be deterministic, but
// sorting it would scramble the category grouping the defaults are written in
// and churn the prompt for every existing user.
func resolveBlockedCommands(perms *config.Permissions) []string {
	if perms == nil {
		return slices.Clone(defaultBlockedCommands)
	}
	// Both sides are normalized the way the block list itself matches, so a
	// config entry spelled "/usr/bin/CURL" or " curl " lines up with the
	// "curl" already on the list. Without this, allowing a command by a
	// slightly different spelling did nothing at all, which is the worst way
	// for a permission setting to be wrong: it looks applied.
	allowed := make(map[string]struct{}, len(perms.AllowedCommands))
	for _, cmd := range perms.AllowedCommands {
		allowed[shell.NormalizeCommandName(cmd)] = struct{}{}
	}

	// Clone rather than pre-sizing: the capacity arithmetic on two lengths
	// is flagged as a possible overflow, and the append growth is cheap
	// next to the config read that produced the list.
	blocked := slices.Clone(defaultBlockedCommands)
	for _, cmd := range perms.BlockedCommands {
		if name := shell.NormalizeCommandName(cmd); name != "" {
			blocked = append(blocked, name)
		}
	}
	blocked = slices.DeleteFunc(blocked, func(c string) bool {
		_, ok := allowed[shell.NormalizeCommandName(c)]
		return ok
	})

	seen := make(map[string]struct{}, len(blocked))
	deduped := blocked[:0]
	for _, cmd := range blocked {
		if _, dup := seen[cmd]; dup {
			continue
		}
		seen[cmd] = struct{}{}
		deduped = append(deduped, cmd)
	}
	return deduped
}

// describeCommand derives a short label from the command for calls that
// leave description out. The label is only shown in the UI and the job
// list; permission prompts already show the command itself.
func describeCommand(command string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(command), "\n")
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) <= 60 {
		return line
	}
	return string([]rune(line)[:57]) + "..."
}

func bashDescription(attribution *config.Attribution, blocked []string) string {
	var out bytes.Buffer
	if err := bashDescriptionTpl.Execute(&out, bashDescriptionData{
		BannedCommands:  strings.Join(blocked, ", "),
		MaxOutputLength: MaxOutputLength,
		Attribution:     *attribution,
		RgAvailable:     getRg() != "",
		GhAvailable:     ghAvailable,
	}); err != nil {
		// this should never happen.
		panic("failed to execute bash description template: " + err.Error())
	}
	return out.String()
}

// blockFuncs builds the block list from the resolved dangerous commands.
// The flat command list is configurable; the argument blockers below are
// structural rules about specific flags rather than a list of binaries, so
// they stay built in.
func blockFuncs(blocked []string) []shell.BlockFunc {
	return []shell.BlockFunc{
		shell.CommandsBlocker(blocked),

		// System package managers
		shell.ArgumentsBlocker("apk", []string{"add"}, nil),
		shell.ArgumentsBlocker("apt", []string{"install"}, nil),
		shell.ArgumentsBlocker("apt-get", []string{"install"}, nil),
		shell.ArgumentsBlocker("dnf", []string{"install"}, nil),
		shell.ArgumentsBlocker("pacman", nil, []string{"-S"}),
		shell.ArgumentsBlocker("pkg", []string{"install"}, nil),
		shell.ArgumentsBlocker("yum", []string{"install"}, nil),
		shell.ArgumentsBlocker("zypper", []string{"install"}, nil),

		// Language-specific package managers
		shell.ArgumentsBlocker("brew", []string{"install"}, nil),
		shell.ArgumentsBlocker("cargo", []string{"install"}, nil),
		shell.ArgumentsBlocker("gem", []string{"install"}, nil),
		shell.ArgumentsBlocker("go", []string{"install"}, nil),
		shell.ArgumentsBlocker("npm", []string{"install"}, []string{"--global"}),
		shell.ArgumentsBlocker("npm", []string{"install"}, []string{"-g"}),
		shell.ArgumentsBlocker("pip", []string{"install"}, []string{"--user"}),
		shell.ArgumentsBlocker("pip3", []string{"install"}, []string{"--user"}),
		shell.ArgumentsBlocker("pnpm", []string{"add"}, []string{"--global"}),
		shell.ArgumentsBlocker("pnpm", []string{"add"}, []string{"-g"}),
		shell.ArgumentsBlocker("yarn", []string{"global", "add"}, nil),

		// `go test -exec` can run arbitrary commands
		shell.ArgumentsBlocker("go", []string{"test"}, []string{"-exec"}),
	}
}

// NewBashTool returns the bash tool. Every shell it starts is tagged with
// shellOwner, so the owner can stop them with
// [shell.BackgroundShellManager.KillOwned].
func NewBashTool(permissions permission.Service, workingDir, spillDir string, attribution *config.Attribution, modelID string, maxBackgroundJobs int, perms *config.Permissions, shellOwner any, tracker filetracker.Service) fantasy.AgentTool {
	blocked := resolveBlockedCommands(perms)
	userSafe := userSafeCommands(perms)
	return fantasy.NewAgentTool(
		BashToolName,
		string(bashDescription(attribution, blocked)),
		func(ctx context.Context, params BashParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			workingDir := WorkingDirFrom(ctx, workingDir)
			if params.Command == "" {
				return fantasy.NewTextErrorResponse("missing command"), nil
			}

			// The field is optional: a model that leaves it out still gets
			// a label for the UI and the job list.
			if strings.TrimSpace(params.Description) == "" {
				params.Description = describeCommand(params.Command)
			}

			// Expand the attribution token with the model that is running
			// right now, before anything else inspects the command.
			if strings.Contains(params.Command, modelIDPlaceholder) {
				params.Command = strings.ReplaceAll(params.Command, modelIDPlaceholder, modelID)
			}

			// Determine working directory. A relative working_dir resolves
			// inside this call's working directory, which is the task
			// worktree when a teammate is in one; an absolute one is used
			// as given.
			execWorkingDir := filepathext.SmartJoin(workingDir, cmp.Or(params.WorkingDir, workingDir))

			safeReadOnly := isSafeReadOnly(params.Command, userSafe)

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for executing shell command")
			}
			// Check whether the command is dangerous so we can surface a
			// warning in the permission dialog.
			var check shell.CommandCheck
			var adminBlocks []shell.BlockFunc
			mode := permissions.PermissionMode()
			if !safeReadOnly {
				check = shell.CheckCommand(params.Command, blockFuncs(blocked))

				if check.Matched && mode.RefusesDangerous() {
					adminBlocks = adminBlockFuncs(blocked)
					if inner := shell.CheckCommand(params.Command, adminBlocks); inner.Matched {
						name := inner.Reason
						if slices.Contains(adminCommands, name) {
							name = "the command " + name + " runs"
						}
						return fantasy.NewTextErrorResponse(yoloBlockedMessage(name)), nil
					}
				}

				p, err := permissions.Request(
					ctx,
					permission.CreatePermissionRequest{
						SessionID:   sessionID,
						Path:        execWorkingDir,
						ToolCallID:  call.ID,
						ToolName:    BashToolName,
						Action:      "execute",
						Description: fmt.Sprintf("Execute command: %s", params.Command),
						Params:      BashPermissionsParams(params),
						Danger:      check.Reason,
						// Path is only the working directory here, so a
						// session grant keyed on it alone would turn one
						// approval of `ls` into standing approval for every
						// later command run in that directory. Key it to the
						// command the user was actually shown.
						GrantKey: params.Command,
					},
				)
				if err != nil {
					return fantasy.ToolResponse{}, err
				}
				if !p {
					return NewPermissionDeniedResponse(), nil
				}
			}

			// A named match reaching here was approved at a prompt; keep the block list for everything else.
			var blocksToUse []shell.BlockFunc
			switch {
			case adminBlocks != nil:
				blocksToUse = adminBlocks
			case !check.Matched && mode != permission.PermissionModeSysadmin:
				blocksToUse = blockFuncs(blocked)
			}
			before := snapshotReadFiles(ctx, tracker, sessionID, params, workingDir)
			resp, err := executeBashCommand(ctx, params, execWorkingDir, spillDir, blocksToUse, sessionID, maxBackgroundJobs, shellOwner)
			recordShellReads(ctx, tracker, sessionID, params, execWorkingDir, resp, err)
			noteChangedReads(&resp, err, before)
			return resp, err
		},
	)
}

// readFileState is one file the session read earlier: the path the note
// names, the absolute path the snapshot resolved once, and the modification
// time at the moment the command started.
type readFileState struct {
	path    string
	full    string
	modTime time.Time
}

// maxSnapshottedReads bounds how many read files one command watches: a
// session that read thousands of files would otherwise stat all of them
// around every command it runs, twice. The cap keeps a command's cost
// proportional to the size of the session, not to the command.
const maxSnapshottedReads = 200

// snapshotReadFiles records the modification times of the files the session
// read earlier, so the command's effect on them can be named afterwards. A
// command that runs in the background, or a session with no tracker, has
// nothing to compare.
func snapshotReadFiles(ctx context.Context, tracker filetracker.Service, sessionID string, params BashParams, workingDir string) []readFileState {
	if tracker == nil || sessionID == "" || params.RunInBackground {
		return nil
	}
	paths, err := tracker.ListReadFiles(ctx, sessionID)
	if err != nil || len(paths) == 0 {
		return nil
	}
	if len(paths) > maxSnapshottedReads {
		paths = paths[:maxSnapshottedReads]
	}
	states := make([]readFileState, 0, len(paths))
	for _, path := range paths {
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(workingDir, full)
		}
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		states = append(states, readFileState{path: path, full: full, modTime: info.ModTime()})
	}
	return states
}

// maxNamedChangedReads bounds how many paths the note names, so a formatter
// run over a large tree does not bury the command's own output.
const maxNamedChangedReads = 10

// noteChangedReads tells the model which of the files it read earlier changed
// while the command ran, so it views them before editing them instead of
// learning the same thing from a refusal. It records no read: the command may
// have rewritten the file to something the model has not seen.
func noteChangedReads(resp *fantasy.ToolResponse, err error, before []readFileState) {
	if len(before) == 0 || err != nil || resp.IsError {
		return
	}
	var changed []string
	for _, state := range before {
		if len(changed) == maxNamedChangedReads {
			break
		}
		// Re-stat the path the snapshot resolved, not the same relative
		// name under the command's directory: with a working_dir of a
		// subdirectory the two differ, and a miss there would name a file
		// that did not change.
		info, statErr := os.Stat(state.full)
		if statErr != nil && !os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil || !info.ModTime().Equal(state.modTime) {
			changed = append(changed, state.path)
		}
	}
	if len(changed) == 0 {
		return
	}
	note := fmt.Sprintf("Files you read earlier changed during this command: %s. View them before editing.", humanList(changed))
	if resp.Content == "" {
		resp.Content = note
		return
	}
	resp.Content += "\n\n" + note
}

// recordShellReads counts the files a finished foreground command only read
// as reads, the way view does, so an edit to a file the model inspected
// through bash is not refused. A command that was moved to the background, or
// that failed, says nothing about the file as it stands.
func recordShellReads(ctx context.Context, tracker filetracker.Service, sessionID string, params BashParams, workingDir string, resp fantasy.ToolResponse, err error) {
	if tracker == nil || sessionID == "" || err != nil || resp.IsError || params.RunInBackground {
		return
	}
	var meta BashResponseMetadata
	if len(resp.Metadata) > 0 {
		if jsonErr := json.Unmarshal([]byte(resp.Metadata), &meta); jsonErr != nil {
			return
		}
	}
	if meta.Background || meta.Interrupted || meta.ExitCode == nil || *meta.ExitCode != 0 {
		return
	}
	for _, path := range shellReadPaths(params.Command, workingDir) {
		tracker.RecordRead(ctx, sessionID, path)
	}
}

// adminCommands still run in yolo mode. They ask for the user's own
// credentials (a password, Touch ID) when they run, so yolo answering the
// permission prompt does not let them act unseen. What they run is still
// checked against the block list.
var adminCommands = []string{"doas", "sudo"}

// adminValueFlags are the flags of each admin command that take a separate
// value, so the value is not mistaken for the command being run.
var adminValueFlags = map[string][]string{
	"doas": {"-a", "-C", "-u"},
	"sudo": {
		"-C", "--close-from", "-D", "--chdir", "-g", "--group", "-h", "--host",
		"-p", "--prompt", "-R", "--chroot", "-r", "--role", "-T", "--command-timeout",
		"-t", "--type", "-U", "--other-user", "-u", "--user",
	},
}

// adminInner returns the command an admin command runs, and false when args
// is not an admin command. An admin command with nothing after its flags
// (sudo -v) runs nothing, so it resolves to an empty command.
func adminInner(args []string) ([]string, bool) {
	args = shell.ResolveArgv(args)
	if len(args) == 0 {
		return nil, false
	}
	name := shell.NormalizeCommandName(args[0])
	if !slices.Contains(adminCommands, name) {
		return nil, false
	}
	valueFlags := adminValueFlags[name]
	rest := args[1:]
options:
	for len(rest) > 0 {
		tok := rest[0]
		switch {
		case tok == "--":
			rest = rest[1:]
			break options
		case !strings.HasPrefix(tok, "-") || tok == "-":
			break options
		case strings.HasPrefix(tok, "--"):
			// --user=root is one word; --user root is two.
			words := 1
			if slices.Contains(valueFlags, tok) {
				words = 2
			}
			rest = rest[min(words, len(rest)):]
		default:
			rest = rest[min(shortFlagWords(tok, valueFlags), len(rest)):]
		}
	}
	// sudo sets leading NAME=VALUE words in the command's environment, so
	// the command is the first word after them.
	if name == "sudo" {
		for len(rest) > 0 && strings.Index(rest[0], "=") > 0 {
			rest = rest[1:]
		}
	}
	if len(rest) == 0 {
		return nil, true
	}
	return rest, true
}

// shortFlagWords is how many words a cluster of short flags such as -Eu
// takes. The first letter that takes a value ends the cluster: the rest of
// the word is its value (-uroot), or the next word is when nothing is left
// (-Eu root).
func shortFlagWords(cluster string, valueFlags []string) int {
	for i := 1; i < len(cluster); i++ {
		if slices.Contains(valueFlags, "-"+cluster[i:i+1]) {
			if i == len(cluster)-1 {
				return 2
			}
			return 1
		}
	}
	return 1
}

// adminBlockFuncs is the block list with the admin commands taken off it,
// plus a rule that applies the list to whatever an admin command runs, so
// `sudo true` runs in yolo mode while `sudo curl` stays blocked.
func adminBlockFuncs(blocked []string) []shell.BlockFunc {
	inner := blockFuncs(slices.DeleteFunc(slices.Clone(blocked), func(cmd string) bool {
		return slices.Contains(adminCommands, shell.NormalizeCommandName(cmd))
	}))
	var wrapped shell.BlockFunc
	wrapped = func(args []string) bool {
		cmd, ok := adminInner(args)
		if !ok || len(cmd) == 0 {
			return false
		}
		if wrapped(cmd) {
			return true
		}
		return slices.ContainsFunc(inner, func(block shell.BlockFunc) bool { return block(cmd) })
	}
	return append(inner, wrapped)
}

// yoloBlockedMessage names the way out so the model doesn't retry variations.
func yoloBlockedMessage(command string) string {
	return fmt.Sprintf("blocked: %s is a dangerous command, and yolo mode blocks dangerous commands without asking. Tell the user: they can switch to normal mode to be asked about it, use sysadmin mode to allow it, or mark it safe with `permissions safe`.", command)
}

func executeBashCommand(ctx context.Context, params BashParams, execWorkingDir, spillDir string, blockFuncsToUse []shell.BlockFunc, sessionID string, maxBackgroundJobs int, shellOwner any) (fantasy.ToolResponse, error) {
	// If explicitly requested as background, start immediately with detached context
	if params.RunInBackground {
		startTime := time.Now()
		bgManager := shell.GetBackgroundShellManager()
		// Use background context so it continues after tool returns
		bgShell, err := bgManager.StartWithSession(context.Background(), shellOwner, execWorkingDir, blockFuncsToUse, params.Command, params.Description, sessionID, maxBackgroundJobs)
		if err != nil {
			return fantasy.NewTextErrorResponse("Failed to start background shell: " + err.Error()), nil
		}

		// Wait a short time to detect fast failures (blocked commands, syntax errors, etc.)
		time.Sleep(1 * time.Second)
		stdout, stderr, done, execErr := bgShell.GetOutput()

		if done {
			// Finished inside the fast-failure probe window. Keep it tracked:
			// the completion notice already told the model to read this job
			// with job_output, and the retention window reaps it later.
			// Removing it here made that instruction a dead end.

			interrupted := shell.IsInterrupt(execErr)
			exitCode := shell.ExitCode(execErr)
			if exitCode == 0 && !interrupted && execErr != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("[Job %s] Error executing command: %s", bgShell.ID, execErr)), nil
			}

			stdout = formatOutput(stdout, stderr, execErr, spillDir)

			metadata := BashResponseMetadata{
				StartTime:        startTime.UnixMilli(),
				EndTime:          time.Now().UnixMilli(),
				Output:           stdout,
				Description:      params.Description,
				Background:       params.RunInBackground,
				WorkingDirectory: bgShell.WorkingDir,
				ShellID:          bgShell.ID,
			}
			metadata.ExitCode, metadata.Interrupted = commandOutcome(execErr)
			if stdout == "" {
				return fantasy.WithResponseMetadata(fantasy.NewTextResponse(BashNoOutput), metadata), nil
			}
			stdout += fmt.Sprintf("\n\n<cwd>%s</cwd>", normalizeWorkingDir(bgShell.WorkingDir))
			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(stdout), metadata), nil
		}

		// Still running after fast-failure check - return as background job
		metadata := BashResponseMetadata{
			StartTime:        startTime.UnixMilli(),
			EndTime:          time.Now().UnixMilli(),
			Description:      params.Description,
			WorkingDirectory: bgShell.WorkingDir,
			Background:       true,
			ShellID:          bgShell.ID,
		}
		response := fmt.Sprintf("Background shell started with ID: %s\n\nUse job_output tool to view output or job_kill to terminate.", bgShell.ID)
		return fantasy.WithResponseMetadata(fantasy.NewTextResponse(response), metadata), nil
	}

	// Start synchronous execution with auto-background support
	startTime := time.Now()

	// Start with detached context so it can survive if moved to background.
	// No session is attached yet: a command that finishes while we wait is
	// answered inline, so announcing it as a background completion would
	// send the model after output it already has.
	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(context.Background(), shellOwner, execWorkingDir, blockFuncsToUse, params.Command, params.Description, maxBackgroundJobs)
	if err != nil {
		return fantasy.NewTextErrorResponse("Failed to start shell: " + err.Error()), nil
	}

	// Wait for either completion, auto-background threshold, or context cancellation
	autoBackgroundAfter := cmp.Or(params.AutoBackgroundAfter, DefaultAutoBackgroundAfter)
	autoBackgroundThreshold := time.Duration(autoBackgroundAfter) * time.Second
	timer := time.NewTimer(autoBackgroundThreshold)
	defer timer.Stop()

	select {
	case <-bgShell.Done():
	case <-timer.C:
	case <-ctx.Done():
		// Incoming context was cancelled before we moved to background.
		// Kill the shell and wait, bounded, for it to end: a descendant the
		// kill does not reach can still write, and the scope check that
		// follows the call must see that write. The cancelled context would
		// let Kill return at once, so it gets a fresh bounded one. Kill's
		// "may still be exiting" error is expected here and not worth
		// reporting.
		killCtx, killCancel := context.WithTimeout(context.WithoutCancel(ctx), cancelledJobWait)
		_ = bgManager.Kill(killCtx, bgShell.ID)
		killCancel()
		// The result is still stored, from this metadata and the error,
		// so the stamp says the command was aborted.
		return fantasy.WithResponseMetadata(fantasy.ToolResponse{}, BashResponseMetadata{
			StartTime:        startTime.UnixMilli(),
			EndTime:          time.Now().UnixMilli(),
			Description:      params.Description,
			WorkingDirectory: bgShell.WorkingDir,
			Interrupted:      true,
		}), ctx.Err()
	}
	if !bgShell.IsDone() {
		// It outlived the foreground window, so it is a background job now
		// and its completion should reach the model as a notice.
		bgShell.SetSessionID(sessionID)
	}
	stdout, stderr, done, execErr := bgShell.GetOutput()

	if done {
		// Command completed within threshold - return synchronously
		// Remove from background manager since we're returning directly
		// Don't call Kill() as it cancels the context and corrupts the exit code
		bgManager.Remove(bgShell.ID)

		interrupted := shell.IsInterrupt(execErr)
		exitCode := shell.ExitCode(execErr)
		if exitCode == 0 && !interrupted && execErr != nil {
			return fantasy.NewTextErrorResponse("Error executing command: " + execErr.Error()), nil
		}

		stdout = formatOutput(stdout, stderr, execErr, spillDir)

		metadata := BashResponseMetadata{
			StartTime:        startTime.UnixMilli(),
			EndTime:          time.Now().UnixMilli(),
			Output:           stdout,
			Description:      params.Description,
			Background:       params.RunInBackground,
			WorkingDirectory: bgShell.WorkingDir,
		}
		metadata.ExitCode, metadata.Interrupted = commandOutcome(execErr)
		if stdout == "" {
			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(BashNoOutput), metadata), nil
		}
		stdout += fmt.Sprintf("\n\n<cwd>%s</cwd>", normalizeWorkingDir(bgShell.WorkingDir))
		return fantasy.WithResponseMetadata(fantasy.NewTextResponse(stdout), metadata), nil
	}

	// Still running - keep as background job
	metadata := BashResponseMetadata{
		StartTime:        startTime.UnixMilli(),
		EndTime:          time.Now().UnixMilli(),
		Description:      params.Description,
		WorkingDirectory: bgShell.WorkingDir,
		Background:       true,
		ShellID:          bgShell.ID,
	}
	response := fmt.Sprintf("Command is taking longer than expected and has been moved to background.\n\nBackground shell ID: %s\n\nUse job_output tool to view output or job_kill to terminate.", bgShell.ID)
	return fantasy.WithResponseMetadata(fantasy.NewTextResponse(response), metadata), nil
}

// formatOutput formats the output of a completed command with error handling
func formatOutput(stdout, stderr string, execErr error, spillDir string) string {
	interrupted := shell.IsInterrupt(execErr)
	exitCode := shell.ExitCode(execErr)

	stdout = truncateOutput(stdout, spillDir)
	stderr = truncateOutput(stderr, spillDir)

	errorMessage := stderr
	if errorMessage == "" && execErr != nil {
		errorMessage = execErr.Error()
	}

	if interrupted {
		if errorMessage != "" {
			errorMessage += "\n"
		}
		errorMessage += "Command was aborted before completion"
	} else if exitCode != 0 {
		if errorMessage != "" {
			errorMessage += "\n"
		}
		errorMessage += fmt.Sprintf("Exit code %d", exitCode)
	}

	hasBothOutputs := stdout != "" && stderr != ""

	if hasBothOutputs {
		stdout += "\n"
	}

	if errorMessage != "" {
		stdout += "\n" + errorMessage
	}

	return stdout
}

// commandOutcome reports how a finished command ended, for the result
// metadata: its exit status, or that it was aborted, which has none.
func commandOutcome(execErr error) (exitCode *int, interrupted bool) {
	if shell.IsInterrupt(execErr) {
		return nil, true
	}
	return new(shell.ExitCode(execErr)), false
}

// TruncateOutput keeps the head and tail of oversized tool output. The
// dropped middle is written in full to a file under spillDir so the agent
// can read it back rather than losing it for good.
func TruncateOutput(content, spillDir string) string {
	if ansi.StringWidth(content) <= MaxOutputLength {
		return content
	}

	halfLength := MaxOutputLength / 2
	start := ansi.Truncate(content, halfLength, "")
	end := ansi.TruncateLeft(content, ansi.StringWidth(content)-halfLength, "")

	truncatedLinesCount := max(strings.Count(content, "\n")-strings.Count(start, "\n")-strings.Count(end, "\n"), 0)
	marker := fmt.Sprintf("... [%d lines truncated] ...", truncatedLinesCount)
	if path, err := shell.SpillOutput(ansi.Strip(content), spillDir); err == nil {
		marker = fmt.Sprintf("... [%d lines truncated, full output: %s] ...", truncatedLinesCount, path)
	} else {
		slog.Debug("Could not spill tool output", "dir", spillDir, "error", err)
	}
	return fmt.Sprintf("%s\n\n%s\n\n%s", start, marker, end)
}

func truncateOutput(content, spillDir string) string {
	return TruncateOutput(content, spillDir)
}

func normalizeWorkingDir(path string) string {
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, fsext.WindowsWorkingDirDrive(), "")
	}
	return filepath.ToSlash(path)
}
