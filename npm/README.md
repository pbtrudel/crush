# @conanmontreal/crush

A fork of [Charm's Crush](https://github.com/charmbracelet/crush), a
terminal-based AI coding assistant, carrying local patches:

- Copilot Responses-API routing can be extended from a user config file
  (`~/.config/crush/copilot-responses-models`) without rebuilding.
- Mistral: content-block streaming deltas are rewritten into standard
  reasoning/text shapes, reasoning history is replayed in Mistral's thinking
  block format, and requests are signed like the Mistral Vibe CLI so a
  subscription-linked key draws on the Vibe allowance.
- Test-suite hardening: the binary is built from a tree where `go test` can
  never launch a browser.

## Install

```bash
npm install -g @conanmontreal/crush
```

The postinstall step downloads the patched binary for your platform
(Linux/macOS/Windows, x64/arm64) from the fork's
[GitHub releases](https://github.com/pbtrudel/crush/releases) and verifies its
sha256 checksum.

Both this fork and upstream provide the same `crush` command, so uninstall the
upstream package first if it is installed:

```bash
npm uninstall -g @charmland/crush
```

The fork reads the same configuration as upstream (`~/.config/crush/`,
project `.crush/` directories), so no config changes are needed.

## Update

```bash
npm install -g @conanmontreal/crush@latest
```

Every merge to the fork's default branch produces a new version; the pipeline
stages it on npm and a maintainer approves it with 2FA.

---

Maintainer note: `install.js`, `lib.js` and `run-crush.js` are the wrapper
scripts GoReleaser Pro generates for npm packages (copied verbatim from an
installed `@charmland/crush` 0.98.1). goreleaser's npm publisher is
Pro-only, so the `publish npm` workflow packages this directory itself:
`lib.js` reads the `archives` manifest in `package.json` at install time and
downloads the archive matching `${process.platform}-${process.arch}` from the
release URL, verifying the sha256 digest. To refresh the scripts, copy the
three files out of a newer installed `@charmland/crush` package directory and
re-run the local end-to-end install test.
