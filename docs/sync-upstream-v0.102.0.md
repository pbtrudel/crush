# Sync upstream v0.101.0 and v0.102.0

Tracking the merge of `charmbracelet/crush` v0.101.0 and v0.102.0 into the
fork. Both releases are merged in one step because v0.102.0 contains
v0.101.0.

## Context

- Upstream tags: `v0.101.0` (`0dcd3d5f`) and `v0.102.0` (`c62fa029`).
- Merge base: upstream `v0.100.0` (`c7854d54`), the previous sync.
- Upstream commits: 136 since v0.100.0; the fork had 628 on top of it.
- Conflicts: 63 files (57 content, 3 add/add, 3 deleted-by-fork).

## What upstream brings

- **Request guard.** One wrapper owns cancel and timeouts, stops cancelled
  or silent streams, gives keepalive-only streams a stall budget, and makes
  timeouts retryable. It replaces the fork's `request_timeout.go`.
- **Content refusal retry.** A refusal is retried once per turn.
- **Client/server hardening.** About 60 `fix(server)` commits: socket and
  pipe ownership, DNS rebinding and cross-site protection, event streams
  that end on overflow so clients resync, per-workspace teardown,
  `crush run` signal handling, and question/permission routing by batch ID.
- **Per-session permissions.** Prompts are serialized per root session
  instead of process-wide, and yolo refuses dangerous commands instead of
  auto-approving them.
- **Concurrent question batches.** `Answer`/`Cancel` take a batch ID and
  `Pending` returns every batch.
- **Shared coordinator.** `initCoderAgent` is create-only under a mutex,
  read through `Agent()`, so a second client no longer replaces the
  coordinator.
- **Claude Channels TUI.** Channels dialog, sidebar/landing section, and
  rendering of channel messages. The channel origin is set server-side only.
- **Skill toggles** stored per repo, **custom prompt paths**
  (`options.prompts`, `--prompt`), **`CRUSH_DATA_DIR`**, model display names
  persisted on messages, and brotli/gzip request compression for Hyper.
- **TUI.** One animation clock for every live timer, elapsed time on tool
  calls, a queued prompt shown in the pill right away, nerd font detection,
  `kitt` and Rose Pine themes, themeable mode and exit banners, and cheaper
  theme switches.

## Merge decisions

| Area | Decision |
| --- | --- |
| `go.mod` | Upstream's versions (fantasy v0.48.1, Go 1.27.4); kept the fork's newer `charmtone`, `modernc.org/libc` and `modernc.org/sqlite`. The fork has no module of its own. |
| Request timeout | Upstream's request guard. It now also wraps the fork's fusion step models. |
| Refusal retry | Upstream's retry; `turn_end.go` maps a final refusal to `TurnEndContentFilter` and subagents keep their refusal advice. |
| Settling tool results | Upstream supersedes the fork: one result per call, preferring a real result over an error. |
| Step injections | The fork's `stepInjection` supersedes upstream's folded prompt insertions; upstream's behavioural test still passes. |
| Prompts | Upstream's custom prompt paths, plus the fork's side-question prompt and lean profile. The lean variant applies only while the coder prompt is the built-in one. Custom summary prompts reach the fork's compaction. |
| Coordinator | Upstream's create-only init and `Agent()` accessor everywhere, including the fork's backend, reload, and shutdown paths. The fork's `coordinatorContext`, `stopCoordinator`, `agentRole` and auto-reviewer are kept. |
| Permissions | Upstream's per-scope locks, `ActiveRequests`, `RefusesDangerous` and `InheritAutoApproval`; the fork's reviewer still runs before the scope lock. |
| Yolo and sudo | Yolo refuses dangerous commands, but `sudo` and `doas` are exempt so upstream's "use plain sudo" guidance works. Whatever they run is still checked: `sudo curl` is refused. |
| Questions | Upstream's batch API. Discord and web answer and cancel by the batch ID of the card they show; web keeps every pending batch, keyed by ID. |
| Event streams | The fork's shared per-workspace event sink with upstream's resync rule: a client whose 256-frame backlog overflows is disconnected and resyncs. Upstream's converters were ported into `internal/wire`. |
| Hyper compression | Kept. The fork's `prompt_cache_key` retry and Responses replay transports decode and re-encode compressed bodies, since fantasy compresses outside them. |
| `sessions.sql.go` | Merged by hand to match the merged SQL (`sqlc` is not installed). Upstream's two migrations sort after the fork's newest. |
| TUI lists | Upstream's stale-cache list replaces the fork's `Finished()` freeze; the fork's empty message and scroll position API are kept. |
| Animation | Upstream's single clock drives both the main chat and the fork's agent viewer. |
| Themes | Upstream's banner tokens and themes, with the Matrix theme preserved. Matrix takes upstream's lighter destructive fill. |
| CI | Took upstream's `golangci-lint` source build; upstream's CLA, release and snapshot workflows stay deleted. |

Fork features verified still present: agent teams, Discord remote control,
web mode, goals and loops, live-prompt compaction, prompt-cache accounting,
lazy MCP and tool search, Mistral streaming and Vibe signing, the worktree
tool, TPS stats, secret masking, config reload, the permission auto-reviewer,
Responses reasoning replay, and side questions.

## Fixes from the merge review

An independent review of the resolutions found these, each fixed with a
test that failed first:

- **Yolo bypass through sudo flags.** `sudo -Eu root curl` and
  `sudo FOO=1 curl` hid the inner command from the block list. Short flag
  clusters and sudo's leading `NAME=VALUE` words are now parsed.
- **Event stream hang.** A client attaching just after the shared
  subscription overflowed was never told to resync. The sink now remembers
  the overflow and signals late attachers at once.
- **`messages.sql.go` drift.** `ListMessagesBySessionFromCut` lacked
  `model_name`, so the compaction cut path lost model names.
- **Web question routing.** Answers went to the newest batch, not the one on
  screen. The browser now sends `batch_id`, and the web pending store and
  snapshot hold every batch (this was follow-up 1 of the evaluation).
- **Failed coordinator upgrade.** The old coordinator was cancelled before
  its replacement was built; it now survives a failed build. The test that
  asserted the old behaviour was replaced by one that asserts the new one.
- **Fusion steps** now get the refusal retry as well as the request guard.

## Follow-ups (implemented with this sync)

Each landed as its own commit on the sync branch.

1. **Session approvals before the reviewer.** "Allow for session" grants
   and session-wide auto-approval are checked before the fork's automatic
   reviewer, so a review cannot deny what the user already allowed and
   costs no model call when an approval covers the request.
2. **Skills in the prompt.** A skills reload now rebuilds each agent's
   system prompt once, on its next turn, so the listed skills match the
   reload. Turns without a reload keep the cached prompt.
3. **Opt-in request compression.** Any OpenAI-compatible provider can set
   `request_compression` (`br`, `gzip`, in order of preference). Hyper keeps
   both by default; Mistral is excluded until tested with its Vibe signing.
4. **TPS by routed model.** The session average already followed the active
   provider and model. With Hyper routing it now follows the model that
   actually answered, and restarts when the router switches models.
5. **Shared sessions for `crush --worktree`.** Such runs use the original
   checkout's data directory, so their sessions stay with the project's.
   `--data-dir`, `CRUSH_DATA_DIR` and `data_directory` still take precedence.

## Verification

- `go build ./...`
- `go vet ./...` (one pre-existing `internal/csync` lock-copy warning)
- `go test ./...`
- `go test -race` over `internal/agent`, `app`, `backend`, `cmd`, `config`,
  `db`, `discord`, `message`, `permission`, `proto`, `pubsub`, `question`,
  `server`, `session`, `shell`, `web`, `wire` and `workspace`.
- `gofumpt -l .`

## Known limitations

- `TestEnvPutsToolchainFirst` in `internal/eval/grade` can fail under the
  load of a full parallel `go test ./...`; it passes on its own.
- Upstream's skill re-enable does not re-enable a skill disabled in config,
  because the config filter runs first. Left as upstream ships it.
