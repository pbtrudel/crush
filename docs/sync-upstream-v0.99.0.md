# Sync upstream v0.99.0

Tracking the merge of `charmbracelet/crush` v0.99.0 into the fork.

## Context

- Upstream tag: `v0.99.0` (`e38655aa`), published 2026-10-29.
- Merge base: `b7b55225`, before upstream v0.98.2.
- Upstream commits: 100 (v0.98.2 + v0.99.0).
- Dry-run conflicts: 45 files (41 content, 4 deleted-by-fork).

## Phases

- [x] Merge `upstream-v0.99.0` into `chore/sync-upstream-v0.99.0`.
- [x] Dependencies: `charm.land/catwalk/v3`, `go mod tidy`.
- [x] Catwalk v3 API migration (73 files).
- [x] Config, schema, and docs.
- [x] Agent core (`agent.go`, `coordinator.go`).
- [x] Shell, tools, and background jobs.
- [x] Server, proto, and backend.
- [x] UI and login.
- [x] CI and golden files.
- [x] Build, test, lint.
- [ ] PR.

## Conflict checklist

### Dependencies and config

- [x] `go.mod`, `go.sum`
- [x] `internal/config/config.go`
- [x] `docs/config/README.md`
- [x] `schema.json`

### Agent core

- [x] `internal/agent/agent.go`
- [x] `internal/agent/coordinator.go`
- [x] `internal/agent/coordinator_test.go`
- [x] `internal/agent/testdata/TestCoderAgent/deepseek-v4/*.yaml` (upstream cassettes kept; see limitations)

### Tools and shell

- [x] `internal/agent/tools/bash.go`
- [x] `internal/agent/tools/bash.md.tpl`
- [x] `internal/agent/tools/job_output.md`
- [x] `internal/agent/tools/job_test.go`
- [x] `internal/shell/background.go`

### Server, proto, backend

- [x] `internal/server/events.go`
- [x] `internal/server/proto.go`
- [x] `internal/server/agent_cancel_test.go`
- [x] `internal/server/e2e_agent_test.go`
- [x] `internal/server/sessions_isbusy_test.go`
- [x] `internal/proto/proto.go`
- [x] `internal/backend/agent_runcomplete_test.go`
- [x] `internal/backend/agent_test.go`
- [x] `internal/backend/channels_test.go`

### UI and login

- [x] `internal/ui/model/ui.go`
- [x] `internal/ui/model/status.go`
- [x] `internal/ui/dialog/commands.go`
- [x] `internal/ui/list/filterable.go`
- [x] `internal/ui/util/util.go`
- [x] `internal/login/tui.go`

### CI

- [x] `.github/cla-signatures.json` (kept deleted)
- [x] `.github/workflows/nightly.yml` (kept deleted)
- [x] `.github/workflows/release.yml` (kept deleted)
- [x] `.github/workflows/schema-update.yml` (kept deleted)

## Verification

- [x] `go mod tidy`
- [x] `go build ./...`
- [x] `go test ./...`
- [x] `gofumpt -w .`
- [x] `go vet ./...` (one pre-existing `internal/csync` warning)
- [ ] Smoke-test fork features: Discord `/rc`, `/btw`, `/compact`, `/reload`,
      cache %, TPS, background notices, lazy MCP, Mistral provider, Copilot
      routing, config migration.

## Known limitations

- `TestCoderAgent` is skipped: its VCR cassettes predate the Catwalk v3
  prompt and tool-description changes, so replay cannot match the new
  requests. Re-recording needs live provider credentials.
- The fork's model-switch values (`largeModel`/`smallModel`) coexist with
  upstream's `models func() ModelSet`; `Run` reads the function so a
  credential refresh reaches in-flight requests, while `Model()` reads the
  switchable value.
- The coalesced SSE sink now carries the raw event so per-client
  permission/question scoping still works after encoding.
