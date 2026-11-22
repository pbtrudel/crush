# Background compaction: multi-turn benefit attempt (multiturn-1), inconclusive

Batch `multiturn-1`, 12 runs (3 tasks x 2 arms x 2 repetitions) x 4 prompts on
one session, both arms on `7fa78e63`, route `deepseek-flash-ctx` (200k window
declared), threshold 0.3 and tail 8000 in both arms, margin 0 in control and
0.1 in the background arm, so the limit is 60k tokens and the band in which a
finished turn starts a summary is 40k to 60k. Complete, exit 0, 29 minutes.

`compactions` came back empty for 11 of 12 runs and only ever held
`threshold` for the twelfth, so neither arm adopted a background summary and
**the benefit is not measured**. This note is the diagnosis, not a result.

## The overlay reached the child

Every run's generated config (`runs/<run>/config/crush/crush.json`) carries
`auto_compact_threshold: 0.3`, `compact_tail_tokens: 8000` and the arm's own
`compact_background_margin`, 0.1 in the background arm and 0 in control. The
same files carry the disabled tool list both arms share. The option was not
lost between the experiment file and the child.

## The margin fired exactly as designed

Compaction lines in each run's `data/logs/crush.log`:

| arm | runs | background summaries started | failures |
| --- | --- | --- | --- |
| control (margin 0) | 6 | 0 | - |
| background (margin 0.1) | 6 | 7 | 7 |

Every one of the 7 failed with:

```
WARN Background compaction failed | sql: database is closed
```

So `shouldStartBackgroundCompaction` matched the band (the feature works as
written), and `startBackgroundCompaction` started the job, and then the job
died because the process that started it was already shutting down.

## Why the process was shutting down

`crush run` sends **one** prompt and exits at the end of that turn
(`internal/cmd/run.go`: "Run a single non-interactive prompt"). The driver
feeds four prompts as four `crush run --session <id>` processes. The summary
goroutine is deliberately detached from the turn
(`context.WithoutCancel`, 10-minute bound), so nothing stops it except the
turn returning, after which the run closes the database and exits: the DB
handle closes under the goroutine mid-summary.

Adoption is worse than timing: `adoptBackgroundCompaction` reads
`a.bgCompactions`, an in-memory map on the coordinator. A later
`--continue` process starts with that map empty, so even a summary that had
finished writing could never be adopted across a process boundary. This is
the structural limit stated in the experiment file before the run, now
demonstrated: with `crush run` the option can only ever pay for summaries.

## Per-turn context against the 60k limit

End context is the last request's prompt plus cached tokens (what
`prompt_tokens + completion_tokens` holds, which is the number the limit is
compared against); peak is the highest step inside that turn. Values are
tokens.

**pr-0020 control rep2** (margin 0, pass True)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 225.7 | 42 | 41 | 42722 | 43463 | 0 |
| 2 | 56.0 | 6 | 5 | 43604 | 52945 | 0 |
| 3 | 8.4 | 2 | 1 | 53227 | 53227 | 0 |
| 4 | 2.9 | 1 | 0 | 53367 | 53645 | 0 |

**pr-0020 control rep1** (margin 0, pass False)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 275.2 | 41 | 60 | 29226 | 47190 | 0 |
| 2 | 138.5 | 20 | 19 | 58265 | 58265 | 0 |
| 3 | 40.2 | 2 | 1 | 59208 | 59347 | 0 |
| 4 | 18.4 | 1 | 0 | 59896 | 60537 | 1 |

**pr-0020 background rep1** (margin 0.1, pass True)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 313.9 | 38 | 71 | 56955 | 56955 | 0 |
| 2 | 45.1 | 4 | 4 | 58187 | 61352 | 0 |
| 3 | 16.6 | 3 | 2 | 62429 | 62994 | 1 |
| 4 | 2.5 | 1 | 0 | 25929 | 25977 | 0 |

**pr-0020 background rep2** (margin 0.1, pass False)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 174.2 | 26 | 29 | 45204 | 47259 | 0 |
| 2 | 68.7 | 8 | 7 | 53343 | 58836 | 0 |
| 3 | 14.6 | 3 | 2 | 60645 | 60645 | 1 |
| 4 | 4.8 | 1 | 0 | 24403 | 24436 | 0 |

**pr-0035 control rep1** (margin 0, pass True)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 99.5 | 21 | 21 | 24887 | 31839 | 0 |
| 2 | 35.4 | 5 | 4 | 32065 | 35570 | 0 |
| 3 | 11.1 | 2 | 1 | 36080 | 36080 | 0 |
| 4 | 2.0 | 1 | 0 | 36250 | 36287 | 0 |

**pr-0035 control rep2** (margin 0, pass True)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 141.1 | 37 | 36 | 32932 | 49024 | 0 |
| 2 | 42.8 | 5 | 4 | 50928 | 52715 | 0 |
| 3 | 10.6 | 2 | 1 | 52988 | 52988 | 0 |
| 4 | 1.9 | 1 | 0 | 53194 | 53194 | 0 |

**pr-0035 background rep1** (margin 0.1, pass True)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 107.4 | 22 | 30 | 38495 | 39365 | 0 |
| 2 | 39.5 | 3 | 2 | 43429 | 43429 | 0 |
| 3 | 8.2 | 2 | 1 | 43711 | 43711 | 0 |
| 4 | 3.2 | 1 | 0 | 43892 | 43950 | 0 |

**pr-0035 background rep2** (margin 0.1, pass True)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 123.9 | 24 | 41 | 28889 | 44447 | 0 |
| 2 | 34.6 | 5 | 6 | 52228 | 52228 | 0 |
| 3 | 53.8 | 3 | 2 | 53018 | 53018 | 0 |
| 4 | 2.9 | 1 | 0 | 53236 | 53236 | 0 |

**pr-0076 control rep1** (margin 0, pass False)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 140.2 | 15 | 14 | 39505 | 41693 | 0 |
| 2 | 34.1 | 2 | 1 | 42175 | 44047 | 0 |
| 3 | 13.8 | 2 | 1 | 45036 | 45036 | 0 |
| 4 | 4.1 | 1 | 0 | 45075 | 45075 | 0 |

**pr-0076 control rep2** (margin 0, pass True)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 126.7 | 10 | 17 | 35481 | 46007 | 0 |
| 2 | 67.4 | 4 | 4 | 46505 | 52250 | 0 |
| 3 | 17.2 | 2 | 1 | 52586 | 52586 | 0 |
| 4 | 2.5 | 1 | 0 | 52837 | 52861 | 0 |

**pr-0076 background rep1** (margin 0.1, pass True)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 237.4 | 31 | 35 | 44938 | 52016 | 0 |
| 2 | 79.6 | 5 | 4 | 58727 | 58727 | 0 |
| 3 | 19.0 | 2 | 1 | 59161 | 59161 | 0 |
| 4 | 2.8 | 1 | 0 | 59427 | 59427 | 0 |

**pr-0076 background rep2** (margin 0.1, pass False)

| turn | wall s | steps | tool calls | end context | peak context | summary rows |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 140.6 | 11 | 23 | 15499 | 43619 | 0 |
| 2 | 75.5 | 4 | 3 | 46913 | 56404 | 0 |
| 3 | 14.5 | 2 | 1 | 56550 | 56790 | 0 |
| 4 | 1.8 | 1 | 0 | 56851 | 56851 | 0 |

Reading the tables: turn 1 ends between 15k and 57k, and the follow-up turns
add 0.2k to 29k each. Only 3 of 48 turns ended above 60k, and those are the
three blocking summaries (one control at turn 4, two background at turn 3).
Fourteen turns ended inside the 40k to 60k band, which is where the
background arm did start its summaries, so the band was reachable; nothing
adopted them.

## Side effects worth recording

- A background attempt that dies leaves an unfinished summary row: in
  pr-0020 background rep1 a summary message exists with `finished_at` NULL.
  The option's cost is paid even when nothing is adopted.
- A blocking summary costs 13 to 19 seconds of wall time in these runs
  (summary row `finished_at - created_at`: 13, 14, 19 s), out of turn walls of
  2 to 314 s. That is the ceiling on what adoption could save per compaction,
  which is the only benefit figure this session can support.

## Verdict

Inconclusive, and for a mechanical reason that no threshold or follow-up
length changes: `crush run` ends the process at the turn boundary, so the
pending summary is killed (`sql: database is closed`) and a later process
cannot adopt it because the map it would live in is per-process memory. Pass
rates, 4 of 6 in each arm, are far too small to mean anything either.

## Proposed rerun, not started

Keep the same tasks, arms, repetitions and threshold family, but drive the
four prompts through **one process** so the coordinator that owns the pending
summary survives between prompts:

1. `crush server --host http://127.0.0.1:<port>` in the run's prepared
   checkout, with the same `CRUSH_GLOBAL_CONFIG`/`CRUSH_GLOBAL_DATA` the
   single-run path uses.
2. `POST /v1/workspaces` for the checkout, `POST /v1/workspaces/{id}/sessions`
   for one session, then four `POST /v1/workspaces/{id}/agent` calls with
   `proto.AgentMessage` (202, fire-and-forget), waiting for each turn to end
   by polling the run's database, which is also where the per-turn context and
   first-row timings come from.
3. Stop the server, grade the checkout, read the compaction triggers exactly
   as now.

Threshold 0.2 (limit 40k, band 20k to 40k) instead of 0.3: the tables above
show turn ends clustered at 25k to 55k with 1k to 20k of growth per follow-up,
so a 40k limit puts turn 1 or 2 in the band and turn 3 over the limit in most
runs, where a 60k limit produced three crossings in twelve runs. Follow-up 2
would also ask for a test to be added, to grow the context faster.

Cost: 12 runs x 4 turns, about 40 to 60 minutes of wall clock at concurrency
2, 0 USD priced (commandcode/deepseek-flash). Development: a `server`
transport in `internal/eval/multiturn` (start, four prompts, poll, stop),
roughly 250 to 350 lines plus a unit test of the client against a stub HTTP
server, since the fake child binary cannot serve the API; one 3-run smoke
before the batch. Nothing starts until that is approved.
