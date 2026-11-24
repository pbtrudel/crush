Execute shell commands; long-running commands automatically move to background and return a shell ID.

<cross_platform>
Uses mvdan/sh interpreter (Bash-compatible on all platforms including Windows).
Use forward slashes for paths: "ls C:/foo/bar" not "ls C:\foo\bar".
Common shell builtins and core utils available on Windows.
</cross_platform>

<execution_steps>
1. Directory Verification: If creating directories/files, use LS tool to verify parent exists
2. Security Check: Dangerous commands ({{ .BannedCommands }}) require explicit user approval with a warning popup in normal mode; YOLO mode blocks them without asking; Sysadmin mode approves them without asking. Safe read-only commands always execute without prompts
3. Command Execution: Execute with proper quoting, capture output
4. Auto-Background: Commands exceeding 1 minute (default, configurable via `auto_background_after`) automatically move to background and return shell ID
5. Output Processing: Truncate if exceeds {{ .MaxOutputLength }} characters. The truncation marker names a file holding the complete output - read it with the view tool when the omitted middle matters
6. Return Result: Include errors, metadata with <cwd></cwd> tags
</execution_steps>

<usage_notes>
- command is required; description (a label under 30 characters) is recommended; working_dir is optional
- IMPORTANT: Use Grep/Glob/Agent tools instead of 'find'/'grep'. Use View/LS tools instead of 'cat'/'head'/'tail'/'ls'
- Chain with ';' or '&&', avoid newlines except in quoted strings
- Each command runs in independent shell (no state persistence between calls)
- Prefer absolute paths over 'cd' (use 'cd' only if user explicitly requests)
- Run privileged commands with plain `sudo`, never `sudo -n`: the user can approve with Touch ID or another PAM method, and without one sudo fails at once ("a terminal is required") instead of hanging. Each command authenticates separately
{{- if .RgAvailable }}
- Ripgrep (`rg`) is available; prefer it over `grep` for faster, more intuitive searching
{{- end }}
</usage_notes>

<background_execution>
- Set run_in_background=true to run a command in a separate background shell; it returns a shell ID (read it with job_output, stop it with job_kill). NEVER use `&` for this - use the parameter instead.
- Use it for long-running servers (`npm start`, `python -m http.server`), watch tasks (`npm run watch`, `tail -f logfile`), and anything that does not exit on its own.
- Do not use it for build commands, test suites, git operations, file operations, or short-lived scripts.
- Backgrounding buys you the time the command takes, so spend it: start the job, then keep working.
- Do NOT call job_output right after starting a job. It returns the output so far, and a job that just started has printed nothing.
- Call job_output once the job's result is the next thing you need, or once you have run out of other work.
- job_output with wait=true blocks until the job finishes, giving back the time you just bought; only wait when waiting is the useful thing to do.
- A command that auto-backgrounds on the timeout behaves the same way: it is still running, so keep working unless you need its result now.
- Track every background job id you start. You are notified in-session when a job finishes; do not busy-poll or sleep on one. Keep working on independent steps. Before a final answer, collect every still-relevant job with job_output (use wait=true only when genuinely blocked).
</background_execution>

<git>
- Before creating a commit or a pull request, view `crush://skills/git-workflow/SKILL.md` and follow it.
- Never update git config, never push unless asked, and never use interactive flags (-i).
{{- if .GhAvailable }}
- Use the `gh` command for ALL GitHub tasks.
{{- end }}
{{- if or .Attribution.GeneratedWith (eq .Attribution.TrailerStyle "assisted-by") (eq .Attribution.TrailerStyle "co-authored-by") }}
- End every commit message with these lines, after a blank line (one blank line between them when more than one is listed):
{{- if .Attribution.GeneratedWith }}
  💘 Generated with Crush
{{- end }}
{{- if eq .Attribution.TrailerStyle "assisted-by" }}
  Assisted-by: Crush:<MODEL>
{{- else if eq .Attribution.TrailerStyle "co-authored-by" }}
  Co-Authored-By: Crush <crush@charm.land>
{{- end }}
{{- end }}
{{- if .Attribution.GeneratedWith }}
- End every pull request body with: 💘 Generated with Crush
{{- end }}
</git>

<examples>
Good: pytest /foo/bar/tests
Bad: cd /foo/bar && pytest tests
</examples>
