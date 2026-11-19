# Model Intelligence Roadmap

This is a roadmap, not an implementation plan. It ranks candidate changes and records the evidence behind each. No code has been changed. Each workstream needs its own design in `docs/superpowers/specs/` and its own implementation plan before code is written.

## Goal

Make the same model finish more tasks correctly inside Crush, in fewer steps, with fewer false claims of completion.

A harness does not make a model smarter. It decides what the model sees, which actions it has, what it is told when an action fails, and whether anything verifies the result. Each item below is judged by its effect on those four things, and an improvement counts only once it is measured.

## Summary

1. Nothing measures task success today, and the session log cannot tell one build from another. Fix that first (W0).
2. Some things are plainly wrong and cheap to correct: reasoning data dropped before it is stored, tool results that hide why they failed, subagents handed the Team tools, prompt lines that name missing tools or contradict each other (W1.1, W2.2 to W2.4, W4.2, W5.1).
3. Nothing checks the work when a turn ends, and hooks cannot send the agent back. Add a gate at the end of the turn (W3).
4. Two defaults of this fork rest on untested assumptions: delegation is mandatory (W8) and files are read in small pages (W6). Both are experiments.
5. A relaxed edit guard, project rules for subagents, a shorter prompt and per-family variants are plausible gains with real risks. They are behavior changes or experiments, accepted on measurement (W2.1, W4.1, W5.2, W5.3, W7).
6. Seven model families are in scope: DeepSeek, GLM, Kimi, GPT, Claude, MiMo and Qwen. GPT models on the Responses API lose their reasoning between steps today. That is to be fixed inside Crush, pending a live check per provider, with a fork of the provider library as the fallback (W1.2). Qwen and MiMo each bring a vendor rule about earlier reasoning (W1.3).

## Evidence

Gathered on 2026-11-17 from three sources.

1. **Code** at `c71dec5e` (`origin/main`): prompts, turn loop, context handling, tools, provider options, delegation. The provider layer `charm.land/fantasy` v0.45.5 was read from the module cache; its files are cited as `fantasy/...`.
2. **The session log of this workspace**, 2026-10-21 to 2026-11-17: 296 sessions, 17,365 assistant messages, 24,240 tool calls. DeepSeek V4.1 Flash, served by five providers, wrote 89% of the assistant messages; the rest is Kimi K3.1 Pro, Claude through the Claude Agent provider, and GLM 5.3. No OpenAI, Gemini or Qwen model appears, and MiMo wrote two messages. Only counts, hashes and Crush's own error strings were extracted, never prompts, answers or file contents.
3. **Published results** where the model was fixed and only the harness changed, plus vendor documentation. Only pages opened and checked for this document are cited; see [Sources](#sources).

Two limits on the session numbers. They describe one workspace and mostly one model family, so they rank problems for this usage and not for Crush in general. And the harness changed during the window, while the log does not record which build wrote a message. One example of why that matters: from October 29 to November 7 nearly every shell command produced a background-job notice (5,272 notices) and the model answered most of them with `job_kill` (2,757 calls, 11% of all tool calls in the window). Commit `997003c2` of November 6 fixed it. Figures are therefore given for the whole window and, where it changes the picture, for the period since November 9 (78 sessions, 143 user prompts, 7,928 tool calls).

The statistics came from one-off scripts that open the session database read-only. W0.2 replaces them with a command.

### What the log shows

| Observation | Whole window | Since Nov 9 |
|---|---|---|
| Tool calls that returned an error result | 633 of 24,240 (2.6%) | 77 of 7,928 (1.0%) |
| Shell commands that exited non-zero (reported as text, so not counted in the row above) | 298 of 11,641 (2.6%) | not computed |
| `edit` and `multiedit` calls that failed | 185 of 1,845 (10.0%) | 33 of 333 (9.9%) |
| of those, refused by the read guard (file not read through `view`, or changed since) | 155 (84%) | 29 (88%) |
| of those, text not found | 16 (9%) | 1 |
| guard refusals in top-level sessions / teammates / subagents | 123 / 26 / 6 | not computed |
| `write` errors that are the same guard with a misleading message | 13 of 21 | 3 of 4 |
| `view` and `read` calls | 3,413 | 1,693 |
| share that pass an explicit limit | 75% | 74% |
| median lines requested when a limit is given | 120 | 120 |
| share that touch a file already read in the session, mostly to page further | 43% | not computed |
| Calls to any of the eight language-server tools | 26 | 14 |
| Responses cut off by the output limit | 0 | 0 |
| Turns that ended on an empty response | 1 | |
| Calls to a tool that does not exist / with arguments that failed to parse | 2 / 3 | |
| Sessions that were compacted | 10 of 296 | |
| Todo reminders injected | 297 in 29 sessions | |

Who does the work, by stored message count: 51 top-level sessions hold about 26,400 messages, 43 teammate sessions about 13,000, 59 subagent sessions started by `agent` or `agent_dispatch` about 6,000 (14 of them with write access), and 143 `agentic_fetch` sessions about 2,600.

### Already in place

Listed so that nobody rebuilds them.

- Reasoning text is sent back to the provider, between tool calls and across turns, on OpenAI-compatible endpoints, and Anthropic signed thinking is replayed: `internal/message/content.go:854-872`, `fantasy/providers/openaicompat/language_model_hooks.go:481-490`. Whether each model family then uses it is W1.3.
- Edits tolerate whitespace differences and explain a miss with the nearest text (`internal/agent/tools/edit_whitespace.go`). Text mismatch is 9% of edit failures.
- Tool output is bounded with a spill file, diagnostics ride on tool results, and the prompt prefix is cache-stable (`docs/superpowers/specs/2026-11-14-token-efficiency-design.md`).
- Malformed tool-call JSON is repaired by fantasy before validation.
- Repeated-call and repeated-text detection exist. They fired 6 times and never.

### Checked and dropped

| Idea | Why it is not in the roadmap |
|---|---|
| Continue automatically after an output-limit cutoff | No occurrence in 17,365 assistant messages |
| Retry an empty response | One occurrence |
| Run tool calls that arrive with a finish reason other than tool calls, such as `stop` (when streaming, `fantasy/agent.go:1705` dispatches only on the tool-calls reason) | One candidate occurrence; watch it in the W0 report |
| Return the edited region after each edit | Only 7% of successful edits are followed by a read of the same file |
| Filter the skills list per turn | Changing the list in the system prompt rewrites the cached prefix, and nothing measured suggests skills are being missed |
| A reviewer agent after every turn | It costs a second model run per turn and nothing found supports it. The one ablation located [7] is too small to decide: its verifier stage made a net difference of 1 task in 125 and 3 in 36. W3 checks in the working context first |
| Set sampling parameters per model | Nothing is sent unless configured, so provider defaults apply; no evidence of a problem |

## Workstreams

Corrections first, then the task suite, then behavior changes and experiments measured on it. Sizes: S under 150 changed lines, M up to 400, L several pull requests.

| | Workstream | Kind | Main evidence | Size |
|---|---|---|---|---|
| W0 | Measurement | prerequisite | nothing scores task success | S, M, M, L |
| W1 | Reasoning state the harness drops | fix, behavior change | code and vendor documentation; little or no local traffic on the affected models | S, M to L, S per family |
| W2 | Edit and search feedback | behavior change, fixes | 10% of edits fail, 84% of those on the read guard; 1% of all steps | M, S, S, S |
| W3 | Turn-end gate and reminders | new capability | code; one published bundle of harness changes; one session's reminders | M to L, S, S |
| W4 | Project instructions for every agent | behavior change, fixes | 59 subagent sessions exposed; harm not measured | S, S, S |
| W5 | System prompt | fix, then experiment | five wrong or contradictory statements; vendor guidance | S, L, M |
| W6 | Reading files | experiment | 75% of reads ask for a slice, median 120 lines | M |
| W7 | Tools matched to the model | experiment | 43 tools sent, 26 language-server calls; one published report and vendor documentation | M |
| W8 | Delegation by default | experiment | policy text; teammates hold 27% of messages; outcome never measured | S |
| W9 | State after compaction | fix | code; 10 sessions affected | M |
| W10 | Backlog | | | |

### W0. Measurement

Nothing in the repository scores whether a task was completed: no task suite, no benchmark adapter, no CI job beyond unit tests, UI golden files and recorded provider streams. `crush run` cannot serve as a runner yet: its flags (`internal/cmd/run.go:167-176`) offer no structured output, step cap or time limit, and every error exits 1 (`internal/cmd/root.go:243-251`), so a provider outage and a failed task look the same. The session log lacks the build that wrote each message, the trigger of a compaction and the reason a turn ended, which is why the table above needed a manual split by date.

Items 1 to 3 are designed in `docs/superpowers/specs/2026-11-17-measurement-design.md`. That design widens item 1 from three stamps to five and adds a wait for delegated work to item 3.

1. **Stamp the log** (S). Record the Crush version and commit on each session and in each assistant message's finish record, the compaction trigger, and why the turn ended (model stopped, repeat guard, repetition guard, context guard, error, cancel).
2. **Report from the log** (M). Extend `crush stats` with, per tool, model and build: calls, error results by class, shell exit codes, runs of consecutive failed edits, guard refusals and whether the retry used the same text, share of reads that page, steps per prompt, share of work done in child sessions, and the volume of injected notices and reminders. Counts only. This makes every later change checkable against real use and would have shown the notice flood on its first day.
3. **Make `crush run` scriptable** (M). `--output json` (final text, session id, steps, tool calls, token usage, finish reason), `--max-steps`, `--timeout`, and exit codes that separate a harness or provider failure from a run that ended normally.
4. **Task suite** (L). 20 to 30 tasks cut from this repository's merged pull requests that carry tests. The prompt is the problem the pull request states (its Why section or the issue, never its How section), the checkout is its parent commit, the grade is its tests. Tasks whose tests depend on names the pull request introduced are dropped, or the prompt states the interface. Each run happens in a throwaway worktree, three runs per variant. DeepSeek V4.1 Flash runs on every change because it is cheap and dominant here. A change aimed at one family (W1.2, W1.3, W7.1) runs on that family first, with DeepSeek as the guard. One model each of GLM, Kimi, GPT, Claude, MiMo and Qwen runs before a change is accepted for its family (Decision 1). At 25 tasks the full matrix is seven models and three runs: 525 runs per variant and 1,050 per comparison, so it runs at acceptance only. Families that receive the same variant are pooled; a decision for one family alone needs more tasks or runs for that family, stated before the run. Each family enters the matrix with a named route and its usage limits: Decision 1 names families, not routes, and subscription routes are capped (the log already holds a "You've hit your 5-hour limit" error). The GPT model runs on Copilot, the only provider configured here whose catalog types GPT models as `responses`, once a captured request confirms the API in use (W1.2). Claude runs through the Claude Agent provider, where the CLI owns the turn loop, so W3 and W9 are reported separately for it. Known bias: merged pull requests are tasks the current harness already solved with a person steering. The suite stays on this repository's pull requests; no public benchmark is planned (Decision 3).

How results are judged. Variants are compared on the same tasks, run for run. The metric a change targets is the primary result. Pass rate is a guard with a margin, not a strict "no drop". With 25 tasks and three runs, one model's pass rate carries a standard error near 6 points and an unpaired difference between two variants near 8, so for a single model a difference under roughly 16 points is noise. Pairing narrows that by an amount that depends on how consistently a task passes or fails, which the baseline shows. Each experiment therefore states, before it runs and from the baseline's variation, the margin it accepts and the smallest effect it can detect; smaller effects need more tasks, more runs or pooled models. A false completion is a run that ended on its own, not by step cap, time limit or error, while its grade fails; it is counted separately.

A baseline window is collected once item 1 and the corrections of step 2 have landed, before any behavior change, and no two changes to the same code path share a measurement window.

### W1. Reasoning state the harness drops

1. **Stored reasoning loses its provider data** (S, certain). `OnReasoningEnd` stores the Anthropic signature, the Gemini thought signature and the OpenAI Responses reasoning data, then calls `FinishThinking` (`internal/agent/agent.go:1528-1546`). On the first reasoning block of a message, `FinishThinking` rebuilds the part from four fields and discards `ThoughtSignature`, `ToolID` and `ResponsesData` (`internal/message/content.go:583-597`). `AppendReasoningContent`, `AppendReasoningSignature` and `SetReasoningResponsesData` rebuild the part the same way (`content.go:516-581`). For that block only the Anthropic signature reaches the log. Inside a turn fantasy still holds the data, so nothing fails; from the next turn on, Gemini receives its history without signatures, which Google says "will degrade the model's reasoning and answer quality" [4]. Fix: each helper copies the part and changes one field, with a test that a part carrying every payload survives every helper. Related, in the same pull request or the next: Anthropic redacted thinking has no field to be stored in, and an assistant message whose only content is a signature is skipped on replay because the check reads the text instead of `HasReplayData` (`agent.go:3324`).
2. **OpenAI Responses models reason from scratch at every step** (M to L, behavior change, made inside Crush). For reasoning models typed `responses` (the stock `openai` provider, and the Copilot and OpenCode gateway models of that type) Crush requests `reasoning.encrypted_content` (`internal/agent/coordinator.go:825-829`). fantasy then skips every reasoning item when it builds the next request, also between steps of one turn (`fantasy/providers/openai/responses_language_model.go:596-603`), and sends `store: false` (`:194-198`). OpenAI documents the opposite: "any reasoning items returned in model responses with tool calls must also be passed back with tool call outputs" [17], and for stateless use "Preserve and replay every returned reasoning item" [3]. It reports Tau-Bench Retail rising from 73.9% to 78.2% from passing reasoning back [1] and lists "Retain reasoning" among three settings it recommends to API users [2].

   What the check for Decision 2 found:
   - No release fixes it. The skip is unchanged in fantasy v0.48.1, the newest.
   - Server-side chaining is not a way around it. `Store` and `PreviousResponseID` exist as options, but fantasy rejects a chained request whose prompt holds an assistant or tool message (`responses_language_model.go:385-395`), and the next request of a tool loop always holds the tool result.
   - fantasy keeps the first value it sees. In a stream it takes `encrypted_content` from the event that opens a reasoning item and reuses it when the item closes (`responses_language_model.go:1079-1090`, `:1166-1179`). In fantasy's own recorded OpenAI and Azure streams (`fantasy/providertests/testdata`), all six reasoning items that carry encrypted content have one value on the opening event (908 to 952 characters) and a different, longer one on the closing event (1,572 to 4,068). GitHub's Copilot client uses the closing one [19]; whether the opening one is usable is untested. Crush stores what fantasy hands it, so `ResponsesData` holds the opening value, and a fork would have to change the stream parser as well as the skip.
   - No hook reaches the request. Of fantasy's model hooks only the header hook runs for Responses models (`fantasy/providers/openai/openai.go:191-201`), and a step cannot change provider options (`fantasy/agent.go:996`).
   - The HTTP client belongs to Crush, which already rewrites provider traffic in transports of its own: one edits a Responses request body (`internal/oauth/openai/transport.go:66-95`), one rewrites request bodies and the response stream (`internal/agent/mistral_content_block.go:14-35`), one adds body fields (`internal/agent/mistral_vibe.go:39-67`).

   Change, in this repository: a transport on every provider that speaks the Responses API (`coordinator.go:1644-1680`, `:1740-1754`, `:1798-1806`). From each response it records every closed reasoning item and the output items that follow it. Into each later request it puts each item back where it stood in the original output, ahead of the first item that followed it; a function call is recognised by its `call_id`. It stands down when the body already holds a reasoning item, which is what a later fantasy that replays them would send. It gives an item back only to the provider endpoint and model that produced it. On a rejection it retries once without the inserted items and stops inserting for that session. fantasy stays a version bump.

   What this route does not give for free is reasoning carried across turns, and some models use it: "The GPT-5.6 model family supports `all_turns` and uses it by default. Earlier models default to `current_turn`" [16]. A fork would get it from fantasy, which already receives every reasoning part in order (`internal/message/content.go:848-872`). The transport needs two more things. It has to hand each closing value, by item id, to the code that stores the message, because item 1 alone preserves the opening value. And the reasoning behind a final answer needs an anchor other than a `call_id`. Without both, the fix covers the steps of one turn only. A fork pinned with `replace` is the fallback if a live check fails, or if replay across turns cannot be anchored reliably for the models that use it.

   Not yet verified: that each endpoint accepts the replay. GitHub's client builds the same request, with the item's `id` and `store: false` [19]. That shows the shape the Copilot endpoint is built against, not that the endpoint accepts it every time, because the same client can chain with `previous_response_id` instead. fantasy's comment records the opposite experience: with `store` off "the item IDs are ephemeral and referencing them causes 'Item not found' errors" (`responses_language_model.go:596-603`). A live check per provider is therefore the first step. It covers an item sent with and without its `id`, a reasoning item followed by text and then a call, and several calls after one reasoning item.

   Which API a GPT model uses is also read from a captured request, not assumed. A model listed in the user's provider configuration replaces its catalog entry, type included, and nothing restores the type (`internal/config/load.go:516-543`). On Copilot and the OpenCode gateways the provider's own type is chat completions, so a GPT model listed that way leaves the Responses API. Keeping the catalog type when a listed model omits it is a small correction that belongs with this item.

   Measured on the suite with a GPT model on a Responses route. No session in the window used one.
3. **Vendor rules for keeping earlier reasoning** (S per family, after one captured request per host). Sending reasoning back is not enough for every family, and for one it is mandatory.
   - GLM discards earlier thinking on its standard endpoint unless the request sets `clear_thinking: false` [11].
   - Kimi ignores earlier turns' thinking unless `thinking.keep` is `all`, which is not the default for every model [12].
   - Qwen reads it only when a flag is on: "`preserve_thinking` controls whether the Model reads historical assistant message `reasoning_content`" [14]. The flag is off by default except on `qwen4-max`, `qwen3.8-max` and `qwen3.8-flash`, and the reasoning it adds is billed as input [15].
   - MiMo requires it. With thinking on and tool calls in the history, the assistant messages sent back "must completely pass back the `reasoning_content` field, otherwise the API will return a 400 error" [13]. DeepSeek states the same rule for requests that carry tools [20], and fantasy's comment says it of Kimi (`fantasy/providers/openaicompat/language_model_hooks.go:481-485`). fantasy sends the field whenever the message has a reasoning part, which meets the rule; on a MiMo host that is unconfirmed, with two messages in the log. The option that drops plain reasoning text from older history removes what these families require (`internal/agent/history_slim.go:218-236`, `internal/config/config.go:490-492`, off by default), so it has to exempt them.
   - Copilot's chat-completions models are a separate case. GitHub's client carries their reasoning in the `reasoning_opaque` and `reasoning_text` fields of the assistant message [19]. Neither fantasy nor Crush contains the name `reasoning_opaque`, so for reasoning models on Copilot that are not typed `responses` Crush would neither read that reasoning nor send it back. Confirm on a captured response before building anything.

   Crush sets none of these flags, and a user cannot add them per model, because the OpenAI-compatible branch replaces `extra_body` with its own map (`internal/agent/coordinator.go:1023`). Kimi and GLM wrote about 8% of the assistant messages, MiMo two messages and Qwen none, all through third-party hosts whose handling of these flags is unknown. First capture one request per host and read the documentation of each family in use; then set the flag per family and stop replacing the user's `extra_body`.

### W2. Edit and search feedback

One in ten `edit` and `multiedit` calls fails, and matching is rarely the reason. The scale is modest: 185 failed edits against 17,365 model steps, about 1%. W2 removes friction; it is not a major lever for task success.

1. **The read guard** (M, behavior change). `edit` and `multiedit` refuse a file that was not read through `view` or `read` in the same session, or whose modification time moved since (`internal/agent/tools/edit.go:283-296`). The guard accounts for 155 of the 185 failed edits, and 123 of those happened in top-level sessions, so it is not an effect of teammates starting without read records. What the log says about them:
   - In all 49 "changed since read" cases the session had run a shell command since it last read or edited the file, which fits a formatter or another command the model ran itself. The log cannot prove who changed the file.
   - After the refusal, 94 of the 155 edits (61%) were sent again with the identical `old_string` and applied by exact match. Another 31 succeeded with a different `old_string`, so for those the forced read changed the edit.
   - Why a model edits a file it has not read through `view` is not known. Reading it through the shell and taking the text from a brief are candidates; the log holds no content to tell.

   Proposal: when the guard would refuse and each `old_string` matches the current content exactly and exactly once, apply the edit, say in the result that the file had not been read or had changed, and do not mark the file as read (today a successful edit does, `edit.go:261`). Keep refusing when the match would need the whitespace-tolerant fallback or `replace_all`. `write` keeps its guard because it replaces the whole file. Predicted from the retries above: the failure rate falls from 10% to about 5% (91 failures in about 1,750 calls, since the 94 retries disappear).

   Risk: the guard is the only protection teammates have against editing a file another member just changed. The Team policy promises an `FS_STALE_VERSION` error (`internal/agentteam/team.go:44`) that no code returns. An exact match keeps the edit itself correct, but what the model assumes about the rest of the file may be stale. The 31 edits that succeeded with a different `old_string` are the other exposure: where the first text did match, the tool would have applied an edit the model revised once it had read the file. The design has to choose between applying with a notice and refusing with the changed region shown, and may choose differently for sessions that have teammates. Regression signal for the W0 report: an edit applied without a read, followed within a few steps by another edit of the same lines or by a revert.
2. **`multiedit` hides which edits failed** (S). The result says `Applied 2 of 5 edits ... (3 edit(s) failed)` and keeps the reasons in metadata the model never receives (`internal/agent/tools/multiedit.go:51`, `:263`, `:323`). Put each failed edit's index and reason, with the nearest-text hint, in the result text.
3. **`write` blames a change that did not happen** (S). For a file never read it answers `has been modified since it was last read ... Last read: 0001-01-01T00:00:00Z` (`internal/agent/tools/write.go:75-80`). That is 13 of 21 `write` errors. Say that the file must be read first.
4. **Small texts** (S, one pull request). `edit` with several matches should give their count and line numbers (`edit.go:205`). `grep` is described as returning file paths but returns matching lines with line numbers, answers `No files found` when nothing matches, and reports `Found N matches` after the cap is applied (`grep.md.tpl:1`, `grep.go:149-182`). `glob` is described as sorted by modification time but sorts by path length on its ripgrep path (`glob.md.tpl:1`, `glob.go:182-184`). `ls` drops its truncation notice whenever a depth limit is set (`ls.go:162-167`). `view` rejects an oversized section without saying that a smaller `limit` would work (`view.go:336-337`).

### W3. Turn-end gate and reminders

When the model stops calling tools the turn ends, and nothing looks at the work (`internal/agent/agent.go:1976-2008`). A `TurnEnd` hook runs, but its result is thrown away (`internal/agent/turn_hooks.go:25-31`), so no hook can send the agent back. The one built-in check, the stale todo list, appends a note to the model's own message instead of asking it (`internal/agent/todo_reminder.go:152-158`). Projects compensate in prose: the user-level instructions on this machine carry a whole section on not claiming success without evidence. The Claude Code documentation gives the rule for that case: "If it's a rule that can't bend, turn it into a hook" [9].

Published support is one data point. An effort that took one model from 52.8% to 66.5% on Terminal-Bench 2.0 by changing only the harness used a check that "intercepts the agent before it exits and reminds it to run a verification pass" [5]. That gain came from a bundle of prompt, tool and middleware changes with no split per part, so it shows the idea is worth testing and nothing more.

1. **A gate at the end of the turn** (M to L). One internal continuation point that built-in checks and `TurnEnd` hooks both use: a check may return a reason, the reason is delivered as a labelled harness message, and the loop resumes. Bounded per turn (default one continuation, configurable). Design points the code forces:
   - Today the hook fires after the reply is sent and the request is released (`agent.go:1994-2008`). The gate has to run before both, so a slow check (a test run) keeps the session busy; a queued user prompt should take precedence over a continuation.
   - Teammate and subagent turns skip turn hooks (`internal/agent/coordinator.go:2417`, `turn_hooks.go:26`), so the gate covers the Lead's turns only. With delegation mandatory (W8), much of the work never passes it until that is decided.
   - On the Claude Agent provider the CLI owns the loop, and a continuation is one more steered message.
2. **Built-in check, off by default** (S, experiment). When a turn that changed files is about to end, one extra step asks the model to state what was asked, what it verified and with which command, and what remains unverified. When measuring it, count claims whose command does not appear among the turn's tool calls, since the question invites invented verification. It becomes a default, which Decision 4 allows, only if the suite shows fewer false completions without a pass-rate drop beyond the stated margin.
3. **Quieter todo reminders** (S). The mid-turn reminder fires after any step that ran `bash`, an edit tool, a delegation tool or `wait_agent` without touching the list (`todo_reminder.go:27-43`), up to six times per turn, and after two unanswered reminders about the same item it tells the model to "either mark it completed with the `todos` tool, or say plainly in your answer that it is unfinished" (`:125-131`). A read-only shell command and a wait for a teammate both count as progress, which follows from the code alone. One observation, from the session that produced this document: 12 reminders in 66 steps, 8 of them escalated, while the list was accurate. First narrow the trigger to steps that can finish a task, and drop the wording that offers "mark it completed" as the first way out. Once item 1 exists, move the stale-list check there and ask once. On the Claude Agent provider a reminder is steered into the running turn (`internal/claudeagent/session.go:435-436`, `internal/claudeagent/transport.go:565-566`) and the CLI wraps it in text saying the user sent it, also observed in that session; attach it to the next tool result instead.

### W4. Project instructions for every agent

1. **Subagents work without the project's rules** (S, behavior change). A subagent started by `agent` or `agent_dispatch` gets `task.md.tpl`, 1,328 bytes, built with two options, the working directory and a write flag (`internal/agent/agent_tool.go:127-131`); context files are rendered only by the coder template (`internal/agent/templates/coder.md.tpl:431-457`). In the window that was 59 sessions and about 6,000 messages, 14 of them with write access, working without the build, test, format and style rules in `AGENTS.md`. Teammates are not affected: they reuse the Lead's prompt. This is exposure, not measured harm. Change: render the project context chain, under the existing byte budget, in the task prompt, with one line saying that rules about commits, pull requests and delegation address the parent agent. The global chain stays out: on this machine it holds commit, pull-request and delegation procedures written for a top-level agent. Note that the subagent prompt is built against the main checkout (`agent_tool.go:129`), not the task worktree. Verify with a test on the built prompt; once the suite exists, add a configuration that routes the work through a write subagent.
2. **Write-access subagents receive the Team tools** (S). Their tool list is a copy of the coder's (`agent_tool.go:43-50`), the role filter removes only `question`, `agent` and the dispatch tools (`internal/agent/coordinator.go:1226-1238`), and the Team tools are appended for every agent (`:1276-1278`). The definitions are therefore sent to a write-access subagent, which according to the audit can use them under the Lead's identity. Confirm with a test, then strip them for the subagent role.
3. **Starting in a subdirectory loses the root instructions** (S). Context files are resolved against the working directory only (`internal/agent/prompt/prompt.go:166-168`), and "Is directory a git repo" checks `<dir>/.git` (`:460-463`). Walk up to the repository root for both. How often sessions start in a subdirectory was not measured.

### W5. System prompt

`coder.md.tpl` is 24,445 bytes. Its first 394 lines, 21,683 bytes, contain no template action and are sent with every request. It is the same text for every model: `Provider` and `Model` are passed to the template and no template uses them (`prompt.go:39-40`, `:421-423`).

1. **Corrections** (S, no measurement needed; each statement is wrong today).
   - Line 94 names `find_references`; the tool is `lsp_references`. Line 331 lists `web_fetch`, which only the `agentic_fetch` subagent has.
   - Line 214 says "The Edit tool is extremely literal. \"Close enough\" will fail", which stopped being true with the whitespace-tolerant fallback.
   - Line 72 says "Read entire file before editing it"; line 20 says "Avoid reading entire files". Change line 72 to the wording of line 6, "the relevant context". Line 20 itself stays until W6 measures what removing it does.
   - Lines 11 and 16 forbid committing and pushing unless asked; line 108 says "Commit and push the branch from the worktree, then open a PR and get it merged". Make line 108 conditional on the user having asked.
   - The Team policy, rendered into the same prompt, tells the model what to do when a file operation "returns FS_STALE_VERSION" (`internal/agentteam/team.go:44`). No code returns that error.
2. **Shorter prompt** (L, experiment). One statement per rule. The mechanics of exact matching take about 4 KB in three places (lines 161-211, 213-240, 281-287) and belong in the tool description and the error texts, next to the action they govern. Rules that pull against each other get one wording: answers capped at four lines (lines 9, 26, 91, 369) against formatted, longer answers (lines 34, 374-380), and "don't ask" (lines 7, 97) against the list of when to ask (lines 133-137), where the `question` tool is never named. Emphasis is kept for the few rules that must hold, and those move into harness checks where one exists. Target: fixed text under 10 KB. Reasons: piling on emphasis "mostly makes Claude over-apply the rule" [9], and contradictory instructions make a model spend "reasoning tokens searching for a way to reconcile the contradictions" [1]. Risk: DeepSeek V4.1 Flash writes 89% of the messages here and may rely on the explicit style, so the short prompt is accepted per model family, on the suite, and the current text stays wherever it wins.
3. **Family overlays** (M, experiment). A small block appended per model family. First candidate: let models that run without reasoning write a short plan before acting, since lines 62 and 249 tell every model to plan silently. Full per-model profiles, of which a prompt block is one part next to tools and middleware, were worth 10 to 20 points on a tool-use benchmark subset for two models [6].

### W6. Reading files

Experiment. Models read in small pages because they are told to. Rule 15 of the prompt says "Avoid reading entire files" (line 20); 75% of reads pass an explicit limit, and the median request is 120 lines, below the tool's default of 200 (`internal/agent/tools/view.go:80`). 43% of reads touch a file already read in the session, mostly to page further: of the 843 reads that target the same file as the previous read, 839 carry an explicit offset or limit. Every page is a model round trip.

The remedy is not obvious, because a token that enters the context is re-read on every later step (about 100 times, by the token-efficiency measurements). Candidates, compared on the suite by steps per task and total input tokens:

1. Remove rule 15 from the prompt and measure what models do without it.
2. When a file is below a size threshold (for example 600 lines or 24 KB), return it whole even if a smaller range was asked, and say so.
3. Above the threshold, put a symbol outline with line ranges ahead of the first page when a language server is available, so the model jumps instead of paging. The standalone `lsp_symbols` tool was called 18 times and failed 5 times, 3 of them because no language server handled the file, so its low use may reflect availability.
4. Optional context lines in `grep`.

### W7. Tools matched to the model

Experiment. A Team Lead's session sends 43 built-in tool definitions (counted in the session that produced this document); the number varies with role and configuration but not with the model. The eight language-server tools were called 26 times in 24,240 calls, and availability may explain part of that (see W6). The prompt forbids `apply_patch` twice (lines 18 and 169). No such call appears in this log, which holds no GPT traffic, so the rule was presumably written for GPT models. Cursor reports that OpenAI's models are trained on a patch format and Anthropic's on string replacement, and that the unfamiliar tool "costs extra reasoning tokens and produces more mistakes" [8].

Candidates:

1. An `apply_patch` tool for GPT models in place of `edit` and `multiedit`. OpenAI's guidance is direct: "we highly recommend using `apply_patch` for file edits to match the training distribution" [1]; in that guide the patch travels through an ordinary shell tool call. Its API also has a built-in tool of that name, enabled with `tools=[{"type": "apply_patch"}]`, for which "you don't provide an input schema; the model knows how to construct `operation` objects", and the harness applies the diff [18]. fantasy has no built-in tool type for it, so the variant to test is a function tool with the same name and patch format. Whether that keeps the benefit is what the experiment measures. GPT is in scope (Decision 1), so this runs once the suite has a GPT model.
2. A smaller palette for models that do not use the language-server navigation tools, keeping diagnostics in tool results.

### W8. Delegation by default

Experiment. Agent Teams is on unless configured off (`internal/config/config.go:739-747`), and its policy, rendered into the Lead's prompt, says "Requests with two or more steps, multiple files, investigation, or tests must use the Team" and "When in doubt, delegate" (`internal/agentteam/team.go:38`). The comment beside the default says something else: "the Lead decides whether to work alone or dispatch teammates". In the window teammates hold about 13,000 of 48,000 stored messages.

Whether mandatory delegation raises or lowers task success for the models used here has never been measured, and the published evidence is cautious: Anthropic reports that multi-agent systems "use about 15× more tokens than chats" and that "most coding tasks involve fewer truly parallelizable tasks than research" [10]. Other items depend on the answer: the turn-end gate covers only the Lead's turns (W3.1), and the edit guard is the only protection between teammates (W2.1).

Run the suite with the current policy and with a policy that leaves the choice to the Lead, and compare pass rate, steps and tokens (S for the variant itself). The policy changes only if the variant improves on it: a higher pass rate, or the same pass rate at a lower cost in tokens and steps, beyond the margin stated before the run. If the variant measures the same or worse, mandatory delegation stays (Decision 5).

### W9. State after compaction

Ten of 296 sessions were compacted, so this is rare, and the log cannot confirm a fix. It is a correctness change, verified by tests on the rebuilt message list.

- The summary is sent back as an ordinary user message with no marker (`internal/agent/agent.go:3454-3472`). The model is not told that a compaction happened or that the messages after it are the preserved tail.
- The environment snapshot (date, branch, status) is created once per session and never recreated (`agent.go:3206-3209`).
- The todo list survives only if the summarizer copies it into its text (`agent.go:4406-4413`).
- A skill's instructions are a tool result and are summarized away, while the tracker, created once per coordinator, still reports the skill as loaded (`internal/agent/coordinator.go:270`).
- The file tracker still counts a file as read after a compaction dropped its content, so the edit guard passes on text the model no longer has. W9 leaves the tracker alone; the question belongs to the design of W2.1.

Change (M): label the summary, and after a compaction re-attach from session state the todo list, a fresh environment snapshot, the names of files changed in the session, and the names of skills whose instructions were dropped, with a note to read them again. The block lists names only and is capped, so a compaction forced by an overflow does not refill the window. Track loaded skills per session instead of per coordinator. The harness knows all of this, so none of it should depend on what a summarizer chose to keep.

### W10. Backlog

Real, but small or unmeasured.

- The goal checker is the working model without tools, grading its own prose (`internal/ui/model/automation.go:533-545`). Give it the read-only tools the Fusion judge has (`internal/agent/fusion.go:68-82`).
- Copilot Fusion: the reasoning effort the router picks per step is placed in the model selection (`coordinator.go:1511-1526`) and, according to the audit, never reaches the request; confirm with a captured request first. `has_image` is always false (`internal/agent/fusion_route.go:69`).
- `agentic_fetch`: its `web_search` failed 74 of 110 calls on DuckDuckGo rate limits, and `web_fetch` failed 13 times creating its temporary file (`internal/agent/tools/web_fetch.go:54-57`). Neither was called since November 9. Allow a configured search backend.
- `view`, `grep`, `glob` and `ls` run one after another even when the model sends several in one message, because they are not registered as parallel tools (`fantasy/agent.go:1715-1725`). This costs time, not capability.
- Models call `read` 2,237 times and `view` 1,176; both are listed. One `tool not found: read` shows a tool set that lacks the alias.

## Sequence

1. W0.1, W0.2, W0.3. Small, independent, and they make everything else checkable.
2. Corrections, each with a failing test first: W1.1, W2.2, W2.3, W2.4, W4.2, W4.3, W5.1, W9. The baseline window starts when these have landed.
3. W0.4, the task suite.
4. Behavior changes, one per code path per measurement window, judged on the suite and on the W0.2 report: W2.1, W4.1, W3.1, W3.3, W1.2, W1.3. The captured requests that W1.2 and W1.3 start from need no suite and can be taken earlier.
5. Experiments on the suite: W3.2, W5.2, W5.3, W6, W7, W8.

Each item is one pull request of at most about 400 changed lines, except W0.4, W3.1 and W5.2.

## Constraints

- The prompt prefix must stay byte-stable within a session (`docs/superpowers/specs/2026-11-14-token-efficiency-design.md`). Prompt and tool variants are chosen when the session starts. When the model changes mid-session, by a switch or by a Fusion route, a variant either stays as chosen or is re-picked at the cost of one cache miss; each experiment states which.
- This fork tracks upstream Crush. Changes to `coder.md.tpl` and to tool descriptions widen the merge surface, so overlays and new tools live in their own files.
- `fantasy` is not forked unless the fallback condition of W1.2 is met (Decision 2). Provider-layer fixes live in Crush's own transports and model wrappers, so a `fantasy` upgrade stays a version bump.
- Session statistics stay counts only.

## Decisions

Made on 2026-11-17.

1. **Model families in scope:** DeepSeek, GLM, Kimi, GPT, Claude, MiMo and Qwen. This sets the suite matrix (W0.4), brings W1.2 and W7.1 into the plan, and adds Qwen and MiMo to W1.3.
2. **W1.2 is fixed in this repository.** A fork of `fantasy` is the fallback, used if the transport route fails its live check or cannot carry reasoning across turns reliably. An upstream pull request is not part of the plan.
3. **Task suite:** this repository's pull requests only.
4. **Completion check:** W3.2 may become a default once it measures well.
5. **Delegation policy:** changes only if the W8 variant measures better than the current policy. It stays if the variant is equal or worse.

## Sources

1. OpenAI, "GPT-5 prompting guide". https://developers.openai.com/cookbook/examples/gpt-5/gpt-5_prompting_guide
2. OpenAI, "How enabling two settings tripled our scores on the ARC-AGI-3 benchmark". https://openai.com/index/how-two-settings-tripled-our-arc-agi-3-scores
3. OpenAI, "Migrate to the Responses API". https://developers.openai.com/api/docs/guides/migrate-to-responses
4. Google, "New Gemini API updates for Gemini 3". https://developers.googleblog.com/new-gemini-api-updates-for-gemini-3
5. LangChain, "Improving Deep Agents with harness engineering". https://www.langchain.com/blog/improving-deep-agents-with-harness-engineering
6. LangChain, "Tuning Deep Agents to Work Well with Different Models", 2026-04-29. https://www.langchain.com/blog/tuning-deep-agents-different-models
7. Pan et al., "Natural-Language Agent Harnesses", arXiv 2603.25723. https://arxiv.org/html/2603.25723v1
8. Cursor, "Continually improving our agent harness". https://cursor.com/blog/continually-improving-agent-harness
9. Anthropic, "Best practices for Claude Code". https://code.claude.com/docs/en/best-practices
10. Anthropic, "How we built our multi-agent research system". https://www.anthropic.com/engineering/multi-agent-research-system
11. Z.AI, "Thinking Mode". https://docs.z.ai/guides/capabilities/thinking-mode
12. Moonshot AI, "Thinking Models", Kimi API Platform. https://platform.kimi.ai/docs/guide/use-thinking-models
13. Xiaomi, "Deep Thinking", MiMo API documentation. https://mimo.mi.com/docs/en-US/quick-start/usage-guide/text-generation/deep-thinking
14. Alibaba Cloud, "Use deep thinking models via API", Model Studio. https://www.alibabacloud.com/help/en/model-studio/deep-thinking
15. Alibaba Cloud, "OpenAI compatible - Chat", Model Studio. https://www.alibabacloud.com/help/en/model-studio/qwen-api-via-openai-chat-completions
16. OpenAI, "Reasoning models". https://developers.openai.com/api/docs/guides/reasoning
17. OpenAI, "Function calling". https://developers.openai.com/api/docs/guides/function-calling
18. OpenAI, "Apply Patch". https://developers.openai.com/api/docs/guides/tools-apply-patch
19. Microsoft, `vscode-copilot-chat`, `src/platform/endpoint/node/responsesApi.ts` and `copilotChatEndpoint.ts`, read on 2026-11-17. https://github.com/microsoft/vscode-copilot-chat/blob/main/src/platform/endpoint/node/responsesApi.ts
20. DeepSeek, "Thinking Mode". https://api-docs.deepseek.com/guides/thinking_mode
