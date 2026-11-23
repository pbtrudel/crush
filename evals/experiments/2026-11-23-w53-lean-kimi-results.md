# Lean prompt profile on Kimi K3.1 Pro (w53-lean-kimi-1), partial

Batch `w53-lean-kimi-1`, seed 20261129, both variants on `bea9c91e`, route
`kimi-3.1-pro`. Both arms pin `prompt_profile` explicitly. The planned batch
was 288 runs at six repetitions. It was stopped at the user's request because
of cost after 129 final runs: 65 full and 64 lean, with no invalid or
contaminated runs. Total measured cost was $98.74.

This is a **partial** batch. The paired guard has 11 complete task units, not
the 24 the experiment planned. It is sufficient for the conservative decision
to leave the default unchanged, but it is not a precise whole-suite estimate
of the effect.

## Result (partial)

| variant | runs | pass | false completion | step limit | time limit | median steps | total tokens | cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| full | 65 | 51 (78%) | 2 (3%) | 9 | 37 | 51 | 137,886,563 | $51.16 |
| lean | 64 | 44 (69%) | 3 (5%) | 11 | 38 | 52.5 | 122,983,270 | $47.58 |

Paired pass-rate estimate `-0.0985`, 95% `[-0.1742, -0.0152]`, margin 0.10:
the guard **fails**, and the whole interval is below zero. On the completed
task units, lean is associated with a 9.8-point pass-rate loss. Lean used
10.8% fewer total tokens and cost $3.58 less, but that saving does not meet
the acceptance rule when pass rate falls.

Only 19 full runs and 15 lean runs ended before a step or time limit. Both
arms therefore share reduced sensitivity from the suite's frozen limits; the
comparison remains like-for-like, but these partial results should not be
used to tune either prompt further.

## Decision

Keep the full prompt as the unset default for Kimi, including K3.1 Pro. The
partial batch already fails the guard and its interval excludes no effect, so
spending more runs on this default-on question is not justified. Explicit
`prompt_profile: lean` remains available. DeepSeek's accepted result does not
transfer to Kimi, and the shared default-family add-on is not evidence of
shared prompt effectiveness.
