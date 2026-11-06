Launch a subagent that works in its own context window and reports back a single result. By default it uses the read-only task preset. Set `agent` to a configured preset to give the subagent that preset's tools, MCP servers, and context paths.

Use this to:
- Fan out independent work. Emit several agent calls in one message and they run concurrently (up to 5 at a time). Reviewing several aspects of a change, searching several subsystems, or checking several hypotheses are all a good fit.
- Keep your own context clean. A subagent can read a large amount of material and hand back only the conclusion, which extends how long this session runs before it needs compaction.

Set `access` to choose how much the subagent can do:
- `read` (the default) gives it glob, grep, ls, view, bash, and the LSP tools. It can inspect and run things but cannot change files. Use it for review, search, and investigation.
- `write` adds the editing tools, so it can carry out a change end to end. Use it when the task is a self-contained piece of work you want done rather than described. Give it a scope it will not collide with, since parallel write subagents editing the same files will clobber each other.

Either way the subagent cannot launch further subagents.

Each call is independent: subagents cannot see your conversation, each other, or anything you did not put in the prompt. Write the prompt as a standalone brief. Say what to examine, what to do, and what shape the answer should take. State the scope explicitly, since two subagents given overlapping scopes will duplicate each other's work.

You get one text response back per call and cannot reply to it or ask a follow-up. A call that outruns a minute moves to the background and its report arrives later; use agent_status, agent_send, and agent_stop to follow up on it. If the work needs a conversation, do it yourself.
