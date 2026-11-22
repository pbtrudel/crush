# Background compaction, multi-turn benefit: multiturn-2 results

Batch `multiturn-2`, 2026-11-21 18:39-19:16Z, exit 0, 12/12 runs written.

## Setup

- Build: ref `7fa78e63`, agent initialised with `{"interactive": true}` (PR #135
  commit `088704ad` makes a non-interactive coordinator never start a
  background summary).
- Transport: one `crush server` per run, six prompts posted to one session
  (the task, then the five `FollowUps`). A turn ends once the session is idle
  and every assistant and summary row is finished, on three polls in a row.
  The driver waits for a background summary to finish before the next prompt,
  which stands in for a user's think time; its cost shows as the gap between
  `wall_ms` and `idle_ms`, not as user-visible latency.
- Arms: `control` (`compact_background_margin` 0) vs `background` (0.1).
  `auto_compact_threshold` 0.25, not the approved 0.2: at 0.2 the smoke's first
  turn ended past the limit, so no turn end could land in the band. With a
  200k window the limit is 50k and the background band is 30-50k.
- Tasks pr-0020, pr-0035, pr-0076, 2 reps, concurrency 2, 2 h timeout, route
  `deepseek-flash-ctx` intended for every run.

## The batch is mostly void: 8 of 12 runs ran on the wrong model

Only the four pr-0020 runs ran on `commandcode / deepseek/deepseek-v4.1-flash`.
All pr-0035 and pr-0076 runs (8 runs, about 16.9M tokens) ran on
`hyper / qwen3.8-max` through the user's Hyper OAuth login. That breaks the
free-route-only limit.

The cause is in the server transport, not in Crush:

- Every run sets `CRUSH_GLOBAL_DATA` to the eval home's `data/` directory, so
  that the login tokens are shared. That directory's `crush.json` is a shared
  config, and its `models` take precedence over the run's global config.
- `crush run` passes `-m` and `--small-model`, which override both, so the
  single-turn batches and multiturn-1 are not exposed.
- `crush server` has no model flag. `writeServerModels` writes the models into
  the run's global config, and the shared file outranks it.
- At 13:55:35 local, pr-0035-control-r1's server logged `Migrated config file
  to the current schema` for `~/.local/state/crush-eval/data/crush.json`. That
  file's `models.main` is now `hyper / qwen3.8-max`. A backup from 2026-11-19,
  `crush.json.bak-before-model-strip`, has `commandcode` there. What set hyper
  between the pr-0020 runs at 13:39 and 13:55 is not established.
- `qwen3.8-max` declares a 1M window, so the limit was 250k and none of those
  runs could compact: they ended turns at up to 90k with no compaction in
  either arm.
- Two pr-0076 background runs also ended early on a driver HTTP client
  timeout (`GET .../agent/sessions/<id>`, 2 and 5 turns).

The fix is for the server transport to pin the models where they win, for
example in the workspace data directory's config, or to give each run its own
`CRUSH_GLOBAL_DATA` with only the route's token. The shared file is left
untouched because other sessions read it, but it will hijack any further
server-transport run until it is fixed.

## Valid sample: pr-0020, 2 runs per arm

Adopted means the server log line `Context compacted trigger=background`.
Logreport's `background` count is written summary rows, so it also counts a
summary still pending when the server stopped (here, one written after the
sixth turn in each background run).

| | control | background |
|---|---|---|
| pass | 1/2 | 1/2 |
| blocking `threshold` compactions | 3 | 3 |
| background summaries written | 0 | 4 |
| adopted `background` | 0 | 2 |
| `background_unused` | 0 | 0 |
| pending at exit (after the final turn) | 0 | 2 |
| tokens per run | 3.52M, 4.52M | 3.23M, 2.38M |
| wall per run | 507 s, 420 s | 601 s, 412 s |

The latency at a compacting turn's end is `wall - idle`, the work done after
the agent stopped reporting busy:

| compaction | n | post-turn wait |
|---|---|---|
| blocking `threshold` (both arms) | 6 | 13.6, 16.6, 16.6, 16.9, 19.1, 21.7 s (median 16.8 s) |
| adopted `background` | 2 | 1.0, 1.1 s |

- Both adoptions came at the end of turn 5. The summaries were written after
  turn 2 (45.6k, r2) and turn 4 (32.8k, r1). Context fell to 1.6k and 2.7k.
- Writing those summaries took 15.7 s and 25.1 s after their turns ended. In
  an interactive session that time overlaps the user's next prompt; here the
  settle wait hid it.
- First-output delay does not show the saving with this driver. The
  compaction runs at the end of the turn, and the driver waits for it to
  settle before posting, so the next turn's first output (2-4 s in both arms)
  is never delayed. The measured gain is the turn-end wait above. First output
  is also a 1 s-resolution proxy, because message timestamps are in seconds.
- Tokens and wall per run are dominated by run-to-run variance (control r2's
  first turn took 75 steps). n=2 per arm cannot separate an effect.

## Verdict

- Adoption works through the server transport: 2 of 2 valid background runs
  adopted a summary. Each adoption replaced a 14-22 s blocking turn-end
  compaction with about a 1 s wait.
- This is one task, two adoptions, against six blocking compactions. Each
  background summary still costs a full summary call, and a session that ends
  inside the band wastes one (2 here).
- pass/fail and tokens: no detectable difference (1/2 vs 1/2).
- pr-0035 and pr-0076 are void (wrong model, paid route, no compaction
  possible).
- The default stays off. A rerun needs the model-pinning fix first and should
  be smoke-checked for `provider=commandcode` on every run.
