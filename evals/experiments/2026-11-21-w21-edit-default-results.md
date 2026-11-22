# edit_without_read default-on decision (w21-default-1, w21-default-2)

Experiment `2026-11-21-w21-edit-default.yaml`: both variants build
`9b617481`; the edit arm sets `edit_without_read: true` through its config
overlay. 24 tasks, 6 repetitions, 288 runs per batch.

**Decision: the option stays off by default.** The second batch is valid and
its pass-rate guard fails with the whole interval below zero.

## w21-default-1 (route deepseek-flash, commandcode), void

| metric | estimate | 95% interval |
| --- | --- | --- |
| edit_refusals | -0.1753 | [-0.2125, -0.1390] |
| pass_rate (guard, margin 0.1) | -0.0056 | [-0.0694, +0.0583], pass |

Void at 3.1% contamination (9 of 288). Eight runs listed or searched the
batch's `runs` directory, which their own working directory named; the jail
already hid the sibling runs, so nothing was found, but the attempt counts.
One run ran `gh pr list` against the upstream repository. PR #141 then moved a
jailed child to a neutral path, `/tmp/crush-run`.

## w21-default-2 (route deepseek-flash-nw, Neuralwatt), valid

Seed 20261125, eval binary from `81feb939` (after PR #141), invalid 0.
Contamination 1 of 288 (0.35%): pr-0018, edit arm, repetition 4, `gh api`
against the upstream repository. No run named the batches path.

| variant | pass | false completion | step limit | median steps | edits | refused | tokens | cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| control | 127/144 (88%) | 8% | 5 | 30 | 785 | 14% | 198.1M | 4.65 |
| edit | 116/143 (81%) | 14% | 9 | 25 | 618 | 1% | 162.2M | 3.96 |

| metric | estimate | 95% interval |
| --- | --- | --- |
| edit_refusals | -0.1362 | [-0.1697, -0.1042] |
| pass_rate (guard, margin 0.1) | -0.0736 | [-0.1250, -0.0278], fail |

The edit arm applied 241 edits to files it had not read. False completions
rose most on interface tasks (8% to 18%), which suggests that an edit made
without reading is more often wrong and the model then reports the task done.

The two batches disagree on the pass rate (-0.6 points against -7.4). They
differ in provider as well as in contamination, and only the second is valid,
so it decides.
