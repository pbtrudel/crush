# Step 2 Corrections Design

Design for the corrections of step 2 of the roadmap (`docs/superpowers/plans/2026-11-17-model-intelligence-roadmap.md`): W1.1, W2.2, W2.3, W2.4, W4.2, W4.3, W5.1 and W9. No code has been changed. Code references are to commit `3f18401c` (`origin/main`). The roadmap was written against `c71dec5e`; pull requests #96 to #118 landed since, so every claim was checked again and every reference below is new.

## Goal

Fix eight things that are wrong today, each with a test that fails before the fix. None of them is an experiment. The baseline window of the roadmap starts once all eight have landed.

What stays out: W1.2, W1.3, W2.1, W3, W4.1, W5.2, W5.3, W6, W7 and W8. Where a correction touches the same code as one of those, the boundary is stated in the item.

## Summary

| Item | Status | Size | Main files |
|---|---|---|---|
| W1.1 Reasoning helpers drop provider data | reshaped: core claim holds, the replay sub-point is dropped | S | `internal/message/content.go`, `internal/agent/agent.go` |
| W2.2 `multiedit` hides why edits failed | verified | S | `internal/agent/tools/multiedit.go` |
| W2.3 `write` blames a change that did not happen | verified | S | `internal/agent/tools/write.go` |
| W2.4 Small tool texts | reshaped: the `ls` claim is narrower, `glob` is fixed in its description | S | `edit.go`, `grep.go`, `grep.md.tpl`, `glob.md.tpl`, `ls.go`, `view.go` |
| W4.2 Write subagents receive the Team tools | verified, and wider: `agentic_fetch` leaks too | S | `internal/agent/agent_tool.go`, `internal/config/config.go` |
| W4.3 A subdirectory loses the root instructions | verified, and wider: the environment snapshot loses git status too | S | `internal/agent/prompt/prompt.go`, `internal/config/load.go` |
| W5.1 Prompt statements that are wrong | verified | S | `internal/agent/templates/coder.md.tpl`, `internal/agentteam/team.go` |
| W9 State after compaction | reshaped: skills are found from the log, the skill tracker is left alone | M | `internal/agent/agent.go`, `internal/agent/compaction_state.go` (new) |

Sizes follow the roadmap: S under 150 changed lines, M up to 400, tests included.

## W1.1. Reasoning helpers drop provider data

### What the code does

A stored reasoning part has seven fields: `Thinking`, `Signature`, `ThoughtSignature`, `ToolID`, `ResponsesData`, `StartedAt`, `FinishedAt` (`internal/message/content.go:57-65`).

Five helpers change a reasoning part. Each builds a new struct from a subset of the fields instead of changing one field:

| Helper | Lines | Fields it keeps | Fields it drops |
|---|---|---|---|
| `AppendReasoningContent` | `content.go:571-590` | Thinking, Signature, StartedAt, FinishedAt | ThoughtSignature, ToolID, ResponsesData |
| `AppendThoughtSignature` | `content.go:592-607` | all but ResponsesData | ResponsesData |
| `AppendReasoningSignature` | `content.go:609-622` | Thinking, Signature, StartedAt, FinishedAt | ThoughtSignature, ToolID, ResponsesData |
| `SetReasoningResponsesData` | `content.go:624-636` | Thinking, StartedAt, FinishedAt | Signature, ThoughtSignature, ToolID |
| `FinishThinking` | `content.go:638-652` | Thinking, Signature, StartedAt | ThoughtSignature, ToolID, ResponsesData |

`OnReasoningEnd` stores the Anthropic signature, then the Gemini thought signature, then the OpenAI Responses data, then calls `FinishThinking` (`internal/agent/agent.go:1575-1593`). `FinishThinking` rebuilds the part only while `FinishedAt` is zero, which is the end of the first reasoning block of the message. So for that block the Gemini signature, its tool id and the Responses data are dropped before the message is stored. A later block's start calls `AppendReasoningContent` (`agent.go:1553`, `:1569`), which drops them again.

Within a turn fantasy keeps its own copy, so nothing fails. From the next turn on, the history is rebuilt from the store, and `ToAIMessage` sends only what survived (`content.go:939-957`). Gemini then receives its earlier turns without thought signatures. This also means the replayed history differs from the request fantasy sent in the live turn, which moves the cached prefix for those providers. That second effect follows from the code and was not measured.

For OpenAI Responses models the request does not change: fantasy skips every reasoning item when it builds a Responses request (roadmap W1.2). Storing `ResponsesData` correctly is still needed by W1.2.

### Claims that changed

- **Signature-only messages skipped on replay.** The check is now at `agent.go:3464`. It skips an assistant message with no tool calls, no text and no reasoning text, even when the reasoning carries a signature. This item drops that sub-point. Such a message is a turn that produced only signed reasoning, the "empty response" case seen once in the log. No provider documentation found requires it to be replayed, and an assistant message that holds only a reasoning block may itself be rejected. Changing the check would add risk with no evidence of a gain.
- **Anthropic redacted thinking.** Still true. fantasy reports it as `ReasoningOptionMetadata.RedactedData` (`fantasy@v0.45.5/providers/anthropic/anthropic.go:520-528`, `:1360-1369`) and replays it as a redacted block (`:1124-1125`). Crush calls `AppendReasoningSignature` with the empty signature and loses the data. It stays in this item, bounded as described below.

### Change

1. Add one private method, `updateReasoning(fn func(*ReasoningContent)) bool`, that applies `fn` to the first reasoning part in place and reports whether one existed. Each of the five helpers calls it and changes only its own field. `AppendReasoningContent` today rewrites every reasoning part (it has no `return`); messages hold one reasoning part, so editing the first is the same behavior.
2. Add `RedactedData string `json:"redacted_data,omitempty"`` to `ReasoningContent`. `HasReplayData` includes it. `ToAIMessage` puts it in the Anthropic metadata next to `Signature`. `OnReasoningEnd` stores it through a new helper, `SetReasoningRedactedData`. Old rows decode unchanged because the field is optional.

Known limit, left alone: a message stores one reasoning part, so a step with several reasoning blocks keeps their text concatenated and one set of provider data. Fixing that needs one part per block, which belongs with W1.2.

`internal/proto/message.go:89-95` has a smaller copy of the type for the client-server wire. It is display data and not replayed, so it is not changed.

### Test

`internal/message/reasoning_parts_test.go` (new):

- A table over the five helpers and the new one. Each starts from a part with every field set, calls the helper, and asserts that every field except the one the helper owns is unchanged. Fails today for five of the five existing helpers.
- The sequence `OnReasoningEnd` runs for a Gemini block (`AppendThoughtSignature`, then `FinishThinking`) leaves `ThoughtSignature` and `ToolID` set, and `ToAIMessage` puts them in the Google metadata. Fails today.
- A part with only `RedactedData` round-trips through `EncodeParts` and `DecodeParts` and replays as Anthropic metadata with that data.

### Size and risks

S: about 50 changed lines of code and 100 of tests.

Risk: Gemini starts receiving its thought signatures from the next turn on. That is what Google asks for, but it is a change in what is sent. Messages stored before the fix stay without them.

### Upstream surface

`content.go` is shared with upstream, which has the same helpers. The change is local to the helpers and the struct. The agent-side change is three lines in `OnReasoningEnd`.

## W2.2. `multiedit` hides why edits failed

### What the code does

Each failed edit is recorded with its 1-based index, its error text and its kind (`internal/agent/tools/multiedit.go:130-148`). The error text of a match failure already carries the nearest-text hint (`edit.go:226-232`). Only the metadata receives it. The text the model reads is:

- `Applied 2 of 5 edits to file: <path> (3 edit(s) failed)` when some applied (`multiedit.go:327-331`);
- `no changes made - all 3 edit(s) failed` when none did (`:261-276`);
- `File created with 2 of 5 edits: <path> (3 edit(s) failed)` on creation (`:227-228`).

Claim verified.

### Change

One helper, `failedEditsText([]FailedEdit) string`, renders:

```
Failed edits:
- edit 3: old_string not found in file. Make sure it matches exactly, including whitespace and line breaks

  <hint>
- edit 5: old_string appears multiple times in the file. ...
```

It is appended to all three messages. The hint was computed against the content as it stood after the earlier edits of the batch, which is what the model needs. Metadata is unchanged, so the UI and the stats report keep working.

### Test

`internal/agent/tools/multiedit_test.go`: a batch of three edits where the second does not match. The response text must contain `edit 2:` and `old_string not found`. A second case where all edits fail checks the error response the same way. Both fail today. `edit_match_kind_test.go:140` pins the old all-failed text exactly and is updated to check the prefix.

### Size and risks

S: about 30 lines of code and 50 of tests. Risk: a batch with many failures and long hints produces a long result. Tool output is already bounded with a spill file, so the window is protected.

### Upstream surface

`multiedit.go` is shared with upstream. The change is one new function and three call sites.

## W2.3. `write` blames a change that did not happen

### What the code does

`write` compares the file's modification time with the last read time (`internal/agent/tools/write.go:75-86`). For a file never read in the session the last read time is zero, so the test passes and the model reads `File <path> has been modified since it was last read ... Last read: 0001-01-01T00:00:00Z`. Since #98 the error kind already separates the two cases in the log (`write.go:79-83`), and a comment there records that the wording was kept on purpose. Claim verified.

### Change

When the last read time is zero, answer:

`File <path> exists but has not been read in this session. Read it with view before overwriting it.`

The kind stays `not_read`. The changed-since-read case keeps its current text and kind.

### Test

`internal/agent/tools/error_kind_test.go:100-134` covers both cases and asserts the old wording for both (`:122`). The never-read case changes to assert `has not been read` and the absence of `0001-01-01`. Fails today.

### Size and risks

S: about 10 lines of code and 15 of tests. No risk found. The stats report reads the kind, not the wording.

### Upstream surface

`write.go` is shared, and upstream has the same message. One branch.

## W2.4. Small tool texts

Five texts, one pull request.

### What the code does, and what changed

1. **`edit` with several matches.** The error says `old_string appears multiple times in the file ...` with no count and no lines (`internal/agent/tools/edit.go:203`). Verified.
2. **`grep`.** The description says it "returns matching file paths sorted by modification time" (`grep.md.tpl:1`). It returns matching lines with line numbers, grouped by file (`grep.go:155-177`). With no match it answers `No files found` (`grep.go:151`). It writes `Found %d matches` with the count after the cap of 100 (`grep.go:153`, `:211-214`), so 3,000 matches read as `Found 100 matches` followed by a truncation note. Verified.
3. **`glob`.** The description says results are "sorted by modification time" (`glob.md.tpl:1`). The ripgrep path sorts by path length (`glob.go:182-184`). The fallback sorts by modification time (`internal/fsext/fileutil.go:170`). Verified: the order depends on whether ripgrep is installed.
4. **`ls`.** The roadmap says the truncation notice is dropped "whenever a depth limit is set". Narrower than that: the depth notice overwrites the truncation notice when the configured `max_depth` is positive (`ls.go:161-167`, `internal/config/config.go:1061-1063`). A depth passed by the model does not trigger it. The overwrite itself is verified.
5. **`view`.** A section over 200 KB is rejected with `Content section is too large (N bytes). Maximum size is M bytes` (`view.go:334-337`). The reader knows how many lines fitted when it stopped (`view.go:576-596`) but does not say. Verified.

### Change

1. `findAndReplace` keeps the existing sentence and appends the count and the first lines: `It matches 3 times, starting on lines 12, 40 and 77.` At most ten line numbers are listed. Keeping the sentence keeps the prefix test in `edit_match_kind_test.go:23-48` valid. `multiedit` reports the same text through W2.2.
2. `grep.md.tpl`: "Search file contents by regex or literal text; returns matching lines with line numbers, grouped by file, most recently modified first (max {{ .MaxResults }} matches); respects .gitignore. Use glob to filter by filename, not contents." `No files found` becomes `No matches found`. `searchFiles` also returns the count before the cap, and the header reads `Found 3000 matches, showing the first 100` when it was cut.
3. `glob.md.tpl`: drop the order claim: "Find files by name/pattern (glob syntax); max {{ .MaxResults }} results; skips hidden files. Use grep to search file contents." Making both paths sort the same way would change results, which is more than a correction.
4. `ls`: build both notices and join them. The condition for the depth notice is unchanged.
5. `view`: `contentTooLargeError` records the lines that fitted. The message adds `The first K lines fit; read again with a limit of at most K, or start at a later offset.`

### Test

- `internal/agent/tools/edit_test.go`: three matches give a message with `3 times` and their line numbers.
- `grep_test.go`: no match gives `No matches found`; 150 matching lines give `Found 150 matches, showing the first 100`; the rendered description does not say `file paths`.
- `glob_test.go`: the rendered description does not claim an order.
- `ls_test.go` (new): a configured `max_depth` and a `max_items` that cuts the listing give both notices.
- `view_test.go:158`: the oversized case also contains `lines fit`.

Each fails today.

### Size and risks

S, close to the limit: about 70 lines of code and 90 of tests.

Risk: the two description changes alter the tool block of every request. That costs one cache miss per session on the first request after upgrade, like any tool description change. The recorded coder cassettes would also stop matching, but `TestCoderAgent` is already skipped until they are re-recorded (`internal/agent/agent_test.go:57-63`).

### Upstream surface

All six files are shared with upstream, and upstream has the same texts. Each edit is one line or one small branch. This is the item with the widest upstream surface per changed line.

## W4.2. Write subagents receive the Team tools

### What the code does

- The read-only subagent's tool list removes the delegation tools, `agentic_fetch`, `question` and the nine Team tools (`internal/config/config.go:1440-1460`, `:1477-1479`, `:1514`).
- The write subagent's list is a copy of the coder's (`internal/agent/agent_tool.go:43-50`), so it starts with all of them.
- The role filter removes only `question`, `agent` and the four dispatch tools for a subagent (`internal/agent/coordinator.go:1249-1261`).
- The Team tools are built for every agent while Agent Teams is on (`coordinator.go:1301-1303`) and kept when the allowed list names them (`:1375-1380`). The write subagent's list does.
- They are bound to the member name found on the build context (`coordinator.go:1302`, `internal/agentteam/team.go:169-174`). For the Lead's subagents that is `lead`.

So a write subagent is offered `spawn_teammate`, `send_message`, the task board and the rest under the Lead's name, and `agentic_fetch` as well. The subagent session that wrote this document was offered all of them. Claim verified, and wider than stated.

### Change

`writeAgentConfig` removes the subagent-excluded tools from the copied list. `config.go` exports the existing list through a function, `SubAgentExcludedTools()`, so both variants follow one rule. The write subagent keeps everything else it has today: the edit tools, `worktree`, MCP tools.

Removing `agentic_fetch` follows the stated intent of that list ("the tools a subagent never gets", `config.go:1440-1442`). The read-only subagent already lives without it.

Teammates are not touched. They are built from the coder config with `roleTeammate` (`internal/agent/team.go:233-256`), and neither `writeAgentConfig` nor the role filter applies to them.

### Test

In `internal/agent/team_cache_test.go`, using its coordinator with a live Team service (`:21-43`) and `agentToolNames` (`:62`): build `taskAgents(ctx, false)` and assert that the write variant's palette holds none of `agentteam.ToolNames` and not `agentic_fetch`, and still holds `edit` and `write`. Fails today. `TestTeammateToolPaletteMatchesLead` (`:75`) stays as it is and guards the teammate palette. `TestWriteAgentConfigInheritsCoderMCPs` (`agent_preset_test.go:60-73`) is updated to the filtered list.

### Size and risks

S: about 10 lines of code and 40 of tests.

Risk: a write subagent that used `agentic_fetch` loses it. No such use was counted; the roadmap's log counts the 143 `agentic_fetch` sessions but not their parents' kind.

### Upstream surface

`agent_tool.go` and `config.go` are shared, but `writeAgentConfig` and the exclusion list are fork code. Small.

## W4.3. A subdirectory loses the root instructions

### What the code does

- Every context path is joined to the working directory (`internal/agent/prompt/prompt.go:166-168`) and loaded from there only (`:216-229`).
- "Is directory a git repo" stats `<dir>/.git` (`prompt.go:420`, `:460-463`).
- The environment snapshot uses the same check (`prompt/snapshot.go:29`), so a session started in a subdirectory also gets no git status.
- Config loading already walks up from the working directory to the git top level, and stops at the working directory outside git (`internal/config/load.go:1146-1170`, `:1521-1565`).

Claim verified, and wider: the snapshot is affected too.

### Change

1. `config` exports `ProjectRoot(dir string) (root string, inRepo bool)`, built on the existing `worktreeRoot`, which runs `git rev-parse --show-toplevel` once per directory and caches it.
2. For a relative context path, `processContextPath` looks in each directory from the project root down to the working directory, broadest first. The budget already ranks deeper files as more specific (`prompt.go:231-233`), so the order keeps the most specific file longest. Files are de-duplicated by resolved path. Absolute and `~` paths are unchanged. The global chain is unchanged.
3. `isGitRepo` becomes the `inRepo` result. The prompt and the snapshot both use it.

Outside a repository the root is the working directory, so nothing above it is read. That is the boundary config loading already uses.

### Test

Using `newRepo` from `internal/agent/prompt/git_test.go:26`:

- `prompt_context_test.go` (new): a repository with a root `AGENTS.md` holding a marker and a subdirectory `pkg/`. A prompt built with `config.Init` on `pkg/` contains the marker and `Is directory a git repo: yes`. A second case places an `AGENTS.md` above a directory that is not in a repository, and it must not appear.
- `snapshot_test.go`: `SessionEnvSnapshot` on `pkg/` contains the git status.

Each fails today.

### Size and risks

S: about 50 lines of code and 80 of tests.

Risk: in a large repository started from a subdirectory the prompt grows by the root files, up to the context byte budget. That is the intended effect. The prompt is built once per process, so the cached prefix stays stable within a session.

### Upstream surface

`prompt.go` is shared but already differs from upstream by about 300 lines. `load.go` gains one exported function.

## W5.1. Prompt statements that are wrong

### What the code does

`coder.md.tpl` is still 24,445 bytes, and the line numbers of the roadmap still hold:

| Line | Text | Why it is wrong |
|---|---|---|
| 72 | `- Read entire file before editing it` | Line 20 says "Avoid reading entire files"; line 6 says "the relevant context" |
| 94 | `- Use find_references before changing shared code` | The tool is `lsp_references` |
| 108 | `4. Commit and push the branch from the worktree, then open a PR and get it merged.` | Lines 11 and 16 forbid committing and pushing unless asked |
| 214 | `The Edit tool is extremely literal. "Close enough" will fail.` | Whitespace differences are forgiven (`edit_whitespace.go`) |
| 331 | `... subagents, tests, web_fetch, etc.` | Only the `agentic_fetch` subagent has `web_fetch`; the coder has `fetch` |

The Team policy, rendered into the Lead's prompt, now at `internal/agentteam/team.go:52`, says what to do when a file operation "returns FS_STALE_VERSION". No code returns that string; the guards answer with the texts of W2.3 and `edit.go:310-323`. Verified.

### Change

| Line | New text |
|---|---|
| 72 | `- Read the relevant context before editing it` |
| 94 | `- Use lsp_references before changing shared code` |
| 108 | `4. If the user asked for it, commit and push the branch from the worktree, then open a PR and get it merged. Otherwise leave the work in the worktree and report its path.` |
| 214 | `The Edit tool matches text literally. Only whitespace differences are forgiven; anything else fails.` |
| 331 | `web_fetch` becomes `fetch` |

Team policy: "If an edit or write is refused because the file changed since you read it, read the current file, rebase your intended change onto the new content, and retry." It names no error string, so W2.3 can reword the guard texts freely.

Line 20 stays, as the roadmap says, until W6 measures removing it.

### Test

`internal/agent/prompt_text_test.go` (new, fork-owned):

- Render the Lead's prompt with its default palette. For every name in `config`'s full tool list, plus `web_fetch`, `web_search` and `find_references`, the prompt mentions the name only when it is in the palette. Fails today on `find_references` and `web_fetch`.
- The prompt and `agentteam.Policy` contain none of `Read entire file before editing`, `extremely literal` and `FS_STALE_VERSION`, and the worktree step about pushing carries the condition. Fails today.

### Size and risks

S: 6 changed lines of text and about 60 of tests.

Risk: the static part of the prompt changes, which costs one cache miss per session on the first request after upgrade. Teammates reuse the Lead's prompt, so they follow it with no extra miss. No golden file contains the prompt; the only recorded requests that do are the skipped coder cassettes.

### Upstream surface

Lines 72, 94, 214 and 331 exist upstream as they are here, so each is a one-line merge conflict risk. Line 108 and the Team policy are fork-only. To keep the surface small, nothing else in the template moves.

## W9. State after compaction

### What the code does

- **The summary is unlabelled.** A compacted session sends the summary as a user message, then the preserved tail, then everything newer (`internal/agent/agent.go:3593-3640`). The text is the summary alone. The compaction prompt knows how to describe a previous summary (`agent.go:4533-4538`), but the working model is never told.
- **The environment snapshot is made once.** It is created only when the history has no user text (`agent.go:1331-1350`, `:3346-3349`). After a compaction it lies before the cut, so it leaves the context, and the summary, sent as user text, prevents a new one.
- **The todo list survives only through the summarizer.** The compaction prompt lists the items and asks the summarizer to copy them (`agent.go:4546-4553`).
- **Skills.** A skill's instructions are a `view` or `read` tool result whose metadata carries `resource_type: skill` and the skill name (`internal/agent/tools/view.go:357-367`, `:727-734`). Before the cut that result is summarized away. The skill tracker is created once per coordinator (`internal/agent/coordinator.go:283`) and is only read by `crush_info` and a log line (`tools/crush_info.go:344`, `coordinator.go:605-608`).
- **The file tracker** still counts a file as read after its content was dropped. W9 leaves that to W2.1, as the roadmap says.

All verified.

### Change

1. **Label the summary at replay.** In `getSessionMessages`, the copy of the summary that is sent becomes:

   ```
   <compacted_history>
   The earlier part of this conversation was compacted. This is its summary. The messages after it are kept verbatim.

   <summary text>
   </compacted_history>
   ```

   The stored message is unchanged, so the UI and the stats report are not affected. The text is fixed, so replay stays byte-stable.

2. **Re-attach session state.** At the end of a successful compaction, after the session is saved (`agent.go:2565-2574`), store one hidden user message that opens with `<compaction_state>`. It holds, in this order:
   - the todo list from the session, one line per item with its status;
   - for agents that get an environment snapshot, a fresh one from `prompt.SessionEnvSnapshot`, and none for subagents and teammates, the same rule as `shouldCreateEnvSnapshot`;
   - the paths of files changed in the session, relative to the working directory, most recent first, from `history.Service.ListLatestSessionFiles`;
   - the names of skills whose instructions were summarized away, with one line asking to read them again before relying on them.

   It holds names only, never file or skill contents. Caps: 20 todo items of at most 200 bytes each, 30 paths, 20 skill names, each list followed by `and N more` when cut, and 4,000 bytes for the whole block. With these caps a compaction forced by an overflow cannot refill the window.

   The message is created after the summary, so replay places it after the preserved tail and before the next prompt. That is also where it is true: it describes the state at compaction time.

3. **Find the dropped skills from the log.** A skill counts as dropped when a tool result before the cut carries `resource_type: skill` with its name and no result after the cut does. Skills listed by the previous compaction and not read since are carried forward. To carry them, `CompactionStats` gains `reload_skills` (`internal/message/content.go:259-269`, optional). It is display metadata and never sent.

4. **Wiring.** `SessionAgentOptions` gains an optional `History history.Service`, set in `buildAgentWithModels` (`coordinator.go:1183-1200`). When it is nil, as in most tests, the file list is left out.

5. **Report tag.** `compaction_state` is added to `injectionTags` (`internal/logreport/injections.go:19-24`). The report skips injection rows when it detects continuations (`internal/logreport/turns.go:132`), so a continuation after a compaction is still counted. Without the tag the row would count as a hidden turn.

Left out, compared with the roadmap: tracking loaded skills per session. The tracker affects only what `crush_info` reports. Finding skills from the log is durable across restarts, which an in-memory tracker is not. A per-session tracker can follow as a small fix of `crush_info`.

The compaction prompt keeps asking the summarizer to copy the todo list. Removing that request would change what summaries contain.

### Test

In `internal/agent/compaction_state_test.go` (new), with the scripted model of `turn_injections_test.go:71`:

- A session with two todos, a `view` result of a skill file before the cut, and a changed file in history is compacted. The next request holds the summary inside `<compacted_history>`, followed after the tail by a `<compaction_state>` message with both todos, `Today's date`, the file path and the skill name. Fails today.
- The same skill read again after the cut is not listed.
- A second compaction carries a skill that was not read again.
- 500 todos and 500 changed files give a block of at most 4,000 bytes with `and N more`.
- A subagent's block has no environment snapshot.

In `internal/agent/prompt/injection_tag_test.go`: the block's text maps to the `compaction_state` tag.

### Size and risks

M: about 220 lines of code and 200 of tests. If review needs it smaller, split it in two: first the label, the todo list, the snapshot and the files; then the skills and `reload_skills`.

Risks:

- The label changes the replayed bytes of every session that is already compacted. Those sessions pay one cache miss on their first request after upgrade.
- The block costs up to about 1,000 tokens per compaction. Only 10 of 296 sessions were compacted in the roadmap's window.
- On the Claude Agent provider the CLI owns the loop and its own context. Whether this block reaches the model there was not checked. The roadmap already reports W9 separately for that provider.

### Upstream surface

`agent.go` is shared, but compaction is mostly fork code. New code goes into `compaction_state.go`. The shared files gain a call site each.

## Interactions and order

### Files each item touches

| Item | Files |
|---|---|
| W1.1 | `internal/message/content.go`, `internal/agent/agent.go` (`OnReasoningEnd`, about line 1575), new `internal/message/reasoning_parts_test.go` |
| W2.2 | `internal/agent/tools/multiedit.go`, `multiedit_test.go`, `edit_match_kind_test.go` |
| W2.3 | `internal/agent/tools/write.go`, `error_kind_test.go` |
| W2.4 | `internal/agent/tools/edit.go`, `edit_test.go`, `grep.go`, `grep.md.tpl`, `grep_test.go`, `glob.md.tpl`, `glob_test.go`, `ls.go`, new `ls_test.go`, `view.go`, `view_test.go` |
| W4.2 | `internal/agent/agent_tool.go`, `internal/config/config.go`, `internal/agent/team_cache_test.go`, `agent_preset_test.go` |
| W4.3 | `internal/agent/prompt/prompt.go`, `prompt/snapshot_test.go`, new `prompt/prompt_context_test.go`, `internal/config/load.go` |
| W5.1 | `internal/agent/templates/coder.md.tpl`, `internal/agentteam/team.go`, new `internal/agent/prompt_text_test.go` |
| W9 | `internal/agent/agent.go` (summarize and `getSessionMessages`), new `compaction_state.go` and its test, `coordinator.go`, `internal/message/content.go` (`CompactionStats`), `internal/logreport/injections.go`, `prompt/injection_tag_test.go` |

### Overlaps

- **W1.1 and W9** share `agent.go` and `content.go`. The hunks are far apart (`OnReasoningEnd` against `summarize` and `getSessionMessages`; the reasoning helpers against `CompactionStats`), but both are long files. They are the only pair that touches the same files.
- **W4.2 and W4.3** both touch the `config` package, in different files (`config.go`, `load.go`).
- **W2.2 and W2.4** meet in behavior, not in files: once both land, a `multiedit` failure from several matches shows the count and lines. W2.4's test lives in `edit_test.go`, so `edit_match_kind_test.go` belongs to W2.2 alone.
- **W2.3 and W5.1**: the new policy text names no error string, so their order does not matter.
- **W4.3 and W9**: W9 calls `SessionEnvSnapshot`, which W4.3 fixes for subdirectories. No shared file. With W4.3 first, W9's fresh snapshot is right from the start.
- **W5.1, W2.4 and the cached prefix**: W5.1 changes the system prompt and W2.4 the tool block. Each costs one cache miss per session at upgrade. Landing them in the same release costs one miss, not two.
- **W4.2 and the Team policy**: the write subagent uses `task.md.tpl`, which does not render the policy, so removing its Team tools changes no prompt text.

### Parallel work

These seven groups touch disjoint files and can be built at the same time, in separate worktrees:

1. W1.1, then W9 in the same worktree or after W1.1 merges.
2. W2.2.
3. W2.3.
4. W2.4.
5. W4.2.
6. W4.3.
7. W5.1.

### Merge order

1. W2.3, W2.2, W2.4: the smallest and most local first.
2. W4.2.
3. W5.1, merged in the same release as W2.4 so the cache cost is paid once.
4. W4.3.
5. W1.1.
6. W9, last: it is the largest, shares files with W1.1, and uses W4.3's snapshot.

Each lands with its failing test first, as step 2 of the roadmap asks. Then the baseline window starts.

## Validation

- Each item's failing test, written first and shown failing on `origin/main`.
- `go build ./...`, `go test ./...`, `task lint`.
- `TestTeammateToolPaletteMatchesLead` and `TestTeammateSystemPromptExtendsLead` unchanged and passing after W4.2, W5.1 and W9.
- A request-identity check for W9: the request built from a session without a compaction is unchanged.

## Noticed, not changed here

- `glob` reports `truncated` when ripgrep returns exactly the limit (`glob.go:117`).
- A whitespace-tolerant match that finds several places falls through to "not found" (`edit_whitespace.go:77-78`, `edit.go:209-212`), so the model is told the text is missing when it is ambiguous.
- `ls` shows the depth notice only for a configured depth, not for one the model passed (`ls.go:165`).
- The skill tracker is per coordinator, so `crush_info` reports a skill as loaded in every session once any session loaded it (`coordinator.go:283`).
- The Team policy asks to remove the worktree "only after the branch is pushed and merged" (`team.go:50`), which assumes a push the coder prompt forbids unless asked. W5.1 leaves it, because it is a condition on removal and not an instruction to push.
