# Lean prompt profile on Claude Haiku (w53-lean-claude-1)

Batch `w53-lean-claude-1`, seed 20261128, both variants on `b4703395`, route
`claude-haiku` on the Claude Agent provider, with the fetch and team tools
disabled in both arms. The full arm uses the complete coder prompt. The lean
arm uses the 6.9 KB shared base and the Anthropic-family add-on through
`prompt_profile: lean`.

The batch ran in chunks so that no run outlived the host Claude login token.
A wrapper wait stalled after another host process renewed the token first;
the wrapper was corrected and the batch resumed with the same id and seed.
The final batch has all 144 planned runs, 72 per arm, with no invalid or
contaminated runs.

## Result

| variant | runs | pass | false completion | step limit | time limit | median steps | total tokens | cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| full | 72 | 58 (81%) | 13 (18%) | 0 | 3 | 14 | 52,485,399 | n/a |
| lean | 72 | 54 (75%) | 13 (18%) | 0 | 5 | 15 | 47,103,907 | n/a |

Paired pass-rate estimate `-0.0556`, 95% `[-0.1250, +0.0000]`, margin 0.10:
the guard **fails** on all 24 paired task units. The observed drop is 5.6
points, and the interval cannot exclude a 12.5-point regression. Lean used
10.3% fewer total tokens, but its median run took 172.1 seconds against 155.0
for full. Cost is unavailable because this route uses the host subscription.

By task kind:

| variant | plain pass | interface pass | plain tokens | interface tokens |
| --- | --- | --- | --- | --- |
| full | 31/36 (86%) | 27/36 (75%) | 25,731,014 | 26,754,385 |
| lean | 29/36 (81%) | 25/36 (69%) | 21,958,474 | 25,145,433 |

## Decision

Keep the full prompt as the unset default for Anthropic models. The token
saving does not meet the acceptance rule while the pass-rate guard fails.
Users can still select `prompt_profile: lean` explicitly. A different result
would need a new experiment with a predeclared design; this batch is not
evidence for enabling lean by default on Claude Haiku or other Claude models.
