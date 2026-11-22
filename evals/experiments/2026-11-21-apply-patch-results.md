# apply_patch experiment (apply-patch-1), incomplete

Batch `apply-patch-1`, seed 20261123, both variants on `093cdf3d`, route
`gpt-5.6-luna`, team and fetch tools off in both arms. The queued shell hit
its 4-hour bound at 113 of 144 runs and the batch exited 124; it was not
resumed, so this is a **partial** batch: 79% of the planned runs, 57 in the
control arm and 56 in the patch arm, not the 72 each the experiment asks for.

## Result (partial)

| variant | runs | pass | false completion | median steps | total tokens |
| --- | --- | --- | --- | --- | --- |
| control (no apply_patch) | 57 | 49 (86%) | 14% | 23 | 57,525,512 |
| patch (apply_patch on) | 56 | 46 (82%) | 19% | 22 | 52,011,114 |

Paired pass-rate estimate `-0.0351`, 95% `[-0.1404, +0.0526]`, margin 0.10:
the guard reports **fail**, which here means the batch is too short to place
an interval inside the margin on 19 paired units, not that a harm was found.
The estimate is a 3.5-point drop with a 14-point downward bound; nothing in
this batch separates the arms.

Two runs in the patch arm are contaminated (pr-0010 and pr-0065 read their own
batch directory), so the valid patch count is 54.

## The tool is adopted, which the batch can show

Tool calls counted from each run's messages, over all 113 runs:

| | control | patch |
| --- | --- | --- |
| `edit` | 235 | 0 |
| `multiedit` | 52 | 0 |
| `apply_patch` | 0 | 41 |
| `lsp_replace_symbol` | 13 | 135 |

The splice replaces the editing tools rather than adding alongside them, and
the model uses the replacement: zero `edit`/`multiedit` calls in the patch arm,
41 `apply_patch` calls, and four times the structural edits. Failure rates per
the stats report over the run logs:

- control: 15 failures in 282 edit calls (5.3%): `multiple_matches` 4, `other`
  4, `text_not_found` 3, `changed_since_read` 3, `invalid_arguments` 1.
- patch: 2 failures in 41 `apply_patch` calls (4.9%), both `text_not_found`.

## Verdict

Inconclusive on pass rate: 113 of 144 runs, an interval four times the margin,
and a guard that fails for want of runs. What the batch does establish is that
the tool reaches the palette and is used (41 calls, zero edits), and that its
failure rate is of the same order as the `edit` tool it replaces (4.9% against
5.3%), with its failures concentrated in `text_not_found` rather than
`multiple_matches`. A decision on the branch needs the missing 31 runs, a
fresh batch, or a narrower claim that does not rest on pass rate.
