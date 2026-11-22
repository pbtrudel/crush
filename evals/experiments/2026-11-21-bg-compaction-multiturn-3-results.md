# Background compaction, multi-turn benefit: multiturn-3 results

Batch `multiturn-3`, 2026-11-22 00:43-01:19Z, exit 0, 12/12 runs written, no
run error. This repeats multiturn-2 with the model-pinning fix, which voids
none of the runs this time.

## Setup

- Build: ref `7fa78e63`, agent initialised with `{"interactive": true}`.
- Transport: one `crush server` per run, six prompts posted to one session
  (the task, then the five `FollowUps`). A turn ends once the session is idle
  and every assistant and summary row is finished, on three polls in a row.
  The driver waits for a background summary to finish before the next prompt,
  which stands in for a user's think time. That cost shows as the gap between
  `wall_ms` and `idle_ms`; the user does not see it as latency.
- Arms: `control` (`compact_background_margin` 0) vs `background` (0.1).
  `auto_compact_threshold` 0.25, the same deviation from the approved 0.2 as
  multiturn-2. With the declared 200k window the limit is 50k and the
  background band is 30-50k.
- Tasks pr-0020, pr-0035, pr-0076, 2 reps, concurrency 2, 2 h timeout.
- Route `deepseek-flash-nw-ctx`: Neuralwatt `deepseek-v4.1-flash` (main and
  small). The provider config declares a 200k window, the same one
  `deepseek-flash-ctx` declares. The priced endpoints list this model at 1M,
  which would put the limit at 250k, and no six-turn session here would ever
  compact.
- Driver at `b0319822`: each run gets its own `CRUSH_GLOBAL_DATA`, so the
  shared eval home's `crush.json` can no longer name the model. After turn 1
  the driver reads back the model that answered and stops the run on a
  mismatch.

## Model verification

Every run's store has assistant rows from `neuralwatt / deepseek-v4.1-flash`
only (48-81 rows per run), and each server log names only that
provider/model pair. No run hit the driver's model guard, and `error` is empty
on all 12. The only server-log ERROR, once per run, is `Title generation hit
token limit with small model; trying next`, which does not touch the measured
turns. No background summary failed or was discarded.

A first attempt at 00:42Z exited at once with `evals/tasks: no task files`
and wrote no batch: its queue script had been rewritten on disk while bash
was still reading it, so it skipped the `cd` into the worktree. It made no
model call.

## Per arm

Adopted means a server-log `Context compacted` line with `trigger=background`.
Logreport's `background` count is written summary rows, so it also counts a
summary still pending when the server stopped.

| | control | background |
|---|---|---|
| pass | 3/6 | 2/6 |
| pass by task (pr-0020, pr-0035, pr-0076) | 1/2, 2/2, 0/2 | 0/2, 2/2, 0/2 |
| blocking `threshold` compactions | 11 | 8 |
| background summaries written | 0 | 10 |
| adopted `background` | 0 | 7 |
| `background_unused` | 0 | 0 |
| pending at exit (the session ended before the limit) | 0 | 3 |
| summary calls in all | 11 | 18 |
| tokens, sum (uncached input / cache read / output) | 17.18M (0.56M / 16.44M / 0.18M) | 15.50M (0.71M / 14.62M / 0.17M) |
| tokens per run, median | 2.91M | 2.39M |
| wall per run, median (sum) | 265 s (2021 s) | 292 s (1802 s) |

- Every background run adopted at least once (1-2 each). pr-0035-background-r1
  never ran a blocking compaction: turn 1 ended at 46.8k, inside the band, and
  both later limit crossings were served by adopted summaries.
- Turn 1 is out of reach. In 11 of 12 runs, turn 1 went past the limit on its
  own (54-76k) before any turn end could land in the band, so it compacted
  blocking in both arms. That is 6 of the 8 blocking compactions in the
  background arm. The other two came at turn 6 of a run whose context climbed
  from about 3k past 50k inside a single turn.

## Turn-end wait

Each compaction is placed in its turn by the server-log time, relative to the
session's first user row and the driver's per-turn walls. All 26 fell in the
post-idle window of a turn, so each wait is that turn's `wall - idle`.

| compaction | n | post-turn wait |
|---|---|---|
| blocking `threshold`, control | 11 | 7.5-17.8 s, median 10.7 s |
| blocking `threshold`, background arm | 8 | 5.5-13.0 s, median 8.6 s |
| blocking, both arms | 19 | median 9.0 s |
| adopted `background` | 7 | 1.0 s each |

- 1.0 s is the floor on turns that do no compaction at all: it is the settle
  polls. An adoption therefore costs no measurable time at the turn end.
- The 10 background summaries took 7.5-27.7 s (median 10.2 s) after their
  turns ended. In an interactive session that work overlaps the user's next
  prompt; here the settle wait hid it. A summary that is still being written
  when the next turn hits the limit is not measured by this driver.
- First-output delay does not show the saving with this driver, for the
  reason given in the multiturn-2 note: the next prompt is posted only after
  the compaction settles. Message timestamps also have 1 s resolution.

## Verdict

- The mechanism works on a valid sample of three tasks: 7 adoptions in 6
  background runs, each replacing a median 9 s blocking turn-end compaction
  with no measurable wait. The background arm ran 8 blocking compactions
  against control's 11.
- The cost: the background arm made 18 summary calls against 11, and 3 of its
  10 background summaries were never used because the session ended first.
  Uncached input rose 27% (0.71M vs 0.56M). Total tokens fell 10% and wall
  fell 11%, but both are dominated by run-to-run variance (pr-0076-control-r1
  alone took 629 s and 3.5M), so n=6 per arm cannot attribute them to the arm.
- Pass: 3/6 vs 2/6. The only difference is one pr-0020 run, within noise at
  this n. No quality effect is detected either way.
- Turn 1 is never helped. In these tasks the first turn crosses the limit on
  its own, so the gain is capped at the later crossings.
- The default stays off. A follow-up worth running: a driver that posts the
  next prompt without waiting for the background summary. That would measure
  the race this one hides, and the cases where an unfinished summary forces a
  blocking compaction anyway.
