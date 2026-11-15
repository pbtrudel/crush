---
name: git-workflow
description: Use when creating or amending a git commit, or creating or updating a pull request. Covers message quality, the commit procedure, the pull request procedure, and attribution lines.
---

# Git Workflow

## Message quality

These rules apply whenever creating or updating commit messages, PR titles, or
PR bodies:

- Messages MUST be understandable to someone unfamiliar with the codebase: a
  new contributor reading only the message should understand what problem this
  solves, why it matters, and the impact without opening files, reading the
  diff, or knowing internal code names.
- Avoid code identifiers, filenames, function names, and implementation
  details unless necessary to understand the user-facing impact.

## Commit messages

Commit messages are for future readers scanning history. Before committing:

- Follow the message quality rules above.
- Draft a concise 1-2 sentence message focusing on why the change exists and
  what outcome it enables, not a list of files or implementation details.
- Use clear, accurate verbs ("add"=new capability, "update"=enhancement,
  "fix"=bug fix) and avoid generic messages.
- The first line MUST be under 72 characters.
- Add a body only when needed to explain the reasoning, tradeoffs, or
  important context; wrap body lines at 72 characters.
- If the change is internal-only, describe the benefit or maintenance outcome
  rather than naming private code.
- Bad: "fix: nil pointer in session.go"
- Good: "fix: prevent session loading from crashing on missing metadata"

## Commit procedure

When the user asks to create a git commit:

1. Single message with three tool calls (IMPORTANT for speed):
   - git status (untracked files)
   - git diff (staged/unstaged changes)
   - git log (recent commit message style)

2. Add relevant untracked files to staging. Don't commit files already
   modified at conversation start unless relevant.

3. Analyze staged changes:
   - List changed/added files, summarize nature (feature/enhancement/bug
     fix/refactoring/test/docs)
   - Brainstorm purpose/motivation, assess project impact, check for sensitive
     info
   - Don't use tools beyond the context of git

4. Draft a commit message following the commit message rules; review it
   against the litmus test of whether a newcomer understands the change.

5. Create commit using HEREDOC:
   git commit -m "$(cat <<'EOF'
   Commit message here.
   EOF
   )"
   End the commit message with the attribution lines listed in the `<git>`
   block of the bash tool description, if it lists any.

6. If pre-commit hook fails, retry ONCE. If it fails again, the hook is
   preventing the commit. If it succeeds but modifies files, amend the commit.

7. Run git status to verify.

Notes: Use "git commit -am" when possible, don't stage unrelated files, NEVER
update config, don't push, no -i flags, no empty commits, return empty
response, when rebasing always use -m.

## Pull request procedure

Use the `gh` command for GitHub tasks when it is available.

When the user asks to create or update a PR:

1. Single message with multiple tool calls (VERY IMPORTANT for speed):
   - git status (untracked files)
   - git diff (staged/unstaged changes)
   - Check if branch tracks remote and is up to date
   - git log and 'git diff main...HEAD' (full commit history from main
     divergence)

2. Create a new branch if needed, commit if needed, and push with -u if
   needed.

3. Analyze the changes: list commits since diverging from main, summarize
   their nature, brainstorm purpose/motivation, assess project impact, and
   check for sensitive information. Don't use tools beyond the git context.

4. Draft the PR message following the message quality rules: 1-2 bullets
   focused on "why", reflecting ALL changes since main divergence; review it
   against the litmus test.

5. Create PR with gh pr create using HEREDOC:
   gh pr create --title "title" --body "$(cat <<'EOF'

   <summary>

   EOF
   )"
   End the pull request body with the "Generated with Crush" line when the
   `<git>` block of the bash tool description lists it.

Important: return empty response, the user sees gh output. Never update git
config.
