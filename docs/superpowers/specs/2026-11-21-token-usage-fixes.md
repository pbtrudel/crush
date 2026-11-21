# Token usage: what actually costs, and the fixes

Date: 2026-11-21. Status: implemented in this branch, one PR.

## Measured evidence

Numbers below come from two sources: a local run of this binary against a
recording fake OpenAI-compatible server (no provider tokens spent), and the
session databases of two real checkouts.

A single `crush run "say hi"` produces two requests:

| Part of the request | Empty directory | Repo with a 57 KB AGENTS.md |
| --- | --- | --- |
| System message | 28,672 chars | 85,981 chars |
| Tool schemas (37 built-ins) | 33,941 chars | 33,941 chars |
| Whole request body | ~30 KB | 122,574 chars (~30k tokens) |

The first request is the session title: 492 chars of system prompt plus the
title instruction, about 143 tokens, answered with the 4-6 token title. It
fires once per session, including every teammate and subagent session, which
is why a fan-out shows up in a provider log as a burst of identical small
requests in the same second.

The per-request floor is therefore not the conversation. It is the fixed
prefix: context files, the personal global context file, skill descriptions,
and tool schemas. Long sessions then re-send the whole history on top of it,
up to the compaction ceiling (400,000 tokens by default). The two session
databases recorded 16.9 billion cache-read tokens and $261 of accumulated
cost, almost all of it re-sent context rather than new work.

Subagent prompts are the one part that is already cheap: only `coder.md.tpl`
renders context files, so a task prompt is the base prompt plus tools (~14.3K
tokens) and the provider cache key is the working directory, so siblings
already share a cache entry. Their cold portion is whatever differs between
siblings.

## Changes in this branch

1. `feat(agent): skip session titles for child sessions`. Teammate and
   subagent titles are never displayed, so the request is pure waste.
2. `perf(agent): share the prompt prefix across sibling dispatches`. Anything
   task-specific belongs after the shared system prompt and tools, so a wave
   of siblings pays the cold prefix once.
3. Prefix-stable compaction needs no change here: it already landed on main
   in PR #128 (`78c6effe`). `TestCompaction_RequestExtendsTheLastStep` proves
   the summarize request re-sends the last step's system prompt, tools and
   history and appends the instruction last, which is exactly the fix for the
   observed "70,144 cached tokens dropped". Verified again on this base.
4. `feat(tools): add opt-in lazy tool schemas with tool_search`. Off by
   default; when on, requests carry a small core palette and deferred schemas
   are revealed on demand.
5. `docs(agentteam): discourage fan-out waves for small jobs`. Policy change
   only.

Deliberately out of scope: `prompt_cache_key` handling (owned by another
change), per-request usage logging (already covered by the token-efficiency
work on `crush/token-efficiency`), and context-file trimming in downstream
repositories, which is a documentation change there rather than code here.

## Recommended settings

These belong in the user's `crushrc`, not in this repository:

```bash
option context-max-bytes 24576        # default 65536; broader files are omitted first
option auto-compact-max-tokens 150000 # default 400000; the per-turn ceiling
option compact-tail-tokens 16384      # default 24576
option lazy-tools true                # only once this branch is merged
```

`context-max-bytes` omits broader context files rather than trimming them, so
it works as a guardrail only after the long files have been split into a
short core plus on-demand references. Trimming the files is the real saving;
the cap just stops them from growing back silently.
