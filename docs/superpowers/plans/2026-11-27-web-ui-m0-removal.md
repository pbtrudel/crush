# Web UI M0 (Removal) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Delete the old Preact web UI and the gateway surface the new DSH
design drops, leaving `crush --web`, `/web`, the `WEB` badge and the gateway
security core working against a committed placeholder page.

**Architecture:** Four independently mergeable removals. Task 1 deletes the
`web/` tree with its e2e specs, baselines and configs, plus the Go test that
keeps the deleted client's wire fixture in sync, and defuses the CI and
Taskfile steps that would run npm in a tree that no longer exists. Task 2
replaces the committed frontend build in `internal/web/dist` with a two-file
placeholder page and pins it with new asset tests. Task 3 removes
`GET /api/snapshot` and the root-session counter behind the old pin/browse
toggle, keeping the snapshot payload itself because the stream still sends it
as its first frame. Task 4 removes `options.web_display`, the
`display_changed` frame and the two config write routes, and regenerates
`schema.json`. Nothing else in the gateway moves: auth, launch, controller,
the stream hub, the assets handler and every other route stay byte-identical.

**Tech Stack:** Go 1.27.4 (`go 1.27.4` in `go.mod`), `net/http`,
`github.com/coder/websocket`, `embed`, testify, gofumpt, GitHub Actions.
No new Go module, no new npm dependency, no Node needed to build.

**Spec:** `docs/superpowers/specs/2026-11-27-web-ui-dsh-design.md`. Read
sections "Milestones" (the M0 row), "Gateway API and stream" (the Removed
list), "Testing" and "Cleanup in M0" before Task 1. The M4 plan
(`docs/superpowers/plans/2026-11-24-web-mode-m4.md`) shows the conventions
this plan follows; its Task B1 built the `options.web_display` block that
Task 4 removes.

This plan names code by symbol, never by line number: line anchors go stale
on the first rebase. Every step that touches existing code names the
function, type or test, and where a bare name could be ambiguous it gives the
`grep` that finds it.

Date: 2026-11-27.

### Execution order and PR grouping

Each task is one PR of roughly 400 changed lines or less, excluding pure
deletions. The PRs must merge in order: Task 3 and Task 4 both edit
`internal/web/snapshot.go`, so running them in parallel guarantees a
conflict. Branch each PR from `origin/main` after `git fetch origin`, after
the previous PR has merged.

| PR | Task | Branch | Added or changed lines (excl. pure deletions) |
|---|---|---|---|
| 1 | Task 1, delete the old frontend | `crush/web-ui-m0-1-removal` | ~35 |
| 2 | Task 2, the placeholder page | `crush/web-ui-m0-2-placeholder` | ~150 |
| 3 | Task 3, drop `GET /api/snapshot` | `crush/web-ui-m0-3-snapshot` | ~130 |
| 4 | Task 4, drop `options.web_display` | `crush/web-ui-m0-4-web-display` | ~330 |

Task 5 is the whole-branch verification pass. It runs after PR 4 merges and
needs no PR of its own.

## Global Constraints

- Go floor: `go 1.27.4`, as `go.mod` states. `CGO_ENABLED=0` and
  `GOEXPERIMENT=greenteagc` (the `env` block of `Taskfile.yaml`).
- `go build ./...` must still need no Node and no npm.
- `github.com/coder/websocket` stays; the stream keeps using it.
- Keep unchanged, and keep their tests: `internal/web/auth.go`,
  `launch.go`, `controller.go`, the TUI callbacks, the Host allow-list, the
  Origin checks, the CSP and the other security headers, the stream hub and
  the replay ring, `assetHandler` and `embeddedAssets`, every route not
  listed as removed, the `--web` flag, `/web`, the palette items and the
  `WEB` status badge.
- Keep `internal/web/cmd/webfake` building and running.
- No inline script and no inline style anywhere under `internal/web/dist`:
  the CSP is `default-src 'self'; style-src 'self'` (`const csp` in
  `internal/web/auth.go`), so an inline `<script>` or a `style="…"` attribute
  renders nothing.
- Remove only behavior the spec removes. Never delete or skip a test that
  covers kept behavior; adapt it instead (Task 3 and Task 4 each adapt
  several).
- Comments: own line, capital letter, period, wrapped at 78 columns
  (`AGENTS.md`). Log messages start with a capital letter, enforced by
  `./scripts/check_log_capitalization.sh`.
- Format every Go change with `gofumpt -w .` (`task fmt` runs the same).
- Commits: one line, imperative, 72 characters or less, semantic prefix.
  No attribution line and no `Co-authored-by:` trailer.
- `gofumpt` is on this host. `task` and `golangci-lint` are not installed;
  where this plan says `task lint`, run it only if `command -v task` finds
  it, and use the fallback the step gives otherwise.

## Review Focus

1. A tab opened on a deep path (`/sessions/s1`, `/settings`) after the old
   UI is gone: `assetHandler` must answer with the placeholder page and a
   200, not a 404. Pinned by `TestPlaceholderAnswersAnyPath` (Task 2).
2. A stale tab asking for a hashed bundle the placeholder does not ship
   (`/assets/index-43JjnKFV.js`): it must get a 404 with
   `Cache-Control: no-store`, never a year-long cache header on a missing
   file. Pinned by `TestStaleHashedAssetIsNotFoundAndNotCached` (Task 2).
3. A page rendered under the strict CSP: no inline script or style may sneak
   into `index.html`, or the tab is blank with no error the user can see.
   Pinned by `TestRootServesThePlaceholderPage` (Task 2).
4. An existing `crush.json` that still sets `options.web_display`: it must
   load without an error after the field is gone. Pinned by
   `TestOptionsIgnoreARemovedWebDisplayBlock` (Task 4).
5. A cached tab or a script calling a removed route
   (`GET /api/snapshot`, `POST /api/config/set`, `POST /api/config/remove`):
   it must get a clean 404, not a 401, a 500 or a panic. Pinned by
   `TestSnapshotRouteIsGone` (Task 3) and `TestConfigWriteRoutesAreGone`
   (Task 4).

---

## Task 1: Delete the old frontend tree and its fixture contract

**Files:**
- Delete: `web/` (281 tracked files: `src/`, `e2e/`, `public/`, `scripts/`,
  `index.html`, `package.json`, `package-lock.json`, `tsconfig.json`,
  `vite.config.ts`, `playwright.config.ts`, `.nvmrc`)
- Delete: `internal/web/fixtures_test.go`
- Modify: `internal/web/cmd/webfake/main.go` (the dev-server hint block)
- Modify: `Taskfile.yaml` (`web:install`, `web:build`, `web:test`,
  `web:e2e`)
- Modify: `.github/workflows/web.yml` (both jobs)

**Interfaces:**
- Consumes: nothing.
- Produces: a repository with no `web/` directory. `internal/web/dist` is
  untouched by this task and still carries the old Preact build until Task 2.
  `webtest.SeedM4b` still sets `Options.WebDisplay` at the end of this task;
  Task 4 removes it.

- [ ] **Step 1: Confirm the gateway suite is green before the deletion**

Run: `go test ./internal/web/ -count=1`
Expected: `ok github.com/charmbracelet/crush/internal/web` (about 7 s on
this host).

- [ ] **Step 2: Delete the Go test that keeps the deleted client's fixture fresh**

`internal/web/fixtures_test.go` exists for one job: `TestWireFixtures`
marshals every frame into `../../web/src/state/__fixtures__/events.json`,
the fixture file the Preact tests imported. That file leaves with `web/`, so
the test's subject is gone. The frames themselves stay covered by the
feature tests (`stream_test.go`, `api_models_test.go`, `team_test.go`,
`api_signin_test.go` each assert real payload JSON). M1 reintroduces the
contract test against the new client's zod-typed fixtures.

Run: `git rm internal/web/fixtures_test.go`
Expected: `rm 'internal/web/fixtures_test.go'`.

- [ ] **Step 3: Run the suite to prove nothing else used those helpers**

Run: `go test ./internal/web/ -count=1`
Expected: PASS. If it fails with `undefined: fixtureTeam`, `fixtureRuns` or
`snapshotFrame`, some other test borrowed a helper from the deleted file;
move that helper into the borrowing test's file rather than restoring the
fixture code.

- [ ] **Step 4: Delete the frontend tree**

Run: `git rm -r web`
Expected: 281 `rm 'web/…'` lines, ending with the last file under `web/src/`.

Do not touch `.gitignore` lines 59-61 (`web/node_modules/`,
`web/test-results/`, `web/playwright-report/`). M1 recreates `web/` and those
rules apply again.

- [ ] **Step 5: Drop the npm dev-server hint from the fake gateway**

`internal/web/cmd/webfake/main.go` prints a `CRUSH_WEB_DEV_PORT` hint naming
`npm --prefix web run dev`, which cannot work with `web/` gone. Replace the
block

```go
	fmt.Println(g.LaunchURL())
	if u, err := url.Parse(g.URL()); err == nil {
		fmt.Fprintf(os.Stderr, "Dev server: CRUSH_WEB_DEV_PORT=%s npm --prefix web run dev, then open http://localhost:5173/#t=%s\n",
			u.Port(), strings.TrimPrefix(g.LaunchURL(), g.URL()+"/#t="))
	}
```

with

```go
	fmt.Println(g.LaunchURL())
	fmt.Fprintln(os.Stderr, "Dev server: the web UI is being rebuilt; this serves the placeholder page.")
```

Then delete the now-unused `"net/url"` and `"strings"` imports: no other
line of that file uses `url.` or `strings.` (`grep -n 'url\.\|strings\.'
internal/web/cmd/webfake/main.go` must print nothing after the edit).

- [ ] **Step 6: Turn the `web:*` npm tasks into notices**

In `Taskfile.yaml`, replace the four npm tasks. `web:dev` stays as it is,
and `web:check` keeps its `task: web:build` + `git diff` + stray-file check,
which still proves `internal/web/dist` is committed and clean.

```yaml
  web:install:
    desc: Install web mode frontend dependencies
    cmds:
      - echo "The web frontend is being rebuilt; web/ returns in M1 of the DSH rebuild."

  web:build:
    desc: Build the web mode frontend into internal/web/dist
    cmds:
      - echo "The web frontend is being rebuilt; internal/web/dist carries a placeholder page."

  web:test:
    desc: Typecheck and unit test the web mode frontend
    cmds:
      - echo "The web frontend is being rebuilt; its unit tests return in M1."

  web:e2e:
    desc: Run Playwright smoke tests against the fake gateway
    cmds:
      - echo "The web frontend is being rebuilt; the Playwright suite returns in M1."
```

- [ ] **Step 7: Make both CI jobs pass without the frontend**

Rewrite `.github/workflows/web.yml`. Keep the workflow name `web`, both job
names (`frontend`, `e2e`) and the e2e shard matrix: required status checks
reference them, and a job that disappears leaves the PR blocked forever.
Keep the pinned action SHAs exactly as they are.

```yaml
name: web
on:
  pull_request:
    paths: ['web/**', 'internal/web/**', 'internal/wire/**', '.github/workflows/web.yml']
  push:
    branches: [main]
    paths: ['web/**', 'internal/web/**', 'internal/wire/**', '.github/workflows/web.yml']

permissions:
  contents: read

concurrency:
  group: web-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: true

jobs:
  frontend:
    runs-on: ubuntu-26.04
    # The frontend is being rebuilt (M0 of the DSH rebuild). Until web/
    # returns in M1 this job only runs the gateway tests; the npm steps and
    # the dist freshness check come back with the new tree.
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - run: go test ./internal/web/... ./internal/wire/...

  e2e:
    runs-on: ubuntu-26.04
    # The shard matrix stays so the web / e2e (1), (2) and (3) checks keep
    # reporting while the suite is gone.
    timeout-minutes: 25
    strategy:
      fail-fast: false
      matrix:
        shard: [1, 2, 3]
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - name: Playwright suite returns with the new frontend
        run: echo "web/e2e was deleted with the old frontend; M1 restores this job."
```

- [ ] **Step 8: Verify the tree still builds and tests**

```bash
go build ./...
go test ./internal/web/... ./internal/wire/... -count=1
go vet ./internal/web/... ./internal/wire/...
gofumpt -l internal/web
```

Expected: the build prints nothing; both test packages report `ok`; `go vet`
prints nothing; `gofumpt -l` prints nothing (fix with
`gofumpt -w internal/web/cmd/webfake/main.go` if it does).

- [ ] **Step 9: Verify the fake gateway still starts and prints a usable link**

```bash
WEBFAKE_PROJECT_DIR=/tmp/crush-m0-check go run ./internal/web/cmd/webfake > /tmp/webfake-url.txt 2>/tmp/webfake-err.txt &
sleep 5
cat /tmp/webfake-url.txt
cat /tmp/webfake-err.txt
kill %1
```

Expected: `http://127.0.0.1:<port>/#t=<token>` on the first file and
`Dev server: the web UI is being rebuilt; this serves the placeholder page.`
on the second.

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "chore: delete the old web frontend tree"
```

---

## Task 2: Serve a placeholder page from internal/web/dist

**Files:**
- Delete: `internal/web/dist/assets/` (69 tracked files),
  `internal/web/dist/index.html`
- Keep: `internal/web/dist/favicon.svg`
- Create: `internal/web/dist/index.html`, `internal/web/dist/style.css`
- Create: `internal/web/assets_test.go`
- Modify: `docs/web-mode.md` (the "being rebuilt" note)
- Modify: `README.md` (the web mode section body)

**Interfaces:**
- Consumes: `assetHandler` and `embeddedAssets` in `internal/web/assets.go`
  (unchanged), `startTest` and `do` in `internal/web/auth_test.go`
  (unchanged test helpers).
- Produces: `internal/web/dist/index.html` served at `/` and at any unknown
  path, `internal/web/dist/style.css` served at `/style.css`. `//go:embed
  all:dist` picks both up with no Go change.

- [ ] **Step 1: Write the failing asset tests**

Create `internal/web/assets_test.go`:

```go
package web

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootServesThePlaceholderPage pins the page a tab gets while the new
// UI is being built. The CSP allows no inline script and no inline style,
// so either one would leave the tab blank.
func TestRootServesThePlaceholderPage(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, rsp.Header.Get("Content-Type"), "text/html")
	require.Equal(t, "no-cache", rsp.Header.Get("Cache-Control"))
	body, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	page := string(body)
	require.Contains(t, page, "being rebuilt")
	require.Contains(t, page, `href="/style.css"`)
	require.NotContains(t, page, "<script", "the CSP allows no inline script")
	require.NotContains(t, page, `style="`, "the CSP allows no inline style")
}

// TestPlaceholderAnswersAnyPath pins the deep-link case: assetHandler
// rewrites an unknown path to "/" so a bookmark opens the page.
func TestPlaceholderAnswersAnyPath(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	for _, path := range []string{"/sessions/s1", "/settings/appearance"} {
		rsp := do(t, nil, http.MethodGet, g.URL()+path, nil)
		body, err := io.ReadAll(rsp.Body)
		rsp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, rsp.StatusCode, path)
		require.Contains(t, string(body), "being rebuilt", path)
	}
}

// TestPlaceholderStylesheetIsServed pins the external stylesheet the page
// loads: a 404 here means an unstyled page under the strict CSP.
func TestPlaceholderStylesheetIsServed(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/style.css", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, rsp.Header.Get("Content-Type"), "text/css")
	require.Equal(t, "no-cache", rsp.Header.Get("Cache-Control"))
}

// TestStaleHashedAssetIsNotFoundAndNotCached pins the cached-tab case: a
// bundle the old build shipped is gone, so it must answer 404 and must not
// carry the year-long immutable header a real asset gets.
func TestStaleHashedAssetIsNotFoundAndNotCached(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/assets/index-43JjnKFV.js", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Equal(t, "no-store", rsp.Header.Get("Cache-Control"))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/web/ -run 'TestRootServes|TestPlaceholder|TestStaleHashed' -count=1`
Expected: FAIL on the first three tests. `TestRootServesThePlaceholderPage` reports
`"<script" should not be contained`, because the current `dist/index.html`
is the old Vite document. `TestPlaceholderStylesheetIsServed` gets a 200
carrying `text/html`, which is `assetHandler` rewriting the unknown
`/style.css` to `/`. `TestPlaceholderAnswersAnyPath` gets the old document
and fails on `being rebuilt`. `TestStaleHashedAssetIsNotFoundAndNotCached`
passes already: a missing `/assets/` path 404s today too.

- [ ] **Step 3: Delete the old build and write the placeholder**

```bash
git rm -r internal/web/dist/assets internal/web/dist/index.html
```

Create `internal/web/dist/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="color-scheme" content="dark light" />
    <title>Crush web mode</title>
    <link rel="icon" href="/favicon.svg" type="image/svg+xml" />
    <link rel="stylesheet" href="/style.css" />
  </head>
  <body>
    <main>
      <h1>Crush web mode is being rebuilt</h1>
      <p>
        The old web UI was removed and the new one is still being built. The
        terminal is unaffected: this session keeps running, and
        <code>/web</code> brings a tab back at any time.
      </p>
      <p>
        Until the new UI lands, this page is all a tab shows. The gateway,
        the session, sign-in and the <code>WEB</code> badge in the status bar
        keep working.
      </p>
    </main>
  </body>
</html>
```

Create `internal/web/dist/style.css`:

```css
:root {
  color-scheme: dark light;
  --bg: #0b0b0f;
  --fg: #e6e6ef;
  --muted: #9a9aab;
  --accent: #8b7bf7;
  --panel: #16161d;
  --edge: #262631;
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 2rem;
  background: var(--bg);
  color: var(--fg);
  font-family: system-ui, -apple-system, "Segoe UI", sans-serif;
  line-height: 1.5;
}

main {
  max-width: 34rem;
  padding: 2rem 2.25rem;
  border: 1px solid var(--edge);
  border-radius: 12px;
  background: var(--panel);
}

h1 {
  margin: 0 0 1rem;
  font-size: 1.375rem;
  letter-spacing: -0.01em;
}

p {
  margin: 0 0 1rem;
  color: var(--muted);
}

p:last-child {
  margin-bottom: 0;
}

code {
  padding: 0.1rem 0.35rem;
  border-radius: 4px;
  background: rgb(255 255 255 / 6%);
  color: var(--accent);
  font-family: ui-monospace, "SFMono-Regular", "JetBrains Mono", monospace;
  font-size: 0.9em;
}

@media (prefers-color-scheme: light) {
  :root {
    --bg: #f6f6f9;
    --fg: #16161d;
    --muted: #55556a;
    --panel: #ffffff;
    --edge: #e2e2ea;
  }

  code {
    background: rgb(0 0 0 / 6%);
  }
}
```

Leave `internal/web/dist/favicon.svg` in place; the page links it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/web/ -run 'TestRootServes|TestPlaceholder|TestStaleHashed' -count=1`
Expected: PASS.

- [ ] **Step 5: Run the whole gateway suite**

Run: `go test ./internal/web/ -count=1`
Expected: PASS. Any failure here is unrelated to the page and must be
reported, not papered over.

- [ ] **Step 6: Add the "being rebuilt" note to the web mode docs**

`docs/web-mode.md` documents the holodeck stage, the chronoline, the file
drawer and `options.web_display`, none of which exist any more. M6 rewrites
the document; until then a note at the top is the whole change the spec asks
for. Insert after the `# Web mode` heading, before the "Web mode serves a
running Crush session…" paragraph:

```markdown
> **Note:** The web UI is being rebuilt. Web mode currently serves a
> placeholder page: `crush --web`, `/web`, the `WEB` badge and the gateway
> all still work, but the tab only shows a "being rebuilt" notice until the
> new UI lands. Everything the rest of this document says about the tab is
> history: the holodeck stage, the chronoline, the file drawer, the display
> settings and the pin/browse toggle were removed with the old UI. Display
> settings are gone for good, so `options.web_display` no longer exists and
> a config that still sets it is read and ignored.
```

- [ ] **Step 7: Trim the README web mode section**

In `README.md`, replace the two paragraphs under `### Web mode` (the
`crush --web` starts the session… paragraph and the `The gateway is
loopback only…` paragraph), leaving the heading:

```markdown
`crush --web` opens a browser tab for the session, and `/web` does the same
from a running session, showing the link in a notification so a slow or
missing browser never hides it. To turn it on by default, use
`option web true` in your `crushrc`, or `"options": {"web": true}` in
`crush.json`.

The web UI is being rebuilt, so the tab serves a placeholder page for now;
the terminal, the gateway and the `WEB` badge keep working. The gateway is
loopback only, with a per-run token sent as a header, and a private launch
file for the browser opener. See [docs/web-mode.md](docs/web-mode.md) for
the security model, client/server mode and troubleshooting.
```

- [ ] **Step 8: Verify GET / serves the placeholder through the webfake**

```bash
WEBFAKE_PROJECT_DIR=/tmp/crush-m0-check go run ./internal/web/cmd/webfake > /tmp/webfake-url.txt 2>/tmp/webfake-err.txt &
sleep 5
url=$(head -1 /tmp/webfake-url.txt)
origin=${url%%/#t=*}
curl -sS "$origin/" | head -12
curl -sSi "$origin/style.css" | head -4
curl -sSi "$origin/assets/index-43JjnKFV.js" | head -4
kill %1
```

Expected: the first command prints the `<!doctype html>` document with the
`Crush web mode is being rebuilt` heading and no `<script>` tag; the second
prints `HTTP/1.1 200 OK`, `Cache-Control: no-cache` and a `text/css`
content type; the third prints `HTTP/1.1 404 Not Found` with
`Cache-Control: no-store`.

- [ ] **Step 9: Verify formatting and vet**

```bash
go build ./...
gofumpt -l internal/web
go vet ./internal/web/...
```

Expected: no output from any of the three.

- [ ] **Step 10: Commit**

```bash
git add internal/web/dist internal/web/assets_test.go docs/web-mode.md README.md
git commit -m "feat(web): serve a placeholder page while the UI is rebuilt"
```

---

## Task 3: Remove GET /api/snapshot and the root-session counter

**Files:**
- Modify: `internal/web/api.go` (`registerAPI`, delete `handleSnapshot`)
- Modify: `internal/web/snapshot.go` (`Snapshot`, `buildSnapshot`, delete
  `rootSessionCount`)
- Delete: `internal/web/snapshot_test.go`
- Test: `internal/web/api_test.go` (helpers, `TestHealthAndSnapshot`,
  `TestSnapshotExposesEveryPendingBatch`, `TestSnapshotReportsAgentReady`,
  new `TestSnapshotRouteIsGone`)
- Test: `internal/web/controller_test.go`
  (`TestControllerReportsInputModeBeforeStart`,
  `TestControllerReportsPlanReadyBeforeStart`)
- Test: `internal/web/api_models_test.go` (`TestSnapshotModelInfo`)
- Test: `internal/web/api_prompts_test.go`
  (`TestSecureAnswerRefusedForgetsThePrompt`,
  `TestSecureCancelRefusedForgetsThePrompt`)
- Test: `internal/web/api_signin_test.go` (`TestAPIKeyNeverLogged`,
  `TestSignInSnapshotCarriesTheState`, `TestSignInImportIsRecorded`)
- Test: `internal/web/team_test.go` (`TestTeamFramesKeepLastGoodRuns`)
- Test: `internal/web/seed_m3_test.go` (`TestSeedM3FeedsTheRoutes`)

**Interfaces:**
- Consumes: `Gateway.buildSnapshot(ctx context.Context, sessionID string)
  Snapshot` (`internal/web/snapshot.go`), which stays: `handleStream` sends
  its result as the first `snapshot` frame.
- Produces: two Go test helpers other web tests reuse,
  `snapshotOf(t *testing.T, g *Gateway, sessionID string) Snapshot` and
  `snapshotJSON(t *testing.T, g *Gateway, sessionID string) string`, both
  in `internal/web/api_test.go`. `Snapshot` keeps every field except
  `RootSessions`. Task 4 removes `Snapshot.Display` from the same struct.

- [ ] **Step 1: Write the failing test for the removed route**

Add to `internal/web/api_test.go`, next to the other route tests:

```go
// TestSnapshotRouteIsGone pins the M0 removal: a tab loads a bootstrap plus
// per-session snapshots now, so the old full-state route is a 404 even for
// a signed-in client.
func TestSnapshotRouteIsGone(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/snapshot", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.NotContains(t, string(body), "current_session_id")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/web/ -run TestSnapshotRouteIsGone -count=1`
Expected: FAIL with `expected: 404` and `actual: 200`.

- [ ] **Step 3: Delete the route and the handler**

In `internal/web/api.go`, delete the line
`g.route(api, "GET /api/snapshot", g.handleSnapshot)` from `registerAPI`,
and delete the whole `handleSnapshot` function (the one that reads the
`session` query parameter and calls `g.buildSnapshot`). Nothing else in
`api.go` changes: `buildSnapshot` stays because `handleStream` uses it.

- [ ] **Step 4: Delete the pin/browse counter**

`Snapshot.RootSessions` exists only for the old HUD's pin toggle, the switch
that lets a tab browse the other root sessions. With the UI gone the count
has no reader.

In `internal/web/snapshot.go`:

1. Delete the `RootSessions int` field (JSON tag `root_sessions`) from the
   `Snapshot` struct.
2. Delete the line `s.RootSessions = g.rootSessionCount(ctx)` from
   `buildSnapshot`.
3. Delete the whole `rootSessionCount` function, including its comment.

Then delete `internal/web/snapshot_test.go`: its only test,
`TestSnapshotCountsRootSessions`, covers exactly that counter ("the count
the HUD's pin toggle reads").

Run: `git rm internal/web/snapshot_test.go`

- [ ] **Step 5: Add the two test helpers**

Add to `internal/web/api_test.go`, right after the `doJSON` and `runResult`
block:

```go
// snapshotOf builds the payload the gateway sends as the first frame of a
// stream. GET /api/snapshot served the same payload until M0 removed it.
func snapshotOf(t *testing.T, g *Gateway, sessionID string) Snapshot {
	t.Helper()
	return g.buildSnapshot(t.Context(), sessionID)
}

// snapshotJSON is snapshotOf as JSON, for the tests that assert a secret
// never reaches a tab.
func snapshotJSON(t *testing.T, g *Gateway, sessionID string) string {
	t.Helper()
	raw, err := json.Marshal(snapshotOf(t, g, sessionID))
	require.NoError(t, err)
	return string(raw)
}
```

- [ ] **Step 6: Adapt the tests that called the removed route**

`TestHealthAndSnapshot` in `internal/web/api_test.go`: keep the health
assertions and drop the snapshot ones (they covered the deleted route's
query-parameter resolution). Rename the function and use:

```go
func TestHealth(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/health", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"ok":true}`, string(body))
}
```

`TestSnapshotExposesEveryPendingBatch` (same file): replace

```go
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/snapshot", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var snap struct {
		Questions []struct {
			ID string `json:"id"`
		} `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(body, &snap))
	require.Len(t, snap.Questions, 2)
	require.Equal(t, "b1", snap.Questions[0].ID)
	require.Equal(t, "b2", snap.Questions[1].ID)
```

with

```go
	snap := snapshotOf(t, g, "")
	require.Len(t, snap.Questions, 2)
	var first, second struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(snap.Questions[0], &first))
	require.NoError(t, json.Unmarshal(snap.Questions[1], &second))
	require.Equal(t, "b1", first.ID)
	require.Equal(t, "b2", second.ID)
```

`TestSnapshotReportsAgentReady` (same file): replace

```go
			rsp, data := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/snapshot", "")
			rsp.Body.Close()
			require.Equal(t, http.StatusOK, rsp.StatusCode)
			var snap Snapshot
			require.NoError(t, json.Unmarshal(data, &snap))
			require.Equal(t, tc.want, snap.AgentReady)
```

with

```go
			snap := snapshotOf(t, g, "")
			require.Equal(t, tc.want, snap.AgentReady)
```

`TestControllerReportsInputModeBeforeStart` in
`internal/web/controller_test.go`: replace the `doJSON` call, the status
check and the anonymous-struct decode with

```go
	require.Equal(t, "plan", c.gw.buildSnapshot(t.Context(), "").InputMode)
```

`TestControllerReportsPlanReadyBeforeStart` in the same file: replace its
equivalent block with

```go
	require.Equal(t, "s1", c.gw.buildSnapshot(t.Context(), "").PlanReadySessionID)
```

`TestSnapshotModelInfo` in `internal/web/api_models_test.go`: replace the
first `doJSON` block (through the `json.Unmarshal` into `snap`) with
`snap := snapshotOf(t, g, "")`, and the second one, after
`fake.SetReadyErr(workspace.ErrAgentNotInitialized)`, with
`snap = snapshotOf(t, g, "")`. The assertions stay as they are.

`TestSecureAnswerRefusedForgetsThePrompt` in
`internal/web/api_prompts_test.go`: replace

```go
	rsp, snap := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/snapshot", "")
	rsp.Body.Close()
	require.NotContains(t, string(snap), `"id":"p1"`, "a reload does not restore a ghost prompt")
```

with

```go
	require.NotContains(t, snapshotJSON(t, g, ""), `"id":"p1"`, "a reload does not restore a ghost prompt")
```

`TestSecureCancelRefusedForgetsThePrompt` in the same file: the same
replacement, with `"s6"` in place of `"p1"`.

`TestAPIKeyNeverLogged` in `internal/web/api_signin_test.go`: replace

```go
	rsp, snap := doJSON(t, c, http.MethodGet, g.URL()+"/api/snapshot", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	for _, k := range keys {
		require.NotContains(t, string(snap), k)
	}
```

with

```go
	snap := snapshotJSON(t, g, "")
	for _, k := range keys {
		require.NotContains(t, snap, k)
	}
```

`TestSignInSnapshotCarriesTheState` in the same file: replace its `doJSON`
call, status check and `require.Contains` with

```go
	require.Contains(t, snapshotJSON(t, g, ""), `"signin":{"flow_id":"`+st.FlowID+`"`)
```

`TestSignInImportIsRecorded` in the same file: replace

```go
	rsp, snap := doJSON(t, c, http.MethodGet, g.URL()+"/api/snapshot", "")
	rsp.Body.Close()
	require.Contains(t, string(snap), `"signin":{"flow_id":"`+st.FlowID+`"`)
```

with

```go
	require.Contains(t, snapshotJSON(t, g, ""), `"signin":{"flow_id":"`+st.FlowID+`"`)
```

`TestTeamFramesKeepLastGoodRuns` in `internal/web/team_test.go`: replace

```go
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/snapshot", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var later Snapshot
	require.NoError(t, json.Unmarshal(body, &later))
	require.Len(t, later.AgentRuns, 1)
```

with

```go
	later := snapshotOf(t, g, "s1")
	require.Len(t, later.AgentRuns, 1)
```

`TestSeedM3FeedsTheRoutes` in `internal/web/seed_m3_test.go`: replace

```go
	var snap Snapshot
	get("/api/snapshot", &snap)
```

with

```go
	snap := g.buildSnapshot(t.Context(), "s1")
```

- [ ] **Step 7: Run the gateway suite**

Run: `go test ./internal/web/ -count=1`
Expected: PASS, including `TestSnapshotRouteIsGone`. If a file now has an
unused import, the compiler names it with `imported and not used`; delete
only the import it names. `controller_test.go` keeps `encoding/json` (its
session test still uses it) and `api_prompts_test.go` never imported it.

- [ ] **Step 8: Prove no reference to the route is left**

Run: `grep -rn '/api/snapshot' --include=*.go . ; grep -rn 'RootSessions\|rootSessionCount' --include=*.go .`
Expected: no output from either command.

- [ ] **Step 9: Format, vet and build**

```bash
gofumpt -w internal/web
go vet ./internal/web/...
go build ./...
```

Expected: `gofumpt` prints nothing, `go vet` and `go build` print nothing.

- [ ] **Step 10: Commit**

```bash
git add internal/web
git commit -m "refactor(web): drop GET /api/snapshot and the pin counter"
```

---

## Task 4: Remove options.web_display and the display plumbing

**Files:**
- Modify: `internal/web/api_config.go` (keep only `handleConfigGet`)
- Modify: `internal/web/gateway.go` (`Gateway` struct, `Start`)
- Modify: `internal/web/stream.go` (`frameDisplayChanged`)
- Modify: `internal/web/snapshot.go` (`Snapshot.Display`)
- Modify: `internal/web/api_systems.go` (`registerSystemRoutes`)
- Modify: `internal/web/auth_test.go` (`TestEveryRouteRequiresTheToken`)
- Modify: `internal/config/config.go` (`WebDisplayOptions`,
  `Options.WebDisplay`)
- Modify: `schema.json` (regenerated)
- Modify: `internal/web/webtest/seed_m4b.go`, `seed_m4b_test.go`
- Test: `internal/web/api_config_test.go` (rewritten, 8 tests deleted)
- Test: `internal/config/schema_test.go` (new),
  `internal/config/options_web_display_test.go` (new)

**Interfaces:**
- Consumes: nothing from Task 3 (the rewritten `api_config_test.go` below
  needs no snapshot payload).
- Produces: `GET /api/config` answers `{"agent_teams": <bool>}` and nothing
  else. `config.Options` has no `WebDisplay` field. `internal/web` has no
  `DisplaySettings` type, no `frameDisplayChanged` constant, no display
  mutex and no config write routes. `webtest.SeedM4b` no longer reads
  `WEBFAKE_THEME`.

- [ ] **Step 1: Write the failing config-schema test**

Create `internal/config/schema_test.go`:

```go
package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSchemaHasNoWebDisplay pins the committed schema.json to the structs:
// options.web_display was removed with the old web UI, so the generated
// schema must not offer it. Regenerate with task schema after a change.
func TestSchemaHasNoWebDisplay(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../schema.json")
	require.NoError(t, err)
	require.NotContains(t, string(raw), "web_display")
	require.NotContains(t, string(raw), "WebDisplayOptions")
}
```

- [ ] **Step 2: Write the config-load test**

Create `internal/config/options_web_display_test.go`:

```go
package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOptionsIgnoreARemovedWebDisplayBlock pins what happens to a config
// written before M0: the block is unknown after the field is gone, and the
// loader must read the rest of the file without an error.
func TestOptionsIgnoreARemovedWebDisplayBlock(t *testing.T) {
	t.Parallel()

	raw := `{"options":{"web":true,"web_display":{"theme":"solid","motion":"off","low_power":true,"cinematic_hint":false}}}`
	var opts Options
	require.NoError(t, json.Unmarshal([]byte(raw), &opts))
	require.True(t, opts.Web)
}
```

This one passes before the change too: `encoding/json` ignores unknown
fields, and the test exists to keep it that way, because a future strict
decoder would break every user who ever opened the old display settings
dialog. Do not weaken it to force a red.

- [ ] **Step 3: Write the failing gateway tests**

Rewrite `internal/web/api_config_test.go` to this content. It deletes eight
tests and adapts the ninth:

- Deleted, because their subject is gone with the write routes and the
  display payload: `TestConfigSetWritesAnAllowedKey`,
  `TestConfigSetRefusesOtherKeys`, `TestConfigSetValidatesValues`,
  `TestConfigRemoveRestoresTheDefault`,
  `TestConfigWriteFailureKeepsTheSettings`,
  `TestSnapshotCarriesDisplayFromConfig`,
  `TestConfigRemoveAnUnsetKeySucceeds` and `TestConfigSetsAreSerialised`.
- Kept and adapted to the payload that is left:
  `TestConfigGetAnswersDisplayAndAgentTeams` becomes
  `TestConfigGetAnswersAgentTeams`, plus the new
  `TestConfigWriteRoutesAreGone` below.

The rewritten file:

```go
package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

func TestConfigGetAnswersAgentTeams(t *testing.T) {
	t.Parallel()
	fake := webtest.NewFake()
	off := false
	fake.Cfg = &config.Config{Options: &config.Options{AgentTeams: &off}}
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/config", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"agent_teams":false}`, string(body))
}

// TestConfigWriteRoutesAreGone pins the M0 removal of the display write
// routes: a cached tab gets a 404, not the old key_not_allowed error.
func TestConfigWriteRoutesAreGone(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := authedClient(t, g)
	for _, path := range []string{"/api/config/set", "/api/config/remove"} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+path,
			`{"key":"options.web_display.theme","value":"solid"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusNotFound, rsp.StatusCode, path)
		require.NotContains(t, string(body), "key_not_allowed", path)
	}
}
```

- [ ] **Step 4: Run the new and rewritten tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestSchemaHasNoWebDisplay|TestOptionsIgnoreARemovedWebDisplayBlock' -count=1`
Expected: `TestSchemaHasNoWebDisplay` FAILS with `"web_display" should not
be contained`; `TestOptionsIgnoreARemovedWebDisplayBlock` passes, as Step 2
explains.

Run: `go test ./internal/web/ -run 'TestConfigGet|TestConfigWriteRoutesAreGone' -count=1`
Expected: both FAIL. `TestConfigGetAnswersAgentTeams` gets
`{"display":{…},"agent_teams":false}`; `TestConfigWriteRoutesAreGone` gets a
200 because the routes still exist and still accept
`options.web_display.theme`.

- [ ] **Step 5: Cut the display half out of internal/web/api_config.go**

Replace the whole file with:

```go
package web

import "net/http"

// handleConfigGet answers the settings a tab renders at start. The tab's
// display settings lived here until the web UI rebuild; every one of them
// went with the old UI, and only the Agent Teams toggle is left.
func (g *Gateway) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	teams := false
	if cfg := g.ws.Config(); cfg != nil {
		teams = cfg.Options.GetAgentTeams()
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent_teams": teams})
}
```

This deletes `DisplaySettings`, `displayDefaults`, `displayFromConfig`,
`enumString`, `boolValue`, `displayKeys`, `applyDisplay`, `displayDefault`,
`currentDisplay`, `handleConfigSet` and `handleConfigRemove`. Nothing else
used those symbols: `grep -rn 'enumString\|boolValue' --include=*.go
internal/web` must print nothing after the edit.

- [ ] **Step 6: Unregister the two write routes**

In `internal/web/api_systems.go`, delete these two lines from
`registerSystemRoutes`:

```go
	g.route(api, "POST /api/config/set", g.handleConfigSet)
	g.route(api, "POST /api/config/remove", g.handleConfigRemove)
```

Then delete the two matching entries from the `want` slice of
`TestEveryRouteRequiresTheToken` in `internal/web/auth_test.go`:

```go
		"POST /api/config/set",
		"POST /api/config/remove",
```

`"GET /api/config"` stays in that slice.

- [ ] **Step 7: Remove the display state from the gateway**

In `internal/web/gateway.go`:

1. Delete the `displayMu sync.Mutex` and `display DisplaySettings` fields
   and their comment block from the `Gateway` struct.
2. Delete these two lines from the `Start` struct literal:

```go
		// The settings the tab starts from are the ones in the config.
		display: displayFromConfig(opts.Workspace.Config()),
```

- [ ] **Step 8: Remove the frame constant and the snapshot field**

In `internal/web/stream.go`, delete from the `const` block:

```go
	// frameDisplayChanged carries the tab's display settings after every
	// successful config write.
	frameDisplayChanged = "display_changed"
```

In `internal/web/snapshot.go`, delete the `Display DisplaySettings` field
(JSON tag `display`) from `Snapshot`, and the `Display: g.currentDisplay(),`
line from the `buildSnapshot` literal.

- [ ] **Step 9: Remove the option from the config**

In `internal/config/config.go`:

1. Delete the `WebDisplayOptions` type and its three-line comment.
2. Delete the `WebDisplay *WebDisplayOptions` line from `Options`.

- [ ] **Step 10: Regenerate the schema**

Run: `go run main.go schema > schema.json`
(`task schema` runs the same command.)

Expected: `git diff --stat schema.json` shows `schema.json | 38 +---…`,
removing the `"web_display"` property from `Options` and the whole
`"WebDisplayOptions"` definition under `$defs`. No other key may change; if
the diff touches anything else, the working tree was dirty before the
regeneration.

- [ ] **Step 11: Drop the fake's theme seeding**

In `internal/web/webtest/seed_m4b.go`:

1. Delete the sentence about the Holodeck theme from `SeedM4b`'s doc
   comment, keeping the rest.
2. Delete the `theme, err := m4bTheme()` block, the
   `if f.Cfg.Options == nil { … }` block and the
   `f.Cfg.Options.WebDisplay = &config.WebDisplayOptions{Theme: theme}`
   line. The `err` variable is still declared by the `SeedM4` call above.
3. Delete the whole `m4bTheme` function.
4. Delete the now-unused `github.com/charmbracelet/crush/internal/config`
   import: after the edit, `grep -n 'config\.'
   internal/web/webtest/seed_m4b.go` prints nothing.

In `internal/web/webtest/seed_m4b_test.go`:

1. Delete `t.Setenv("WEBFAKE_THEME", "")` from
   `TestSeedM4bKeepsTheM4Seams`, `TestSeedM4bHonoursTheModelName`,
   `TestSeedM4bHonoursTheHudServers` and
   `TestSeedM4bRefusesAnUnknownHudServers`. Keep the `t.Setenv` calls for
   `WEBFAKE_MODEL_NAME` and `WEBFAKE_HUD_SERVERS`.
2. Delete the four lines at the end of `TestSeedM4bKeepsTheM4Seams`:

```go
	// The tab starts on the Holodeck theme.
	require.NotNil(t, cfg.Options)
	require.NotNil(t, cfg.Options.WebDisplay)
	require.Equal(t, "holodeck", cfg.Options.WebDisplay.Theme)
```

3. Delete `TestSeedM4bHonoursTheTheme` and
   `TestSeedM4bRefusesAnUnknownTheme` outright: both cover `m4bTheme`, and
   the first reads `cfg.Options.WebDisplay`.
4. Replace the file's leading comment

```go
// The tests below set WEBFAKE_THEME, which t.Setenv forbids in a parallel
// test, so none of them run in parallel.
```

with

```go
// The tests below set WEBFAKE_* variables, which t.Setenv forbids in a
// parallel test, so they do not run in parallel.
```

- [ ] **Step 12: Run the affected packages**

```bash
go build ./...
go test ./internal/config/ -count=1
go test ./internal/web/... -count=1
go test ./internal/ui/... -count=1
```

Expected: all PASS. `internal/ui` is in the list because the TUI owns the
palette entries and the `WEB` badge; nothing there should change, and a
failure means the removal reached further than planned.

- [ ] **Step 13: Prove the symbols are gone**

Run: `grep -rn 'web_display\|WebDisplay\|display_changed\|frameDisplayChanged\|DisplaySettings' --include=*.go --include=*.json --include=*.md . | grep -v '^./docs/superpowers/'`
Expected: every hit is in `docs/web-mode.md`: the prose note Task 2 added
plus the stale "Display settings" section, which stays until M6 rewrites the
document. No hit may be in a `.go` or `.json` file, and `schema.json` must
contain neither `web_display` nor `WebDisplayOptions`.

- [ ] **Step 14: Verify the webfake's M4b scenario still starts**

```bash
WEBFAKE_PROJECT_DIR=/tmp/crush-m0-check WEBFAKE_SCENARIO=m4b go run ./internal/web/cmd/webfake > /tmp/webfake-url.txt 2>/tmp/webfake-err.txt &
sleep 5
head -1 /tmp/webfake-url.txt
cat /tmp/webfake-err.txt
kill %1
```

Expected: a launch URL and no `webfake:` error line.

- [ ] **Step 15: Format, vet and commit**

```bash
gofumpt -w internal/web internal/config
go vet ./internal/web/... ./internal/config/...
git add -A
git commit -m "refactor(config): remove options.web_display and its plumbing"
```

---

## Task 5: Whole-suite verification

No commit: this task either passes or sends the work back to the task that
broke it. Run it after the four PRs have merged, from a clean checkout of
`main`.

**Files:** none.

**Interfaces:**
- Consumes: every task above.
- Produces: a verified M0 on `main`, and the baseline M1 builds on.

- [ ] **Step 1: Run the build and the test suites the milestone touches**

```bash
go build ./...
go test ./internal/web/... ./internal/ui/... ./internal/config/... ./internal/wire/... -count=1
```

Expected: the build prints nothing; every package prints `ok`. `internal/ui`
is slow; run it once, not per task.

- [ ] **Step 2: Run vet and the repository-wide formatter check**

```bash
go vet ./...
gofumpt -l .
```

Expected: no output from either. `task fmt` runs `gofumpt -w .`, so
`gofumpt -l .` is the check that the formatter would change nothing.

- [ ] **Step 3: Run the log-capitalization script and the linter if present**

```bash
./scripts/check_log_capitalization.sh
if command -v task >/dev/null; then task lint; else echo "task not installed; the log check is the lint run"; fi
```

Expected: the script prints nothing and exits 0. `task lint` also runs
`golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m`; if
you want that check and `golangci-lint` is missing, install it first with
`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`.

- [ ] **Step 4: Verify the placeholder through the webfake one last time**

```bash
WEBFAKE_PROJECT_DIR=/tmp/crush-m0-check go run ./internal/web/cmd/webfake > /tmp/webfake-url.txt 2>/tmp/webfake-err.txt &
sleep 5
url=$(head -1 /tmp/webfake-url.txt)
origin=${url%%/#t=*}
curl -sS "$origin/" | grep -c 'being rebuilt'
curl -sS -o /dev/null -w '%{http_code}\n' "$origin/"
curl -sS -o /dev/null -w '%{http_code}\n' "$origin/sessions/s1"
curl -sS -o /dev/null -w '%{http_code}\n' "$origin/assets/index-43JjnKFV.js"
kill %1
```

Expected: `1` (the heading appears once), then `200`, `200`, `404`.

- [ ] **Step 5: Confirm the deleted surface is really gone**

```bash
test ! -d web && echo "web/ deleted"
git ls-files internal/web/dist
grep -rn '/api/snapshot\|/api/config/set\|/api/config/remove' --include=*.go internal/ || echo "routes gone"
```

Expected: `web/ deleted`; `internal/web/dist` lists exactly three files
(`favicon.svg`, `index.html`, `style.css`); `routes gone`.

- [ ] **Step 6: Sanity-check the TUI side by hand**

Run `go run . --web`, then confirm the browser tab shows the placeholder
page, the status bar shows the `WEB` badge, `/web` in the running session
prints and copies the link, and `Ctrl+P` → **Stop Web Mode** stops the
gateway and leaves the tab saying web mode stopped. The tests cover the
controller, the badge and the palette; this step covers what only a human
sees.

---

### Notes for whoever executes this

- The spec's "Cleanup in M0" mentions `internal/ui/model/plan_handoff_web_test.go`
  as an example. It does not break: it drives the controller and the plan
  handoff callback, neither of which changes here. Do not edit it.
- `internal/ui/model/web_action_test.go`, `web_stopped_test.go`,
  `status_web_test.go` and `plan_handoff_web_guards_test.go` cover the TUI's
  own web plumbing and keep passing untouched.
- If a reviewer wants the wire fixture contract test back before M1, the
  cheap version is a Go golden file under `internal/web/testdata/` fed by
  the same `fixtureEvents` map. This plan leaves it out because the map only
  existed to feed the deleted client, and M1 replaces it with the zod
  contract the spec describes.
- `internal/wire` is untouched by every task; it is in the verification
  commands because CI runs `go test ./internal/web/... ./internal/wire/...`.
