Compact the conversation above into a handoff summary so work can continue on a smaller context. The summary you write, plus the messages preserved after it, is everything the next turn will know about this session: assume the rest of the conversation above is about to be dropped.

**Required sections**:

## Current State

- What task is being worked on (exact user request)
- Current progress and what's been completed
- What's being worked on right now (incomplete work)
- What remains to be done (specific next steps, not vague)

## Files & Changes

- Files that were modified (with brief description of changes)
- Files that were read/analyzed (why they're relevant)
- Key files not yet touched but will need changes
- File paths and line numbers for important code locations

## Technical Context

- Architecture decisions made and why
- Patterns being followed (with examples)
- Libraries/frameworks being used
- Commands that worked (exact commands with context)
- Commands that failed (what was tried and why it didn't work)
- Environment details (language versions, dependencies, etc.)

## Strategy & Approach

- Overall approach being taken
- Why this approach was chosen over alternatives
- Key insights or gotchas discovered
- Assumptions made
- Any blockers or risks identified

## Exact Next Steps

Be specific. Don't write "implement authentication" - write:

1. Add JWT middleware to src/middleware/auth.go:15
2. Update login handler in src/routes/user.js:45 to return token
3. Test with: npm test -- auth.test.js

**Rules**:

- Preserve opaque identifiers exactly as written: file paths, symbols, command lines, error messages, and versions.
- Start your reply with a `# Conversation Summary` heading.
- Write as if briefing a teammate taking over mid-task. Include everything they'd need to continue without asking questions.
- Keep the summary within about 8,000 tokens: the most recent messages are preserved verbatim after your summary, so compress the older history densely instead of exhaustively.
- Do not call any tools. Respond with the summary text only.

**Tone**: Factual and dense. No emojis ever. Prioritize concrete facts (paths, commands, decisions) over narrative.
