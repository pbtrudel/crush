# Eval task suite

The suite measures whether the same model finishes more tasks correctly inside
Crush. A task is one merged pull request of this fork: a base commit, a prompt
taken from the pull request, and a grade made of the pull request's own Go
tests. `crush-eval` runs a task in a throwaway checkout, grades it with those
tests, and records the result.

Layout:

- `tasks/pr-NNNN.yaml`: one task per file, named after its pull request.
- `footer.md`: the last paragraph of every prompt, the same for all tasks.
- `reserve/pr-NNNN.yaml`: vetted tasks kept out of the suite, most because
  more than six tasks would grade `internal/agent` (rule 9 of the design).
  A reserve replaces a task that stops passing vetting: move its file into
  `tasks/`, which changes the suite version.
- `matrix.yaml`, `experiments/` and `baselines/`: added by later pull requests.

Hidden tests are referenced by the blob sha of their contents, not copied, so
no answer code lives in this directory. The suite version is the git tree hash
of `tasks/` and `footer.md`; results of two batches compare only when it
matches.

## Adding a task

1. Pick a merged pull request of `pbtrudel/crush` that changes a non-test `.go`
   file and a `_test.go` file. Its tests must either compile against `merge^1`
   (plain) or fail to compile only on at most four names the pull request
   introduces (interface).
2. Copy `tasks/pr-0010.yaml` for a plain task or `tasks/pr-0051.yaml` for an
   interface task, and fill it in: the merge commit and its first parent, the
   Why section verbatim in `why`, the edited prompt in `prompt`, the interface
   paragraph (interface tasks only), the packages and tests of the grade, and
   each touched `_test.go` file with its blob sha at the merge
   (`git rev-parse <merge>:<path>`).
3. Run `go run ./cmd/crush-eval validate`. It checks every field, resolves the
   commits and hidden blobs against this repository, and prints the suite
   version.

The design is
[docs/superpowers/specs/2026-11-19-task-suite-design.md](../docs/superpowers/specs/2026-11-19-task-suite-design.md).
