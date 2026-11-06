# Sync upstream v0.100.0

Tracking the merge of `charmbracelet/crush` v0.100.0 into the fork.

## Context

- Upstream tag: `v0.100.0` (`c7854d54`), published 2026-11-05.
- Merge base: `b7b55225`, before upstream v0.98.2.
- Upstream commits: 199 since the merge base (95 since v0.99.0).
- Conflicts: 105 files (100 content, 5 deleted-by-fork).

## What upstream v0.100.0 brings

- **A named subagent system.** `agent_dispatch` starts a detached subagent,
  `agent_send` messages one mid-run, `agent_status` reports it, and
  `agent_stop` stops it. Subagents get a name, a report that is queued until
  the model reads it, a model and reasoning override, and a transcript row in
  the TUI. The `agent` tool stays for blocking delegation and hands a call to
  the background after a minute.
- **Provider routing by model type.** `servingProvider` picks the fantasy
  client from catwalk's recorded endpoint type, replacing the hardcoded
  Copilot Responses and OpenCode Messages lists.
- **Routed Prism pricing.** Per-step cost uses the model the provider actually
  routed to, and Hyper credits are summed across steps.
- **Truncation and provider-stop reporting.** A response cut short by the
  output limit, a safety classifier, or the provider itself is recorded as
  such instead of looking like a clean end of turn.
- **Coalescing pubsub broker.** Slow subscribers keep only the newest pending
  update per message, and terminal events can be marked must-deliver.
- **Three-mode permissions.** `PermissionMode` (normal, yolo, sysadmin)
  replaces the fork's `skipRequests` boolean.
- Smaller fixes across the shell, LSP, markdown, and the TUI.

## Merge decisions

Both sides changed the agent core heavily. Where they overlapped:

| Area | Decision |
| --- | --- |
| Subagent machinery | Replaced the fork's `subagentManager`, `subagent_output` tool, and `run_in_background` parameter with upstream's dispatch system. The fork's files were deleted, not left dead. |
| Agent Teams | Kept the fork's feature wholesale; it is orthogonal to upstream's subagents. |
| `agent` tool | Upstream's read/write access variants plus the fork's `agent` preset selection. |
| `buildAgent` / `buildTools` | Upstream's caller-owned readiness group and `parentModel`, with the fork's `agentRole` and `agentSystemPrompt`. |
| Prompt cache | Kept the fork's per-session MCP instruction freeze, stable tool order, and cache breakpoints; dropped upstream's inline cache-control loop. |
| Compaction | Kept the fork's live-prompt compaction with a verbatim tail and measured stats; added upstream's finish-reason reporting. |
| Usage accounting | Kept the fork's SQL-side `AddStepUsage` rollup with upstream's routed-model pricing. |
| Provider routing | Took upstream's `servingProvider`; the fork's `copilotResponsesModels` and `isOpenCodeMessagesModel` were dead code by then. |
| Permissions | Took upstream's three-mode API; the fork's auto-reviewer was re-added on top. |
| Model switching | Kept the fork's `SetModels`/`previousLarge` pair; `Model()` reads the switchable value, not the shared closure. |
| Themes | Upstream's token changes, with the fork's Matrix theme preserved. |
| CI | Kept the fork's workflows; upstream's CLA and release/nightly/schema-update/snapshot workflows stay deleted. |

Fork features verified still present: `IsYolo`, agent teams, Discord remote
control, goals and loops, auto-compaction knobs, prompt-cache accounting, lazy
MCP, Mistral streaming and Vibe signing, the worktree tool, the read alias,
TPS stats, secret masking, and config reload.

## Versioning

The fork's release version is now read from `UPSTREAM_VERSION` instead of the
latest upstream tag. The old workflow jumped the fork to `0.100.0-fork.N`
before v0.100.0 was merged; the file moves only in the commit that performs a
sync.

## Verification

- `go build ./...`
- `go test ./...`
- `go test -race ./internal/config/ ./internal/backend/ ./internal/server/ ./internal/agent/`
- `go vet ./...` (one pre-existing `internal/csync` lock-copy warning)
- `gofumpt -w .`

The race pass is not optional: the CI build runs `-race`, and it caught three
publish-then-mutate races in the config reload path that a plain `go test`
does not. `reloadFromDiskLocked` now finishes the Config (models, agents)
before `setConfig` publishes it, `ConfigStore.SetupAgents` publishes a clone
instead of mutating the live Config, the staleness snapshot has its own
RWMutex, and the coordinator's skill snapshot is guarded so `RefreshSkills`
cannot swap it under a running turn.

## Known limitations

- `TestCoderAgent` is skipped: its VCR cassettes predate the new prompt and
  tool descriptions, so replay cannot match the new requests.
- `internal/agent/hyper/provider.json` is taken from upstream wholesale; the
  fork's single `default_max_tokens` tweak there was superseded.
- The fork's `background-subagents` option now only decides whether a blocking
  `agent` call may hand off to the background; detached work goes through
  `agent_dispatch`.
