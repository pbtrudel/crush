# Background compaction: cost and harm guard (bg-compaction-1)

Batch `bg-compaction-1`, seed 20261124, 48 runs (24 tasks x 1 repetition x 2
variants), complete, exit 0, 70 minutes of wall clock. Both variants run
`7fa78e63` and differ only in the overlay, so the comparison isolates the
option.

## Result

| variant | runs | pass | false completion | median steps | median duration | total tokens |
| --- | --- | --- | --- | --- | --- | --- |
| control (margin 0) | 24 | 22 (92%) | 9% | 20 | 175 s | 18,993,557 |
| background (margin 0.1) | 24 | 21 (88%) | 8% | 20 | 182 s | 24,473,760 |

Paired pass-rate estimate `+0.0000`, 95% `[-0.1304, +0.1304]`, margin 0.20:
the guard passes in point terms, but the batch is **void on contamination**
(one control run, pr-0093, listed the batch directory: 2.1% against a 2%
limit), and at 24 runs an arm it can only exclude a 13-point harm, not a
small one. Tokens rose 29% in the background arm for no measurable pass-rate
change.

## What the option did in this batch

`crush stats report --json` over each run's data directory:

| variant | compactions | by trigger | background attempts | attempts that failed |
| --- | --- | --- | --- | --- |
| control | 18 | `threshold` 18 | 0 | - |
| background | 16 | `threshold` 16 | 7 | 7 |

Every one of the 7 background attempts logged
`Background compaction failed | sql: database is closed`. The reason is
structural, not a bug in the option: a run is one prompt, `crush run` returns
when its turn ends and then closes the database and exits, so the summary
goroutine, which is detached from the turn on purpose, is still in flight when
the process tears down its store. The pending job lives in the coordinator's
memory (`a.bgCompactions`), so it also cannot be handed to a later
`crush run --continue` process.

Consequence: with `crush run` each attempt pays for a summary and no run can
ever adopt one, which is why no `background` or `background_unused` trigger
appears anywhere in the batch.

## Verdict

The guard finds no harm it can resolve (CI +-13 points) and a 29% token cost;
the latency saving this option exists for is not measurable with `crush run`
at all. Per the ruled default, background compaction stays off. The saving
needs two turns in one process, which is what the multi-turn experiment
(`2026-11-21-bg-compaction-multiturn-results.md`) tried and why it is
inconclusive.
