# W6 reading files: results

Batch `w6-read-1`, seed 61, route `deepseek-flash-nw`, 24 tasks, 6
repetitions per arm, 288 runs, 0 invalid. Both arms pin the lean prompt
profile; the variant removes the lean prompt's rule "LIMIT FILE READS" and
nothing else. Contamination is 3 of 288 runs (1.0%), under the 2% limit,
so the batch is valid.

| Arm | Runs | Pass | False completion | Median steps | Total tokens | Cost |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| control | 144 | 85% | 8% | 26 | 166,147,663 | $4.01 |
| no-read-rule | 144 | 85% | 8% | 26 | 164,598,090 | $4.00 |

Pass-rate guard: estimate `-0.0076`, 95% `[-0.0500, +0.0368]`, margin
`0.1000`, pass.

Read behaviour, counted from the `view` calls in each run's session:

| Arm | Reads | Reads per run (median / mean) | Reads with a limit | Median limit |
| --- | ---: | ---: | ---: | ---: |
| control | 1,071 | 6 / 7.4 | 69% | 60 |
| no-read-rule | 1,057 | 6 / 7.3 | 66% | 60 |

## Decision

The rule goes. Pass rate holds inside the margin, steps are unchanged and
total tokens are 0.9% lower, which meets the experiment's stated bar.

The rule also does almost nothing: without it DeepSeek still passes a
limit on two reads in three, with the same 60-line median. The tool
description does not ask for paging either, so the paging is the model's
own habit. Removing the rule shortens the prompt but will not cut round
trips. Candidate 2 of W6 (return a small file whole when a smaller range
is asked) is the change that can act on this habit.
