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
- `experiments/<date>-<name>.yaml`: the variants, routes, repetitions and
  margin of one comparison, committed before its batch starts (design 4.3).
- `baselines/<date>-<route>.json`: the per-task pass counts of a finished
  batch, counts only and no text, written by `crush-eval baseline` (4.4).
- `matrix.yaml`: the families and their route names, added by a later pull
  request.

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

## Signing in, catalogs and probes

`crush-eval login <route>` runs `crush login` with the eval home as
`CRUSH_GLOBAL_DATA`, so the route's OAuth token lands in
`~/.local/state/crush-eval/data` and never in your own Crush data directory. A
jailed Copilot run must also finish inside about 25 minutes: a Copilot token
lives about 30, and the runner renews one before the run when it would die
inside the run plus a buffer of a tenth of its lifetime or five minutes,
whichever is larger. A longer task timeout fails up front with that bound in
the message, because the jail blocks the endpoint that would renew the token.
That sign-in also stores a main model, its recent models and UI options in the
same file. `login` strips those three fields afterwards: a run names its model
on the command line, and a pinned one the route does not configure fails every
run with "model provider not configured". A jailed child's token refresh can
write them back mid-batch, so `run` strips them again on the host before every
run, atomically and with mode 0o600. A data config with none of them is left
byte for byte alone.

A run's child has provider auto-update off, so Crush serves the model catalog
its data dir holds. Freeze your own Crush catalog into the eval home once, and
again when you want newer models:

```sh
crush-eval catalog freeze
```

It copies `providers.json` and `hyper.json` from your Crush data directory
(`$CRUSH_GLOBAL_DATA`, else `$XDG_DATA_HOME/crush`, else
`~/.local/share/crush`), byte for byte and with mode 0o600, and prints their
sha256. `batch` and `run` record those hashes in `batch.json` and in every
result line, so two batches are known to have run on the same models, and both
refuse to start when `providers.json` is missing from the eval home: the
embedded catalog lacks the models the routes name.

`crush-eval probe <route> [--extra JSON] [--reps N]` checks whether a model
recalls a value it wrote in its own reasoning, which is what lets a suite trust
a chain of turns. It sends non-streaming chat requests holding a user turn, an
assistant turn whose `reasoning_content` names a fixed nonce and whose text is
`OK`, and a user turn asking for the nonce, once as Crush builds the request
and once with `--extra` merged into the body, then prints only the two recall
counts and the short answers:

```
$ crush-eval probe gpt-6-luna --reps 3
without 0/3 recalled
  I did not note a codeword.
  ...
with 3/3 recalled
  A7F3-B2Q9
  ...
```

A probe runs on the host, outside the jail, so it renews an expired OAuth token
itself, and it prints no credential.

## Network jail

On Linux, `crush-eval run`, `batch` and `grade` put the model's process and the
grading `go test` in a network, mount and user namespace with only loopback.
The mount namespace hides `/run`, `/var/run`, `/tmp`, `/mnt/wslg` and
`/run/user` behind empty tmpfs mounts, so filesystem unix sockets are out of
reach, and the child keeps your uid and gid without capabilities.

A run also hides the directories that hold the answers. It masks the git
toplevel of the repository the tasks come from, the directory of sibling
worktrees beside it when it is a linked worktree (`.crush-worktrees`, for
example), and the eval home's `batches`. The run's own directory under
`batches` is bind mounted back over the mask, so the child keeps writing its
checkout, home, config and data while other runs and other batches stay out
of reach. A worktrees parent that would hide the eval home, the go toolchain
or its build and module caches is left alone, with a warning: the jail then
masks the checkout by itself. What the child reads stays readable: the eval home's `data`, with
the route's token in `crush.json` and the `providers.json` and `hyper.json`
catalog, the binary under `bin`, and the go toolchain with its build and
module caches.

The one host it may reach is the route's `base_url` host, through an HTTP
proxy on a per-run unix socket. `--network host` turns this off; on other
systems it is the default and the jail refuses to start. A machine where user
namespaces are blocked fails with a message that says to use `--network
host`. A result line records `network`, and in jail mode `net_denied` with the
sorted `net_denied_hosts` the proxy refused.

## Contamination check

After a run, the runner opens the run's own session database read-only and
scans the input of every tool call for the task's merge commit, the fork's
pull request and repository URLs (`pbtrudel/crush/pull`,
`github.com/pbtrudel/crush`), the `git cat-file` command, the `gh pr` and
`gh api` commands when the same input also names a repository the fix can
come from: the task's `source.repo`, the checkout's `origin` and `upstream`
remotes, or the constant upstream `charmbracelet/crush`. It also scans for the
suite path and the host paths a jailed run hides: the repository the runner
resolves, the git toplevel of the checkout the tasks come from, the worktrees
beside it and the eval home's `batches`. Naming one
of them flags the run. The run's own directory under `batches` is not one of
them, since the child names its own checkout all the time. A bare
`gh pr create` flags nothing: the checkout's own bash tool description
carries that recipe, so it is how a model publishes, not how it looks at the
fork. A contaminated run is reported and left out of the comparisons; a rate
above 2% of a batch stops the comparison until the cause is found.

Every result line keeps `contaminated_evidence`: for each contaminated tool
call, the markers it matched and about 200 characters of its input around the
first match, with anything that looks like a credential redacted. `crush-eval compare`
prints the contaminated count per variant, then one line per contaminated run
with its markers and those excerpts, so a void comparison can be diagnosed
after the session databases are gone.

The design is
[docs/superpowers/specs/2026-11-19-task-suite-design.md](../docs/superpowers/specs/2026-11-19-task-suite-design.md).
