# Lean prompt profile on DeepSeek (w52-lean-1)

Batch `w52-lean-1`, seed 20261126, both variants on `b4703395`, both on the
`deepseek-flash-nw` route. The full arm uses the complete coder prompt. The
lean arm uses the 6.9 KB shared base and the default-family add-on through
`prompt_profile: lean`.

The first attempt stopped during checkout with `git archive` terminated after
287 runs. Resuming the same batch id and seed ran the missing DeepSeek lean
repeat. The final batch has all 288 planned runs, 144 per arm, with no invalid
or contaminated runs.

## Result

| variant | runs | pass | false completion | step limit | time limit | median steps | total tokens | cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| full | 144 | 120 (83%) | 18 (13%) | 6 | 8 | 28 | 192,875,869 | $4.56 |
| lean | 144 | 122 (85%) | 12 (8%) | 12 | 9 | 27 | 177,341,992 | $4.46 |

Paired pass-rate estimate `+0.0139`, 95% `[-0.0556, +0.0903]`, margin 0.10:
the guard **passes** on all 24 paired task units. Lean used 8.1% fewer total
tokens. Its median run took 196.9 seconds against 178.5 for full, and more
runs reached the step limit, so the token saving is not a uniform latency or
completion improvement.

By task kind:

| variant | plain pass | interface pass | plain tokens | interface tokens |
| --- | --- | --- | --- | --- |
| full | 60/72 (83%) | 60/72 (83%) | 81,519,007 | 111,356,862 |
| lean | 58/72 (81%) | 64/72 (89%) | 84,602,359 | 92,739,633 |

## Decision

Adopt lean as the unset default for DeepSeek only. The batch establishes
non-inferiority at the predeclared 10-point margin for DeepSeek V4.1 Flash on
this suite, not superiority. An explicit `prompt_profile: full` remains an
override.

What was measured is one model on one route: DeepSeek V4.1 Flash through
`deepseek-flash-nw`. Selecting lean by default for every `deepseek*` model ID
is a family-level extrapolation from that run, not individually measured
evidence. Other DeepSeek models, and other routes that serve DeepSeek, inherit
the default through the roadmap's per-family rule; a valid comparison for each
of them would still be the evidence that settles it.

Kimi, GLM, Qwen and MiMo also use the default-family add-on when lean is
explicitly selected, but they were not in this batch. They keep the full
prompt by default until each family has its own valid comparison. The default
add-on grouping is not evidence that those models were measured.
