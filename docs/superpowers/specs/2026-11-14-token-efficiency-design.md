# Token Efficiency Design

## Goal

Send fewer input tokens per request and keep more of them served from the provider's prompt cache, without removing information the model needs. Where a change also makes the model better informed, that takes priority over the saving.

## Evidence

Measured on one workspace's session log (Oct 21 to Nov 14, 13,800 model requests, token counts only):

- 2.45 billion input tokens, 178k per request on average, 94.6% served from cache. At a cached price of 10%, cached re-reads are about 64% of input spend and full-price tokens about 36%.
- A token that enters the conversation is re-read about 100 times, so per-result waste multiplies.
- Tool results are 68% of conversation content. LSP diagnostics appended to view, edit and write results are 14% of tool-result bytes, mostly style hints for unrelated files.
- About 27k tokens of tool definitions and system prompt are sent with every request.
- Compaction ran at a median context of 560k tokens on 1M-window models.

## Design

### 1. The running turn and the replayed session send the same bytes

Providers cache a request prefix only while it stays byte-identical, and the next turn rebuilds its prefix from the session log. Three places sent one thing live and stored another.

1. Messages added between steps of a turn (queued follow-up prompts, repeat and todo reminders, background job notices) reached the model for one request only, because the agent loop rebuilds each step from the turn's initial prompt plus its own responses. They now stay in every later request of the turn, at the position where the session log stores them, in the order they were stored: reminders, then queued prompts, then notices.
2. The turn prompt is stored trimmed, which is how it is sent.
3. A tool's text or error output is bounded by `tool_output_max_tokens` where the tool returns it, so the running turn carries the same bounded text the session log stores. Media results are untouched.

### 2. Compaction

1. Pruning oversized tool results before a compaction request rewrites text the provider has already cached, so the request is billed in full and the summarizer sees less. Pruning now happens only when the unpruned request cannot fit the window (context reported by the last request, plus the summary's output budget, plus an 8,192-token reserve), or when the compaction recovers from a provider overflow, where the provider has already rejected the request as too large and the size estimate cannot be trusted. The mid-turn guard no longer forces pruning: it fires while the request still fits, so that compaction rides the warm cache. The compaction request otherwise sends the same system prompt, tools, and messages the turn's last step sent, so the provider serves the whole prefix from cache.
2. New option `auto_compact_max_tokens` (crushrc: `auto-compact-max-tokens`), default 400000. A finished turn compacts at the smaller of `auto_compact_threshold` times the window and this ceiling. 0 disables the ceiling. The mid-turn guard and model-switch compaction are unchanged, and a disabled threshold still disables automatic compaction.

### 3. Slimming history that is already cold

When a user message arrives at least `history_slim_idle_minutes` (default 60, 0 disables) after the message before it, the provider's cache has expired and the whole context is about to be re-read at full price. Everything before the latest such message is slimmed in the in-memory copy requests are built from. The session log is never changed.

- A `view` or `read` result is replaced by a one-line stub when the same file was successfully changed later, inside the slimmed region, by `edit`, `multiedit`, `write` or `lsp_replace_symbol`. The pre-edit copy is stale; the stub tells the model to view the file again.
- With `history_slim_reasoning` (default false), plain reasoning text of assistant messages in the region is dropped. Reasoning that carries a signature, a thought signature or provider response data is protocol state and always stays. This is opt-in because providers differ on whether earlier reasoning helps.

The slimmed region is a pure function of stored message times and content, so every request after the gap, including a compaction request, rebuilds the same bytes. It is not applied to the Claude Agent provider, which keeps history in its own session and would replay it as text.

### 4. Diagnostics attached to tool results

Results of view, edit, multiedit, write, lsp_replace_symbol and lsp_rename carry errors and warnings for the file itself and errors elsewhere in the project. Hints, information and other files' warnings are omitted, and nothing is attached when nothing qualifies. `lsp_diagnostics` still returns everything.

### 5. Fixed prefix

1. The provider and model catalog leaves the `agent_dispatch` and `spawn_teammate` descriptions (about 7 KB each) and becomes a `[models]` section of `crush_info`. A refused model points there.
2. The commit and pull request procedure leaves the bash description (4.2 KB) and becomes the builtin skill `git-workflow`. The bash description keeps a short block: the pointer to the skill, the standing prohibitions, and any configured attribution lines.
3. The Agent Teams policy is omitted when the agent's tool palette has no team tools. Its text is otherwise unchanged: shortening it or delivering it later would either rewrite the cached system prompt mid-session or move rules into tool results.

### 6. Teammate reports

A report longer than the inline cap is written in full next to the team state, and the inline text names the file.

### 7. Reading several files in one call

`view` and `read` accept `file_paths` (up to 10 files) so a model that does not issue parallel tool calls can still read several files in one request.

### 8. Claude Agent provider

1. Chat turns replace Claude Code's system prompt instead of appending to it, so the model gets one set of rules. Verified against CLI 2.1.322: with `--system-prompt` the request carries a one-line SDK identity and the given text only. Crush therefore keeps its own environment block and adds today's date, which the CLI's prompt used to supply.
2. Tool descriptions are cut at 2,048 characters on this provider. The part that is cut is now delivered in the system prompt, so no tool guidance is lost.

### 9. Cache routing and attribution

1. Each assistant message's finish record stores what its request used (uncached input, cache read, cache write, output) and how its prompt compared with the session's previous request (messages sent, messages previously sent, leading messages shared, whether the tool definitions changed). A low cache read with a fully shared prefix is a provider-side miss; a short shared prefix names the message that changed.
2. On Mistral the `prompt_cache_key` is derived from the session instead of the workspace, matching Mistral's guidance to use a conversation or session ID. One workspace key is shared by every concurrent agent. OpenAI keeps the workspace key.

## Failure behavior

- A spill or report file that cannot be written falls back to inline truncation without a file pointer.
- Recording request usage never fails a turn.
- A compaction whose sizes are unknown prunes as before.
- History slimming changes nothing when timestamps are missing, the option is 0, or the provider keeps its own history.

## Validation

- Unit tests for each new function, and run-level tests that compare the prompt of a later step with the prompt rebuilt from the session log.
- `go build ./...` and `go test ./...`.
- Live checks, reported in the pull request: the Claude Agent live test, a Mistral A/B of workspace and session cache keys, and a trial of multi-file reads on a model that rarely issues parallel calls.
