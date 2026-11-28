# Web UI M2 (Chat) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn M1's read-only plain-text log into the DSH chat: safe
Markdown with lazy Shiki highlighting and lazy KaTeX math, streamed
assistant replies that the log follows in place, reasoning previews,
nested tool-call trees with one view per tool type (bash, view,
edit/diff, grep/glob, fetch, todos, agent/subagent, MCP), a usage footer
with a context ring, and a virtualized message list.

**Architecture:** Everything lands in `web/` except two small Go changes:
one golden-frame fixture (a tool-result message in the snapshot example)
and the webfake M2 scenario. The gateway already sends everything M2
draws: message parts carry the tool calls, results, finish reason and
model, and the session entity carries `prompt_tokens`,
`completion_tokens` and `cost`, so no gateway field is added. The
frontend gains three directories the spec names: `web/src/chat/` (parts
model, message view, markdown, usage), `web/src/tools/` (tool-call tree,
registry, per-tool views) and the virtualized log in
`web/src/conversation/`. Shiki and KaTeX are dynamic imports, so first
paint stays under budget, and both render without a single inline style
or `innerHTML`.

**Tech Stack:** React 18.3, Vite 6, TypeScript strict, CSS Modules,
`clsx`, `zod` 4, Vitest 4 with Testing Library and jsdom, Playwright with
`@axe-core/playwright`. New runtime dependencies, versions checked against
the npm registry on 2026-11-27: `mdast-util-from-markdown` 2.1.0,
`mdast-util-gfm` 3.1.0, `micromark-extension-gfm` 3.0.0,
`mdast-util-math` 3.0.0, `micromark-extension-math` 3.1.0,
`micromark-util-sanitize-uri` 2.0.1, `shiki` 4.6.0, `@shikijs/langs`
4.6.0, `katex` 0.19.1 (ships its own types), `diff` 9.0.0 (ships its own
types), `@tanstack/react-virtual` 3.14.28 (peer `react ^18`, pulls
`@tanstack/virtual-core` 3.19.13). New dev dependency: `@types/mdast`
4.0.4. Go 1.27.4 and testify for the two Go tasks.

**Spec:** `docs/superpowers/specs/2026-11-27-web-ui-dsh-design.md`. Read
"Milestones" (the M2 row), "Frontend structure" (`chat/`, `tools/`,
`conversation/`), "Porting rule", "Bundle budget" and "Testing" before
Task 1. The M1 plan (`docs/superpowers/plans/2026-11-27-web-ui-m1-shell.md`)
is the state this plan starts from and shows the conventions it follows.

Every file, symbol and test name below was checked against `origin/main`
at `d3bf42b42` (2026-11-27, after every M1 merge). The plan names code by
symbol, never by line number.

Date: 2026-11-27.

### Execution order and PR grouping

Each task is one PR of roughly 400 changed lines or less, excluding
`web/package-lock.json`, the committed `internal/web/dist` build output
and `internal/web/testdata/frames.golden.json`. The PRs merge in order;
every branch starts from `origin/main` after `git fetch origin`, once its
predecessor has merged. Tasks 6 to 11 all add entries to
`web/src/tools/registry.ts` and Tasks 7, 12 and 13 all rewrite
`web/src/conversation/Conversation.tsx`, so out-of-order or parallel work
guarantees a conflict.

Every frontend task ends by rebuilding the committed bundle
(`npm run build` in `web/`) and committing `internal/web/dist` with it,
because CI fails on a stale build.

| PR | Task | Branch | Scope |
|---|---|---|---|
| 1 | Task 1 | `crush/web-ui-m2-1-parts` | Typed message parts, tool status, the tool-result golden |
| 2 | Task 2 | `crush/web-ui-m2-2-pages` | Older pages in the store, generation counter, child sessions |
| 3 | Task 3 | `crush/web-ui-m2-3-markdown` | Safe micromark/mdast renderer |
| 4 | Task 4 | `crush/web-ui-m2-4-shiki` | Lazy Shiki code blocks, class-based tokens, budget markers |
| 5 | Task 5 | `crush/web-ui-m2-5-katex` | Lazy KaTeX as sanitized MathML |
| 6 | Task 6 | `crush/web-ui-m2-6-tool-tree` | Tool-call row, registry, generic view |
| 7 | Task 7 | `crush/web-ui-m2-7-messages` | Message view: parts in order, reasoning, notices, copy |
| 8 | Task 8 | `crush/web-ui-m2-8-bash` | Bash terminal card with ANSI and the exit-code capsule |
| 9 | Task 9 | `crush/web-ui-m2-9-files` | View, edit, multiedit and write with diffs |
| 10 | Task 10 | `crush/web-ui-m2-10-search` | Grep/glob/ls, fetch, todos and MCP views |
| 11 | Task 11 | `crush/web-ui-m2-11-subagent` | Agent and subagent nesting |
| 12 | Task 12 | `crush/web-ui-m2-12-usage` | Turn footer, usage summary and context ring |
| 13 | Task 13 | `crush/web-ui-m2-13-virtual` | Virtualized log, follow in place, anchored paging |
| 14 | Task 14 | `crush/web-ui-m2-14-webfake` | webfake M2 scenario |
| 15 | Task 15 | `crush/web-ui-m2-15-e2e` | Playwright chat spec, CSP listener, axe |
| 16 | Task 16 | (no branch) | Whole-branch verification |

## Global Constraints

- Frontend stack is unchanged from M1: React 18, Vite 6, TypeScript
  strict with `noUnusedLocals` and `noUnusedParameters`, CSS Modules plus
  `clsx`, no component library, `zod` at the wire boundary, no `zustand`
  or `redux`. Node from `web/.nvmrc` (`26`).
- CSP stays `default-src 'self'; style-src 'self'; connect-src 'self'`.
  No inline `<script>`, no `style="…"` attribute in any HTML string, no
  `innerHTML`/`dangerouslySetInnerHTML` anywhere in `web/src`, no
  `eval`/`new Function` (zod runs `jitless`), no WebAssembly (Shiki uses
  its JavaScript regex engine, never Oniguruma WASM). Colors and themes
  come from classes; a React `style` prop is allowed only for computed
  geometry (the virtualizer's offsets), because React writes it through
  the CSSOM, which the CSP does not restrict.
- Markdown never renders raw HTML: an mdast `html` node is shown as its
  source text. Links render only for absolute `http:`, `https:` and
  `mailto:` URLs, with `target="_blank" rel="noopener noreferrer"`.
  Images never load (the CSP's `img-src` is `self`): they render as a
  link to their URL.
- First-paint JavaScript stays under 600 KiB gzipped (`web/scripts/budget.mjs`
  in CI). Shiki (core, engine and every grammar) and KaTeX load only
  through dynamic `import()`, and `budget.mjs` fails if their marker
  strings appear in a first-paint chunk.
- No bundled fonts: no `@font-face`, no `.woff`/`.woff2`, no `katex.css`.
  KaTeX renders MathML only, which uses the browser's math fonts.
- DSH porting rule: every lifted file starts with a comment naming its
  DSH source path and the pinned commit
  `4b399c39a6d1479cd84ca591b4d7c88e24d38a96`, and lists what changed;
  `web/THIRD_PARTY_NOTICES.md` names every lifted file. The pinned clone
  is `/tmp/dsh-research/repo` (read only, never run). Only leaf pieces are
  lifted: the Markdown grammar setup, the Shiki highlighter core with its
  Go grammar patch, and the Shiki token palette.
- The gateway wire is unchanged: no new frame type and no new field. The
  only Go changes are the golden fixture (Task 1) and webfake (Task 14).
- Subagent sessions have ids `<message id>$$<tool call id>`
  (`session.Service.CreateAgentToolSessionID`) and a non-empty
  `parent_session_id`. They never appear in the sidebar.
- The tab subscribes the selected session, then every non-idle session,
  then every subagent session an open tool row watches, capped at 32.
- Tool status vocabulary is exactly `running`, `done`, `error`,
  `interrupted`; the capsule labels are `Running`, `Done`, `Failed`,
  `Interrupted`.
- Comments: own line, capital letter, period, wrapped at 78 columns
  (`AGENTS.md`); log messages start with a capital letter. Format Go with
  `gofumpt -w .`. Commits are one line, semantic, at most 72 characters,
  with no attribution and no `Co-authored-by:` trailer.
- `gofumpt` is installed on this host; `task` and `golangci-lint` are
  not. Run `go vet ./...` in their place.

## Review Focus

1. A reply that streams in place while the reader sits at the end of the
   log (the newest message id does not change, its height grows) must
   keep the end in view; the same stream while the reader has scrolled up
   must not move the view at all. Pinned by the pure tests in
   `web/src/conversation/scroll.test.ts` and the "follows" and "stays"
   cases of `web/src/conversation/Conversation.test.tsx` (Task 13), and
   by the two streaming cases of `web/e2e/chat.spec.ts` (Task 15).
2. Assistant-authored Markdown carrying raw HTML (`<img onerror>`,
   `<script>`), a `javascript:` link or a remote image must render inert
   text and fire no CSP violation. Pinned by the "inert" cases of
   `web/src/chat/markdown/Markdown.test.tsx` (Task 3) and the console
   CSP listener of `web/e2e/chat.spec.ts` (Task 15).
3. A load-older request in flight across a resync, and a live edit or
   delete of a message that lives in a loaded older page, must neither
   resurrect stale history nor leave a stale copy. Pinned by the
   "generation" and "older page" cases of `web/src/store/app.test.ts`
   (Task 2).
4. A tool call left with no result once its turn is over (cancel, crash,
   the next prompt) must read `Interrupted`, never `Running` forever.
   Pinned by `web/src/chat/toolStatus.test.ts` (Task 1) and the
   "interrupted" case of `web/src/chat/MessageView.test.tsx` (Task 7).
5. A code fence whose label is assistant-authored nonsense
   (`constructor`, `__proto__`, an unknown language) or whose grammar
   fails to load must render plain monospace text, never throw. Pinned by
   the "unknown label" cases of `web/src/chat/markdown/CodeBlock.test.tsx`
   and `languages.test.ts` (Task 4).

---

## File map

`web/src/chat/`:

- `parts.ts`: zod part schemas, `messageParts`, `finishOf`,
  `resultIndex`, `resultSignature` (Task 1).
- `toolStatus.ts`: `toolStatus`, `isTurnActive` (Task 1).
- `format.ts`: `formatDuration`, `formatTokens`, `formatCost` (Tasks 7
  and 12).
- `CopyButton.tsx` (Task 4), `MessageView.tsx`, `segments.ts`,
  `Reasoning.tsx`, `TurnNotice.tsx` (Task 7), `Transcript.tsx` (Task 11),
  `turns.ts`, `TurnFooter.tsx`, `UsageSummary.tsx`, `ContextRing.tsx`
  (Task 12).
- `markdown/parse.ts`, `links.ts`, `Markdown.tsx`, `CodeBlock.tsx`,
  `Math.tsx` (Task 3); `languages.ts`, `highlighter.ts`,
  `useHighlight.ts` (Task 4); `MathView.tsx`, `mathml.ts` (Task 5).

`web/src/tools/`:

- `input.ts`, `environment.tsx`, `registry.ts`, `ToolCall.tsx`,
  `StatusCapsule.tsx`, `OutputBlock.tsx`, `views/GenericView.tsx`
  (Task 6); `ansi.ts`, `Terminal.tsx`, `ExitCode.tsx`,
  `views/BashView.tsx` (Task 8); `diff.ts`, `DiffView.tsx`,
  `views/FileViews.tsx` (Task 9); `views/SearchView.tsx`,
  `views/FetchView.tsx`, `views/TodosView.tsx`, `views/McpView.tsx`
  (Task 10); `views/AgentView.tsx` (Task 11).

`web/src/conversation/`: `Conversation.tsx` (Tasks 7, 12, 13),
`ConversationContainer.tsx` (Tasks 2, 7, 11, 12), `scroll.ts` (Task 13).
`MessageView.tsx`, `MessageView.module.css`, `MessageView.test.tsx` and
`ToolCallRow.tsx` move out in Task 7.

`web/src/store/app.ts` (Task 2), `web/src/api/wire.ts` (Task 1),
`web/src/theme/shiki.css` (Task 4), `web/src/test/layout.ts` (Task 13),
`web/scripts/budget.mjs` (Tasks 4 and 5).

Go: `internal/web/frames_test.go` and the golden (Task 1);
`internal/web/webtest/seed_m2.go`, `seed_m2_test.go`, `fake.go`,
`internal/web/cmd/webfake/main.go` (Task 14).

e2e: `web/e2e/support/webfake.ts`, `web/e2e/chat.spec.ts`,
`web/e2e/accessibility.spec.ts` (Task 15).

---

## Task 1: Typed message parts, tool status and the tool-result golden

M1 read parts as `unknown` and grouped them by type, which loses the
order a message was written in. This task gives the chat one typed,
ordered view of a message's parts, and one rule for a tool call's status.

**Files:**
- Create: `web/src/chat/parts.ts`, `web/src/chat/parts.test.ts`
- Create: `web/src/chat/toolStatus.ts`, `web/src/chat/toolStatus.test.ts`
- Modify: `web/src/api/wire.ts` (`z.config`, `messageSchema`,
  `sessionSchema`)
- Modify: `web/src/api/wire.golden.test.ts` (parts of every golden
  message parse)
- Modify: `internal/web/frames_test.go` (`gatewayFrames`: the snapshot
  example gains a tool message; new `fixtureToolMessage`)
- Modify: `internal/web/testdata/frames.golden.json` (regenerated)

**Interfaces:**
- Consumes: `WireMessage` from `web/src/api/wire.ts`; `SessionStatus`
  from the same file.
- Produces (`web/src/chat/parts.ts`):
  - `interface ToolCallPart { id: string; name: string; input: string; finished: boolean }`
  - `interface ToolResultPart { toolCallId: string; name: string; content: string; mimeType: string; metadata: string; isError: boolean }`
  - `interface FinishPart { reason: string; time: number; message: string; details: string }`
  - `type MessagePart` (union on `kind`: `text`, `reasoning`,
    `tool_call`, `tool_result`, `finish`, `shell_command`,
    `sub_agent_report`, `compaction`, `attachment`; fields below)
  - `parsePart(type: string, data: unknown): MessagePart | null`
  - `messageParts(message: WireMessage): MessagePart[]` (cached per
    message object)
  - `finishOf(parts: MessagePart[]): FinishPart | null`
  - `resultIndex(messages: WireMessage[]): Map<string, ToolResultPart>`
  - `resultSignature(messages: WireMessage[]): string`
- Produces (`web/src/chat/toolStatus.ts`):
  - `type ToolStatus = 'running' | 'done' | 'error' | 'interrupted'`
  - `toolStatus(result: ToolResultPart | undefined, finish: FinishPart | null, turnActive: boolean): ToolStatus`
  - `isTurnActive(status: SessionStatus | undefined): boolean`
  - `interruptedResultPrefix = 'tool call was interrupted'`
- Produces (`web/src/api/wire.ts`): `WireMessage` gains optional
  `model` and `provider`; `SessionRow` gains optional `prompt_tokens`,
  `completion_tokens` and `cost`.

- [ ] **Step 1: Write the failing parts tests**

`web/src/chat/parts.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import type { WireMessage } from '../api/wire'
import { finishOf, messageParts, parsePart, resultIndex, resultSignature } from './parts'

function message(parts: WireMessage['parts'], id = 'm1'): WireMessage {
  return { id, role: 'assistant', session_id: 's1', parts, created_at: 1, updated_at: 2 }
}

describe('messageParts', () => {
  it('keeps the order the message was written in', () => {
    const parts = messageParts(
      message([
        { type: 'text', data: { text: 'First I read it.' } },
        { type: 'tool_call', data: { id: 'tc-1', name: 'view', input: '{}' } },
        { type: 'text', data: { text: 'Then I changed it.' } },
        { type: 'reasoning', data: { thinking: 'Late thought.' } },
      ]),
    )
    expect(parts.map((part) => part.kind)).toEqual(['text', 'tool_call', 'text', 'reasoning'])
  })

  it('drops hidden text, unknown types and malformed parts', () => {
    const parts = messageParts(
      message([
        { type: 'text', data: { text: 'model only', hidden: true } },
        { type: 'not_a_part', data: {} },
        { type: 'tool_call', data: { name: 'bash' } },
        { type: 'text', data: { text: 'shown' } },
      ]),
    )
    expect(parts).toEqual([{ kind: 'text', text: 'shown' }])
  })

  it('answers the same array for the same message object', () => {
    const same = message([{ type: 'text', data: { text: 'hi' } }])
    expect(messageParts(same)).toBe(messageParts(same))
  })

  it('reads a tool result with its defaults', () => {
    expect(parsePart('tool_result', { tool_call_id: 'tc-1', content: 'ok' })).toEqual({
      kind: 'tool_result',
      result: { toolCallId: 'tc-1', name: '', content: 'ok', mimeType: '', metadata: '', isError: false },
    })
  })

  it('names an attachment by its path, then its type', () => {
    expect(parsePart('binary', { Path: '/tmp/shot.png', MIMEType: 'image/png' })).toEqual({
      kind: 'attachment',
      name: 'shot.png',
      mimeType: 'image/png',
    })
    expect(parsePart('image_url', { url: 'https://example.com/a.png' })).toEqual({
      kind: 'attachment',
      name: 'a.png',
      mimeType: 'image',
    })
  })
})

describe('finishOf', () => {
  it('answers the last finish part', () => {
    const parts = messageParts(message([{ type: 'finish', data: { reason: 'canceled', time: 9 } }]))
    expect(finishOf(parts)).toEqual({ reason: 'canceled', time: 9, message: '', details: '' })
    expect(finishOf([])).toBeNull()
  })
})

describe('resultIndex', () => {
  const answered = [
    message([{ type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{}' } }], 'a1'),
    message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok', is_error: true } }], 't1'),
  ]

  it('finds a result in a later message', () => {
    expect(resultIndex(answered).get('tc-1')?.isError).toBe(true)
  })

  it('changes its signature only when a result changes', () => {
    const before = resultSignature(answered)
    const token = [...answered, message([{ type: 'text', data: { text: 'more' } }], 'a2')]
    expect(resultSignature(token)).toBe(before)
    const another = [...answered, message([{ type: 'tool_result', data: { tool_call_id: 'tc-2' } }], 't2')]
    expect(resultSignature(another)).not.toBe(before)
  })
})
```

- [ ] **Step 2: Write the failing status tests**

`web/src/chat/toolStatus.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import type { FinishPart, ToolResultPart } from './parts'
import { interruptedResultPrefix, isTurnActive, toolStatus } from './toolStatus'

function result(overrides: Partial<ToolResultPart> = {}): ToolResultPart {
  return { toolCallId: 'tc-1', name: 'bash', content: 'ok', mimeType: '', metadata: '', isError: false, ...overrides }
}

function finish(reason: string): FinishPart {
  return { reason, time: 1, message: '', details: '' }
}

describe('toolStatus', () => {
  it('is done when a result exists, and failed when it is an error', () => {
    expect(toolStatus(result(), null, true)).toBe('done')
    expect(toolStatus(result({ isError: true, content: 'boom' }), null, true)).toBe('error')
  })

  it('reads the settle sentence and the interrupted flag as interrupted', () => {
    expect(toolStatus(result({ isError: true, content: `${interruptedResultPrefix} and did not…` }), null, false)).toBe(
      'interrupted',
    )
    expect(toolStatus(result({ metadata: '{"interrupted":true}' }), null, false)).toBe('interrupted')
  })

  it('runs only while its turn is live', () => {
    expect(toolStatus(undefined, null, true)).toBe('running')
    expect(toolStatus(undefined, finish('tool_use'), true)).toBe('running')
    expect(toolStatus(undefined, null, false)).toBe('interrupted')
  })

  it('is interrupted by a canceled or failed turn even while the session runs', () => {
    expect(toolStatus(undefined, finish('canceled'), true)).toBe('interrupted')
    expect(toolStatus(undefined, finish('error'), true)).toBe('interrupted')
  })
})

describe('isTurnActive', () => {
  it('counts a running session and one waiting for the person', () => {
    expect(isTurnActive('running')).toBe(true)
    expect(isTurnActive('pending_interaction')).toBe(true)
    expect(isTurnActive('queued')).toBe(false)
    expect(isTurnActive('idle')).toBe(false)
    expect(isTurnActive(undefined)).toBe(false)
  })
})
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd web && npx vitest run src/chat`
Expected: FAIL with `Failed to resolve import "./parts"` and
`Failed to resolve import "./toolStatus"`.

- [ ] **Step 4: Write the parts model**

`web/src/chat/parts.ts`:

```ts
import { z } from 'zod'

import type { WireMessage } from '../api/wire'

// The part schemas pin the fields the chat reads. A part that fails its
// schema is dropped, not thrown: one malformed part must not hide the rest
// of the message, and the frame around it already passed the wire boundary.
const textSchema = z.object({ text: z.string(), hidden: z.boolean().optional() }).passthrough()
const reasoningSchema = z
  .object({
    thinking: z.string(),
    started_at: z.number().optional().default(0),
    finished_at: z.number().optional().default(0),
  })
  .passthrough()
const toolCallSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    input: z.string().optional().default(''),
    finished: z.boolean().optional().default(false),
  })
  .passthrough()
const toolResultSchema = z
  .object({
    tool_call_id: z.string(),
    name: z.string().optional().default(''),
    content: z.string().optional().default(''),
    mime_type: z.string().optional().default(''),
    metadata: z.string().optional().default(''),
    is_error: z.boolean().optional().default(false),
  })
  .passthrough()
const finishSchema = z
  .object({
    reason: z.string(),
    time: z.number().optional().default(0),
    message: z.string().optional().default(''),
    details: z.string().optional().default(''),
  })
  .passthrough()
const shellCommandSchema = z
  .object({ command: z.string(), output: z.string().optional().default(''), exit_code: z.number().optional().default(0) })
  .passthrough()
const subAgentReportSchema = z
  .object({
    label: z.string(),
    output: z.string().optional().default(''),
    failed: z.boolean().optional().default(false),
    model: z.string().optional().default(''),
  })
  .passthrough()
const compactionSchema = z
  .object({ saved_tokens: z.number(), kept_messages: z.number().optional().default(0) })
  .passthrough()
// BinaryContent has no JSON tags on the Go side, so its fields arrive
// capitalised. The gateway strips Data before the frame leaves.
const binarySchema = z
  .object({ Path: z.string().optional().default(''), MIMEType: z.string().optional().default('') })
  .passthrough()
const imageUrlSchema = z.object({ url: z.string() }).passthrough()

export interface ToolCallPart {
  id: string
  name: string
  input: string
  finished: boolean
}

export interface ToolResultPart {
  toolCallId: string
  name: string
  content: string
  mimeType: string
  metadata: string
  isError: boolean
}

export interface FinishPart {
  reason: string
  time: number
  message: string
  details: string
}

export type MessagePart =
  | { kind: 'text'; text: string }
  | { kind: 'reasoning'; thinking: string; startedAt: number; finishedAt: number }
  | { kind: 'tool_call'; call: ToolCallPart }
  | { kind: 'tool_result'; result: ToolResultPart }
  | { kind: 'finish'; finish: FinishPart }
  | { kind: 'shell_command'; command: string; output: string; exitCode: number }
  | { kind: 'sub_agent_report'; label: string; output: string; failed: boolean; model: string }
  | { kind: 'compaction'; savedTokens: number; keptMessages: number }
  | { kind: 'attachment'; name: string; mimeType: string }

// parsePart reads one wire part. It answers null for a type the chat does
// not draw, for hidden text (model history only: the environment snapshot,
// the reminders), and for a part whose fields do not parse.
export function parsePart(type: string, data: unknown): MessagePart | null {
  switch (type) {
    case 'text': {
      const parsed = textSchema.safeParse(data)
      if (!parsed.success || parsed.data.hidden === true) {
        return null
      }
      return { kind: 'text', text: parsed.data.text }
    }
    case 'reasoning': {
      const parsed = reasoningSchema.safeParse(data)
      if (!parsed.success || parsed.data.thinking === '') {
        return null
      }
      return {
        kind: 'reasoning',
        thinking: parsed.data.thinking,
        startedAt: parsed.data.started_at,
        finishedAt: parsed.data.finished_at,
      }
    }
    case 'tool_call': {
      const parsed = toolCallSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      const { id, name, input, finished } = parsed.data
      return { kind: 'tool_call', call: { id, name, input, finished } }
    }
    case 'tool_result': {
      const parsed = toolResultSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      const value = parsed.data
      return {
        kind: 'tool_result',
        result: {
          toolCallId: value.tool_call_id,
          name: value.name,
          content: value.content,
          mimeType: value.mime_type,
          metadata: value.metadata,
          isError: value.is_error,
        },
      }
    }
    case 'finish': {
      const parsed = finishSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      const { reason, time, message, details } = parsed.data
      return { kind: 'finish', finish: { reason, time, message, details } }
    }
    case 'shell_command': {
      const parsed = shellCommandSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      return {
        kind: 'shell_command',
        command: parsed.data.command,
        output: parsed.data.output,
        exitCode: parsed.data.exit_code,
      }
    }
    case 'sub_agent_report': {
      const parsed = subAgentReportSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      const { label, output, failed, model } = parsed.data
      return { kind: 'sub_agent_report', label, output, failed, model }
    }
    case 'compaction_stats': {
      const parsed = compactionSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      return { kind: 'compaction', savedTokens: parsed.data.saved_tokens, keptMessages: parsed.data.kept_messages }
    }
    case 'binary': {
      const parsed = binarySchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      return { kind: 'attachment', name: baseName(parsed.data.Path) || parsed.data.MIMEType, mimeType: parsed.data.MIMEType }
    }
    case 'image_url': {
      const parsed = imageUrlSchema.safeParse(data)
      if (!parsed.success) {
        return null
      }
      return { kind: 'attachment', name: baseName(parsed.data.url) || 'image', mimeType: 'image' }
    }
    default: {
      return null
    }
  }
}

function baseName(path: string): string {
  const trimmed = path.replace(/[?#].*$/, '')
  return trimmed.slice(trimmed.lastIndexOf('/') + 1)
}

// cache keeps one parsed array per message object. A streamed token makes a
// new message object, so a stale entry can never be read, and the WeakMap
// lets the old one go with it.
const cache = new WeakMap<WireMessage, MessagePart[]>()

// messageParts is the message's parts in the order they were written.
export function messageParts(message: WireMessage): MessagePart[] {
  const cached = cache.get(message)
  if (cached !== undefined) {
    return cached
  }
  const out: MessagePart[] = []
  for (const part of message.parts) {
    const parsed = parsePart(part.type, part.data)
    if (parsed !== null) {
      out.push(parsed)
    }
  }
  cache.set(message, out)
  return out
}

export function finishOf(parts: MessagePart[]): FinishPart | null {
  for (let index = parts.length - 1; index >= 0; index -= 1) {
    const part = parts[index]
    if (part.kind === 'finish') {
      return part.finish
    }
  }
  return null
}

// resultIndex maps every tool call id to its result. Results live in their
// own tool messages, after the call, so the whole log is read.
export function resultIndex(messages: WireMessage[]): Map<string, ToolResultPart> {
  const out = new Map<string, ToolResultPart>()
  for (const message of messages) {
    for (const part of messageParts(message)) {
      if (part.kind === 'tool_result') {
        out.set(part.result.toolCallId, part.result)
      }
    }
  }
  return out
}

// resultSignature changes when a result is added, removed or rewritten. A
// view memoises the index on it, so a streamed token, which touches no
// result, keeps the index's identity and the memoised messages with it.
export function resultSignature(messages: WireMessage[]): string {
  const keys: string[] = []
  for (const message of messages) {
    for (const part of messageParts(message)) {
      if (part.kind === 'tool_result') {
        const result = part.result
        keys.push(`${result.toolCallId}:${result.isError ? 1 : 0}:${result.content.length}:${result.metadata.length}`)
      }
    }
  }
  return keys.join(',')
}
```

- [ ] **Step 5: Write the status rule**

`web/src/chat/toolStatus.ts`:

```ts
import type { SessionStatus } from '../api/wire'
import type { FinishPart, ToolResultPart } from './parts'

export type ToolStatus = 'running' | 'done' | 'error' | 'interrupted'

// interruptedResultPrefix starts the result the agent's settle step writes
// for a call that never answered (internal/agent/settle.go,
// interruptedToolCallResult). It is an error result, but the call did not
// fail: it was cut off.
export const interruptedResultPrefix = 'tool call was interrupted'

// toolStatus is the one rule for a call's state. A result decides it when
// there is one. Without a result the call runs only while its own turn is
// live: a canceled or failed turn, or a turn the session has moved past,
// leaves it interrupted rather than spinning forever.
export function toolStatus(
  result: ToolResultPart | undefined,
  finish: FinishPart | null,
  turnActive: boolean,
): ToolStatus {
  if (result !== undefined) {
    if (result.isError && result.content.startsWith(interruptedResultPrefix)) {
      return 'interrupted'
    }
    if (metadataFlag(result.metadata, 'interrupted')) {
      return 'interrupted'
    }
    return result.isError ? 'error' : 'done'
  }
  if (finish !== null && (finish.reason === 'canceled' || finish.reason === 'error')) {
    return 'interrupted'
  }
  return turnActive ? 'running' : 'interrupted'
}

// isTurnActive reports whether a session can still be working on its last
// turn: a run is in flight, or it waits on a permission or a question.
export function isTurnActive(status: SessionStatus | undefined): boolean {
  return status === 'running' || status === 'pending_interaction'
}

function metadataFlag(metadata: string, key: string): boolean {
  if (metadata === '') {
    return false
  }
  try {
    const value: unknown = JSON.parse(metadata)
    return typeof value === 'object' && value !== null && (value as Record<string, unknown>)[key] === true
  } catch {
    return false
  }
}
```

- [ ] **Step 6: Run the chat tests to verify they pass**

Run: `cd web && npx vitest run src/chat`
Expected: PASS, 12 tests.

- [ ] **Step 7: Widen the wire schemas and turn zod's JIT off**

In `web/src/api/wire.ts`, directly after the `import` line, add:

```ts
// zod 4 compiles object parsers with new Function unless it is told not
// to, and the gateway's CSP has no 'unsafe-eval': the probe alone reports
// a CSP violation in the console. jitless parses the same way, without it.
z.config({ jitless: true })
```

Replace `sessionSchema` with:

```ts
const sessionSchema = z
  .object({
    id: z.string(),
    parent_session_id: z.string().optional().default(''),
    title: z.string(),
    message_count: z.number(),
    updated_at: z.number(),
    // The usage the footer reads. prompt_tokens and completion_tokens are
    // the last step's context, cost the session's running total.
    prompt_tokens: z.number().optional(),
    completion_tokens: z.number().optional(),
    cost: z.number().optional(),
  })
  .passthrough()
```

Replace `messageSchema` with:

```ts
export const messageSchema = z
  .object({
    id: z.string(),
    role: z.string(),
    session_id: z.string(),
    parts: z.array(z.object({ type: z.string(), data: z.unknown() })),
    created_at: z.number(),
    updated_at: z.number(),
    // The model that wrote an assistant message, for the turn footer.
    model: z.string().optional(),
    provider: z.string().optional(),
  })
  .passthrough()
```

The new fields are optional, not defaulted, so every message and row
literal in the existing tests still types.

- [ ] **Step 8: Add a tool message to the golden snapshot**

In `internal/web/frames_test.go`, in `gatewayFrames`, change the
snapshot example's `Messages` line to:

```go
			Messages:  []json.RawMessage{fixtureUserMessage(t), fixtureToolMessage(t)},
```

and add below `fixtureUserMessage`:

```go
// fixtureToolMessage is the tool message that answers a-1's view call, so
// the contract carries the tool_result shape the chat reads: the call id,
// the content, the metadata string and the error flag.
func fixtureToolMessage(t *testing.T) json.RawMessage {
	t.Helper()
	raw := innerPayload(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
		ID: "t-1", SessionID: "s1", Role: message.Tool, CreatedAt: 103, UpdatedAt: 103,
		Parts: []message.ContentPart{message.ToolResult{
			ToolCallID: "tc-1", Name: "view", Content: "package parser",
			Metadata: `{"file_path":"parser.go","content":"package parser"}`,
		}},
	}})
	require.NotNil(t, raw)
	return raw
}
```

Regenerate and check:

```bash
go test ./internal/web -run TestFrameGolden -update
go test ./internal/web -run 'TestFrameGolden|TestGoldenCoversEveryFrameType' -count=1
gofumpt -l internal/web
```

Expected: `ok`, and `gofumpt -l` prints nothing. `git diff
internal/web/testdata/frames.golden.json` shows only the new `t-1`
message inside `session_snapshot.payload.messages`.

- [ ] **Step 9: Pin every golden part to the parts model**

Append to the `describe('the wire contract', …)` block of
`web/src/api/wire.golden.test.ts`, and add
`import { parsePart } from '../chat/parts'` to its imports:

```ts
  // Every part of a type the chat draws must parse: a renamed Go field
  // would otherwise drop the part silently instead of failing here.
  it('parses every part the golden messages carry', () => {
    const drawn = new Set(['text', 'reasoning', 'tool_call', 'tool_result', 'finish'])
    const snapshot = frames.session_snapshot as { payload: { messages: { parts: { type: string; data: unknown }[] }[] } }
    const live = frames.message as { payload: { payload: { parts: { type: string; data: unknown }[] } } }
    const parts = [...snapshot.payload.messages.flatMap((entry) => entry.parts), ...live.payload.payload.parts]
    expect(parts.some((part) => part.type === 'tool_result')).toBe(true)
    for (const part of parts) {
      if (drawn.has(part.type)) {
        expect(parsePart(part.type, part.data), part.type).not.toBeNull()
      }
    }
  })
```

- [ ] **Step 10: Run the frontend checks**

```bash
cd web && npm run typecheck && npm test && npm run build && cd ..
```

Expected: typecheck quiet, every test passing, a fresh build in
`internal/web/dist`.

- [ ] **Step 11: Commit**

```bash
git add web/src/chat web/src/api internal/web/frames_test.go internal/web/testdata/frames.golden.json internal/web/dist
git commit -m "feat(web): type message parts and the tool status rule"
```

---

## Task 2: Older pages in the store, a generation counter and child sessions

M1 kept loaded older pages in component state, where live `message`
frames never reach them, and guarded a late page only by the snapshot
counter, which a resync does not move until the new snapshot lands. This
task moves the pages into the session's store entry, applies live edits
and deletes to them, drops a page that answers under another generation,
and keeps subagent sessions out of the sidebar while letting a tool row
subscribe to one.

**Files:**
- Modify: `web/src/store/app.ts`
- Modify: `web/src/store/app.test.ts`
- Modify: `web/src/conversation/ConversationContainer.tsx`
- Modify: `web/src/conversation/ConversationContainer.test.tsx`

**Interfaces:**
- Consumes: `MessageEvent`, `SessionSnapshot`, `SessionEvent`,
  `Bootstrap`, `WireMessage` from `web/src/api/wire.ts`.
- Produces (`web/src/store/app.ts`):
  - `SessionData` gains `older: WireMessage[]`, `loadingOlder: boolean`,
    `olderError: string | null`, `generation: number`.
  - `AppState` gains `watchedChildren: string[]`.
  - `interface OlderPageTicket { sessionId: string; generation: number; before: string | null }`
  - `interface MessagesPage { messages: WireMessage[]; has_more: boolean }`
  - `AppStore` gains `beginOlderPage(sessionID: string): OlderPageTicket | null`,
    `applyOlderPage(ticket: OlderPageTicket, page: MessagesPage): void`,
    `failOlderPage(ticket: OlderPageTicket, error: string): void`,
    `watchChild(sessionID: string): void`,
    `unwatchChild(sessionID: string): void`.
  - `reduceOlderPage(state: AppState, ticket: OlderPageTicket, page: MessagesPage): AppState`
  - `visibleMessages(older: WireMessage[], messages: WireMessage[]): WireMessage[]`
  - `isChildSession(row: { parent_session_id?: string }): boolean`
  - `wantedSubscriptions` now appends `watchedChildren`.
  - `reduceFrame` handles `model_changed` (replaces `bootstrap.model`).

- [ ] **Step 1: Write the failing store tests**

Append to `web/src/store/app.test.ts` (the file's existing helpers stay;
`bootstrapPayload` below is local to these blocks):

```ts
import { reduceOlderPage, visibleMessages, type OlderPageTicket } from './app'

function msg(id: string, text: string, sessionID = 's1'): WireMessage {
  return { id, role: 'assistant', session_id: sessionID, parts: [{ type: 'text', data: { text } }], created_at: 1, updated_at: 1 }
}

function snapshot(sessionID: string, messages: WireMessage[], parent = ''): ParsedFrame {
  return {
    seq: 1,
    type: 'session_snapshot',
    sessionId: sessionID,
    payload: {
      session_id: sessionID,
      session: { id: sessionID, parent_session_id: parent, title: 'x', message_count: messages.length, updated_at: 1 },
      messages,
      has_more: true,
      busy: false,
      queued: [],
      files: [],
    },
  }
}

function messageFrame(type: 'created' | 'updated' | 'deleted', next: WireMessage): ParsedFrame {
  return { seq: 2, type: 'message', sessionId: next.session_id, payload: { type, payload: next } }
}

function bootstrapPayload(sessions: Bootstrap['sessions']): Bootstrap {
  return bootstrapSchema.parse({
    version: 't',
    working_dir: '/w',
    current_session_id: 's1',
    sessions,
    model: { id: 'm', name: 'M', provider: 'p', context_window: 1000, agent_ready: true },
    agent_ready: true,
    permission_mode: 'ask',
    input_mode: 'code',
    lsp: {},
    mcp: {},
    agent_runs: [],
  })
}

const top = { id: 's1', parent_session_id: '', title: 'Top', message_count: 1, updated_at: 5, status: 'idle' as const }
const child = { id: 'a1$$tc-1', parent_session_id: 's1', title: 'Sub', message_count: 1, updated_at: 6, status: 'idle' as const }

describe('older pages', () => {
  function loaded() {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.applyFrame(snapshot('s1', [msg('m5', 'five')]))
    const ticket = store.beginOlderPage('s1') as OlderPageTicket
    store.applyOlderPage(ticket, { messages: [msg('m3', 'three'), msg('m4', 'four')], has_more: true })
    return store
  }

  it('asks for the page before the oldest message, once at a time', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.applyFrame(snapshot('s1', [msg('m5', 'five')]))
    const ticket = store.beginOlderPage('s1')
    expect(ticket).toMatchObject({ sessionId: 's1', before: 'm5' })
    expect(store.beginOlderPage('s1')).toBeNull()
    expect(store.get().sessionData.s1.loadingOlder).toBe(true)
  })

  it('pages back from the oldest message already loaded', () => {
    const store = loaded()
    expect(store.beginOlderPage('s1')?.before).toBe('m3')
  })

  it('applies a live edit to a message in a loaded older page', () => {
    const store = loaded()
    store.applyFrame(messageFrame('updated', msg('m3', 'three, edited')))
    const data = store.get().sessionData.s1
    expect(data.older.map((entry) => entry.parts[0].data)).toEqual([{ text: 'three, edited' }, { text: 'four' }])
    expect(data.messages.map((entry) => entry.id)).toEqual(['m5'])
  })

  it('applies a live delete to a message in a loaded older page', () => {
    const store = loaded()
    store.applyFrame(messageFrame('deleted', msg('m4', 'four')))
    expect(store.get().sessionData.s1.older.map((entry) => entry.id)).toEqual(['m3'])
  })

  it('drops a page that answers after a resync: the generation moved', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.applyFrame(snapshot('s1', [msg('m5', 'five')]))
    const ticket = store.beginOlderPage('s1') as OlderPageTicket
    // The resync replaces the bootstrap before the page answers, and the
    // new snapshot has not landed yet.
    store.applyBootstrap(bootstrapPayload([top]))
    store.applyOlderPage(ticket, { messages: [msg('m4', 'stale')], has_more: false })
    const data = store.get().sessionData.s1
    expect(data.older).toEqual([])
    expect(data.loadingOlder).toBe(false)
  })

  it('drops a page that answers after a new snapshot', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.applyFrame(snapshot('s1', [msg('m5', 'five')]))
    const ticket = store.beginOlderPage('s1') as OlderPageTicket
    store.applyFrame(snapshot('s1', [msg('m5', 'five')]))
    expect(reduceOlderPage(store.get(), ticket, { messages: [msg('m4', 'stale')], has_more: false })).toBe(store.get())
  })

  it('keeps has_more true when a resync drops loaded pages', () => {
    const store = loaded()
    store.applyBootstrap(bootstrapPayload([top]))
    expect(store.get().sessionData.s1.hasMore).toBe(true)
  })

  it('records the reason a page failed', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.applyFrame(snapshot('s1', [msg('m5', 'five')]))
    const ticket = store.beginOlderPage('s1') as OlderPageTicket
    store.failOlderPage(ticket, 'no such session')
    expect(store.get().sessionData.s1).toMatchObject({ loadingOlder: false, olderError: 'no such session' })
  })
})

describe('visibleMessages', () => {
  it('shows each id once, oldest first, the loaded page winning', () => {
    const out = visibleMessages([msg('m1', 'one'), msg('m2', 'two')], [msg('m2', 'echo'), msg('m3', 'three')])
    expect(out.map((entry) => entry.id)).toEqual(['m1', 'm2', 'm3'])
    expect(out[1].parts[0].data).toEqual({ text: 'two' })
  })
})

describe('subagent sessions', () => {
  it('keeps a child session out of the rows the bootstrap lists', () => {
    const state = reduceBootstrap(createAppStore(memoryStorage()).get(), bootstrapPayload([top, child]))
    expect(state.sessions.map((row) => row.id)).toEqual(['s1'])
  })

  it('ignores a session frame for a child session', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.applyFrame({ seq: 3, type: 'session', payload: { type: 'created', payload: child } })
    expect(store.get().sessions.map((row) => row.id)).toEqual(['s1'])
  })

  it('opens a watched child from its snapshot without adding a row', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.watchChild(child.id)
    store.applyFrame(snapshot(child.id, [msg('c1', 'child says', child.id)], 's1'))
    expect(store.get().sessionData[child.id].messages.map((entry) => entry.id)).toEqual(['c1'])
    expect(store.get().sessions.map((row) => row.id)).toEqual(['s1'])
  })

  it('drops the snapshot of a child nobody watches', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.applyFrame(snapshot(child.id, [msg('c1', 'child says', child.id)], 's1'))
    expect(store.get().sessionData[child.id]).toBeUndefined()
  })

  it('subscribes a watched child after the live sessions, and forgets it on unwatch', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.watchChild(child.id)
    expect(wantedSubscriptions(store.get())).toEqual(['s1', child.id])
    store.applyFrame(snapshot(child.id, [msg('c1', 'child says', child.id)], 's1'))
    store.unwatchChild(child.id)
    expect(wantedSubscriptions(store.get())).toEqual(['s1'])
    expect(store.get().sessionData[child.id]).toBeUndefined()
  })

  it('stops watching children when the selection changes', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top, { ...top, id: 's2', title: 'Other' }]))
    store.watchChild(child.id)
    store.selectSession('s2')
    expect(store.get().watchedChildren).toEqual([])
  })
})

describe('model_changed frames', () => {
  it('replace the bootstrap model the usage footer reads', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrapPayload([top]))
    store.applyFrame({
      seq: 4,
      type: 'model_changed',
      payload: { id: 'm2', name: 'Bigger', provider: 'p', context_window: 400000, agent_ready: true },
    })
    expect(store.get().bootstrap?.model.context_window).toBe(400000)
  })
})
```

The first line of that block stands for imports: merge the names into
the file's existing `import` statements instead of adding new ones. From
`./app` add `reduceOlderPage`, `visibleMessages`, `type OlderPageTicket`
and `type WireMessage` (keep `createAppStore`, `reduceBootstrap` and
`wantedSubscriptions`); change `import type { Bootstrap, ParsedFrame }`
from `../api/wire` to `import { bootstrapSchema, type Bootstrap, type ParsedFrame }`;
`memoryStorage` from `../app/storage` is already imported.

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/store/app.test.ts`
Expected: FAIL: `reduceOlderPage` and `visibleMessages` are not exported,
and `store.beginOlderPage is not a function`.

- [ ] **Step 3: Extend the session data and the state**

In `web/src/store/app.ts`, replace `SessionData`, `AppState`'s new field
and `emptySessionData`:

```ts
export interface SessionData {
  messages: WireMessage[]
  // older holds the pages read before the snapshot, oldest first. Live
  // edits and deletes reach them the same way they reach messages.
  older: WireMessage[]
  // hasMore says whether history exists before the oldest message held,
  // whichever list it is in.
  hasMore: boolean
  loadingOlder: boolean
  olderError: string | null
  queued: string[]
  files: { path: string }[]
  // snapshotSeq counts the snapshots the tab has applied for the session.
  snapshotSeq: number
  // generation moves on every snapshot for the session and on every
  // bootstrap. An older page is applied only under the generation its
  // request started in, so a page that answers across a resync, before
  // the new snapshot lands, is dropped instead of joining the new list.
  generation: number
}
```

Add to `AppState`, after `sessionData`:

```ts
  // watchedChildren are the subagent sessions an open tool row follows.
  // They are subscribed after the live sessions and never get a row.
  watchedChildren: string[]
```

```ts
export const emptySessionData: SessionData = {
  messages: [],
  older: [],
  hasMore: false,
  loadingOlder: false,
  olderError: null,
  queued: [],
  files: [],
  snapshotSeq: 0,
  generation: 0,
}

// noMessages is the one empty list, so an empty page keeps its identity.
const noMessages: WireMessage[] = []

export interface OlderPageTicket {
  sessionId: string
  generation: number
  before: string | null
}

// MessagesPage is what GET /api/sessions/{sid}/messages answers.
export interface MessagesPage {
  messages: WireMessage[]
  has_more: boolean
}

// isChildSession reports a subagent's session: the workspace lists only
// top-level sessions, and a child that slips in through a frame must not
// become a sidebar row.
export function isChildSession(row: { parent_session_id?: string }): boolean {
  return (row.parent_session_id ?? '') !== ''
}
```

In `createAppStore`, add `watchedChildren: []` to the initial state.

- [ ] **Step 4: Add the actions**

In the object `createAppStore` assigns onto the store, add:

```ts
    beginOlderPage: (sessionID: string): OlderPageTicket | null => {
      const data = store.get().sessionData[sessionID]
      if (data === undefined || data.loadingOlder) {
        return null
      }
      const oldest = data.older[0] ?? data.messages[0]
      const ticket = { sessionId: sessionID, generation: data.generation, before: oldest?.id ?? null }
      store.set((state) => patchSession(state, sessionID, { loadingOlder: true, olderError: null }))
      return ticket
    },
    applyOlderPage: (ticket: OlderPageTicket, page: MessagesPage) => {
      store.set((state) => reduceOlderPage(state, ticket, page))
    },
    failOlderPage: (ticket: OlderPageTicket, error: string) => {
      store.set((state) => {
        const data = state.sessionData[ticket.sessionId]
        if (data === undefined || data.generation !== ticket.generation) {
          return {}
        }
        return patchSession(state, ticket.sessionId, { loadingOlder: false, olderError: error })
      })
    },
    watchChild: (sessionID: string) => {
      store.set((state) =>
        state.watchedChildren.includes(sessionID) ? {} : { watchedChildren: [...state.watchedChildren, sessionID] },
      )
    },
    // unwatchChild also drops the child's messages: nobody draws them, and
    // the next watch subscribes afresh and gets a new snapshot.
    unwatchChild: (sessionID: string) => {
      store.set((state) => {
        if (!state.watchedChildren.includes(sessionID)) {
          return {}
        }
        const sessionData = { ...state.sessionData }
        delete sessionData[sessionID]
        return { watchedChildren: state.watchedChildren.filter((id) => id !== sessionID), sessionData }
      })
    },
```

In `selectSession`, add `watchedChildren: []` to the patch: the open
rows of the previous session are gone with it.

Add to the `AppStore` interface:

```ts
  beginOlderPage(sessionID: string): OlderPageTicket | null
  applyOlderPage(ticket: OlderPageTicket, page: MessagesPage): void
  failOlderPage(ticket: OlderPageTicket, error: string): void
  watchChild(sessionID: string): void
  unwatchChild(sessionID: string): void
```

Add these module functions below `upsertMessage`:

```ts
function patchSession(state: AppState, sessionID: string, patch: Partial<SessionData>): Partial<AppState> {
  const data = state.sessionData[sessionID]
  if (data === undefined) {
    return {}
  }
  return { sessionData: { ...state.sessionData, [sessionID]: { ...data, ...patch } } }
}

// reduceOlderPage puts an older page in front of what is loaded, unless the
// session's generation moved while the request was out.
export function reduceOlderPage(state: AppState, ticket: OlderPageTicket, page: MessagesPage): AppState {
  const data = state.sessionData[ticket.sessionId]
  if (data === undefined || data.generation !== ticket.generation) {
    return state
  }
  const held = new Set([...data.older, ...data.messages].map((entry) => entry.id))
  const fresh = page.messages.filter((entry) => !held.has(entry.id))
  return {
    ...state,
    sessionData: {
      ...state.sessionData,
      [ticket.sessionId]: {
        ...data,
        older: fresh.length === 0 ? data.older : [...fresh, ...data.older],
        hasMore: page.has_more,
        loadingOlder: false,
        olderError: null,
      },
    },
  }
}

// visibleMessages is the log a conversation shows: the loaded pages, then
// the snapshot and its live updates, each id once. A resync can echo a
// message the tab already paged in; the first copy wins.
export function visibleMessages(older: WireMessage[], messages: WireMessage[]): WireMessage[] {
  if (older.length === 0) {
    return messages
  }
  const seen = new Set<string>()
  const out: WireMessage[] = []
  for (const entry of [...older, ...messages]) {
    if (!seen.has(entry.id)) {
      seen.add(entry.id)
      out.push(entry)
    }
  }
  return out
}

// resetPages drops every loaded older page and moves every generation, for
// a bootstrap: whatever was read under the old connection belongs to a
// list the new snapshots replace. Dropping a page means history exists
// before the snapshot, so has_more turns true.
function resetPages(sessionData: Record<string, SessionData>): Record<string, SessionData> {
  const out: Record<string, SessionData> = {}
  for (const [id, data] of Object.entries(sessionData)) {
    out[id] = {
      ...data,
      older: noMessages,
      hasMore: data.older.length > 0 ? true : data.hasMore,
      loadingOlder: false,
      olderError: null,
      generation: data.generation + 1,
    }
  }
  return out
}
```

- [ ] **Step 5: Route the frames**

In `reduceBootstrap`, filter the rows first and use them everywhere the
function read `payload.sessions`:

```ts
  const rows = payload.sessions.filter((row) => !isChildSession(row))
```

(`stillListed`, the `selectedSessionId` fallback, `pruneSeen` and
`updatedAtIn` all take `rows`), and set in the returned state:

```ts
    sessions: rows,
    sessionData: resetPages(state.sessionData),
```

In `reduceFrame`, replace the `session_snapshot` case with:

```ts
    case 'session_snapshot': {
      const snapshot = frame.payload as SessionSnapshot
      const id = snapshot.session_id
      const entity = snapshot.session
      let sessions = state.sessions
      if (entity !== undefined && isChildSession(entity)) {
        // A subagent's session opens only for the tool row that asked.
        if (!state.watchedChildren.includes(id)) {
          return state
        }
      } else if (!hasSession(state, id)) {
        // A subscribe snapshot can beat the session created frame. When it
        // carries a non-empty entity the row comes from it; without one this
        // is a snapshot the tab cannot place and so must not keep.
        if (entity === undefined || entity.id === '') {
          return state
        }
        sessions = mergeSessionEvent(state.sessions, { type: 'created', payload: { ...entity, id } })
      }
      const previous = state.sessionData[id]
      return {
        ...state,
        sessions,
        sessionData: {
          ...state.sessionData,
          [id]: {
            messages: snapshot.messages,
            older: noMessages,
            hasMore: snapshot.has_more,
            loadingOlder: false,
            olderError: null,
            queued: snapshot.queued,
            files: snapshot.files,
            snapshotSeq: (previous?.snapshotSeq ?? 0) + 1,
            generation: (previous?.generation ?? 0) + 1,
          },
        },
      }
    }
```

In the `session` case, directly after the `id === ''` guard, add:

```ts
      // A subagent's session is never a row: its created and updated frames
      // are dropped, and a deleted one only clears what the tab held.
      if (event.type !== 'deleted' && isChildSession(event.payload)) {
        return state
      }
```

Replace the body of the `message` case after `const current = …` and its
guard with:

```ts
      const next = envelope.payload
      if (envelope.type === 'deleted') {
        return {
          ...state,
          sessionData: {
            ...state.sessionData,
            [id]: {
              ...current,
              messages: current.messages.filter((entry) => entry.id !== next.id),
              older: current.older.filter((entry) => entry.id !== next.id),
            },
          },
        }
      }
      // An edit to a message that lives in a loaded older page is written
      // there; anything else is upserted into the live list.
      const olderIndex = current.older.findIndex((entry) => entry.id === next.id)
      if (olderIndex >= 0) {
        const older = current.older.slice()
        older[olderIndex] = next
        return { ...state, sessionData: { ...state.sessionData, [id]: { ...current, older } } }
      }
      return {
        ...state,
        sessionData: { ...state.sessionData, [id]: { ...current, messages: upsertMessage(current.messages, next) } },
      }
```

Add a case before `web_error`:

```ts
    case 'model_changed': {
      if (state.bootstrap === null) {
        return state
      }
      return { ...state, bootstrap: { ...state.bootstrap, model: frame.payload as Bootstrap['model'] } }
    }
```

In `wantedSubscriptions`, after the loop over rows, add:

```ts
  for (const sessionID of state.watchedChildren) {
    wanted.add(sessionID)
  }
```

- [ ] **Step 6: Run the store tests to verify they pass**

Run: `cd web && npx vitest run src/store`
Expected: PASS, the M1 cases included. A case that builds `SessionData`
as a literal fails to type until it spreads `emptySessionData`; change
each such literal to `{ ...emptySessionData, messages, … }`.

- [ ] **Step 7: Move the container onto the store's pages**

Replace everything in `web/src/conversation/ConversationContainer.tsx`
from `interface SessionConversationProps` to the end of the file with:

```tsx
interface SessionConversationProps extends ConversationContainerProps {
  sessionId: string | null
  title: string
}

function SessionConversation({ sessionId, title, client, store }: SessionConversationProps) {
  const data = useStore(store, (state) => (sessionId === null ? undefined : state.sessionData[sessionId]))
  const older = data?.older ?? noPages
  const live = data?.messages ?? noPages
  const messages = useMemo(() => visibleMessages(older, live), [older, live])

  // The store owns the page and its generation: a page that answers after a
  // resync or a new snapshot is dropped there, so nothing here has to guess.
  const loadOlder = useCallback(() => {
    if (sessionId === null) {
      return
    }
    const ticket = store.beginOlderPage(sessionId)
    if (ticket === null) {
      return
    }
    const query = ticket.before
      ? `?before=${encodeURIComponent(ticket.before)}&limit=${messagePageLimit}`
      : `?limit=${messagePageLimit}`
    void client
      .get<MessagesPage>(`/api/sessions/${encodeURIComponent(sessionId)}/messages${query}`)
      .then((page) => store.applyOlderPage(ticket, page))
      .catch((cause: unknown) => store.failOlderPage(ticket, errorText(cause)))
  }, [client, sessionId, store])

  return (
    <Conversation
      sessionId={sessionId}
      title={title}
      messages={messages}
      hasMore={data?.hasMore ?? false}
      loadingOlder={data?.loadingOlder ?? false}
      error={data?.olderError ?? null}
      onLoadOlder={loadOlder}
    />
  )
}

// errorText is the gateway's own sentence for a refused request. The token
// and the request never reach it.
function errorText(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause)
}
```

Replace the file's imports and the `MessagesPage` interface with:

```tsx
import { useCallback, useMemo } from 'react'

import type { ApiClient } from '../api/http'
import { visibleMessages, type AppState, type AppStore, type MessagesPage, type WireMessage } from '../store/app'
import { useStore, type Store } from '../store/store'
import Conversation from './Conversation'
```

`messagePageLimit`, `noPages`, `ConversationContainer` and
`ConversationContainerProps` stay as they are; `OlderPages`,
`emptyPages`, `prependPage` and `message` are deleted.

- [ ] **Step 8: Update the container tests**

In `web/src/conversation/ConversationContainer.test.tsx`, replace the
`data` helper with:

```tsx
function data(messages: WireMessage[], hasMore = false, snapshotSeq = 0): SessionData {
  return { ...emptySessionData, messages, hasMore, snapshotSeq }
}
```

and import `emptySessionData` from `../store/app`. Then add:

```tsx
  it('shows a live edit to a message from a loaded older page', async () => {
    const page = { messages: [message('m0', 'first words')], has_more: false }
    const { store } = setup(
      { selectedSessionId: 's1', sessions: [row()], sessionData: { s1: data([message('m1', 'hello')], true) } },
      async () => page,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('first words')
    act(() => store.applyFrame({ seq: 9, type: 'message', sessionId: 's1', payload: { type: 'updated', payload: message('m0', 'first words, edited') } }))
    expect(screen.getByText('first words, edited')).toBeInTheDocument()
    expect(articles()).toHaveLength(2)
  })

  it('drops a page that answers after a resync, before the new snapshot', async () => {
    let answer: (value: unknown) => void = () => undefined
    const { store } = setup(
      { selectedSessionId: 's1', sessions: [row()], sessionData: { s1: data([message('m1', 'hello')], true) } },
      () => new Promise((resolve) => (answer = resolve)),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    act(() => store.applyBootstrap(bootstrap))
    await act(async () => answer({ messages: [message('m0', 'stale page')], has_more: false }))
    expect(screen.queryByText('stale page')).toBeNull()
  })
```

- [ ] **Step 9: Run the frontend checks**

```bash
cd web && npm run typecheck && npm test && npm run build && cd ..
```

Expected: typecheck quiet; every test passes, the M1 container cases
included (they read the same behaviour through the store now).

- [ ] **Step 10: Commit**

```bash
git add web/src/store web/src/conversation internal/web/dist
git commit -m "feat(web): keep older pages in the store under a generation"
```

---
## Task 3: The safe Markdown renderer

Assistant text is Markdown. It is parsed with micromark into an mdast
tree and rendered into React elements by hand, so no HTML string ever
reaches the DOM: raw HTML in the source shows as text, and links and
images are filtered by protocol. Code blocks and math render as plain
text here; Tasks 4 and 5 make them rich without touching this renderer.

**Files:**
- Create: `web/src/chat/markdown/parse.ts` (lifted)
- Create: `web/src/chat/markdown/links.ts`
- Create: `web/src/chat/markdown/Markdown.tsx`,
  `web/src/chat/markdown/Markdown.module.css`
- Create: `web/src/chat/markdown/CodeBlock.tsx`,
  `web/src/chat/markdown/CodeBlock.module.css`
- Create: `web/src/chat/markdown/Math.tsx`
- Create: `web/src/chat/markdown/Markdown.test.tsx`,
  `web/src/chat/markdown/links.test.ts`
- Modify: `web/package.json`, `web/package-lock.json`
- Modify: `web/THIRD_PARTY_NOTICES.md`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `parseMarkdown(source: string): Root` (mdast `Root`)
  - `safeHref(url: string): string | null`
  - `<Markdown source={string} />` (default export of `Markdown.tsx`,
    memoised on `source`)
  - `<CodeBlock code={string} language?={string | null} lineStart?={number} lineNumbers?={boolean} />`
    (default export of `CodeBlock.tsx`; Task 4 replaces its body)
  - `<MathFormula tex={string} display={boolean} />` (default export of
    `Math.tsx`; Task 5 replaces its body)

- [ ] **Step 1: Add the parser dependencies**

```bash
cd web
npm install --save-exact mdast-util-from-markdown@2.1.0 mdast-util-gfm@3.1.0 micromark-extension-gfm@3.0.0 mdast-util-math@3.0.0 micromark-extension-math@3.1.0 micromark-util-sanitize-uri@2.0.1
npm install --save-dev --save-exact @types/mdast@4.0.4
cd ..
```

Expected: `web/package.json` lists the six under `dependencies` and
`@types/mdast` under `devDependencies`.

- [ ] **Step 2: Write the failing tests**

`web/src/chat/markdown/links.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { safeHref } from './links'

describe('safeHref', () => {
  it('keeps absolute web and mail links', () => {
    expect(safeHref('https://charm.land')).toBe('https://charm.land')
    expect(safeHref('http://localhost:8080/x?y=1')).toBe('http://localhost:8080/x?y=1')
    expect(safeHref('mailto:me@example.com')).toBe('mailto:me@example.com')
  })

  it('refuses script, data and relative links', () => {
    expect(safeHref('javascript:alert(1)')).toBeNull()
    expect(safeHref(' JavaScript:alert(1)')).toBeNull()
    expect(safeHref('data:text/html,<b>x</b>')).toBeNull()
    expect(safeHref('/api/sessions')).toBeNull()
    expect(safeHref('parser.go')).toBeNull()
  })
})
```

`web/src/chat/markdown/Markdown.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import Markdown from './Markdown'

describe('Markdown', () => {
  it('renders headings, emphasis, lists and inline code', () => {
    render(<Markdown source={'## Plan\n\n- **bold** and *soft*\n- `go test`\n\n1. first\n2. second'} />)
    expect(screen.getByRole('heading', { name: 'Plan' }).tagName).toBe('H3')
    expect(screen.getByText('bold').tagName).toBe('STRONG')
    expect(screen.getByText('soft').tagName).toBe('EM')
    expect(screen.getByText('go test').tagName).toBe('CODE')
    expect(screen.getAllByRole('list')).toHaveLength(2)
  })

  it('renders a GFM table with its alignment as a class', () => {
    const { container } = render(<Markdown source={'| a | b |\n| :-- | --: |\n| 1 | 2 |'} />)
    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(container.querySelectorAll('th')).toHaveLength(2)
    expect(container.querySelector('td:last-child')?.className).toMatch(/alignRight/)
    expect(container.querySelector('[style]')).toBeNull()
  })

  it('renders a task list with disabled checkboxes', () => {
    render(<Markdown source={'- [x] done\n- [ ] open'} />)
    const boxes = screen.getAllByRole('checkbox')
    expect(boxes.map((box) => (box as HTMLInputElement).checked)).toEqual([true, false])
    expect(boxes.every((box) => (box as HTMLInputElement).disabled)).toBe(true)
  })

  it('opens a web link in a new tab without an opener', () => {
    render(<Markdown source={'See [Charm](https://charm.land).'} />)
    const link = screen.getByRole('link', { name: 'Charm' })
    expect(link).toHaveAttribute('href', 'https://charm.land')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('resolves a reference link through its definition', () => {
    render(<Markdown source={'Read [the docs][d].\n\n[d]: https://charm.land/docs'} />)
    expect(screen.getByRole('link', { name: 'the docs' })).toHaveAttribute('href', 'https://charm.land/docs')
  })

  it('keeps raw HTML inert: it is text, never an element', () => {
    const { container } = render(
      <Markdown source={'<img src="x" onerror="alert(1)">\n\nHi <script>alert(1)</script> there <b>bold</b>'} />,
    )
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('script')).toBeNull()
    expect(container.querySelector('b')).toBeNull()
    expect(container.textContent).toContain('<img src="x" onerror="alert(1)">')
    expect(container.textContent).toContain('<script>')
  })

  it('keeps a script link inert', () => {
    render(<Markdown source={'[a trap](javascript:alert(1))'} />)
    expect(screen.queryByRole('link')).toBeNull()
    expect(screen.getByText('a trap')).toBeInTheDocument()
  })

  it('never loads an image: it is a link to its address', () => {
    const { container } = render(<Markdown source={'![the logo](https://example.com/logo.png)'} />)
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByRole('link', { name: 'Image: the logo' })).toHaveAttribute('href', 'https://example.com/logo.png')
  })

  it('renders a fenced block as code and keeps its text', () => {
    const { container } = render(<Markdown source={'```go\nfunc main() {}\n```'} />)
    expect(container.querySelector('pre')?.textContent).toContain('func main() {}')
  })

  it('reads $$ math and leaves a lone dollar alone', () => {
    const { container } = render(<Markdown source={'Cost $5, area $$\\pi r^2$$.\n\n$$\nx^2\n$$'} />)
    expect(container.textContent).toContain('Cost $5')
    expect(container.querySelectorAll('[data-math]')).toHaveLength(2)
  })

  it('renders an unterminated fence while it streams', () => {
    const { container } = render(<Markdown source={'Here:\n\n```ts\nconst a = 1'} />)
    expect(container.querySelector('pre')?.textContent).toContain('const a = 1')
  })
})
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd web && npx vitest run src/chat/markdown`
Expected: FAIL with `Failed to resolve import "./links"` and
`Failed to resolve import "./Markdown"`.

- [ ] **Step 4: Write the parser and the link filter**

`web/src/chat/markdown/parse.ts`:

```ts
// Lifted from deepseek-harness
// packages/client/ui-primitives/src/markdown/parse.ts at
// 4b399c39a6d1479cd84ca591b4d7c88e24d38a96. MIT license, see
// web/THIRD_PARTY_NOTICES.md. Changes: one grammar (GFM plus math) instead
// of a streaming and a settled arm; the CJK-strong, local-image,
// Windows-path and math-compatibility extensions were dropped; inline math
// needs $$ because a coding chat writes prices far more often than TeX.
import type { Root } from 'mdast'
import { fromMarkdown } from 'mdast-util-from-markdown'
import { gfmFromMarkdown } from 'mdast-util-gfm'
import { mathFromMarkdown } from 'mdast-util-math'
import { gfm } from 'micromark-extension-gfm'
import { math } from 'micromark-extension-math'

// GFM strikethrough requires ~~; a single ~ stays literal, as in 10~20.
const gfmOptions = { singleTilde: false }

export function parseMarkdown(source: string): Root {
  return fromMarkdown(source, {
    extensions: [gfm(gfmOptions), math({ singleDollarTextMath: false })],
    mdastExtensions: [gfmFromMarkdown(), mathFromMarkdown()],
  })
}
```

`web/src/chat/markdown/links.ts`:

```ts
import { sanitizeUri } from 'micromark-util-sanitize-uri'

const protocols = /^(https?|mailto)$/i
const absolute = /^(https?:\/\/|mailto:)/i

// safeHref answers the address a rendered link may carry, or null. Only
// absolute web and mail links pass: a relative link would navigate the
// gateway's own pages, and every other scheme (javascript:, data:, file:)
// is refused. sanitizeUri also percent-encodes what a URL must not hold.
export function safeHref(url: string): string | null {
  const trimmed = url.trim()
  if (!absolute.test(trimmed)) {
    return null
  }
  const clean = sanitizeUri(trimmed, protocols)
  return clean === '' ? null : clean
}
```

- [ ] **Step 5: Write the plain code block and math placeholders**

`web/src/chat/markdown/CodeBlock.tsx`:

```tsx
import styles from './CodeBlock.module.css'

export interface CodeBlockProps {
  code: string
  language?: string | null
  // lineStart numbers the first line; lineNumbers turns the gutter on.
  lineStart?: number
  lineNumbers?: boolean
}

// CodeBlock is a fenced block or a file excerpt. It renders plain text;
// highlighting arrives with the lazy Shiki chunk.
export default function CodeBlock({ code, language = null, lineStart = 1, lineNumbers = false }: CodeBlockProps) {
  const lines = code.split('\n')
  return (
    <div className={styles.block}>
      {language ? <div className={styles.label}>{language}</div> : null}
      <pre className={styles.pre}>
        <code>
          {lines.map((line, index) => (
            <span key={index} className={styles.line}>
              {lineNumbers ? (
                <span className={styles.gutter} aria-hidden="true">
                  {lineStart + index}
                </span>
              ) : null}
              {line}
            </span>
          ))}
        </code>
      </pre>
    </div>
  )
}
```

`web/src/chat/markdown/CodeBlock.module.css`:

```css
.block {
  margin: 8px 0;
  border: 1px solid var(--dsw-alias-border-l2);
  border-radius: var(--dsw-radius-md);
  background: var(--dsw-alias-markdown-code-block, var(--dsw-alias-bg-layer-2));
  overflow: hidden;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 8px;
  border-bottom: 1px solid var(--dsw-alias-border-l2);
}

.label {
  padding: 4px 8px 0;
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
  font-size: 11px;
}

.pre {
  margin: 0;
  padding: 8px 10px;
  overflow-x: auto;
  font-family: var(--ds-font-family-code);
  font-size: var(--crush-font-size-code);
  line-height: 1.5;
}

.line {
  display: block;
  min-height: 1.5em;
  white-space: pre;
}

.gutter {
  display: inline-block;
  min-width: 3ch;
  margin-right: 12px;
  color: var(--dsw-alias-label-tertiary, var(--dsw-alias-label-secondary));
  text-align: right;
  user-select: none;
}
```

`web/src/chat/markdown/Math.tsx`:

```tsx
import styles from './Markdown.module.css'

export interface MathFormulaProps {
  tex: string
  display: boolean
}

// MathFormula shows TeX source until the lazy KaTeX chunk renders it.
export default function MathFormula({ tex, display }: MathFormulaProps) {
  if (display) {
    return (
      <pre className={styles.mathSource} data-math="display">
        <code>{tex}</code>
      </pre>
    )
  }
  return (
    <code className={styles.mathSource} data-math="inline">
      {tex}
    </code>
  )
}
```

- [ ] **Step 6: Write the renderer**

`web/src/chat/markdown/Markdown.tsx`:

```tsx
import type { AlignType, Definition, Nodes, Root, Table } from 'mdast'
import { Fragment, createElement, memo, useMemo, type ReactNode } from 'react'

import CodeBlock from './CodeBlock'
import MathFormula from './Math'
import { safeHref } from './links'
import { parseMarkdown } from './parse'
import styles from './Markdown.module.css'

export interface MarkdownProps {
  source: string
}

interface RenderContext {
  definitions: Map<string, Definition>
}

// Markdown renders assistant text. The tree is walked node by node into
// React elements: there is no HTML string anywhere, so raw HTML in the
// source can only ever be text.
function Markdown({ source }: MarkdownProps) {
  const tree = useMemo(() => parseMarkdown(source), [source])
  const context = useMemo(() => ({ definitions: collectDefinitions(tree) }), [tree])
  return <div className={styles.markdown}>{children(tree.children, context)}</div>
}

export default memo(Markdown)

function collectDefinitions(root: Root): Map<string, Definition> {
  const out = new Map<string, Definition>()
  const visit = (node: Nodes) => {
    if (node.type === 'definition') {
      out.set(node.identifier, node)
    }
    if ('children' in node) {
      for (const child of node.children) {
        visit(child)
      }
    }
  }
  visit(root)
  return out
}

function children(nodes: Nodes[], context: RenderContext): ReactNode[] {
  return nodes.map((node, index) => <Fragment key={index}>{render(node, context)}</Fragment>)
}

function link(url: string, content: ReactNode): ReactNode {
  const href = safeHref(url)
  if (href === null) {
    return <span className={styles.inertLink}>{content}</span>
  }
  return (
    <a href={href} target="_blank" rel="noopener noreferrer">
      {content}
    </a>
  )
}

const alignClass: Record<NonNullable<AlignType>, string> = {
  left: styles.alignLeft,
  center: styles.alignCenter,
  right: styles.alignRight,
}

function table(node: Table, context: RenderContext): ReactNode {
  const [head, ...body] = node.children
  const cell = (tag: 'th' | 'td', column: number, content: Nodes[]) => {
    const align = node.align?.[column]
    return createElement(tag, { key: column, className: align ? alignClass[align] : undefined }, children(content, context))
  }
  return (
    <div className={styles.tableScroll}>
      <table>
        {head ? (
          <thead>
            <tr>{head.children.map((entry, column) => cell('th', column, entry.children))}</tr>
          </thead>
        ) : null}
        <tbody>
          {body.map((row, index) => (
            <tr key={index}>{row.children.map((entry, column) => cell('td', column, entry.children))}</tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function render(node: Nodes, context: RenderContext): ReactNode {
  switch (node.type) {
    case 'root':
      return children(node.children, context)
    case 'paragraph':
      return <p>{children(node.children, context)}</p>
    case 'heading':
      // The page title is the h1 and the conversation sits under it, so a
      // message's headings start one level down.
      return createElement(`h${Math.min(node.depth + 1, 6)}`, null, children(node.children, context))
    case 'text':
      return node.value
    case 'emphasis':
      return <em>{children(node.children, context)}</em>
    case 'strong':
      return <strong>{children(node.children, context)}</strong>
    case 'delete':
      return <del>{children(node.children, context)}</del>
    case 'inlineCode':
      return <code className={styles.inlineCode}>{node.value}</code>
    case 'code':
      if (node.lang === 'math') {
        return <MathFormula tex={node.value} display />
      }
      return <CodeBlock code={node.value} language={node.lang ?? null} />
    case 'math':
      return <MathFormula tex={node.value} display />
    case 'inlineMath':
      return <MathFormula tex={node.value} display={false} />
    case 'blockquote':
      return <blockquote>{children(node.children, context)}</blockquote>
    case 'list':
      return node.ordered ? (
        <ol start={node.start ?? undefined}>{children(node.children, context)}</ol>
      ) : (
        <ul>{children(node.children, context)}</ul>
      )
    case 'listItem':
      if (node.checked === true || node.checked === false) {
        return (
          <li className={styles.task}>
            <input type="checkbox" checked={node.checked} disabled readOnly aria-label={node.checked ? 'Done' : 'Not done'} />
            {children(node.children, context)}
          </li>
        )
      }
      return <li>{children(node.children, context)}</li>
    case 'thematicBreak':
      return <hr />
    case 'break':
      return <br />
    case 'link':
      return link(node.url, children(node.children, context))
    case 'linkReference': {
      const definition = context.definitions.get(node.identifier)
      const content = children(node.children, context)
      return definition ? link(definition.url, content) : <span>{content}</span>
    }
    case 'image':
      // The CSP loads images from the gateway only, and a remote image is a
      // tracking beacon anyway: it becomes a link the person can choose.
      return link(node.url, <span className={styles.imageLink}>Image: {node.alt || node.url}</span>)
    case 'imageReference': {
      const definition = context.definitions.get(node.identifier)
      const label = <span className={styles.imageLink}>Image: {node.alt || node.identifier}</span>
      return definition ? link(definition.url, label) : label
    }
    case 'definition':
      return null
    case 'footnoteReference':
      return <sup className={styles.footnoteRef}>[{node.label ?? node.identifier}]</sup>
    case 'footnoteDefinition':
      return (
        <div className={styles.footnote}>
          <sup>{node.label ?? node.identifier}</sup> {children(node.children, context)}
        </div>
      )
    case 'table':
      return table(node, context)
    case 'html':
      // Raw HTML is the model's text, shown as it was written.
      return <span className={styles.rawHtml}>{node.value}</span>
    default:
      return null
  }
}
```

`web/src/chat/markdown/Markdown.module.css`:

```css
.markdown {
  overflow-wrap: anywhere;
  line-height: 1.6;
}

.markdown > :first-child {
  margin-top: 0;
}

.markdown > :last-child {
  margin-bottom: 0;
}

.markdown p,
.markdown ul,
.markdown ol,
.markdown blockquote {
  margin: 6px 0;
}

.markdown ul,
.markdown ol {
  padding-left: 22px;
}

.markdown h2,
.markdown h3,
.markdown h4,
.markdown h5,
.markdown h6 {
  margin: 12px 0 6px;
  font-weight: 600;
}

.markdown h2 {
  font-size: 1.25em;
}

.markdown h3 {
  font-size: 1.12em;
}

.markdown h4,
.markdown h5,
.markdown h6 {
  font-size: 1em;
}

.markdown blockquote {
  padding-left: 10px;
  border-left: 3px solid var(--dsw-alias-border-l3);
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
}

.markdown a {
  color: var(--dsw-alias-state-business-primary, inherit);
}

.markdown hr {
  border: 0;
  border-top: 1px solid var(--dsw-alias-border-l2);
}

.inlineCode,
.mathSource {
  padding: 1px 4px;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-alias-markdown-code-block, var(--dsw-alias-bg-layer-2));
  font-family: var(--ds-font-family-code);
  font-size: 0.92em;
}

pre.mathSource {
  padding: 8px 10px;
  overflow-x: auto;
}

.task {
  list-style: none;
  margin-left: -20px;
}

.task input {
  margin-right: 6px;
}

.tableScroll {
  margin: 8px 0;
  overflow-x: auto;
}

.tableScroll table {
  border-collapse: collapse;
}

.tableScroll th,
.tableScroll td {
  padding: 4px 10px;
  border: 1px solid var(--dsw-alias-border-l2);
}

.alignLeft {
  text-align: left;
}

.alignCenter {
  text-align: center;
}

.alignRight {
  text-align: right;
}

.imageLink {
  font-style: italic;
}

.inertLink {
  text-decoration: underline dotted;
}

.rawHtml {
  font-family: var(--ds-font-family-code);
  font-size: 0.92em;
  white-space: pre-wrap;
}

.footnote {
  margin-top: 8px;
  font-size: 0.9em;
}

.footnoteRef {
  font-size: 0.75em;
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/chat/markdown`
Expected: PASS, 13 tests.

- [ ] **Step 8: Record the lifted file**

In `web/THIRD_PARTY_NOTICES.md`, replace the paragraph under
`## DeepSeek Harness (deepseek-harness)` that begins "The CSS token
sheets" with:

```markdown
Files copied or adapted from
<https://github.com/deepseek-ai/deepseek-harness> at commit
`4b399c39a6d1479cd84ca591b4d7c88e24d38a96`. Each names its source path in
its header comment and lists what changed:

- The CSS token sheets under `web/src/theme/` (`packages/client/ui-theme`).
- `web/src/chat/markdown/parse.ts`
  (`packages/client/ui-primitives/src/markdown/parse.ts`).
```

- [ ] **Step 9: Run the frontend checks**

```bash
cd web && npm run typecheck && npm test && npm run build && npm run budget && cd ..
```

Expected: typecheck quiet, every test passing, and the budget well under
600 KiB (the parser adds roughly 40 KiB gzipped to first paint).

- [ ] **Step 10: Commit**

```bash
git add web/package.json web/package-lock.json web/THIRD_PARTY_NOTICES.md web/src/chat/markdown internal/web/dist
git commit -m "feat(web): render assistant Markdown without raw HTML"
```

---

## Task 4: Lazy Shiki code blocks with class-based tokens

Shiki's core, its JavaScript regex engine and every grammar live behind
one dynamic import. Tokens come from Shiki's CSS-variables theme and are
mapped onto `tok-*` classes, so a highlighted block carries no style
attribute at all. The WASM engine is never imported: the CSP has no
`wasm-unsafe-eval`. A streamed fence keeps the lines already highlighted
and shows only its growing tail as plain text until the next pass.

**Files:**
- Create: `web/src/chat/markdown/languages.ts`,
  `web/src/chat/markdown/languages.test.ts`
- Create: `web/src/chat/markdown/highlighter.ts` (partly lifted, lazy
  chunk)
- Create: `web/src/chat/markdown/useHighlight.ts`
- Create: `web/src/chat/CopyButton.tsx`, `web/src/chat/CopyButton.module.css`
- Create: `web/src/theme/shiki.css` (lifted)
- Create: `web/src/chat/markdown/CodeBlock.test.tsx`
- Modify: `web/src/chat/markdown/CodeBlock.tsx` (highlighting, copy)
- Modify: `web/src/theme/index.css` (import `shiki.css`)
- Modify: `web/scripts/budget.mjs` (lazy markers)
- Modify: `web/package.json`, `web/package-lock.json`,
  `web/THIRD_PARTY_NOTICES.md`

**Interfaces:**
- Consumes: `CodeBlockProps` (Task 3).
- Produces:
  - `type LanguageId` (string union below)
  - `resolveLanguage(hint: string | null | undefined): LanguageId | null`
  - `languageForPath(path: string): LanguageId | null`
  - `interface HighlightedToken { text: string; className: string }`
    (`highlighter.ts`)
  - `tokenize(code: string, lang: LanguageId): Promise<HighlightedToken[][]>`
    (`highlighter.ts`, lazy)
  - `interface Highlight { lines: HighlightedToken[][]; covered: number }`
  - `useHighlight(code: string, language: string | null): Highlight | null`
  - `loadHighlighter(): Promise<typeof import('./highlighter')>`
  - `<CopyButton text={string} label={string} />` (default export)

- [ ] **Step 1: Add Shiki**

```bash
cd web && npm install --save-exact shiki@4.6.0 @shikijs/langs@4.6.0 && cd ..
```

- [ ] **Step 2: Write the failing tests**

`web/src/chat/markdown/languages.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { languageForPath, resolveLanguage } from './languages'

describe('resolveLanguage', () => {
  it('resolves fence labels and their aliases, any case', () => {
    expect(resolveLanguage('go')).toBe('go')
    expect(resolveLanguage('TS')).toBe('typescript')
    expect(resolveLanguage('sh')).toBe('shellscript')
    expect(resolveLanguage('yml')).toBe('yaml')
  })

  it('misses on a label that names an inherited property or nothing', () => {
    expect(resolveLanguage('constructor')).toBeNull()
    expect(resolveLanguage('__proto__')).toBeNull()
    expect(resolveLanguage('brainfudge')).toBeNull()
    expect(resolveLanguage('')).toBeNull()
    expect(resolveLanguage(null)).toBeNull()
  })
})

describe('languageForPath', () => {
  it('reads the extension, then the file name', () => {
    expect(languageForPath('/tmp/project/parser.go')).toBe('go')
    expect(languageForPath('web/src/App.tsx')).toBe('tsx')
    expect(languageForPath('Dockerfile')).toBe('dockerfile')
    expect(languageForPath('/repo/Makefile')).toBe('make')
    expect(languageForPath('/repo/notes')).toBeNull()
  })
})
```

`web/src/chat/markdown/CodeBlock.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import CodeBlock from './CodeBlock'

describe('CodeBlock', () => {
  it('highlights a known language with classes and no style attribute', async () => {
    const { container } = render(<CodeBlock code={'package main\n\nfunc main() {}'} language="go" />)
    await waitFor(() => expect(container.querySelector('[class*="tok-"]')).not.toBeNull(), { timeout: 10_000 })
    expect(container.querySelector('[style]')).toBeNull()
    expect(container.querySelector('pre')?.textContent).toContain('func main() {}')
  })

  it('renders an unknown label as plain text', async () => {
    const { container } = render(<CodeBlock code="x = 1" language="constructor" />)
    expect(container.querySelector('pre')?.textContent).toBe('x = 1')
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(container.querySelector('[class*="tok-"]')).toBeNull()
  })

  it('numbers the lines from lineStart', () => {
    const { container } = render(<CodeBlock code={'a\nb'} lineNumbers lineStart={41} />)
    expect([...container.querySelectorAll('[aria-hidden="true"]')].map((node) => node.textContent)).toEqual(['41', '42'])
  })

  it('copies the code', async () => {
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    render(<CodeBlock code="go test ./..." language="sh" />)
    screen.getByRole('button', { name: 'Copy code' }).click()
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('go test ./...'))
    expect(await screen.findByText('Copied')).toBeInTheDocument()
  })
})
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd web && npx vitest run src/chat/markdown`
Expected: FAIL with `Failed to resolve import "./languages"`, and the
CodeBlock cases fail on the missing button and classes.

- [ ] **Step 4: Write the language table (first-paint chunk)**

`web/src/chat/markdown/languages.ts`:

```ts
// LanguageId names the grammars the lazy highlighter can load. The table
// lives in the first-paint chunk because it is small and every code block
// asks it; the grammars themselves load on demand.
export type LanguageId =
  | 'typescript'
  | 'tsx'
  | 'javascript'
  | 'shellscript'
  | 'json'
  | 'go'
  | 'python'
  | 'rust'
  | 'yaml'
  | 'toml'
  | 'markdown'
  | 'html'
  | 'css'
  | 'sql'
  | 'diff'
  | 'dockerfile'
  | 'make'
  | 'c'
  | 'cpp'
  | 'java'
  | 'ruby'
  | 'lua'
  | 'xml'
  | 'ini'
  | 'graphql'
  | 'proto'

// A Map, not an object: a fence label is assistant-authored, and a label
// such as constructor or __proto__ must miss instead of resolving an
// inherited property and reaching the highlighter.
const aliases = new Map<string, LanguageId>([
  ['typescript', 'typescript'],
  ['ts', 'typescript'],
  ['mts', 'typescript'],
  ['tsx', 'tsx'],
  ['jsx', 'tsx'],
  ['javascript', 'javascript'],
  ['js', 'javascript'],
  ['mjs', 'javascript'],
  ['cjs', 'javascript'],
  ['shellscript', 'shellscript'],
  ['shell', 'shellscript'],
  ['bash', 'shellscript'],
  ['sh', 'shellscript'],
  ['zsh', 'shellscript'],
  ['console', 'shellscript'],
  ['json', 'json'],
  ['jsonc', 'json'],
  ['go', 'go'],
  ['golang', 'go'],
  ['python', 'python'],
  ['py', 'python'],
  ['rust', 'rust'],
  ['rs', 'rust'],
  ['yaml', 'yaml'],
  ['yml', 'yaml'],
  ['toml', 'toml'],
  ['markdown', 'markdown'],
  ['md', 'markdown'],
  ['html', 'html'],
  ['css', 'css'],
  ['sql', 'sql'],
  ['diff', 'diff'],
  ['patch', 'diff'],
  ['dockerfile', 'dockerfile'],
  ['docker', 'dockerfile'],
  ['make', 'make'],
  ['makefile', 'make'],
  ['c', 'c'],
  ['h', 'c'],
  ['cpp', 'cpp'],
  ['c++', 'cpp'],
  ['cc', 'cpp'],
  ['hpp', 'cpp'],
  ['java', 'java'],
  ['ruby', 'ruby'],
  ['rb', 'ruby'],
  ['lua', 'lua'],
  ['xml', 'xml'],
  ['svg', 'xml'],
  ['ini', 'ini'],
  ['graphql', 'graphql'],
  ['gql', 'graphql'],
  ['proto', 'proto'],
  ['protobuf', 'proto'],
])

const fileNames = new Map<string, LanguageId>([
  ['dockerfile', 'dockerfile'],
  ['makefile', 'make'],
  ['gnumakefile', 'make'],
])

export function resolveLanguage(hint: string | null | undefined): LanguageId | null {
  if (!hint) {
    return null
  }
  return aliases.get(hint.trim().toLowerCase()) ?? null
}

export function languageForPath(path: string): LanguageId | null {
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase()
  const byName = fileNames.get(name)
  if (byName !== undefined) {
    return byName
  }
  const dot = name.lastIndexOf('.')
  return dot <= 0 ? null : resolveLanguage(name.slice(dot + 1))
}
```

- [ ] **Step 5: Write the lazy highlighter**

`web/src/chat/markdown/highlighter.ts`:

```ts
// Parts lifted from deepseek-harness
// packages/client/ui-primitives/src/markdown/highlight.ts at
// 4b399c39a6d1479cd84ca591b4d7c88e24d38a96. MIT license, see
// web/THIRD_PARTY_NOTICES.md. Lifted: the css-variables theme, the
// JavaScript regex engine and patchGoGrammar (verbatim). Changes: an async
// core loaded by dynamic import instead of a boot-time singleton, a
// smaller grammar table, and tokens mapped onto tok-* class names instead
// of inline styles, because the gateway's CSP allows no inline style.
import {
  createCssVariablesTheme,
  createHighlighterCore,
  type HighlighterCore,
  type LanguageRegistration,
  type ThemedToken,
} from 'shiki/core'
import { createJavaScriptRegexEngine } from 'shiki/engine/javascript'

import type { LanguageId } from './languages'

type Grammar = LanguageRegistration[]

/**
 * Replace the Go grammar's repeated one-line struct-field rule with the rule
 * worlpaker/go-syntax merged in 4a9b120a52c (https://github.com/worlpaker/go-syntax/pull/25):
 * the repeated rule backtracks exponentially on lines such as a brace-bearing
 * comment after `struct {`. Delete this patch once `@shikijs/langs` ships that
 * commit; 4.5.0 does not. The rule is located as the only rule in
 * `struct_variables_types_fields` containing the field-token literal below,
 * taken from `@shikijs/langs` 4.4.3.
 */
export function patchGoGrammar(grammars: Grammar): Grammar {
  const go = grammars.find((grammar) => grammar.name === 'go')
  const fields = go?.repository.struct_variables_types_fields
  const patterns = fields?.patterns ?? []
  const rules = patterns.filter((rule) => typeof rule.match === 'string' && rule.match.includes('[^"/`\\s]+;?'))
  if (go === undefined || fields === undefined || rules.length !== 1) {
    throw new Error(
      `highlighter: expected exactly one Go struct-field rule to patch for worlpaker/go-syntax#25, found ${rules.length}; update or delete the patch`,
    )
  }
  const repository = { ...go.repository }
  repository.struct_variables_types_fields = {
    ...fields,
    patterns: patterns.map((rule) =>
      rule === rules[0] ? { ...rule, match: '(?:(?<=\\{)([^/\\`"]*[^\\s/\\`"])\\s*(?=\\}))' } : rule,
    ),
  }
  return grammars.map((grammar) => (grammar === go ? { ...go, repository } : grammar))
}

// Each grammar is its own dynamic import, so Vite emits one chunk per
// language and a session pays only for the languages it shows.
const grammars: Record<LanguageId, () => Promise<{ default: Grammar }>> = {
  typescript: () => import('@shikijs/langs/typescript'),
  tsx: () => import('@shikijs/langs/tsx'),
  javascript: () => import('@shikijs/langs/javascript'),
  shellscript: () => import('@shikijs/langs/shellscript'),
  json: () => import('@shikijs/langs/json'),
  go: async () => ({ default: patchGoGrammar((await import('@shikijs/langs/go')).default) }),
  python: () => import('@shikijs/langs/python'),
  rust: () => import('@shikijs/langs/rust'),
  yaml: () => import('@shikijs/langs/yaml'),
  toml: () => import('@shikijs/langs/toml'),
  markdown: () => import('@shikijs/langs/markdown'),
  html: () => import('@shikijs/langs/html'),
  css: () => import('@shikijs/langs/css'),
  sql: () => import('@shikijs/langs/sql'),
  diff: () => import('@shikijs/langs/diff'),
  dockerfile: () => import('@shikijs/langs/dockerfile'),
  make: () => import('@shikijs/langs/make'),
  c: () => import('@shikijs/langs/c'),
  cpp: () => import('@shikijs/langs/cpp'),
  java: () => import('@shikijs/langs/java'),
  ruby: () => import('@shikijs/langs/ruby'),
  lua: () => import('@shikijs/langs/lua'),
  xml: () => import('@shikijs/langs/xml'),
  ini: () => import('@shikijs/langs/ini'),
  graphql: () => import('@shikijs/langs/graphql'),
  proto: () => import('@shikijs/langs/proto'),
}

export interface HighlightedToken {
  text: string
  className: string
}

const theme = createCssVariablesTheme({ name: 'css-variables', variablePrefix: '--shiki-', fontStyle: true })

// A line longer than this is left untokenized: one minified line must not
// stall the tab.
const maxLineLength = 1000

let core: Promise<HighlighterCore> | null = null
const loading = new Map<LanguageId, Promise<void>>()

function highlighter(): Promise<HighlighterCore> {
  core ??= createHighlighterCore({
    themes: [theme],
    langs: [],
    engine: createJavaScriptRegexEngine({ forgiving: true }),
  })
  return core
}

// ensure loads a grammar once. A grammar that fails to load stays failed:
// its blocks render plain, and the tab does not retry on every token.
async function ensure(lang: LanguageId): Promise<HighlighterCore> {
  const instance = await highlighter()
  let pending = loading.get(lang)
  if (pending === undefined) {
    pending = grammars[lang]().then((module) => instance.loadLanguage(module.default))
    loading.set(lang, pending)
  }
  await pending
  return instance
}

export async function tokenize(code: string, lang: LanguageId): Promise<HighlightedToken[][]> {
  const instance = await ensure(lang)
  const lines = instance.codeToTokensBase(code, {
    lang,
    theme: 'css-variables',
    tokenizeMaxLineLength: maxLineLength,
  })
  return lines.map((line) => line.map(toToken))
}

const variable = /^var\(--shiki-token-([a-z-]+)\)$/

// toToken turns Shiki's colour, which the css-variables theme spells as
// var(--shiki-token-keyword), into the matching tok-keyword class from
// web/src/theme/shiki.css. The foreground colour needs no class.
function toToken(token: ThemedToken): HighlightedToken {
  const classes: string[] = []
  const match = token.color ? variable.exec(token.color) : null
  if (match) {
    classes.push(`tok-${match[1]}`)
  }
  const style = token.fontStyle ?? 0
  if (style & 1) {
    classes.push('tok-italic')
  }
  if (style & 2) {
    classes.push('tok-bold')
  }
  if (style & 4) {
    classes.push('tok-underline')
  }
  return { text: token.content, className: classes.join(' ') }
}
```

- [ ] **Step 6: Write the hook**

`web/src/chat/markdown/useHighlight.ts`:

```ts
import { useEffect, useState } from 'react'

import type { HighlightedToken } from './highlighter'
import { resolveLanguage, type LanguageId } from './languages'

export interface Highlight {
  // lines are the highlighted lines; covered is how much of the code they
  // stand for. A streamed block is longer than its last pass, and the rest
  // renders plain until the next pass lands.
  lines: HighlightedToken[][]
  covered: number
}

let module: Promise<typeof import('./highlighter')> | null = null

// loadHighlighter is the one dynamic import of the Shiki chunk.
export function loadHighlighter(): Promise<typeof import('./highlighter')> {
  module ??= import('./highlighter')
  return module
}

interface Pass {
  code: string
  lang: LanguageId
  lines: HighlightedToken[][]
}

export function useHighlight(code: string, language: string | null): Highlight | null {
  const lang = resolveLanguage(language)
  const [pass, setPass] = useState<Pass | null>(null)

  useEffect(() => {
    if (lang === null) {
      return
    }
    let live = true
    loadHighlighter()
      .then((highlighter) => highlighter.tokenize(code, lang))
      .then(
        (lines) => {
          if (live) {
            setPass({ code, lang, lines })
          }
        },
        // A grammar that cannot load leaves the block plain.
        () => undefined,
      )
    return () => {
      live = false
    }
  }, [code, lang])

  if (lang === null || pass === null || pass.lang !== lang || !code.startsWith(pass.code)) {
    return null
  }
  if (pass.code === code) {
    return { lines: pass.lines, covered: code.length }
  }
  // The block grew at its end. Every line of the last pass but its last is
  // final; the last one may still be growing, so it renders plain.
  const complete = pass.code.lastIndexOf('\n') + 1
  const kept = complete === 0 ? 0 : pass.code.slice(0, complete).split('\n').length - 1
  return { lines: pass.lines.slice(0, kept), covered: complete }
}
```

- [ ] **Step 7: Write the copy button**

`web/src/chat/CopyButton.tsx`:

```tsx
import { useEffect, useState } from 'react'

import styles from './CopyButton.module.css'

export interface CopyButtonProps {
  text: string
  label: string
}

// CopyButton writes its text to the clipboard and says so for a moment.
export default function CopyButton({ text, label }: CopyButtonProps) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) {
      return
    }
    const timer = window.setTimeout(() => setCopied(false), 1500)
    return () => window.clearTimeout(timer)
  }, [copied])
  return (
    <button
      type="button"
      className={styles.copy}
      aria-label={label}
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(
          () => setCopied(true),
          () => undefined,
        )
      }}
    >
      {copied ? 'Copied' : 'Copy'}
    </button>
  )
}
```

`web/src/chat/CopyButton.module.css`:

```css
.copy {
  padding: 2px 8px;
  border: 1px solid var(--dsw-alias-border-l3);
  border-radius: var(--dsw-radius-sm);
  background: transparent;
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}

.copy:hover {
  color: var(--dsw-alias-label-primary);
}
```

- [ ] **Step 8: Highlight the code block**

Replace `web/src/chat/markdown/CodeBlock.tsx` with:

```tsx
import { type ReactNode } from 'react'

import CopyButton from '../CopyButton'
import type { HighlightedToken } from './highlighter'
import { useHighlight } from './useHighlight'
import styles from './CodeBlock.module.css'

export interface CodeBlockProps {
  code: string
  language?: string | null
  // lineStart numbers the first line; lineNumbers turns the gutter on.
  lineStart?: number
  lineNumbers?: boolean
}

function tokens(line: HighlightedToken[]): ReactNode[] {
  return line.map((token, index) =>
    token.className === '' ? token.text : (
      <span key={index} className={token.className}>
        {token.text}
      </span>
    ),
  )
}

// CodeBlock is a fenced block or a file excerpt. It renders plain text at
// once and swaps in the highlighted lines when the lazy Shiki chunk
// answers; an unknown language stays plain.
export default function CodeBlock({ code, language = null, lineStart = 1, lineNumbers = false }: CodeBlockProps) {
  const highlight = useHighlight(code, language)
  const rows: ReactNode[][] =
    highlight === null
      ? code.split('\n').map((line) => [line])
      : [
          ...highlight.lines.map(tokens),
          ...(highlight.covered < code.length ? code.slice(highlight.covered).split('\n').map((line) => [line]) : []),
        ]
  return (
    <div className={styles.block}>
      <div className={styles.header}>
        <span className={styles.label}>{language ?? ''}</span>
        <CopyButton text={code} label="Copy code" />
      </div>
      <pre className={styles.pre}>
        <code>
          {rows.map((row, index) => (
            <span key={index} className={styles.line}>
              {lineNumbers ? (
                <span className={styles.gutter} aria-hidden="true">
                  {lineStart + index}
                </span>
              ) : null}
              {row}
            </span>
          ))}
        </code>
      </pre>
    </div>
  )
}
```

In `CodeBlock.module.css`, change `.label`'s `padding` to `0`.

- [ ] **Step 9: Lift the token palette**

`web/src/theme/shiki.css`:

```css
/* Copied from deepseek-harness packages/client/ui-theme/src/styles/shiki.css
 * at 4b399c39a6d1479cd84ca591b4d7c88e24d38a96. MIT license, see
 * web/THIRD_PARTY_NOTICES.md. Changes: the light block moved from :root to
 * body, where the --dsw-* tokens it aliases are defined; the dark selector
 * was rewritten from body[data-ds-dark-theme] to
 * html[data-ds-theme='dark'] body; the tok-* classes are new: they carry
 * the token colours because the CSP forbids the inline styles Shiki would
 * otherwise emit. */
body {
  --shiki-foreground: var(--dsw-alias-label-primary);
  --shiki-background: var(--dsw-alias-markdown-code-block);
  --shiki-token-constant: #1c7ed6;
  --shiki-token-string: #2f9e44;
  --shiki-token-comment: #868e96;
  --shiki-token-keyword: #d6336c;
  --shiki-token-parameter: #e8590c;
  --shiki-token-function: #6741d9;
  --shiki-token-string-expression: #2b8a3e;
  --shiki-token-punctuation: #495057;
  --shiki-token-link: #1971c2;
}

html[data-ds-theme='dark'] body {
  --shiki-token-constant: #4dabf7;
  --shiki-token-string: #69db7c;
  --shiki-token-comment: #adb5bd;
  --shiki-token-keyword: #faa2c1;
  --shiki-token-parameter: #ffa94d;
  --shiki-token-function: #b197fc;
  --shiki-token-string-expression: #8ce99a;
  --shiki-token-punctuation: #ced4da;
  --shiki-token-link: #74c0fc;
}

.tok-constant {
  color: var(--shiki-token-constant);
}

.tok-string {
  color: var(--shiki-token-string);
}

.tok-comment {
  color: var(--shiki-token-comment);
}

.tok-keyword {
  color: var(--shiki-token-keyword);
}

.tok-parameter {
  color: var(--shiki-token-parameter);
}

.tok-function {
  color: var(--shiki-token-function);
}

.tok-string-expression {
  color: var(--shiki-token-string-expression);
}

.tok-punctuation {
  color: var(--shiki-token-punctuation);
}

.tok-link {
  color: var(--shiki-token-link);
}

.tok-italic {
  font-style: italic;
}

.tok-bold {
  font-weight: 600;
}

.tok-underline {
  text-decoration: underline;
}
```

In `web/src/theme/index.css`, add `@import './shiki.css';` after the
`design-platform.css` import. Add to the list in
`web/THIRD_PARTY_NOTICES.md`:

```markdown
- `web/src/chat/markdown/highlighter.ts`, in part
  (`packages/client/ui-primitives/src/markdown/highlight.ts`).
- `web/src/theme/shiki.css` (`packages/client/ui-theme/src/styles/shiki.css`).
```

- [ ] **Step 10: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/chat`
Expected: PASS. The highlight case loads the real Go grammar in jsdom,
which takes a second or two the first time.

- [ ] **Step 11: Make the budget refuse a lazy library in first paint**

In `web/scripts/budget.mjs`, add `readdirSync` to the `node:fs` import,
and append at the end of the file:

```js
// Lazy libraries must stay out of first paint. Each marker is a string
// literal the library's own code carries, which minification keeps. A
// marker found in no chunk at all means it went stale with an upgrade:
// that fails too, so the check can never pass by accident.
const lazyMarkers = [{ name: 'Shiki', text: 'ShikiError' }]
const assets = readdirSync(join(dist, 'assets')).filter((name) => name.endsWith('.js'))
const firstPaint = new Set(sources.map((source) => source.replace(/^\//, '')))
let lazyFailed = false
for (const marker of lazyMarkers) {
  const holders = assets.filter((name) => readFileSync(join(dist, 'assets', name), 'utf8').includes(marker.text))
  if (holders.length === 0) {
    console.error(`budget: no chunk carries the ${marker.name} marker "${marker.text}"; pick a new marker`)
    lazyFailed = true
    continue
  }
  const eager = holders.filter((name) => firstPaint.has(`assets/${name}`))
  if (eager.length > 0) {
    console.error(`budget: ${marker.name} is in first paint (${eager.join(', ')})`)
    lazyFailed = true
  } else {
    console.log(`${marker.name}: lazy (${holders.join(', ')})`)
  }
}
if (lazyFailed) {
  process.exit(1)
}
```

- [ ] **Step 12: Build and check the budget**

```bash
cd web && npm run typecheck && npm test && npm run build && npm run budget && cd ..
```

Expected: `Shiki: lazy (highlighter-….js)`, first paint still well under
600 KiB, and `internal/web/dist/assets` now holds one small chunk per
grammar. `grep -l 'style=' internal/web/dist/assets/*.js` may list
chunks (Shiki's own HTML renderer code is in the bundle) but nothing in
`web/src` calls it.

- [ ] **Step 13: Commit**

```bash
git add web/package.json web/package-lock.json web/THIRD_PARTY_NOTICES.md web/scripts/budget.mjs web/src/chat web/src/theme internal/web/dist
git commit -m "feat(web): highlight code lazily with class-based Shiki tokens"
```

---

## Task 5: Lazy KaTeX as sanitized MathML

KaTeX renders TeX to an HTML string. Its HTML output needs `katex.css`,
KaTeX's own fonts and inline styles, all three of which the global rules
forbid, so it runs with `output: 'mathml'`. The string is parsed with
`DOMParser`, walked through a MathML allowlist (tags and attributes; no
`style`, no `href`, no event handlers) and rebuilt as React elements: no
`innerHTML`. The browser draws MathML with its own math fonts.

**Files:**
- Create: `web/src/chat/markdown/mathml.ts`,
  `web/src/chat/markdown/mathml.test.ts`
- Create: `web/src/chat/markdown/MathView.tsx` (lazy chunk)
- Modify: `web/src/chat/markdown/Math.tsx`
- Create: `web/src/chat/markdown/Math.test.tsx`
- Modify: `web/scripts/budget.mjs` (KaTeX marker)
- Modify: `web/package.json`, `web/package-lock.json`

**Interfaces:**
- Consumes: `<MathFormula tex display />` and `Markdown.module.css`'s
  `mathSource` class (Task 3).
- Produces:
  - `interface MathNode { tag: string; attributes: Record<string, string>; children: (MathNode | string)[] }`
  - `mathTree(html: string): MathNode | null`
  - `renderMathNode(node: MathNode | string, key: number): ReactNode`
  - `<MathView tex display />` (default export, lazy)
  - `<MathFormula tex display />` now suspends on the lazy chunk and
    falls back to the TeX source.

- [ ] **Step 1: Add KaTeX**

```bash
cd web && npm install --save-exact katex@0.19.1 && cd ..
```

KaTeX 0.19 ships `types/katex.d.ts`; no `@types/katex` is needed. Do not
import `katex/dist/katex.css` anywhere.

- [ ] **Step 2: Write the failing tests**

`web/src/chat/markdown/mathml.test.ts`:

```ts
import katex from 'katex'
import { describe, expect, it } from 'vitest'

import { mathTree } from './mathml'

describe('mathTree', () => {
  it('keeps the MathML KaTeX writes', () => {
    const tree = mathTree(katex.renderToString('x^2', { output: 'mathml', throwOnError: true }))
    expect(tree?.tag).toBe('math')
    expect(JSON.stringify(tree)).toContain('"msup"')
  })

  it('drops style, href and event attributes, and foreign elements', () => {
    const tree = mathTree(
      '<span class="katex"><math><mi style="color:red" href="javascript:alert(1)" onclick="x()" mathvariant="bold">a</mi><img src="x"><script>alert(1)</script></math></span>',
    )
    expect(tree).toEqual({
      tag: 'math',
      attributes: {},
      children: [{ tag: 'mi', attributes: { mathvariant: 'bold' }, children: ['a'] }],
    })
  })

  it('answers null when there is no math element', () => {
    expect(mathTree('<span>no math</span>')).toBeNull()
  })
})
```

`web/src/chat/markdown/Math.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import MathFormula from './Math'

describe('MathFormula', () => {
  it('renders TeX as MathML in the MathML namespace', async () => {
    const { container } = render(<MathFormula tex={'\\frac{1}{3}'} display />)
    await waitFor(() => expect(container.querySelector('math')).not.toBeNull(), { timeout: 10_000 })
    const math = container.querySelector('math')
    expect(math?.namespaceURI).toBe('http://www.w3.org/1998/Math/MathML')
    expect(math?.getAttribute('display')).toBe('block')
    expect(container.querySelector('[style]')).toBeNull()
  })

  it('shows the source of TeX that does not parse', async () => {
    const { container } = render(<MathFormula tex={'\\frac{1}{'} display={false} />)
    await screen.findByText('\\frac{1}{')
    expect(container.querySelector('math')).toBeNull()
  })
})
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd web && npx vitest run src/chat/markdown/mathml.test.ts src/chat/markdown/Math.test.tsx`
Expected: FAIL with `Failed to resolve import "./mathml"`, and the
MathFormula case times out waiting for a `math` element.

- [ ] **Step 4: Write the sanitizer**

`web/src/chat/markdown/mathml.ts`:

```ts
import { createElement, type ReactNode } from 'react'

export interface MathNode {
  tag: string
  attributes: Record<string, string>
  children: (MathNode | string)[]
}

// The MathML Core elements KaTeX emits. Anything else is dropped with its
// subtree, so an element that could load or run something never survives.
const tags = new Set([
  'math', 'semantics', 'annotation', 'mrow', 'mi', 'mn', 'mo', 'ms', 'mtext', 'mspace',
  'msup', 'msub', 'msubsup', 'mfrac', 'msqrt', 'mroot', 'mover', 'munder', 'munderover',
  'mtable', 'mtr', 'mtd', 'mstyle', 'mpadded', 'mphantom', 'menclose', 'merror',
  'mmultiscripts', 'mprescripts', 'none',
])

// Presentation attributes only: no style (the CSP), no href (MathML Core
// links), no event handlers, no xmlns (React picks the namespace).
const attributes = new Set([
  'display', 'mathvariant', 'stretchy', 'fence', 'separator', 'lspace', 'rspace', 'width',
  'height', 'depth', 'voffset', 'minsize', 'maxsize', 'movablelimits', 'symmetric',
  'largeop', 'form', 'accent', 'accentunder', 'linethickness', 'displaystyle',
  'scriptlevel', 'columnalign', 'rowalign', 'columnspacing', 'rowspacing', 'columnlines',
  'rowlines', 'frame', 'framespacing', 'equalrows', 'equalcolumns', 'notation',
  'encoding', 'mathcolor', 'mathbackground', 'align', 'side',
])

function walk(element: Element): MathNode | null {
  const tag = element.localName
  if (!tags.has(tag)) {
    return null
  }
  const kept: Record<string, string> = {}
  for (const attribute of element.attributes) {
    if (attributes.has(attribute.name)) {
      kept[attribute.name] = attribute.value
    }
  }
  const children: (MathNode | string)[] = []
  for (const child of element.childNodes) {
    if (child.nodeType === Node.TEXT_NODE) {
      children.push(child.textContent ?? '')
    } else if (child.nodeType === Node.ELEMENT_NODE) {
      const node = walk(child as Element)
      if (node !== null) {
        children.push(node)
      }
    }
  }
  return { tag, attributes: kept, children }
}

// mathTree reads KaTeX's MathML string through the browser's own HTML
// parser, which applies the MathML foreign-content rules, and keeps only
// the allowlisted tree under the first <math>.
export function mathTree(html: string): MathNode | null {
  const parsed = new DOMParser().parseFromString(html, 'text/html')
  const math = parsed.querySelector('math')
  return math === null ? null : walk(math)
}

export function renderMathNode(node: MathNode | string, key: number): ReactNode {
  if (typeof node === 'string') {
    return node
  }
  return createElement(node.tag, { key, ...node.attributes }, ...node.children.map(renderMathNode))
}
```

Format this file with the repository's formatter settings if a later
lint pass wants one entry per line; the sets are data, not logic.

- [ ] **Step 5: Write the lazy view and switch the formula to it**

`web/src/chat/markdown/MathView.tsx`:

```tsx
import katex from 'katex'
import { useMemo } from 'react'

import { mathTree, renderMathNode } from './mathml'
import styles from './Markdown.module.css'

export interface MathViewProps {
  tex: string
  display: boolean
}

// MathView is the lazy chunk: KaTeX plus the MathML sanitizer. trust is
// false so \href, \url and \htmlClass never produce anything, and a TeX
// error shows the source instead of KaTeX's red, inline-styled message.
export default function MathView({ tex, display }: MathViewProps) {
  const tree = useMemo(() => {
    try {
      return mathTree(katex.renderToString(tex, { output: 'mathml', displayMode: display, throwOnError: true, trust: false, strict: 'ignore' }))
    } catch {
      return null
    }
  }, [tex, display])
  if (tree === null) {
    return display ? (
      <pre className={styles.mathSource} data-math="display">
        <code>{tex}</code>
      </pre>
    ) : (
      <code className={styles.mathSource} data-math="inline">
        {tex}
      </code>
    )
  }
  return (
    <span className={display ? styles.mathDisplay : styles.mathInline} data-math={display ? 'display' : 'inline'}>
      {renderMathNode(tree, 0)}
    </span>
  )
}
```

Replace `web/src/chat/markdown/Math.tsx` with:

```tsx
import { Suspense, lazy } from 'react'

import styles from './Markdown.module.css'

export interface MathFormulaProps {
  tex: string
  display: boolean
}

const MathView = lazy(() => import('./MathView'))

function Source({ tex, display }: MathFormulaProps) {
  if (display) {
    return (
      <pre className={styles.mathSource} data-math="display">
        <code>{tex}</code>
      </pre>
    )
  }
  return (
    <code className={styles.mathSource} data-math="inline">
      {tex}
    </code>
  )
}

// MathFormula shows the TeX source at once and the rendered MathML when
// the lazy KaTeX chunk has loaded.
export default function MathFormula({ tex, display }: MathFormulaProps) {
  return (
    <Suspense fallback={<Source tex={tex} display={display} />}>
      <MathView tex={tex} display={display} />
    </Suspense>
  )
}
```

Append to `Markdown.module.css`:

```css
.mathDisplay {
  display: block;
  margin: 8px 0;
  overflow-x: auto;
}

.mathInline {
  display: inline;
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/chat/markdown`
Expected: PASS, the Task 3 math case included (it counts `data-math`,
which the fallback and the view both carry). If the namespace assertion
fails, the React build in use does not map `math` to the MathML
namespace; stop and report it rather than switching to `innerHTML`.

- [ ] **Step 7: Add the KaTeX marker to the budget**

In `web/scripts/budget.mjs`, change `lazyMarkers` to:

```js
const lazyMarkers = [
  { name: 'Shiki', text: 'ShikiError' },
  { name: 'KaTeX', text: 'KaTeX parse error' },
]
```

- [ ] **Step 8: Build and check**

```bash
cd web && npm run typecheck && npm test && npm run build && npm run budget && cd ..
grep -rnE '@font-face|\.woff2?' web/src internal/web/dist/assets/*.css || echo "no bundled fonts"
```

Expected: `Shiki: lazy`, `KaTeX: lazy`, and `no bundled fonts`.

- [ ] **Step 9: Commit**

```bash
git add web/package.json web/package-lock.json web/scripts/budget.mjs web/src/chat/markdown internal/web/dist
git commit -m "feat(web): render math lazily as sanitized KaTeX MathML"
```

---
## Task 6: The tool-call row, the view registry and the generic view

Every tool call becomes one row: a header button with the tool's title,
a one-line summary of its input and the status capsule, and a body that
mounts only while the row is open. The body comes from a registry keyed
by tool name; this task registers nothing and falls back to the generic
view (input as JSON, output as text). Which rows are open lives in a
context owned by the conversation, so a row the virtualizer unmounts
comes back open.

**Files:**
- Create: `web/src/tools/input.ts`, `web/src/tools/input.test.ts`
- Create: `web/src/tools/view.ts`
- Create: `web/src/tools/environment.tsx`
- Create: `web/src/tools/registry.ts`
- Create: `web/src/tools/StatusCapsule.tsx`
- Create: `web/src/tools/OutputBlock.tsx`
- Create: `web/src/tools/Tools.module.css`
- Create: `web/src/tools/views/GenericView.tsx`
- Create: `web/src/tools/ToolCall.tsx`, `web/src/tools/ToolCall.test.tsx`

**Interfaces:**
- Consumes: `ToolCallPart`, `ToolResultPart` (Task 1); `ToolStatus`
  (Task 1); `AppState`, `AppStore`, `WireMessage` (Task 2).
- Produces:
  - `type ToolInput = Record<string, unknown>`; `parseInput(raw: string): ToolInput | null`;
    `stringField(input: ToolInput | null, key: string): string`;
    `numberField(input: ToolInput | null, key: string): number | null`;
    `boolField(input: ToolInput | null, key: string): boolean`;
    `arrayField(input: ToolInput | null, key: string): unknown[]`;
    `firstLine(value: string, max?: number): string`;
    `compactInput(input: ToolInput | null): string`
  - `interface ToolBodyProps { call: ToolCallPart; input: ToolInput | null; result: ToolResultPart | undefined; status: ToolStatus; messageId: string; depth: number }`
  - `interface ToolView { title(name: string, mcpServers: readonly string[]): string; summary(input: ToolInput | null, result: ToolResultPart | undefined): string; Body: ComponentType<ToolBodyProps> }`
  - `type ChatStore = Store<AppState> & AppStore`;
    `type RenderTranscript = (messages: WireMessage[], depth: number, turnActive: boolean) => ReactNode`
  - `interface ToolEnvironment { mcpServers: readonly string[]; store: ChatStore | null; renderTranscript: RenderTranscript | null; expanded: ReadonlySet<string>; toggle(callId: string): void }`
  - `ToolEnvironmentContext`, `<ToolEnvironmentProvider mcpServers? store? renderTranscript?>`
  - `toolView(name: string): ToolView` (`registry.ts`)
  - `<ToolCall call result status messageId depth />` (default export,
    memoised); `<StatusCapsule status />`;
    `<OutputBlock text maxLines? tone? />` and `<ToolSection label>`
    (named export of `OutputBlock.tsx`); `genericView: ToolView`

- [ ] **Step 1: Write the failing tests**

`web/src/tools/input.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { arrayField, boolField, compactInput, firstLine, numberField, parseInput, stringField } from './input'

describe('parseInput', () => {
  it('reads a JSON object and nothing else', () => {
    expect(parseInput('{"command":"ls"}')).toEqual({ command: 'ls' })
    expect(parseInput('')).toBeNull()
    expect(parseInput('[1]')).toBeNull()
    // A call still streaming its input has half a JSON document.
    expect(parseInput('{"command":"l')).toBeNull()
  })

  it('reads typed fields with safe defaults', () => {
    const input = parseInput('{"a":"x","n":2,"b":true,"l":[1]}')
    expect(stringField(input, 'a')).toBe('x')
    expect(stringField(input, 'n')).toBe('')
    expect(numberField(input, 'n')).toBe(2)
    expect(numberField(null, 'n')).toBeNull()
    expect(boolField(input, 'b')).toBe(true)
    expect(arrayField(input, 'l')).toEqual([1])
    expect(arrayField(input, 'a')).toEqual([])
  })
})

describe('firstLine and compactInput', () => {
  it('keeps one trimmed line, clipped', () => {
    expect(firstLine('  go test\n./...')).toBe('go test')
    expect(firstLine('x'.repeat(100), 10)).toBe('xxxxxxxxx…')
  })

  it('summarises an input by its first string field', () => {
    expect(compactInput({ query: 'parser bug', limit: 3 })).toBe('parser bug')
    expect(compactInput({ limit: 3 })).toBe('{"limit":3}')
    expect(compactInput(null)).toBe('')
  })
})
```

`web/src/tools/ToolCall.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { ToolCallPart, ToolResultPart } from '../chat/parts'
import { ToolEnvironmentProvider } from './environment'
import OutputBlock from './OutputBlock'
import ToolCall from './ToolCall'

const call: ToolCallPart = { id: 'tc-1', name: 'mystery_tool', input: '{"target":"parser.go"}', finished: true }

function result(overrides: Partial<ToolResultPart> = {}): ToolResultPart {
  return { toolCallId: 'tc-1', name: 'mystery_tool', content: 'it worked', mimeType: '', metadata: '', isError: false, ...overrides }
}

function row(props: Partial<Parameters<typeof ToolCall>[0]> = {}) {
  return render(
    <ToolEnvironmentProvider>
      <ul>
        <ToolCall call={call} result={result()} status="done" messageId="m1" depth={0} {...props} />
      </ul>
    </ToolEnvironmentProvider>,
  )
}

describe('ToolCall', () => {
  it('shows the title, the summary and the status, closed', () => {
    row()
    const header = screen.getByRole('button', { name: /mystery_tool/ })
    expect(header).toHaveAttribute('aria-expanded', 'false')
    expect(header).toHaveTextContent('parser.go')
    expect(header).toHaveTextContent('Done')
    expect(screen.queryByText('it worked')).toBeNull()
  })

  it('opens to the input and the output', async () => {
    row()
    await userEvent.click(screen.getByRole('button', { name: /mystery_tool/ }))
    expect(screen.getByRole('button', { name: /mystery_tool/ })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText(/"target": "parser.go"/)).toBeInTheDocument()
    expect(screen.getByText('it worked')).toBeInTheDocument()
  })

  it('labels a failed call and its error output', async () => {
    row({ status: 'error', result: result({ isError: true, content: 'no such file' }) })
    expect(screen.getByText('Failed')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /mystery_tool/ }))
    expect(screen.getByRole('region', { name: 'Error' })).toHaveTextContent('no such file')
  })

  it('names every status', () => {
    const { rerender } = row({ status: 'running', result: undefined })
    expect(screen.getByText('Running')).toBeInTheDocument()
    rerender(
      <ToolEnvironmentProvider>
        <ul>
          <ToolCall call={call} result={undefined} status="interrupted" messageId="m1" depth={0} />
        </ul>
      </ToolEnvironmentProvider>,
    )
    expect(screen.getByText('Interrupted')).toBeInTheDocument()
  })

  it('carries the tool and status for the browser specs', () => {
    const { container } = row()
    const item = container.querySelector('[data-testid="tool-call"]')
    expect(item).toHaveAttribute('data-tool', 'mystery_tool')
    expect(item).toHaveAttribute('data-status', 'done')
  })
})

describe('OutputBlock', () => {
  it('clips a long output until asked', async () => {
    const text = Array.from({ length: 50 }, (_, index) => `line ${index + 1}`).join('\n')
    render(<OutputBlock text={text} maxLines={10} />)
    expect(screen.queryByText(/line 11/)).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Show all 50 lines' }))
    expect(screen.getByText(/line 50/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/tools`
Expected: FAIL with `Failed to resolve import "./input"` and
`Failed to resolve import "./environment"`.

- [ ] **Step 3: Write the input helpers and the view types**

`web/src/tools/input.ts`:

```ts
// ToolInput is a tool call's arguments. They arrive as a JSON string, and
// a call that is still streaming carries half of one, so every reader
// tolerates null and missing fields.
export type ToolInput = Record<string, unknown>

export function parseInput(raw: string): ToolInput | null {
  if (raw.trim() === '') {
    return null
  }
  try {
    const value: unknown = JSON.parse(raw)
    return typeof value === 'object' && value !== null && !Array.isArray(value) ? (value as ToolInput) : null
  } catch {
    return null
  }
}

export function stringField(input: ToolInput | null, key: string): string {
  const value = input?.[key]
  return typeof value === 'string' ? value : ''
}

export function numberField(input: ToolInput | null, key: string): number | null {
  const value = input?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export function boolField(input: ToolInput | null, key: string): boolean {
  return input?.[key] === true
}

export function arrayField(input: ToolInput | null, key: string): unknown[] {
  const value = input?.[key]
  return Array.isArray(value) ? value : []
}

export function firstLine(value: string, max = 80): string {
  const line = value.trim().split('\n')[0] ?? ''
  return line.length > max ? `${line.slice(0, max - 1)}…` : line
}

// compactInput is the summary of a call no view knows: its first string
// argument, or the arguments as one line of JSON.
export function compactInput(input: ToolInput | null): string {
  if (input === null) {
    return ''
  }
  for (const value of Object.values(input)) {
    if (typeof value === 'string' && value !== '') {
      return firstLine(value)
    }
  }
  return firstLine(JSON.stringify(input))
}
```

`web/src/tools/view.ts`:

```ts
import type { ComponentType } from 'react'

import type { ToolCallPart, ToolResultPart } from '../chat/parts'
import type { ToolStatus } from '../chat/toolStatus'
import type { ToolInput } from './input'

export interface ToolBodyProps {
  call: ToolCallPart
  input: ToolInput | null
  result: ToolResultPart | undefined
  status: ToolStatus
  // messageId is the assistant message that made the call; a subagent's
  // session id is built from it.
  messageId: string
  depth: number
}

// ToolView is one tool type's presentation: the row title, the one-line
// summary beside it, and the body an open row mounts.
export interface ToolView {
  title(name: string, mcpServers: readonly string[]): string
  summary(input: ToolInput | null, result: ToolResultPart | undefined): string
  Body: ComponentType<ToolBodyProps>
}
```

- [ ] **Step 4: Write the environment**

`web/src/tools/environment.tsx`:

```tsx
import { createContext, useCallback, useMemo, useState, type ReactNode } from 'react'

import type { AppState, AppStore, WireMessage } from '../store/app'
import type { Store } from '../store/store'

export type ChatStore = Store<AppState> & AppStore

// RenderTranscript draws a subagent's messages with the chat's own message
// view. It is handed down instead of imported, so the tool views never
// import the message view that imports them.
export type RenderTranscript = (messages: WireMessage[], depth: number, turnActive: boolean) => ReactNode

export interface ToolEnvironment {
  mcpServers: readonly string[]
  store: ChatStore | null
  renderTranscript: RenderTranscript | null
  // expanded holds the open rows by call id. It lives above the rows, so a
  // row the virtualized log unmounts and mounts again keeps its state.
  expanded: ReadonlySet<string>
  toggle(callId: string): void
}

const nothingOpen: ReadonlySet<string> = new Set()
const noServers: readonly string[] = []

export const ToolEnvironmentContext = createContext<ToolEnvironment>({
  mcpServers: noServers,
  store: null,
  renderTranscript: null,
  expanded: nothingOpen,
  toggle: () => undefined,
})

export interface ToolEnvironmentProviderProps {
  mcpServers?: readonly string[]
  store?: ChatStore | null
  renderTranscript?: RenderTranscript | null
  children: ReactNode
}

export function ToolEnvironmentProvider({
  mcpServers = noServers,
  store = null,
  renderTranscript = null,
  children,
}: ToolEnvironmentProviderProps) {
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(nothingOpen)
  const toggle = useCallback((callId: string) => {
    setExpanded((previous) => {
      const next = new Set(previous)
      if (next.has(callId)) {
        next.delete(callId)
      } else {
        next.add(callId)
      }
      return next
    })
  }, [])
  const value = useMemo(
    () => ({ mcpServers, store, renderTranscript, expanded, toggle }),
    [mcpServers, store, renderTranscript, expanded, toggle],
  )
  return <ToolEnvironmentContext.Provider value={value}>{children}</ToolEnvironmentContext.Provider>
}
```

- [ ] **Step 5: Write the row parts and the generic view**

`web/src/tools/StatusCapsule.tsx`:

```tsx
import { clsx } from 'clsx'

import type { ToolStatus } from '../chat/toolStatus'
import styles from './Tools.module.css'

const labels: Record<ToolStatus, string> = {
  running: 'Running',
  done: 'Done',
  error: 'Failed',
  interrupted: 'Interrupted',
}

export default function StatusCapsule({ status }: { status: ToolStatus }) {
  return <span className={clsx(styles.capsule, styles[status])}>{labels[status]}</span>
}
```

`web/src/tools/OutputBlock.tsx`:

```tsx
import { clsx } from 'clsx'
import { useState, type ReactNode } from 'react'

import styles from './Tools.module.css'

export interface OutputBlockProps {
  text: string
  maxLines?: number
  tone?: 'plain' | 'error'
}

// OutputBlock is a tool's text output. A long one is clipped until the
// person asks for the rest, so one huge result cannot stall the log.
export default function OutputBlock({ text, maxLines = 40, tone = 'plain' }: OutputBlockProps) {
  const [all, setAll] = useState(false)
  const lines = text.split('\n')
  const clipped = !all && lines.length > maxLines
  return (
    <div className={styles.output}>
      <pre className={clsx(styles.pre, tone === 'error' && styles.errorText)}>
        {clipped ? lines.slice(0, maxLines).join('\n') : text}
      </pre>
      {clipped ? (
        <button type="button" className={styles.more} onClick={() => setAll(true)}>
          Show all {lines.length} lines
        </button>
      ) : null}
    </div>
  )
}

export function ToolSection({ label, children }: { label: string; children: ReactNode }) {
  return (
    <section className={styles.section} aria-label={label}>
      <div className={styles.sectionLabel}>{label}</div>
      {children}
    </section>
  )
}
```

`web/src/tools/views/GenericView.tsx`:

```tsx
import { compactInput } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import type { ToolBodyProps, ToolView } from '../view'

function GenericBody({ call, input, result }: ToolBodyProps) {
  return (
    <>
      <ToolSection label="Input">
        <OutputBlock text={input === null ? call.input : JSON.stringify(input, null, 2)} />
      </ToolSection>
      {result ? (
        <ToolSection label={result.isError ? 'Error' : 'Output'}>
          <OutputBlock text={result.content} tone={result.isError ? 'error' : 'plain'} />
        </ToolSection>
      ) : null}
    </>
  )
}

// genericView draws any tool no view claims: the raw arguments and the
// text result, so nothing the agent did is hidden.
export const genericView: ToolView = {
  title: (name) => name,
  summary: (input) => compactInput(input),
  Body: GenericBody,
}
```

`web/src/tools/registry.ts`:

```ts
import type { ToolView } from './view'
import { genericView } from './views/GenericView'

// views maps a tool name onto its view. The later M2 tasks add entries.
const views = new Map<string, ToolView>([])

export function toolView(name: string): ToolView {
  return views.get(name) ?? genericView
}
```

- [ ] **Step 6: Write the row**

`web/src/tools/ToolCall.tsx`:

```tsx
import { memo, useContext, useMemo } from 'react'

import type { ToolCallPart, ToolResultPart } from '../chat/parts'
import type { ToolStatus } from '../chat/toolStatus'
import { ToolEnvironmentContext } from './environment'
import { parseInput } from './input'
import { toolView } from './registry'
import StatusCapsule from './StatusCapsule'
import styles from './Tools.module.css'

export interface ToolCallProps {
  call: ToolCallPart
  result: ToolResultPart | undefined
  status: ToolStatus
  messageId: string
  depth: number
}

// ToolCall is one call in the tree. The header is a real button, so the
// row opens from the keyboard; the body mounts only while it is open.
function ToolCall({ call, result, status, messageId, depth }: ToolCallProps) {
  const environment = useContext(ToolEnvironmentContext)
  const view = toolView(call.name)
  const input = useMemo(() => parseInput(call.input), [call.input])
  const open = environment.expanded.has(call.id)
  const summary = view.summary(input, result)
  const Body = view.Body
  return (
    <li className={styles.call} data-testid="tool-call" data-tool={call.name} data-status={status}>
      <button
        type="button"
        className={styles.header}
        aria-expanded={open}
        onClick={() => environment.toggle(call.id)}
      >
        <span className={styles.title}>{view.title(call.name, environment.mcpServers)}</span>
        {summary !== '' ? <span className={styles.summary}>{summary}</span> : null}
        <StatusCapsule status={status} />
      </button>
      {open ? (
        <div className={styles.body}>
          <Body call={call} input={input} result={result} status={status} messageId={messageId} depth={depth} />
        </div>
      ) : null}
    </li>
  )
}

export default memo(ToolCall)
```

`web/src/tools/Tools.module.css`:

```css
.call {
  border: 1px solid var(--dsw-alias-border-l2);
  border-radius: var(--dsw-radius-md);
  background: var(--dsw-alias-bg-layer-1);
}

.header {
  display: flex;
  gap: 8px;
  align-items: center;
  width: 100%;
  padding: 6px 10px;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.title {
  flex: none;
  font-weight: 600;
}

.summary {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  color: var(--dsw-alias-label-secondary);
  font-family: var(--ds-font-family-code);
  font-size: var(--crush-font-size-code);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.capsule {
  flex: none;
  margin-left: auto;
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 11px;
}

.running {
  background: var(--dsw-alias-bg-layer-2);
  color: var(--dsw-alias-state-business-primary);
}

.done {
  background: var(--dsw-alias-bg-layer-2);
  color: var(--dsw-alias-state-success-primary);
}

.error {
  background: var(--dsw-static-red-400-a12);
  color: var(--dsw-alias-state-error-primary);
}

.interrupted {
  background: var(--dsw-alias-bg-layer-2);
  color: var(--dsw-alias-state-warn-primary);
}

.body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 0 10px 10px;
}

.section {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.sectionLabel {
  color: var(--dsw-alias-label-tertiary);
  font-size: 11px;
  text-transform: uppercase;
}

.output {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.pre {
  max-height: 480px;
  margin: 0;
  padding: 8px 10px;
  overflow: auto;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-alias-bg-layer-2);
  font-family: var(--ds-font-family-code);
  font-size: var(--crush-font-size-code);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.errorText {
  color: var(--dsw-alias-state-error-primary);
}

.more {
  align-self: flex-start;
  padding: 2px 8px;
  border: 1px solid var(--dsw-alias-border-l3);
  border-radius: var(--dsw-radius-sm);
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}

.note {
  margin: 0;
  color: var(--dsw-alias-label-secondary);
  font-size: 12px;
}

.document {
  max-height: 480px;
  padding: 8px 10px;
  overflow: auto;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-alias-bg-layer-2);
}

.nested {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-left: 10px;
  border-left: 2px solid var(--dsw-alias-border-l2);
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/tools`
Expected: PASS, 11 tests.

- [ ] **Step 8: Run the frontend checks and commit**

```bash
cd web && npm run typecheck && npm test && npm run build && cd ..
git add web/src/tools internal/web/dist
git commit -m "feat(web): add the tool-call row and its view registry"
```

The build is unchanged in behaviour: nothing renders `ToolCall` yet, and
Task 7 wires it in.

---

## Task 7: The message view: parts in order, reasoning, notices and copy

The M1 view lives in `conversation/`, groups parts by type and draws a
name-and-status row per call. The new one, in `chat/` where the spec
puts message bubbles, walks the parts in the order they were written:
consecutive calls form one tool group, assistant text is Markdown, user
text stays plain, reasoning is a collapsed preview, and a canceled,
failed or truncated turn ends with a notice. A tool call's status comes
from Task 1's rule, with the turn counted live only after the last user
message of a running session.

**Files:**
- Create: `web/src/chat/format.ts`, `web/src/chat/format.test.ts`
- Create: `web/src/chat/segments.ts`
- Create: `web/src/chat/Reasoning.tsx`
- Create: `web/src/chat/TurnNotice.tsx`
- Create: `web/src/chat/MessageView.tsx`, `web/src/chat/MessageView.module.css`,
  `web/src/chat/MessageView.test.tsx`
- Delete: `web/src/conversation/MessageView.tsx`,
  `web/src/conversation/MessageView.module.css`,
  `web/src/conversation/MessageView.test.tsx`,
  `web/src/conversation/ToolCallRow.tsx`
- Modify: `web/src/conversation/Conversation.tsx`,
  `web/src/conversation/Conversation.test.tsx`,
  `web/src/conversation/Conversation.rerender.test.tsx`
- Modify: `web/src/conversation/ConversationContainer.tsx`

**Interfaces:**
- Consumes: `messageParts`, `finishOf`, `resultIndex`,
  `resultSignature`, `toolStatus`, `isTurnActive` (Task 1); `Markdown`
  (Task 3); `CopyButton` (Task 4); `ToolCall`, `ToolEnvironmentProvider`
  (Task 6); `SessionStatus` (`web/src/api/wire.ts`).
- Produces:
  - `formatDuration(seconds: number): string`,
    `formatTokens(tokens: number): string` (`format.ts`)
  - `type Segment`, `segments(parts: MessagePart[]): Segment[]`
  - `<Reasoning thinking startedAt finishedAt streaming />`
  - `noticeFor(finish: FinishPart | null): string | null` and
    `<TurnNotice finish />`
  - `<MessageView message results turnActive depth? />` (default export,
    memoised; Task 12 adds `footer?`)
  - `ConversationProps` gains `status?: SessionStatus`.

- [ ] **Step 1: Write the failing tests**

`web/src/chat/format.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { formatDuration, formatTokens } from './format'

describe('formatDuration', () => {
  it('writes seconds, then minutes and seconds', () => {
    expect(formatDuration(0)).toBe('0s')
    expect(formatDuration(42)).toBe('42s')
    expect(formatDuration(75)).toBe('1m 15s')
    expect(formatDuration(3600)).toBe('60m 0s')
    expect(formatDuration(-3)).toBe('0s')
  })
})

describe('formatTokens', () => {
  it('writes thousands and millions with one decimal', () => {
    expect(formatTokens(950)).toBe('950')
    expect(formatTokens(43800)).toBe('43.8K')
    expect(formatTokens(200000)).toBe('200K')
    expect(formatTokens(1250000)).toBe('1.3M')
  })
})
```

`web/src/chat/MessageView.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { WireMessage } from '../api/wire'
import { ToolEnvironmentProvider } from '../tools/environment'
import MessageView from './MessageView'
import { resultIndex } from './parts'

function message(parts: WireMessage['parts'], role = 'assistant', id = 'm1'): WireMessage {
  return { id, role, session_id: 's1', parts, created_at: 1, updated_at: 2 }
}

const text = (value: string) => ({ type: 'text', data: { text: value } })
const call = (id: string, name = 'bash') => ({ type: 'tool_call', data: { id, name, input: '{"command":"ls"}', finished: true } })
const finish = (reason: string, extra: Record<string, unknown> = {}) => ({ type: 'finish', data: { reason, time: 9, ...extra } })
const noResults = new Map()

function view(entry: WireMessage, turnActive = false, results: ReturnType<typeof resultIndex> = noResults) {
  return render(
    <ToolEnvironmentProvider>
      <MessageView message={entry} results={results} turnActive={turnActive} />
    </ToolEnvironmentProvider>,
  )
}

describe('MessageView', () => {
  it('keeps user text plain, markup included', () => {
    const { container } = view(message([text('**not bold** <b>x</b>')], 'user'))
    expect(screen.getByText('**not bold** <b>x</b>')).toBeInTheDocument()
    expect(container.querySelector('strong')).toBeNull()
  })

  it('renders assistant text as Markdown', () => {
    view(message([text('Use **bold**.')]))
    expect(screen.getByText('bold').tagName).toBe('STRONG')
  })

  it('draws the parts in the order they were written', () => {
    const { container } = view(message([text('First.'), call('tc-1'), text('Then.')]))
    const article = container.querySelector('article') as HTMLElement
    // Every segment is a wrapper div; a tool group's first child is its list.
    const order = [...article.children].map((child) =>
      child.firstElementChild?.tagName === 'UL' ? 'tools' : child.textContent,
    )
    expect(order.slice(0, 3)).toEqual(['First.', 'tools', 'Then.'])
  })

  it('groups consecutive calls in one list', () => {
    const { container } = view(message([call('tc-1'), call('tc-2', 'view')]))
    expect(container.querySelectorAll('ul')).toHaveLength(1)
    expect(container.querySelectorAll('[data-testid="tool-call"]')).toHaveLength(2)
  })

  it('collapses reasoning behind its duration', () => {
    const { container } = view(message([{ type: 'reasoning', data: { thinking: 'Look first.\nThen act.', started_at: 10, finished_at: 13 } }]))
    const details = container.querySelector('details') as HTMLDetailsElement
    expect(details.open).toBe(false)
    expect(screen.getByText('Thought for 3s')).toBeInTheDocument()
    expect(details.querySelector('summary')).toHaveTextContent('Look first.')
  })

  it('says it is thinking while the reasoning streams', () => {
    view(message([{ type: 'reasoning', data: { thinking: 'Hmm' } }]), true)
    expect(screen.getByText('Thinking…')).toBeInTheDocument()
  })

  it('renders nothing for a message of tool results', () => {
    const { container } = view(message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok' } }], 'tool'))
    expect(container.querySelector('article')).toBeNull()
  })

  it('renders nothing it does not understand', () => {
    const { container } = view(message([{ type: 'unknown_part', data: {} }]))
    expect(container.querySelector('article')).toBeNull()
  })

  it('marks a call done from a result in another message', () => {
    const answered = message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok' } }], 'tool', 't1')
    view(message([call('tc-1')]), false, resultIndex([answered]))
    expect(screen.getByText('Done')).toBeInTheDocument()
  })

  it('runs a call while its turn is live, and interrupts it when the turn is over', () => {
    const { unmount } = view(message([call('tc-1')]), true)
    expect(screen.getByText('Running')).toBeInTheDocument()
    unmount()
    view(message([call('tc-1')]), false)
    expect(screen.getByText('Interrupted')).toBeInTheDocument()
  })

  it('interrupts a call and says so when the turn was canceled', () => {
    view(message([call('tc-1'), finish('canceled')]), true)
    expect(screen.getByText('Interrupted')).toBeInTheDocument()
    expect(screen.getByRole('note')).toHaveTextContent('Canceled.')
  })

  it('shows why a turn stopped with an error', () => {
    view(message([text('Partial'), finish('error', { message: 'Rate limited' })]))
    expect(screen.getByRole('note')).toHaveTextContent('The turn stopped with an error. Rate limited')
  })

  it('reads a reason it does not know as no notice', () => {
    view(message([text('Hi'), finish('constructor')]))
    expect(screen.queryByRole('note')).toBeNull()
  })

  it('copies the text of a finished reply', async () => {
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    view(message([text('One.'), call('tc-1'), text('Two.'), finish('end_turn')]))
    screen.getByRole('button', { name: 'Copy message' }).click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('One.\n\nTwo.'))
  })

  it('offers no copy while the reply streams', () => {
    view(message([text('One.')]), true)
    expect(screen.queryByRole('button', { name: 'Copy message' })).toBeNull()
  })

  it('opens a tool row inside the message', async () => {
    view(message([call('tc-1', 'mystery')]))
    await userEvent.click(screen.getByRole('button', { name: /mystery/ }))
    expect(screen.getByRole('region', { name: 'Input' })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/chat/format.test.ts src/chat/MessageView.test.tsx`
Expected: FAIL with `Failed to resolve import "./format"` and
`Failed to resolve import "./MessageView"`.

- [ ] **Step 3: Write the formatters, the segments, the reasoning and the notice**

`web/src/chat/format.ts`:

```ts
// formatDuration writes a span of whole seconds the way the TUI does.
export function formatDuration(seconds: number): string {
  const whole = Math.max(0, Math.round(seconds))
  if (whole < 60) {
    return `${whole}s`
  }
  return `${Math.floor(whole / 60)}m ${whole % 60}s`
}

function oneDecimal(value: number): string {
  return value.toFixed(1).replace(/\.0$/, '')
}

// formatTokens writes a token count in K and M with one decimal.
export function formatTokens(tokens: number): string {
  if (tokens < 1000) {
    return String(tokens)
  }
  if (tokens < 1_000_000) {
    return `${oneDecimal(tokens / 1000)}K`
  }
  return `${oneDecimal(tokens / 1_000_000)}M`
}
```

`web/src/chat/segments.ts`:

```ts
import type { MessagePart, ToolCallPart } from './parts'

export type DrawnPart = Exclude<MessagePart, { kind: 'tool_call' | 'tool_result' | 'finish' }>

// Segment is one block of a message, in the order it was written: a run of
// consecutive tool calls is one tool group, everything else stands alone.
export type Segment = { kind: 'tools'; calls: ToolCallPart[] } | { kind: 'part'; part: DrawnPart }

// segments drops what the message does not draw itself: results belong to
// their calls, and the finish part is the notice under the message.
export function segments(parts: MessagePart[]): Segment[] {
  const out: Segment[] = []
  for (const part of parts) {
    if (part.kind === 'tool_result' || part.kind === 'finish') {
      continue
    }
    if (part.kind === 'tool_call') {
      const last = out[out.length - 1]
      if (last !== undefined && last.kind === 'tools') {
        last.calls.push(part.call)
      } else {
        out.push({ kind: 'tools', calls: [part.call] })
      }
      continue
    }
    out.push({ kind: 'part', part })
  }
  return out
}
```

`web/src/chat/Reasoning.tsx`:

```tsx
import { formatDuration } from './format'
import styles from './MessageView.module.css'

export interface ReasoningProps {
  thinking: string
  startedAt: number
  finishedAt: number
  streaming: boolean
}

function preview(thinking: string): string {
  const line = thinking.trim().split('\n')[0] ?? ''
  return line.length > 120 ? `${line.slice(0, 119)}…` : line
}

// Reasoning is the model's thinking: a one-line preview that opens to the
// whole text. It is never Markdown, because it is not written for people.
export default function Reasoning({ thinking, startedAt, finishedAt, streaming }: ReasoningProps) {
  const label = streaming
    ? 'Thinking…'
    : startedAt > 0 && finishedAt > startedAt
      ? `Thought for ${formatDuration(finishedAt - startedAt)}`
      : 'Reasoning'
  return (
    <details className={styles.reasoning}>
      <summary className={styles.reasoningSummary}>
        <span className={styles.reasoningLabel}>{label}</span>
        <span className={styles.reasoningPreview}>{preview(thinking)}</span>
      </summary>
      <div className={styles.reasoningBody}>{thinking}</div>
    </details>
  )
}
```

`web/src/chat/TurnNotice.tsx`:

```tsx
import { clsx } from 'clsx'

import type { FinishPart } from './parts'
import styles from './MessageView.module.css'

// A Map, so a reason the gateway may add later, or any odd string, finds
// nothing instead of an inherited property.
const notices = new Map<string, string>([
  ['error', 'The turn stopped with an error.'],
  ['canceled', 'Canceled.'],
  ['max_tokens', 'Stopped at the output token limit.'],
  ['content_filter', 'The provider declined to answer.'],
])

export function noticeFor(finish: FinishPart | null): string | null {
  return finish === null ? null : (notices.get(finish.reason) ?? null)
}

// TurnNotice ends a turn that did not end normally, with the provider's
// own message when it gave one.
export default function TurnNotice({ finish }: { finish: FinishPart | null }) {
  const text = noticeFor(finish)
  if (finish === null || text === null) {
    return null
  }
  return (
    <div className={clsx(styles.notice, finish.reason === 'error' && styles.noticeError)} role="note">
      {finish.message === '' ? text : `${text} ${finish.message}`}
      {finish.details !== '' ? (
        <details className={styles.noticeDetails}>
          <summary>Details</summary>
          <pre>{finish.details}</pre>
        </details>
      ) : null}
    </div>
  )
}
```

- [ ] **Step 4: Write the message view**

`web/src/chat/MessageView.tsx`:

```tsx
import { clsx } from 'clsx'
import { memo, type ReactNode } from 'react'

import type { WireMessage } from '../api/wire'
import ToolCall from '../tools/ToolCall'
import CopyButton from './CopyButton'
import { formatTokens } from './format'
import Markdown from './markdown/Markdown'
import { finishOf, messageParts, type FinishPart, type ToolResultPart } from './parts'
import Reasoning from './Reasoning'
import { segments, type Segment } from './segments'
import { toolStatus } from './toolStatus'
import TurnNotice, { noticeFor } from './TurnNotice'
import styles from './MessageView.module.css'

export interface MessageViewProps {
  message: WireMessage
  results: ReadonlyMap<string, ToolResultPart>
  // turnActive says the session may still be writing this message's turn:
  // it runs, and no user message came after this one.
  turnActive: boolean
  // depth is how deep in a subagent tree the message sits; 0 is the log.
  depth?: number
}

interface DrawContext {
  message: WireMessage
  results: ReadonlyMap<string, ToolResultPart>
  finish: FinishPart | null
  turnActive: boolean
  streaming: boolean
  depth: number
  last: boolean
}

function draw(segment: Segment, context: DrawContext): ReactNode {
  if (segment.kind === 'tools') {
    return (
      <ul className={styles.tools}>
        {segment.calls.map((call) => {
          const result = context.results.get(call.id)
          return (
            <ToolCall
              key={call.id}
              call={call}
              result={result}
              status={toolStatus(result, context.finish, context.turnActive)}
              messageId={context.message.id}
              depth={context.depth}
            />
          )
        })}
      </ul>
    )
  }
  const part = segment.part
  switch (part.kind) {
    case 'text':
      return context.message.role === 'user' ? <p className={styles.userText}>{part.text}</p> : <Markdown source={part.text} />
    case 'reasoning':
      return (
        <Reasoning
          thinking={part.thinking}
          startedAt={part.startedAt}
          finishedAt={part.finishedAt}
          streaming={context.streaming && context.last}
        />
      )
    case 'shell_command':
      return (
        <pre className={styles.shell}>
          {`$ ${part.command}\n${part.output}`}
          {part.exitCode !== 0 ? `\nexit ${part.exitCode}` : ''}
        </pre>
      )
    case 'sub_agent_report':
      return (
        <details className={styles.report}>
          <summary>
            {part.label}
            {part.failed ? ' (failed)' : ''}
            {part.model !== '' ? ` · ${part.model}` : ''}
          </summary>
          <Markdown source={part.output} />
        </details>
      )
    case 'compaction':
      return <p className={styles.note}>Compacted the conversation, saving {formatTokens(part.savedTokens)} tokens.</p>
    case 'attachment':
      return <span className={styles.attachment}>{part.name}</span>
  }
}

// MessageView draws one message, its parts in the order they were written.
// It is memoised: a streamed token makes a new object for the last message
// only, and the result index keeps its identity until a result changes.
function MessageView({ message, results, turnActive, depth = 0 }: MessageViewProps) {
  const parts = messageParts(message)
  const finish = finishOf(parts)
  const list = segments(parts)
  if (list.length === 0 && noticeFor(finish) === null) {
    return null
  }
  const user = message.role === 'user'
  const streaming = !user && turnActive && finish === null
  const copyText = parts
    .flatMap((part) => (part.kind === 'text' ? [part.text] : []))
    .join('\n\n')
  return (
    <article
      className={clsx(styles.message, user ? styles.user : styles.assistant, streaming && styles.streaming)}
      aria-busy={streaming ? true : undefined}
      data-role={message.role}
    >
      {list.map((segment, index) => (
        <div key={index} className={styles.segment}>
          {draw(segment, { message, results, finish, turnActive, streaming, depth, last: index === list.length - 1 })}
        </div>
      ))}
      <TurnNotice finish={finish} />
      {!user && !streaming && depth === 0 && copyText !== '' ? (
        <div className={styles.actions}>
          <CopyButton text={copyText} label="Copy message" />
        </div>
      ) : null}
    </article>
  )
}

export default memo(MessageView)
```

`web/src/chat/MessageView.module.css`:

```css
.message {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 14px;
  border-radius: var(--dsw-radius-md);
}

.user {
  align-self: flex-end;
  max-width: 80%;
  background: var(--dsw-alias-bg-layer-2);
}

.assistant {
  align-self: stretch;
}

.segment {
  min-width: 0;
}

.streaming > .segment:last-of-type::after {
  content: '▍';
  margin-left: 2px;
  color: var(--dsw-alias-label-secondary);
  animation: blink 1s steps(2, start) infinite;
}

@keyframes blink {
  to {
    visibility: hidden;
  }
}

@media (prefers-reduced-motion: reduce) {
  .streaming > .segment:last-of-type::after {
    animation: none;
  }
}

.userText {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.tools {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.reasoning {
  color: var(--dsw-alias-label-secondary);
  font-size: 12px;
}

.reasoningSummary {
  display: flex;
  gap: 8px;
  cursor: pointer;
}

.reasoningLabel {
  flex: none;
  font-weight: 600;
}

.reasoningPreview {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.reasoningBody {
  margin-top: 4px;
  padding-left: 10px;
  border-left: 2px solid var(--dsw-alias-border-l2);
  white-space: pre-wrap;
}

.notice {
  padding: 6px 10px;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-alias-bg-layer-2);
  font-size: 12px;
}

.noticeError {
  background: var(--dsw-static-red-400-a12);
  color: var(--dsw-alias-state-error-primary);
}

.noticeDetails pre {
  margin: 4px 0 0;
  white-space: pre-wrap;
}

.shell {
  margin: 0;
  padding: 8px 10px;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-alias-bg-layer-2);
  font-family: var(--ds-font-family-code);
  font-size: var(--crush-font-size-code);
  white-space: pre-wrap;
}

.report summary {
  cursor: pointer;
  font-weight: 600;
}

.note {
  margin: 0;
  color: var(--dsw-alias-label-secondary);
  font-size: 12px;
}

.attachment {
  align-self: flex-start;
  padding: 2px 8px;
  border: 1px solid var(--dsw-alias-border-l3);
  border-radius: 999px;
  font-size: 12px;
}

.actions {
  display: flex;
  justify-content: flex-end;
  opacity: 0.6;
}

.message:hover .actions,
.actions:focus-within {
  opacity: 1;
}
```

- [ ] **Step 5: Delete the M1 view and point the conversation at the new one**

```bash
git rm web/src/conversation/MessageView.tsx web/src/conversation/MessageView.module.css web/src/conversation/MessageView.test.tsx web/src/conversation/ToolCallRow.tsx
```

Replace `web/src/conversation/Conversation.tsx` with:

```tsx
import { useEffect, useMemo, useRef } from 'react'

import type { SessionStatus } from '../api/wire'
import MessageView from '../chat/MessageView'
import { resultIndex, resultSignature } from '../chat/parts'
import { isTurnActive } from '../chat/toolStatus'
import type { WireMessage } from '../store/app'
import styles from './Conversation.module.css'

export interface ConversationProps {
  sessionId: string | null
  title: string
  messages: WireMessage[]
  hasMore: boolean
  loadingOlder: boolean
  error?: string | null
  // status is the session row's run state; it decides which turn is live.
  status?: SessionStatus
  onLoadOlder(): void
}

// followDistance is how close to the end the log counts as "at the end".
const followDistance = 24

// untitled is what a session with no row or no title is called.
const untitled = 'Untitled session'

export default function Conversation({
  sessionId,
  title,
  messages,
  hasMore,
  loadingOlder,
  error = null,
  status,
  onLoadOlder,
}: ConversationProps) {
  const log = useRef<HTMLDivElement>(null)
  const bottom = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)
  const seen = useRef<{ sessionId: string | null; newest: string | null } | null>(null)
  const newest = messages.length === 0 ? null : messages[messages.length - 1].id

  // The index is rebuilt only when a result changes. The signature is the
  // dependency on purpose: a token in the last message makes a new array,
  // and a new index with it would re-render every memoised message.
  const signature = resultSignature(messages)
  const results = useMemo(() => resultIndex(messages), [signature])
  // Only the turn after the last user message can still be running; a call
  // left open in an earlier turn is interrupted, whatever the session does.
  const lastUser = messages.findLastIndex((entry) => entry.role === 'user')
  const live = isTurnActive(status)

  useEffect(() => {
    const previous = seen.current
    seen.current = { sessionId, newest }
    if (previous === null || previous.sessionId !== sessionId) {
      pinned.current = true
      bottom.current?.scrollIntoView?.({ block: 'end' })
      return
    }
    if (previous.newest === newest || !pinned.current) {
      return
    }
    bottom.current?.scrollIntoView?.({ block: 'end' })
  }, [sessionId, newest])

  const trackScroll = () => {
    const element = log.current
    if (element === null) {
      return
    }
    pinned.current = element.scrollHeight - element.scrollTop - element.clientHeight <= followDistance
  }

  if (sessionId === null) {
    return <p className={styles.empty}>Select a session</p>
  }

  return (
    <section className={styles.conversation} aria-label="Conversation">
      <header className={styles.header}>
        <h1 className={styles.title}>{title === '' ? untitled : title}</h1>
      </header>
      <div ref={log} className={styles.log} data-testid="conversation-log" onScroll={trackScroll}>
        {hasMore ? (
          <button type="button" className={styles.older} onClick={onLoadOlder} disabled={loadingOlder}>
            {loadingOlder ? 'Loading…' : 'Load older messages'}
          </button>
        ) : null}
        {error ? (
          <p role="alert" className={styles.error}>
            {error}
          </p>
        ) : null}
        {messages.map((message, index) => (
          <MessageView key={message.id} message={message} results={results} turnActive={live && index > lastUser} />
        ))}
        <div ref={bottom} />
      </div>
    </section>
  )
}
```

The scroll behaviour is M1's on purpose: Task 13 replaces it with the
virtualized log.

- [ ] **Step 6: Wire the status and the tool environment**

In `web/src/conversation/ConversationContainer.tsx`, add the imports:

```tsx
import { ToolEnvironmentProvider } from '../tools/environment'
```

In `SessionConversation`, before `return`, add:

```tsx
  const status = useStore(store, (state) => state.sessions.find((row) => row.id === sessionId)?.status)
  // The bootstrap's MCP map keeps its identity until the next bootstrap,
  // so the server list is rebuilt only then.
  const mcp = useStore(store, (state) => state.bootstrap?.mcp)
  const mcpServers = useMemo(() => Object.keys(mcp ?? {}), [mcp])
```

and wrap the returned `Conversation` (adding `status={status}`):

```tsx
  return (
    <ToolEnvironmentProvider mcpServers={mcpServers} store={store}>
      <Conversation
        sessionId={sessionId}
        title={title}
        messages={messages}
        hasMore={data?.hasMore ?? false}
        loadingOlder={data?.loadingOlder ?? false}
        error={data?.olderError ?? null}
        status={status}
        onLoadOlder={loadOlder}
      />
    </ToolEnvironmentProvider>
  )
```

The provider sits inside `SessionConversation`, which is keyed by the
session, so the open rows reset when the selection changes.

- [ ] **Step 7: Move the conversation tests onto the new rows**

In `web/src/conversation/Conversation.test.tsx`, in the case
`marks a tool call done from a result in a later message`, replace the
two `getByText('done')` / `queryByText('running')` lines with:

```tsx
    expect(screen.getByText('Done')).toBeInTheDocument()
    expect(screen.queryByText('Running')).toBeNull()
```

In `web/src/conversation/Conversation.rerender.test.tsx`, replace the
`vi.mock('./ToolCallRow', …)` block with:

```tsx
vi.mock('../tools/ToolCall', () => ({
  default: ({ call, status }: { call: { name: string }; status: string }) => {
    counters.toolRows += 1
    return <li>{`${call.name} ${status}`}</li>
  },
}))
```

and in `draws the call again when a result answers it`, replace the last
assertion with:

```tsx
    expect(screen.getByText('bash done')).toBeInTheDocument()
```

- [ ] **Step 8: Run the frontend checks**

```bash
cd web && npm run typecheck && npm test && npm run build && npm run budget && cd ..
```

Expected: typecheck quiet, every test passing, the budget well under the
cap (Shiki and KaTeX still lazy).

- [ ] **Step 9: Commit**

```bash
git add -A web/src/chat web/src/conversation internal/web/dist
git commit -m "feat(web): draw message parts in order with tool rows"
```

---

## Task 8: The bash terminal card, ANSI and the exit-code capsule

Bash output keeps its colours: a small SGR parser turns ANSI escapes into
spans with `ansi-*` classes (no inline style), strips every other control
sequence so none reaches the DOM, and keeps only the text after a bare
carriage return, the way a terminal overwrites a progress line. 256-colour
and true-colour codes are parsed and dropped, leaving the default colour.

**Files:**
- Create: `web/src/tools/ansi.ts`, `web/src/tools/ansi.test.ts`
- Create: `web/src/tools/Terminal.tsx`, `web/src/tools/Terminal.module.css`
- Create: `web/src/tools/ExitCode.tsx`
- Create: `web/src/tools/views/BashView.tsx`,
  `web/src/tools/views/BashView.test.tsx`
- Modify: `web/src/tools/registry.ts`

**Interfaces:**
- Consumes: `ToolView`, `ToolBodyProps` (Task 6); `parseInput`,
  `stringField`, `numberField`, `boolField`, `firstLine` (Task 6);
  `ToolSection`, `OutputBlock` (Task 6).
- Produces:
  - `interface AnsiSpan { text: string; classes: readonly string[] }`;
    `parseAnsi(input: string): AnsiSpan[][]` (one array per line)
  - `<Terminal command? output maxLines? />`; `<ExitCode code />`
  - `bashView`, `jobOutputView`, `jobKillView: ToolView`; the registry
    maps `bash`, `job_output` and `job_kill`.

- [ ] **Step 1: Write the failing tests**

`web/src/tools/ansi.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { parseAnsi } from './ansi'

describe('parseAnsi', () => {
  it('splits lines and keeps plain text unstyled', () => {
    expect(parseAnsi('one\ntwo')).toEqual([[{ text: 'one', classes: [] }], [{ text: 'two', classes: [] }]])
  })

  it('maps colours and weights onto classes, and resets them', () => {
    expect(parseAnsi('\x1b[1;31mFAIL\x1b[0m ok')).toEqual([
      [
        { text: 'FAIL', classes: ['bold', 'fg-red'] },
        { text: ' ok', classes: [] },
      ],
    ])
    expect(parseAnsi('\x1b[92mgreen\x1b[39m')).toEqual([[{ text: 'green', classes: ['fg-bright-green'] }]])
  })

  it('drops extended colours with their parameters', () => {
    // Both runs keep the default colour, so they merge into one span, and
    // the 4 after 38;2;1;2;3 is never read as underline.
    expect(parseAnsi('\x1b[38;5;208morange\x1b[38;2;1;2;3;4m rgb')).toEqual([
      [
        { text: 'orange', classes: [] },
        { text: ' rgb', classes: ['underline'] },
      ],
    ])
    expect(parseAnsi('\x1b[38;5;208ma\x1b[38;2;1;2;3mb')).toEqual([[{ text: 'ab', classes: [] }]])
  })

  it('keeps the character after a two-byte escape', () => {
    expect(parseAnsi('\x1bMhello')).toEqual([[{ text: 'hello', classes: [] }]])
  })

  it('strips cursor moves, OSC titles and other escapes', () => {
    expect(parseAnsi('\x1b]0;title\x07a\x1b[2Kb\x1b[1Ac\x1b(B')).toEqual([[{ text: 'abc', classes: [] }]])
  })

  it('keeps the text after a carriage return, like a terminal', () => {
    expect(parseAnsi('10%\r50%\r100%\r\ndone')).toEqual([[{ text: '100%', classes: [] }], [{ text: 'done', classes: [] }]])
  })
})
```

`web/src/tools/views/BashView.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { ToolCallPart, ToolResultPart } from '../../chat/parts'
import { ToolEnvironmentProvider } from '../environment'
import ToolCall from '../ToolCall'

const call: ToolCallPart = {
  id: 'tc-1',
  name: 'bash',
  input: '{"command":"go test ./...","description":"Run the tests"}',
  finished: true,
}

function result(metadata: Record<string, unknown>, content = 'FAIL'): ToolResultPart {
  return { toolCallId: 'tc-1', name: 'bash', content, mimeType: '', metadata: JSON.stringify(metadata), isError: false }
}

async function open(entry: ToolResultPart | undefined) {
  const view = render(
    <ToolEnvironmentProvider>
      <ul>
        <ToolCall call={call} result={entry} status={entry ? 'done' : 'running'} messageId="m1" depth={0} />
      </ul>
    </ToolEnvironmentProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: /Bash/ }))
  return view
}

describe('the bash view', () => {
  it('summarises the call by its description', () => {
    render(
      <ToolEnvironmentProvider>
        <ul>
          <ToolCall call={call} result={undefined} status="running" messageId="m1" depth={0} />
        </ul>
      </ToolEnvironmentProvider>,
    )
    expect(screen.getByRole('button', { name: /Bash/ })).toHaveTextContent('Run the tests')
  })

  it('shows the command, the coloured output and the exit code', async () => {
    const { container } = await open(result({ output: '\x1b[31mFAIL\x1b[0m parser', exit_code: 1 }))
    expect(screen.getByText('go test ./...')).toBeInTheDocument()
    const red = screen.getByText('FAIL')
    expect(red.className).toMatch(/fg-red/)
    expect(screen.getByText('exit 1')).toBeInTheDocument()
    expect(container.querySelector('[style]')).toBeNull()
  })

  it('falls back to the result text without metadata', async () => {
    await open({ ...result({}, 'plain output'), metadata: '' })
    expect(screen.getByText('plain output')).toBeInTheDocument()
    expect(screen.queryByText(/^exit /)).toBeNull()
  })

  it('names a background job', async () => {
    await open(result({ output: '', background: true, shell_id: '007' }, 'started'))
    expect(screen.getByText('Running in the background as job 007')).toBeInTheDocument()
  })

  it('shows the command alone while it runs', async () => {
    await open(undefined)
    expect(screen.getByText('go test ./...')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/tools/ansi.test.ts src/tools/views/BashView.test.tsx`
Expected: FAIL with `Failed to resolve import "./ansi"`, and the bash
cases find `bash` as the generic title instead of `Bash`.

- [ ] **Step 3: Write the ANSI parser**

`web/src/tools/ansi.ts`:

```ts
export interface AnsiSpan {
  text: string
  classes: readonly string[]
}

const colors = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white']

interface SgrState {
  bold: boolean
  dim: boolean
  italic: boolean
  underline: boolean
  fg: string | null
  bg: string | null
}

// sequence matches every escape a terminal consumes: CSI (colours, cursor
// moves, erases), OSC (titles, hyperlinks) ended by BEL or ST, and the
// charset designations (ESC ( B) and the other two-byte escapes. Only
// CSI ... m changes the style; the rest vanish.
const sequence = /\x1b\[([0-9;:?]*)([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[()*+][0-9A-Za-z]|\x1b[@-Z\\-_]/g

function classesOf(state: SgrState): string[] {
  const out: string[] = []
  if (state.bold) {
    out.push('bold')
  }
  if (state.dim) {
    out.push('dim')
  }
  if (state.italic) {
    out.push('italic')
  }
  if (state.underline) {
    out.push('underline')
  }
  if (state.fg !== null) {
    out.push(`fg-${state.fg}`)
  }
  if (state.bg !== null) {
    out.push(`bg-${state.bg}`)
  }
  return out
}

function reset(state: SgrState) {
  state.bold = false
  state.dim = false
  state.italic = false
  state.underline = false
  state.fg = null
  state.bg = null
}

function applySgr(state: SgrState, params: string) {
  const codes = params === '' ? [0] : params.split(/[;:]/).map((code) => Number(code) || 0)
  for (let index = 0; index < codes.length; index += 1) {
    const code = codes[index]
    if (code === 38 || code === 48) {
      // Extended colour: 5;n or 2;r;g;b. The parameters are consumed and
      // the colour dropped; the default colour reads on either theme.
      index += codes[index + 1] === 5 ? 2 : codes[index + 1] === 2 ? 4 : 0
      continue
    }
    if (code === 0) {
      reset(state)
    } else if (code === 1) {
      state.bold = true
    } else if (code === 2) {
      state.dim = true
    } else if (code === 3) {
      state.italic = true
    } else if (code === 4) {
      state.underline = true
    } else if (code === 22) {
      state.bold = false
      state.dim = false
    } else if (code === 23) {
      state.italic = false
    } else if (code === 24) {
      state.underline = false
    } else if (code >= 30 && code <= 37) {
      state.fg = colors[code - 30]
    } else if (code === 39) {
      state.fg = null
    } else if (code >= 40 && code <= 47) {
      state.bg = colors[code - 40]
    } else if (code === 49) {
      state.bg = null
    } else if (code >= 90 && code <= 97) {
      state.fg = `bright-${colors[code - 90]}`
    } else if (code >= 100 && code <= 107) {
      state.bg = `bright-${colors[code - 100]}`
    }
  }
}

// parseAnsi turns terminal output into lines of styled spans.
export function parseAnsi(input: string): AnsiSpan[][] {
  const lines: AnsiSpan[][] = [[]]
  const state: SgrState = { bold: false, dim: false, italic: false, underline: false, fg: null, bg: null }
  const push = (chunk: string) => {
    const pieces = chunk.split('\n')
    pieces.forEach((piece, index) => {
      if (index > 0) {
        lines.push([])
      }
      const line = lines[lines.length - 1]
      const carriage = piece.lastIndexOf('\r')
      if (carriage >= 0) {
        // A bare carriage return rewinds the line: what was written before
        // it on this line is overwritten.
        line.length = 0
      }
      const text = carriage >= 0 ? piece.slice(carriage + 1) : piece
      if (text !== '') {
        line.push({ text, classes: classesOf(state) })
      }
    })
  }
  const source = input.replace(/\r\n/g, '\n')
  let last = 0
  for (const match of source.matchAll(sequence)) {
    push(source.slice(last, match.index))
    last = (match.index ?? 0) + match[0].length
    if (match[2] === 'm') {
      applySgr(state, match[1] ?? '')
    }
  }
  push(source.slice(last))
  // Adjacent spans with the same classes are one run.
  return lines.map((line) =>
    line.reduce<AnsiSpan[]>((merged, span) => {
      const previous = merged[merged.length - 1]
      if (previous !== undefined && previous.classes.join(' ') === span.classes.join(' ')) {
        merged[merged.length - 1] = { text: previous.text + span.text, classes: previous.classes }
      } else {
        merged.push(span)
      }
      return merged
    }, []),
  )
}
```

The "strips cursor moves" test needs the merge: `a`, `b` and `c` arrive
as three spans with the same empty classes.

- [ ] **Step 4: Write the terminal card and the capsule**

`web/src/tools/Terminal.tsx`:

```tsx
import { clsx } from 'clsx'
import { useMemo, useState } from 'react'

import { parseAnsi } from './ansi'
import styles from './Terminal.module.css'

export interface TerminalProps {
  command?: string
  output: string
  maxLines?: number
}

// Terminal is a command and its output, coloured from the ANSI escapes.
export default function Terminal({ command = '', output, maxLines = 200 }: TerminalProps) {
  const [all, setAll] = useState(false)
  const lines = useMemo(() => parseAnsi(output), [output])
  const shown = all ? lines : lines.slice(0, maxLines)
  return (
    <div className={styles.terminal}>
      {command !== '' ? (
        <div className={styles.command}>
          <span className={styles.prompt} aria-hidden="true">
            $
          </span>
          <span>{command}</span>
        </div>
      ) : null}
      {output !== '' ? (
        <pre className={styles.output}>
          {shown.map((line, index) => (
            <span key={index} className={styles.line}>
              {line.map((span, position) =>
                span.classes.length === 0 ? (
                  span.text
                ) : (
                  <span key={position} className={clsx(span.classes.map((name) => styles[name]))}>
                    {span.text}
                  </span>
                ),
              )}
            </span>
          ))}
        </pre>
      ) : null}
      {shown.length < lines.length ? (
        <button type="button" className={styles.more} onClick={() => setAll(true)}>
          Show all {lines.length} lines
        </button>
      ) : null}
    </div>
  )
}
```

`web/src/tools/Terminal.module.css`:

```css
.terminal {
  display: flex;
  flex-direction: column;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-alias-bg-layer-2);
  font-family: var(--ds-font-family-code);
  font-size: var(--crush-font-size-code);
}

.command {
  display: flex;
  gap: 8px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--dsw-alias-border-l2);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.prompt {
  color: var(--dsw-alias-label-tertiary);
  user-select: none;
}

.output {
  max-height: 480px;
  margin: 0;
  padding: 6px 10px;
  overflow: auto;
}

.line {
  display: block;
  min-height: 1.5em;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.more {
  align-self: flex-start;
  margin: 0 10px 8px;
  padding: 2px 8px;
  border: 1px solid var(--dsw-alias-border-l3);
  border-radius: var(--dsw-radius-sm);
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}

.bold {
  font-weight: 600;
}

.dim {
  opacity: 0.7;
}

.italic {
  font-style: italic;
}

.underline {
  text-decoration: underline;
}

/* Basic colours map onto the theme's semantic tokens, after DSH's
   ui-primitives ansi.ts table, so both themes stay legible. Black and
   white both take the primary label colour. */
.fg-black,
.fg-white,
.fg-bright-white {
  color: var(--dsw-alias-label-primary);
}

.fg-bright-black {
  color: var(--dsw-alias-label-tertiary);
}

.fg-red {
  color: var(--dsw-alias-state-error-primary);
}

.fg-bright-red {
  color: var(--dsw-alias-state-error-secondary);
}

.fg-green {
  color: var(--dsw-alias-state-success-primary);
}

.fg-bright-green {
  color: var(--dsw-alias-state-success-secondary);
}

.fg-yellow,
.fg-bright-yellow {
  color: var(--dsw-alias-state-warn-primary);
}

.fg-blue,
.fg-bright-blue {
  color: var(--dsw-alias-state-business-primary);
}

.fg-magenta,
.fg-bright-magenta {
  color: #c2255c;
}

.fg-cyan {
  color: var(--dsw-static-blue-600);
}

.fg-bright-cyan {
  color: var(--dsw-static-blue-500);
}

.bg-red,
.bg-bright-red {
  background: var(--dsw-static-red-400-a12);
}

.bg-green,
.bg-bright-green,
.bg-yellow,
.bg-bright-yellow,
.bg-blue,
.bg-bright-blue,
.bg-magenta,
.bg-bright-magenta,
.bg-cyan,
.bg-bright-cyan,
.bg-black,
.bg-bright-black,
.bg-white,
.bg-bright-white {
  background: var(--dsw-alias-bg-layer-1);
}
```

`web/src/tools/ExitCode.tsx`:

```tsx
import { clsx } from 'clsx'

import styles from './Tools.module.css'

// ExitCode is the command's exit status as a capsule: green for zero.
export default function ExitCode({ code }: { code: number }) {
  return <span className={clsx(styles.capsule, code === 0 ? styles.done : styles.error)}>exit {code}</span>
}
```

- [ ] **Step 5: Write the bash views**

`web/src/tools/views/BashView.tsx`:

```tsx
import ExitCode from '../ExitCode'
import { boolField, firstLine, numberField, parseInput, stringField } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import Terminal from '../Terminal'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

function BashBody({ input, result }: ToolBodyProps) {
  const metadata = parseInput(result?.metadata ?? '')
  const command = stringField(input, 'command')
  // The metadata keeps the raw output with its escapes; the result text is
  // what the model read, with the working directory appended.
  const output = stringField(metadata, 'output') || (result?.content ?? '')
  const exit = numberField(metadata, 'exit_code')
  const shellId = stringField(metadata, 'shell_id')
  return (
    <>
      <Terminal command={command} output={result?.isError ? '' : output} />
      {result?.isError ? (
        <ToolSection label="Error">
          <OutputBlock text={result.content} tone="error" />
        </ToolSection>
      ) : null}
      {boolField(metadata, 'background') && shellId !== '' ? (
        <p className={styles.note}>Running in the background as job {shellId}</p>
      ) : null}
      {exit !== null ? <ExitCode code={exit} /> : null}
    </>
  )
}

export const bashView: ToolView = {
  title: () => 'Bash',
  summary: (input) => firstLine(stringField(input, 'description') || stringField(input, 'command')),
  Body: BashBody,
}

function JobBody({ result }: ToolBodyProps) {
  const metadata = parseInput(result?.metadata ?? '')
  return <Terminal command={stringField(metadata, 'command')} output={result?.content ?? ''} />
}

export const jobOutputView: ToolView = {
  title: () => 'Job output',
  summary: (input) => stringField(input, 'shell_id'),
  Body: JobBody,
}

export const jobKillView: ToolView = {
  title: () => 'Stop job',
  summary: (input) => stringField(input, 'shell_id'),
  Body: JobBody,
}
```

In `web/src/tools/registry.ts`, import the three views and fill the map:

```ts
import { bashView, jobKillView, jobOutputView } from './views/BashView'

const views = new Map<string, ToolView>([
  ['bash', bashView],
  ['job_output', jobOutputView],
  ['job_kill', jobKillView],
])
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/tools`
Expected: PASS. `styles['fg-red']` resolves because CSS Modules keep
hyphenated class names as keys.

- [ ] **Step 7: Run the frontend checks and commit**

```bash
cd web && npm run typecheck && npm test && npm run build && cd ..
git add web/src/tools internal/web/dist
git commit -m "feat(web): show bash calls as a terminal card with ANSI"
```

---
## Task 9: View, edit, multiedit and write, with diffs

A read shows the file slice with real line numbers and the lazy Shiki
highlighting the path's language selects. An edit shows a unified diff
computed in the tab from the metadata's `old_content` and `new_content`
(the whole file before and after), or from the call's own `old_string`
and `new_string` while it is still running. A write shows the unified
diff its metadata already carries. Diff rows carry `+` and `−` marks and
screen-reader words, not colour alone.

**Files:**
- Create: `web/src/tools/diff.ts`, `web/src/tools/diff.test.ts`
- Create: `web/src/tools/DiffView.tsx`, `web/src/tools/DiffView.module.css`
- Create: `web/src/tools/views/FileViews.tsx`,
  `web/src/tools/views/FileViews.test.tsx`
- Modify: `web/src/tools/registry.ts`
- Modify: `web/package.json`, `web/package-lock.json`

**Interfaces:**
- Consumes: `CodeBlock` (Tasks 3 and 4); `languageForPath` (Task 4);
  `ToolView`, `ToolBodyProps`, the input helpers, `OutputBlock`,
  `ToolSection` (Task 6).
- Produces:
  - `interface DiffLine { kind: 'hunk' | 'add' | 'del' | 'context'; text: string; oldNumber: number | null; newNumber: number | null }`
  - `diffContents(before: string, after: string, context?: number): DiffLine[]`
  - `diffFromPatch(unified: string): DiffLine[]`
  - `diffStats(lines: DiffLine[]): { added: number; removed: number }`
  - `<DiffView lines />`
  - `readView`, `editView`, `multiEditView`, `writeView: ToolView`; the
    registry maps `view`, `read`, `edit`, `multiedit` and `write`.

- [ ] **Step 1: Add jsdiff**

```bash
cd web && npm install --save-exact diff@9.0.0 && cd ..
```

`diff` 9 ships its own types. If its `structuredPatch` or `parsePatch`
signature differs from the one used below (the 8.x signature: file
names, the two strings, two headers, options), stop and adapt
`diff.ts` only; nothing else imports the package.

- [ ] **Step 2: Write the failing tests**

`web/src/tools/diff.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { diffContents, diffFromPatch, diffStats } from './diff'

describe('diffContents', () => {
  it('numbers both sides and marks the change', () => {
    const lines = diffContents('package parser\n\nfunc Parse() {}\n', 'package parser\n\nfunc Parse() error { return nil }\n')
    expect(lines[0]).toMatchObject({ kind: 'hunk' })
    expect(lines.filter((line) => line.kind === 'del')).toEqual([
      { kind: 'del', text: 'func Parse() {}', oldNumber: 3, newNumber: null },
    ])
    expect(lines.filter((line) => line.kind === 'add')).toEqual([
      { kind: 'add', text: 'func Parse() error { return nil }', oldNumber: null, newNumber: 3 },
    ])
    expect(diffStats(lines)).toEqual({ added: 1, removed: 1 })
  })

  it('answers no lines for equal text', () => {
    expect(diffContents('same\n', 'same\n')).toEqual([])
  })
})

describe('diffFromPatch', () => {
  it('reads the unified diff the write tool stores', () => {
    const patch = '--- a/notes.md\n+++ b/notes.md\n@@ -1,2 +1,2 @@\n # Notes\n-old line\n+new line\n'
    const lines = diffFromPatch(patch)
    expect(lines.map((line) => line.kind)).toEqual(['hunk', 'context', 'del', 'add'])
    expect(lines[3]).toMatchObject({ text: 'new line', newNumber: 2 })
  })

  it('answers no lines for text that is not a patch', () => {
    expect(diffFromPatch('')).toEqual([])
  })
})
```

`web/src/tools/views/FileViews.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { ToolCallPart, ToolResultPart } from '../../chat/parts'
import { ToolEnvironmentProvider } from '../environment'
import ToolCall from '../ToolCall'

function call(name: string, input: Record<string, unknown>): ToolCallPart {
  return { id: 'tc-1', name, input: JSON.stringify(input), finished: true }
}

function result(metadata: Record<string, unknown> | null, content = ''): ToolResultPart {
  return { toolCallId: 'tc-1', name: '', content, mimeType: '', metadata: metadata ? JSON.stringify(metadata) : '', isError: false }
}

async function open(entry: ToolCallPart, answer: ToolResultPart | undefined, title: RegExp) {
  const view = render(
    <ToolEnvironmentProvider>
      <ul>
        <ToolCall call={entry} result={answer} status={answer ? 'done' : 'running'} messageId="m1" depth={0} />
      </ul>
    </ToolEnvironmentProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: title }))
  return view
}

describe('the file views', () => {
  it('reads a file slice with its own line numbers', async () => {
    const { container } = await open(
      call('view', { file_path: '/tmp/project/parser.go', offset: 40 }),
      result({ file_path: '/tmp/project/parser.go', content: 'package parser\nfunc Parse() {}' }),
      /Read/,
    )
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('/tmp/project/parser.go')
    const gutters = [...container.querySelectorAll('[aria-hidden="true"]')].map((node) => node.textContent)
    expect(gutters).toEqual(['41', '42'])
    await waitFor(() => expect(container.querySelector('[class*="tok-"]')).not.toBeNull(), { timeout: 10_000 })
  })

  it('shows an edit as a diff with its counts', async () => {
    await open(
      call('edit', { file_path: '/tmp/project/parser.go', old_string: 'a', new_string: 'b' }),
      result({ additions: 1, removals: 1, old_content: 'x\nold\ny\n', new_content: 'x\nnew\ny\n' }),
      /Edit/,
    )
    expect(screen.getByRole('button', { name: /Edit/ })).toHaveTextContent('+1 −1')
    expect(screen.getByText('old').closest('[data-kind]')).toHaveAttribute('data-kind', 'del')
    expect(screen.getByText('new').closest('[data-kind]')).toHaveAttribute('data-kind', 'add')
  })

  it('diffs the call itself while the edit runs', async () => {
    await open(call('edit', { file_path: '/tmp/a.go', old_string: 'before', new_string: 'after' }), undefined, /Edit/)
    expect(screen.getByText('before').closest('[data-kind]')).toHaveAttribute('data-kind', 'del')
    expect(screen.getByText('after').closest('[data-kind]')).toHaveAttribute('data-kind', 'add')
  })

  it('shows a write from the diff its metadata carries', async () => {
    await open(
      call('write', { file_path: '/tmp/notes.md', content: '# Notes\nnew line\n' }),
      result({ diff: '--- a/notes.md\n+++ b/notes.md\n@@ -1,2 +1,2 @@\n # Notes\n-old line\n+new line\n', additions: 1, removals: 1 }),
      /Write/,
    )
    expect(screen.getByText('new line').closest('[data-kind]')).toHaveAttribute('data-kind', 'add')
  })

  it('shows the error of an edit that failed', async () => {
    await open(
      call('edit', { file_path: '/tmp/a.go', old_string: 'x', new_string: 'y' }),
      { ...result(null, 'old_string not found in file'), isError: true },
      /Edit/,
    )
    expect(screen.getByRole('region', { name: 'Error' })).toHaveTextContent('old_string not found in file')
  })
})
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd web && npx vitest run src/tools/diff.test.ts src/tools/views/FileViews.test.tsx`
Expected: FAIL with `Failed to resolve import "./diff"`, and the file
cases find generic titles.

- [ ] **Step 4: Write the diff model and view**

`web/src/tools/diff.ts`:

```ts
import { parsePatch, structuredPatch } from 'diff'

export interface DiffLine {
  kind: 'hunk' | 'add' | 'del' | 'context'
  text: string
  oldNumber: number | null
  newNumber: number | null
}

// Hunk is the part of jsdiff's hunk this module reads.
interface Hunk {
  oldStart: number
  oldLines: number
  newStart: number
  newLines: number
  lines: string[]
}

function hunkLines(hunks: Hunk[]): DiffLine[] {
  const out: DiffLine[] = []
  for (const hunk of hunks) {
    out.push({
      kind: 'hunk',
      text: `@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`,
      oldNumber: null,
      newNumber: null,
    })
    let oldNumber = hunk.oldStart
    let newNumber = hunk.newStart
    for (const line of hunk.lines) {
      const mark = line[0]
      const text = line.slice(1)
      if (mark === '\\') {
        // "\ No newline at end of file" describes the line above.
        continue
      }
      if (mark === '+') {
        out.push({ kind: 'add', text, oldNumber: null, newNumber })
        newNumber += 1
      } else if (mark === '-') {
        out.push({ kind: 'del', text, oldNumber, newNumber: null })
        oldNumber += 1
      } else {
        out.push({ kind: 'context', text, oldNumber, newNumber })
        oldNumber += 1
        newNumber += 1
      }
    }
  }
  return out
}

// diffContents diffs two versions of a file in the tab.
export function diffContents(before: string, after: string, context = 3): DiffLine[] {
  return hunkLines(structuredPatch('a', 'b', before, after, '', '', { context }).hunks)
}

// diffFromPatch reads a unified diff, such as the write tool's metadata.
export function diffFromPatch(unified: string): DiffLine[] {
  if (unified.trim() === '') {
    return []
  }
  try {
    return parsePatch(unified).flatMap((file) => hunkLines(file.hunks))
  } catch {
    return []
  }
}

export function diffStats(lines: DiffLine[]): { added: number; removed: number } {
  let added = 0
  let removed = 0
  for (const line of lines) {
    if (line.kind === 'add') {
      added += 1
    } else if (line.kind === 'del') {
      removed += 1
    }
  }
  return { added, removed }
}
```

`web/src/tools/DiffView.tsx`:

```tsx
import { clsx } from 'clsx'

import type { DiffLine } from './diff'
import styles from './DiffView.module.css'

const marks: Record<DiffLine['kind'], string> = { hunk: '', add: '+', del: '−', context: ' ' }
const spoken: Record<DiffLine['kind'], string> = { hunk: '', add: 'Added: ', del: 'Removed: ', context: '' }

// DiffView draws a diff, one row per line with both line numbers. The
// mark and a spoken word carry the change, so colour is never the only
// signal.
export default function DiffView({ lines }: { lines: DiffLine[] }) {
  if (lines.length === 0) {
    return <p className={styles.empty}>No changes</p>
  }
  return (
    <pre className={styles.diff}>
      {lines.map((line, index) => (
        <span key={index} className={clsx(styles.line, styles[line.kind])} data-kind={line.kind}>
          <span className={styles.number} aria-hidden="true">
            {line.oldNumber ?? ''}
          </span>
          <span className={styles.number} aria-hidden="true">
            {line.newNumber ?? ''}
          </span>
          <span className={styles.mark} aria-hidden="true">
            {marks[line.kind]}
          </span>
          <span className={styles.spoken}>{spoken[line.kind]}</span>
          <span>{line.text}</span>
        </span>
      ))}
    </pre>
  )
}
```

`web/src/tools/DiffView.module.css`:

```css
.diff {
  max-height: 480px;
  margin: 0;
  padding: 4px 0;
  overflow: auto;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-alias-bg-layer-2);
  font-family: var(--ds-font-family-code);
  font-size: var(--crush-font-size-code);
}

.line {
  display: flex;
  min-height: 1.5em;
  white-space: pre;
}

.number {
  flex: none;
  width: 4ch;
  padding-right: 6px;
  color: var(--dsw-alias-label-tertiary);
  text-align: right;
  user-select: none;
}

.mark {
  flex: none;
  width: 2ch;
  user-select: none;
}

.spoken {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}

.add {
  background: var(--dsw-alias-bg-layer-1);
  color: var(--dsw-alias-state-success-primary);
}

.del {
  background: var(--dsw-static-red-400-a12);
  color: var(--dsw-alias-state-error-primary);
}

.hunk {
  color: var(--dsw-alias-label-tertiary);
}

.context {
  color: inherit;
}

.empty {
  margin: 0;
  color: var(--dsw-alias-label-secondary);
  font-size: 12px;
}
```

- [ ] **Step 5: Write the file views**

`web/src/tools/views/FileViews.tsx`:

```tsx
import CodeBlock from '../../chat/markdown/CodeBlock'
import { languageForPath } from '../../chat/markdown/languages'
import { diffContents, diffFromPatch, diffStats } from '../diff'
import DiffView from '../DiffView'
import { arrayField, numberField, parseInput, stringField, type ToolInput } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

function ErrorSection({ content }: { content: string }) {
  return (
    <ToolSection label="Error">
      <OutputBlock text={content} tone="error" />
    </ToolSection>
  )
}

function counts(metadata: ToolInput | null): string {
  const added = numberField(metadata, 'additions')
  const removed = numberField(metadata, 'removals')
  return added === null || removed === null ? '' : ` +${added} −${removed}`
}

function ReadBody({ input, result }: ToolBodyProps) {
  if (result === undefined) {
    return <p className={styles.note}>Reading…</p>
  }
  if (result.isError) {
    return <ErrorSection content={result.content} />
  }
  const metadata = parseInput(result.metadata)
  const content = stringField(metadata, 'content')
  // A read of several files carries no single slice: the text is the view.
  if (content === '') {
    return <OutputBlock text={result.content} />
  }
  const path = stringField(metadata, 'file_path') || stringField(input, 'file_path')
  return <CodeBlock code={content} language={languageForPath(path)} lineNumbers lineStart={(numberField(input, 'offset') ?? 0) + 1} />
}

export const readView: ToolView = {
  title: () => 'Read',
  summary: (input) => {
    const paths = arrayField(input, 'file_paths').filter((entry): entry is string => typeof entry === 'string')
    return paths.length > 0 ? `${paths.length} files` : stringField(input, 'file_path')
  },
  Body: ReadBody,
}

function EditBody({ input, result }: ToolBodyProps) {
  if (result?.isError) {
    return <ErrorSection content={result.content} />
  }
  const metadata = parseInput(result?.metadata ?? '')
  const before = stringField(metadata, 'old_content')
  const after = stringField(metadata, 'new_content')
  if (metadata !== null && (before !== '' || after !== '')) {
    return <DiffView lines={diffContents(before, after)} />
  }
  // Still running, or no file contents recorded: the call's own strings.
  const edits = arrayField(input, 'edits')
  if (edits.length > 0) {
    const lines = edits.flatMap((edit) => {
      const entry = typeof edit === 'object' && edit !== null ? (edit as ToolInput) : null
      return diffContents(stringField(entry, 'old_string'), stringField(entry, 'new_string'))
    })
    return <DiffView lines={lines} />
  }
  return <DiffView lines={diffContents(stringField(input, 'old_string'), stringField(input, 'new_string'))} />
}

function editSummary(input: ToolInput | null, result: ToolBodyProps['result']): string {
  return `${stringField(input, 'file_path')}${counts(parseInput(result?.metadata ?? ''))}`
}

export const editView: ToolView = { title: () => 'Edit', summary: editSummary, Body: EditBody }
export const multiEditView: ToolView = { title: () => 'Edit', summary: editSummary, Body: EditBody }

function WriteBody({ input, result }: ToolBodyProps) {
  if (result?.isError) {
    return <ErrorSection content={result.content} />
  }
  const patch = stringField(parseInput(result?.metadata ?? ''), 'diff')
  const lines = diffFromPatch(patch)
  if (lines.length > 0) {
    return <DiffView lines={lines} />
  }
  const path = stringField(input, 'file_path')
  return <CodeBlock code={stringField(input, 'content')} language={languageForPath(path)} lineNumbers />
}

export const writeView: ToolView = {
  title: () => 'Write',
  summary: (input, result) => {
    const lines = diffFromPatch(stringField(parseInput(result?.metadata ?? ''), 'diff'))
    const stats = diffStats(lines)
    return `${stringField(input, 'file_path')}${lines.length > 0 ? ` +${stats.added} −${stats.removed}` : ''}`
  },
  Body: WriteBody,
}
```

In `web/src/tools/registry.ts`, import the four views and add to the
map:

```ts
  ['view', readView],
  ['read', readView],
  ['edit', editView],
  ['multiedit', multiEditView],
  ['write', writeView],
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/tools`
Expected: PASS. The read case loads the real Go grammar.

- [ ] **Step 7: Run the frontend checks and commit**

```bash
cd web && npm run typecheck && npm test && npm run build && npm run budget && cd ..
git add web/package.json web/package-lock.json web/src/tools internal/web/dist
git commit -m "feat(web): show reads with line numbers and edits as diffs"
```

---

## Task 10: Search, fetch, todos and MCP views

**Files:**
- Create: `web/src/tools/views/SearchView.tsx`
- Create: `web/src/tools/views/FetchView.tsx`
- Create: `web/src/tools/views/TodosView.tsx`,
  `web/src/tools/views/TodosView.module.css`
- Create: `web/src/tools/views/McpView.tsx`
- Create: `web/src/tools/views/MoreViews.test.tsx`
- Modify: `web/src/tools/registry.ts` (entries and the `mcp_` prefix)

**Interfaces:**
- Consumes: `Markdown` (Task 3); the Task 6 helpers and types;
  `ToolEnvironmentProvider`'s `mcpServers` (Task 6).
- Produces:
  - `grepView`, `globView`, `lsView`, `sourcegraphView`, `fetchView`,
    `webFetchView`, `webSearchView`, `downloadView`, `todosView`,
    `mcpView: ToolView`
  - `splitMcpName(name: string, servers: readonly string[]): { server: string; tool: string }`
  - `toolView(name)` answers `mcpView` for any name starting `mcp_`
    that has no entry of its own.

- [ ] **Step 1: Write the failing tests**

`web/src/tools/views/MoreViews.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { ToolCallPart, ToolResultPart } from '../../chat/parts'
import { ToolEnvironmentProvider } from '../environment'
import ToolCall from '../ToolCall'
import { splitMcpName } from './McpView'

function call(name: string, input: Record<string, unknown>): ToolCallPart {
  return { id: 'tc-1', name, input: JSON.stringify(input), finished: true }
}

function result(content: string, metadata: Record<string, unknown> | null = null, extra: Partial<ToolResultPart> = {}): ToolResultPart {
  return { toolCallId: 'tc-1', name: '', content, mimeType: '', metadata: metadata ? JSON.stringify(metadata) : '', isError: false, ...extra }
}

async function open(entry: ToolCallPart, answer: ToolResultPart, title: RegExp, servers: string[] = []) {
  render(
    <ToolEnvironmentProvider mcpServers={servers}>
      <ul>
        <ToolCall call={entry} result={answer} status="done" messageId="m1" depth={0} />
      </ul>
    </ToolEnvironmentProvider>,
  )
  const header = screen.getByRole('button', { name: title })
  await userEvent.click(header)
  return header
}

describe('the search views', () => {
  it('summarises a grep by its pattern, place and count', async () => {
    const header = await open(
      call('grep', { pattern: 'func Parse', path: '/tmp/project' }),
      result('Found 1 matches\n/tmp/project/parser.go:\n  Line 3: func Parse() {}', { number_of_matches: 1, truncated: false }),
      /Grep/,
    )
    expect(header).toHaveTextContent('func Parse in /tmp/project · 1 match')
    expect(screen.getByText(/Line 3: func Parse/)).toBeInTheDocument()
  })

  it('counts glob files and says when the list was cut', async () => {
    const header = await open(
      call('glob', { pattern: '**/*.go' }),
      result('/tmp/project/parser.go\n/tmp/project/lexer.go', { number_of_files: 2, truncated: true }),
      /Glob/,
    )
    expect(header).toHaveTextContent('**/*.go · 2 files')
    expect(screen.getByText('The list was cut short.')).toBeInTheDocument()
  })
})

describe('the fetch views', () => {
  it('renders fetched Markdown through the safe renderer', async () => {
    const header = await open(
      call('fetch', { url: 'https://charm.land', format: 'markdown' }),
      result('# Charm\n\n<script>alert(1)</script>We build tools.'),
      /Fetch/,
    )
    expect(header).toHaveTextContent('https://charm.land')
    expect(screen.getByRole('heading', { name: 'Charm' })).toBeInTheDocument()
    expect(document.querySelector('script')).toBeNull()
  })

  it('shows fetched HTML as text', async () => {
    await open(call('fetch', { url: 'https://charm.land', format: 'html' }), result('<h1>Charm</h1>'), /Fetch/)
    expect(screen.getByText('<h1>Charm</h1>')).toBeInTheDocument()
  })
})

describe('the todos view', () => {
  it('lists every todo with its state and counts the done ones', async () => {
    const todos = [
      { content: 'Port the parser', status: 'completed', active_form: 'Porting the parser' },
      { content: 'Write the tests', status: 'in_progress', active_form: 'Writing the tests' },
      { content: 'Update the docs', status: 'pending', active_form: 'Updating the docs' },
    ]
    const header = await open(call('todos', { todos }), result('Todo list updated', { todos, completed: 1, total: 3 }), /Todos/)
    expect(header).toHaveTextContent('1/3 done')
    // The row itself is an li with a data-status too, so read inside it.
    const items = [...document.querySelectorAll<HTMLElement>('[data-testid="tool-call"] li[data-status]')]
    expect(items.map((item) => item.dataset.status)).toEqual(['completed', 'in_progress', 'pending'])
    expect(screen.getByText('Writing the tests')).toBeInTheDocument()
  })
})

describe('the MCP view', () => {
  it('splits a server whose name has an underscore by the known servers', () => {
    expect(splitMcpName('mcp_my_server_search', ['my_server', 'my'])).toEqual({ server: 'my_server', tool: 'search' })
    expect(splitMcpName('mcp_github_search_issues', [])).toEqual({ server: 'github', tool: 'search_issues' })
  })

  it('titles the call by server and tool and shows its output', async () => {
    const header = await open(
      call('mcp_github_search_issues', { query: 'parser bug' }),
      result('#12 Parser drops comments'),
      /github/,
      ['github'],
    )
    expect(header).toHaveTextContent('github › search_issues')
    expect(header).toHaveTextContent('parser bug')
    expect(screen.getByText('#12 Parser drops comments')).toBeInTheDocument()
  })

  it('names an image result instead of loading it', async () => {
    await open(call('mcp_shots_capture', {}), result('', null, { mimeType: 'image/png' }), /shots/)
    expect(screen.getByText('The server answered with an image (image/png).')).toBeInTheDocument()
    expect(document.querySelector('img')).toBeNull()
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/tools/views/MoreViews.test.tsx`
Expected: FAIL with `Failed to resolve import "./McpView"`.

- [ ] **Step 3: Write the search and fetch views**

`web/src/tools/views/SearchView.tsx`:

```tsx
import { boolField, numberField, parseInput, stringField, type ToolInput } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

function SearchBody({ result }: ToolBodyProps) {
  if (result === undefined) {
    return <p className={styles.note}>Searching…</p>
  }
  if (result.isError) {
    return (
      <ToolSection label="Error">
        <OutputBlock text={result.content} tone="error" />
      </ToolSection>
    )
  }
  return (
    <>
      <OutputBlock text={result.content} />
      {boolField(parseInput(result.metadata), 'truncated') ? <p className={styles.note}>The list was cut short.</p> : null}
    </>
  )
}

function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`
}

function where(input: ToolInput | null, subject: string): string {
  const path = stringField(input, 'path')
  return path === '' ? subject : `${subject} in ${path}`
}

function counted(summary: string, result: ToolBodyProps['result'], key: string, one: string, many: string): string {
  const count = numberField(parseInput(result?.metadata ?? ''), key)
  return count === null ? summary : `${summary} · ${plural(count, one, many)}`
}

export const grepView: ToolView = {
  title: () => 'Grep',
  summary: (input, result) => counted(where(input, stringField(input, 'pattern')), result, 'number_of_matches', 'match', 'matches'),
  Body: SearchBody,
}

export const globView: ToolView = {
  title: () => 'Glob',
  summary: (input, result) => counted(where(input, stringField(input, 'pattern')), result, 'number_of_files', 'file', 'files'),
  Body: SearchBody,
}

export const lsView: ToolView = {
  title: () => 'List',
  summary: (input, result) => counted(stringField(input, 'path') || '.', result, 'number_of_files', 'file', 'files'),
  Body: SearchBody,
}

export const sourcegraphView: ToolView = {
  title: () => 'Sourcegraph',
  summary: (input) => stringField(input, 'query'),
  Body: SearchBody,
}
```

`web/src/tools/views/FetchView.tsx`:

```tsx
import Markdown from '../../chat/markdown/Markdown'
import { stringField } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

// FetchBody shows what a page answered. Markdown and text go through the
// safe renderer, which shows raw HTML as text; an HTML answer is shown as
// its source.
function FetchBody({ input, result }: ToolBodyProps) {
  if (result === undefined) {
    return <p className={styles.note}>Fetching…</p>
  }
  if (result.isError) {
    return (
      <ToolSection label="Error">
        <OutputBlock text={result.content} tone="error" />
      </ToolSection>
    )
  }
  if (stringField(input, 'format') === 'html') {
    return <OutputBlock text={result.content} />
  }
  return (
    <div className={styles.document}>
      <Markdown source={result.content} />
    </div>
  )
}

export const fetchView: ToolView = { title: () => 'Fetch', summary: (input) => stringField(input, 'url'), Body: FetchBody }
export const webFetchView: ToolView = { title: () => 'Fetch', summary: (input) => stringField(input, 'url'), Body: FetchBody }
export const webSearchView: ToolView = {
  title: () => 'Search the web',
  summary: (input) => stringField(input, 'query'),
  Body: FetchBody,
}
export const downloadView: ToolView = {
  title: () => 'Download',
  summary: (input) => stringField(input, 'url'),
  Body: ({ result }: ToolBodyProps) => (result ? <OutputBlock text={result.content} tone={result.isError ? 'error' : 'plain'} /> : null),
}
```

- [ ] **Step 4: Write the todos and MCP views**

`web/src/tools/views/TodosView.tsx`:

```tsx
import { arrayField, numberField, parseInput, stringField, type ToolInput } from '../input'
import type { ToolBodyProps, ToolView } from '../view'
import styles from './TodosView.module.css'

interface Todo {
  content: string
  status: string
  activeForm: string
}

const glyphs = new Map([
  ['completed', '✓'],
  ['in_progress', '●'],
  ['pending', '○'],
])

// todosOf prefers the metadata's list, which is the list after the call,
// and falls back to the call's own argument while it runs.
function todosOf(input: ToolInput | null, metadata: ToolInput | null): Todo[] {
  const source = arrayField(metadata, 'todos').length > 0 ? arrayField(metadata, 'todos') : arrayField(input, 'todos')
  return source.flatMap((entry) => {
    if (typeof entry !== 'object' || entry === null) {
      return []
    }
    const todo = entry as ToolInput
    return [{ content: stringField(todo, 'content'), status: stringField(todo, 'status'), activeForm: stringField(todo, 'active_form') }]
  })
}

function TodosBody({ input, result }: ToolBodyProps) {
  const todos = todosOf(input, parseInput(result?.metadata ?? ''))
  return (
    <ul className={styles.todos}>
      {todos.map((todo, index) => (
        <li key={index} className={styles[todo.status] ?? styles.pending} data-status={todo.status}>
          <span className={styles.glyph} aria-hidden="true">
            {glyphs.get(todo.status) ?? '○'}
          </span>
          <span>{todo.status === 'in_progress' && todo.activeForm !== '' ? todo.activeForm : todo.content}</span>
        </li>
      ))}
    </ul>
  )
}

export const todosView: ToolView = {
  title: () => 'Todos',
  summary: (input, result) => {
    const metadata = parseInput(result?.metadata ?? '')
    const todos = todosOf(input, metadata)
    const done = numberField(metadata, 'completed') ?? todos.filter((todo) => todo.status === 'completed').length
    const total = numberField(metadata, 'total') ?? todos.length
    return total === 0 ? '' : `${done}/${total} done`
  },
  Body: TodosBody,
}
```

`web/src/tools/views/TodosView.module.css`:

```css
.todos {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.todos li {
  display: flex;
  gap: 8px;
}

.glyph {
  flex: none;
  width: 2ch;
}

.completed {
  color: var(--dsw-alias-label-secondary);
  text-decoration: line-through;
}

.in_progress {
  font-weight: 600;
}

.pending {
  color: inherit;
}
```

`web/src/tools/views/McpView.tsx`:

```tsx
import { compactInput } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

// splitMcpName reads mcp_<server>_<tool>. A server name may itself hold an
// underscore, so the longest known server that prefixes the name wins; a
// name no known server explains splits at its first underscore.
export function splitMcpName(name: string, servers: readonly string[]): { server: string; tool: string } {
  const rest = name.startsWith('mcp_') ? name.slice(4) : name
  const known = servers
    .filter((server) => rest.startsWith(`${server}_`))
    .sort((a, b) => b.length - a.length)[0]
  if (known !== undefined) {
    return { server: known, tool: rest.slice(known.length + 1) }
  }
  const split = rest.indexOf('_')
  return split < 0 ? { server: rest, tool: '' } : { server: rest.slice(0, split), tool: rest.slice(split + 1) }
}

function McpBody({ call, input, result }: ToolBodyProps) {
  return (
    <>
      <ToolSection label="Input">
        <OutputBlock text={input === null ? call.input : JSON.stringify(input, null, 2)} />
      </ToolSection>
      {result === undefined ? null : result.mimeType.startsWith('image/') ? (
        // The CSP loads images from the gateway only, so a data: image from
        // a server is named, not drawn.
        <p className={styles.note}>The server answered with an image ({result.mimeType}).</p>
      ) : (
        <ToolSection label={result.isError ? 'Error' : 'Output'}>
          <OutputBlock text={result.content} tone={result.isError ? 'error' : 'plain'} />
        </ToolSection>
      )}
    </>
  )
}

export const mcpView: ToolView = {
  title: (name, servers) => {
    const { server, tool } = splitMcpName(name, servers)
    return tool === '' ? server : `${server} › ${tool}`
  },
  summary: (input) => compactInput(input),
  Body: McpBody,
}
```

- [ ] **Step 5: Register them**

In `web/src/tools/registry.ts`, import the views and add to the map:

```ts
  ['grep', grepView],
  ['glob', globView],
  ['ls', lsView],
  ['sourcegraph', sourcegraphView],
  ['fetch', fetchView],
  ['web_fetch', webFetchView],
  ['web_search', webSearchView],
  ['download', downloadView],
  ['todos', todosView],
```

and replace `toolView` with:

```ts
export function toolView(name: string): ToolView {
  return views.get(name) ?? (name.startsWith('mcp_') ? mcpView : genericView)
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/tools`
Expected: PASS.

- [ ] **Step 7: Run the frontend checks and commit**

```bash
cd web && npm run typecheck && npm test && npm run build && cd ..
git add web/src/tools internal/web/dist
git commit -m "feat(web): add the search, fetch, todos and MCP tool views"
```

---

## Task 11: Agent and subagent nesting

An `agent`, `agentic_fetch` or `agent_dispatch` call runs a subagent in
its own session, `<message id>$$<call id>`. Opening the row watches that
session: the store subscribes it (Task 2), its snapshot and live
messages arrive like any session's, and the row draws them with the
chat's own message view one level deeper, so the subagent's own tool
calls form a nested tree that streams while it runs. Closing the row, or
the virtualizer unmounting it, unwatches the session and drops its
messages. Nesting stops at depth 3, where a row shows the final answer
only.

**Files:**
- Create: `web/src/tools/views/AgentView.tsx`,
  `web/src/tools/views/AgentView.test.tsx`
- Create: `web/src/chat/Transcript.tsx`
- Modify: `web/src/tools/registry.ts`
- Modify: `web/src/conversation/ConversationContainer.tsx`
  (`renderTranscript`)

**Interfaces:**
- Consumes: `ToolEnvironmentContext` with `store` and
  `renderTranscript` (Task 6); `watchChild`, `unwatchChild` and the
  child snapshot rule (Task 2); `MessageView`, `resultIndex`,
  `resultSignature` (Tasks 1 and 7); `Markdown` (Task 3).
- Produces:
  - `childSessionId(messageId: string, callId: string): string`
  - `maxAgentDepth = 3`
  - `agentView`, `researchView`, `dispatchView: ToolView`
  - `renderTranscript: RenderTranscript` (`chat/Transcript.tsx`)

- [ ] **Step 1: Write the failing tests**

`web/src/tools/views/AgentView.test.tsx`:

```tsx
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { bootstrapSchema, type ParsedFrame, type WireMessage } from '../../api/wire'
import { memoryStorage } from '../../app/storage'
import type { ToolCallPart, ToolResultPart } from '../../chat/parts'
import { renderTranscript } from '../../chat/Transcript'
import { createAppStore } from '../../store/app'
import { ToolEnvironmentProvider } from '../environment'
import ToolCall from '../ToolCall'
import { childSessionId } from './AgentView'

const call: ToolCallPart = { id: 'tc-agent', name: 'agent', input: '{"prompt":"Map the parser package"}', finished: true }
const child = childSessionId('a2', 'tc-agent')

function store() {
  const created = createAppStore(memoryStorage())
  created.applyBootstrap(
    bootstrapSchema.parse({
      version: 't',
      working_dir: '/w',
      current_session_id: 's1',
      sessions: [{ id: 's1', parent_session_id: '', title: 'Top', message_count: 1, updated_at: 1, status: 'running' }],
      model: { id: 'm', name: 'M', provider: 'p', context_window: 1000, agent_ready: true },
      agent_ready: true,
      permission_mode: 'ask',
      input_mode: 'code',
      lsp: {},
      mcp: {},
      agent_runs: [],
    }),
  )
  return created
}

function childSnapshot(messages: WireMessage[]): ParsedFrame {
  return {
    seq: 2,
    type: 'session_snapshot',
    sessionId: child,
    payload: {
      session_id: child,
      session: { id: child, parent_session_id: 's1', title: 'Sub', message_count: messages.length, updated_at: 1 },
      messages,
      has_more: false,
      busy: true,
      queued: [],
      files: [],
    },
  }
}

const childCall: WireMessage = {
  id: 'c1',
  role: 'assistant',
  session_id: child,
  parts: [{ type: 'tool_call', data: { id: 'tc-child', name: 'view', input: '{"file_path":"/tmp/project/lexer.go"}', finished: true } }],
  created_at: 1,
  updated_at: 1,
}

function view(chat = store(), result?: ToolResultPart) {
  render(
    <ToolEnvironmentProvider store={chat} renderTranscript={renderTranscript}>
      <ul>
        <ToolCall call={call} result={result} status={result ? 'done' : 'running'} messageId="a2" depth={0} />
      </ul>
    </ToolEnvironmentProvider>,
  )
  return chat
}

describe('the agent view', () => {
  it('builds the subagent session id the coordinator uses', () => {
    expect(child).toBe('a2$$tc-agent')
  })

  it('summarises the call by its prompt', () => {
    view()
    expect(screen.getByRole('button', { name: /Agent/ })).toHaveTextContent('Map the parser package')
  })

  it('watches the child while open and draws its calls as a nested tree', async () => {
    const chat = view()
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([child])
    expect(screen.getByText('Loading the subagent…')).toBeInTheDocument()
    act(() => chat.applyFrame(childSnapshot([childCall])))
    const nested = screen.getByRole('button', { name: /Read/ })
    expect(nested).toHaveTextContent('/tmp/project/lexer.go')
    expect(nested).toHaveTextContent('Running')
  })

  it('unwatches the child when the row closes', async () => {
    const chat = view()
    const header = screen.getByRole('button', { name: /Agent/ })
    await userEvent.click(header)
    await userEvent.click(header)
    expect(chat.get().watchedChildren).toEqual([])
  })

  it('shows the final answer as Markdown', async () => {
    view(store(), { toolCallId: 'tc-agent', name: 'agent', content: 'The parser has **two** files.', mimeType: '', metadata: '', isError: false })
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(screen.getByText('two').tagName).toBe('STRONG')
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/tools/views/AgentView.test.tsx`
Expected: FAIL with `Failed to resolve import "../../chat/Transcript"`.

- [ ] **Step 3: Write the transcript renderer**

`web/src/chat/Transcript.tsx`:

```tsx
import { useMemo, type ReactNode } from 'react'

import type { WireMessage } from '../api/wire'
import type { RenderTranscript } from '../tools/environment'
import MessageView from './MessageView'
import { resultIndex, resultSignature } from './parts'

function Transcript({ messages, depth, turnActive }: { messages: WireMessage[]; depth: number; turnActive: boolean }) {
  const signature = resultSignature(messages)
  const results = useMemo(() => resultIndex(messages), [signature])
  const lastUser = messages.findLastIndex((entry) => entry.role === 'user')
  return (
    <>
      {messages.map((message, index) => (
        <MessageView
          key={message.id}
          message={message}
          results={results}
          turnActive={turnActive && index > lastUser}
          depth={depth}
        />
      ))}
    </>
  )
}

// renderTranscript is handed to the tool views through the environment, so
// a subagent's messages draw with the same view as the log's.
export const renderTranscript: RenderTranscript = (messages, depth, turnActive): ReactNode => (
  <Transcript messages={messages} depth={depth} turnActive={turnActive} />
)
```

- [ ] **Step 4: Write the agent view**

`web/src/tools/views/AgentView.tsx`:

```tsx
import { useContext, useEffect, useSyncExternalStore } from 'react'

import Markdown from '../../chat/markdown/Markdown'
import type { WireMessage } from '../../store/app'
import { ToolEnvironmentContext, type ChatStore } from '../environment'
import { firstLine, stringField } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

// maxAgentDepth stops the tree: a subagent's subagent's subagent shows its
// answer only, so one runaway delegation cannot nest the log forever.
export const maxAgentDepth = 3

// childSessionId is session.Service.CreateAgentToolSessionID.
export function childSessionId(messageId: string, callId: string): string {
  return `${messageId}$$${callId}`
}

const noSubscribe = () => () => undefined

function useChildMessages(store: ChatStore | null, sessionID: string): WireMessage[] | undefined {
  return useSyncExternalStore(
    store === null ? noSubscribe : store.subscribe,
    () => store?.get().sessionData[sessionID]?.messages,
    () => undefined,
  )
}

function AgentBody({ call, result, status, messageId, depth }: ToolBodyProps) {
  const environment = useContext(ToolEnvironmentContext)
  const store = environment.store
  const sessionID = childSessionId(messageId, call.id)
  const nests = store !== null && environment.renderTranscript !== null && depth < maxAgentDepth
  useEffect(() => {
    if (!nests || store === null) {
      return
    }
    store.watchChild(sessionID)
    return () => store.unwatchChild(sessionID)
  }, [nests, store, sessionID])
  const messages = useChildMessages(nests ? store : null, sessionID)
  return (
    <>
      {nests ? (
        <div className={styles.nested}>
          {messages === undefined ? (
            <p className={styles.note}>Loading the subagent…</p>
          ) : (
            environment.renderTranscript?.(messages, depth + 1, status === 'running')
          )}
        </div>
      ) : null}
      {result === undefined ? null : result.isError ? (
        <ToolSection label="Error">
          <OutputBlock text={result.content} tone="error" />
        </ToolSection>
      ) : (
        <ToolSection label="Answer">
          <div className={styles.document}>
            <Markdown source={result.content} />
          </div>
        </ToolSection>
      )}
    </>
  )
}

export const agentView: ToolView = {
  title: () => 'Agent',
  summary: (input) => firstLine(stringField(input, 'prompt')),
  Body: AgentBody,
}

export const researchView: ToolView = {
  title: () => 'Research',
  summary: (input) => stringField(input, 'url') || firstLine(stringField(input, 'prompt')),
  Body: AgentBody,
}

export const dispatchView: ToolView = {
  title: () => 'Agent',
  summary: (input) => firstLine(stringField(input, 'prompt') || stringField(input, 'task')),
  Body: AgentBody,
}
```

In `web/src/tools/registry.ts`, import the three views and add:

```ts
  ['agent', agentView],
  ['agentic_fetch', researchView],
  ['agent_dispatch', dispatchView],
```

- [ ] **Step 5: Hand the transcript to the log's tool rows**

In `web/src/conversation/ConversationContainer.tsx`, import
`renderTranscript` from `../chat/Transcript` and add
`renderTranscript={renderTranscript}` to the `ToolEnvironmentProvider`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/tools src/conversation src/store`
Expected: PASS.

- [ ] **Step 7: Run the frontend checks and commit**

```bash
cd web && npm run typecheck && npm test && npm run build && cd ..
git add web/src/tools web/src/chat web/src/conversation internal/web/dist
git commit -m "feat(web): nest a subagent's tool calls under its agent row"
```

---
## Task 12: The turn footer, the usage summary and the context ring

The gateway already sends what the footer shows, so no field is added:
every assistant message carries its `model` and a `finish` part with the
finish time, and the session row (bootstrap, `GET /api/sessions` and
every `session` frame, which the agent publishes on each usage write)
carries `prompt_tokens` and `completion_tokens`, the last step's context
size, and `cost`, the running total. Under each assistant message that
ends a turn the footer reads `model · duration`, the duration running
from the turn's user message to the finish. The conversation header
shows the context ring, the tokens in context against the current
model's `context_window`, and the session cost.

**Files:**
- Modify: `web/src/chat/format.ts`, `web/src/chat/format.test.ts`
  (`formatCost`)
- Create: `web/src/chat/turns.ts`, `web/src/chat/turns.test.ts`
- Create: `web/src/chat/UsageSummary.tsx`, `web/src/chat/ContextRing.tsx`,
  `web/src/chat/Usage.module.css`, `web/src/chat/UsageSummary.test.tsx`
- Modify: `web/src/chat/MessageView.tsx`, `MessageView.module.css`,
  `MessageView.test.tsx` (`footer`)
- Modify: `web/src/conversation/Conversation.tsx`,
  `Conversation.module.css` (header, footers)
- Modify: `web/src/conversation/ConversationContainer.tsx` (usage)

**Interfaces:**
- Consumes: `finishOf`, `messageParts` (Task 1); `formatDuration`,
  `formatTokens` (Task 7); `SessionRow.prompt_tokens`,
  `completion_tokens`, `cost` and `WireMessage.model` (Task 1);
  `bootstrap.model.context_window` kept current by `model_changed`
  (Task 2).
- Produces:
  - `formatCost(cost: number): string`
  - `turnFooters(messages: WireMessage[]): Map<string, string>`
  - `interface Usage { promptTokens: number; completionTokens: number; cost: number; contextWindow: number }`
  - `<UsageSummary usage />` (default export), `<ContextRing fraction />`
  - `MessageViewProps.footer?: string | null`
  - `ConversationProps.usage?: Usage | null`

- [ ] **Step 1: Write the failing tests**

Append to `web/src/chat/format.test.ts` (and import `formatCost`):

```ts
describe('formatCost', () => {
  it('writes dollars to the cent and marks a sub-cent cost', () => {
    expect(formatCost(0)).toBe('$0.00')
    expect(formatCost(0.004)).toBe('<$0.01')
    expect(formatCost(0.1234)).toBe('$0.12')
    expect(formatCost(12.5)).toBe('$12.50')
  })
})
```

`web/src/chat/turns.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import type { WireMessage } from '../api/wire'
import { turnFooters } from './turns'

function entry(id: string, role: string, createdAt: number, parts: WireMessage['parts'] = [], model?: string): WireMessage {
  return { id, role, session_id: 's1', parts, created_at: createdAt, updated_at: createdAt, model }
}

const finish = (reason: string, time: number) => ({ type: 'finish', data: { reason, time } })

describe('turnFooters', () => {
  it('times a turn from its prompt to the message that ends it', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [finish('tool_use', 105)], 'claude-sonnet'),
      entry('t1', 'tool', 106),
      entry('a2', 'assistant', 107, [finish('end_turn', 125)], 'claude-sonnet'),
    ])
    expect([...footers.entries()]).toEqual([['a2', 'claude-sonnet · 25s']])
  })

  it('ends a canceled turn too, and skips a message still streaming', () => {
    const footers = turnFooters([
      entry('u1', 'user', 100),
      entry('a1', 'assistant', 101, [finish('canceled', 103)], 'm'),
      entry('u2', 'user', 200),
      entry('a2', 'assistant', 201, [], 'm'),
    ])
    expect([...footers.keys()]).toEqual(['a1'])
  })

  it('leaves out what it does not know', () => {
    const footers = turnFooters([entry('a1', 'assistant', 101, [finish('end_turn', 0)])])
    expect(footers.size).toBe(0)
  })
})
```

`web/src/chat/UsageSummary.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import UsageSummary from './UsageSummary'

describe('UsageSummary', () => {
  it('shows the context in use, its share of the window and the cost', () => {
    const { container } = render(
      <UsageSummary usage={{ promptTokens: 42000, completionTokens: 1800, cost: 0.1234, contextWindow: 200000 }} />,
    )
    const summary = screen.getByTestId('usage-summary')
    expect(summary).toHaveTextContent('43.8K tokens')
    expect(summary).toHaveTextContent('22% of 200K')
    expect(summary).toHaveTextContent('$0.12')
    expect(screen.getByRole('img', { name: '22% of the context window used' })).toBeInTheDocument()
    expect(container.querySelector('[style]')).toBeNull()
  })

  it('drops the ring when the window is unknown', () => {
    render(<UsageSummary usage={{ promptTokens: 10, completionTokens: 0, cost: 0, contextWindow: 0 }} />)
    expect(screen.queryByRole('img')).toBeNull()
    expect(screen.getByTestId('usage-summary')).toHaveTextContent('10 tokens')
  })

  it('renders nothing for a session that has used nothing', () => {
    const { container } = render(<UsageSummary usage={{ promptTokens: 0, completionTokens: 0, cost: 0, contextWindow: 1000 }} />)
    expect(container).toBeEmptyDOMElement()
  })
})
```

Append to the `describe` in `web/src/chat/MessageView.test.tsx`:

```tsx
  it('shows the turn footer it is given', () => {
    render(
      <ToolEnvironmentProvider>
        <MessageView message={message([text('Done.'), finish('end_turn')])} results={noResults} turnActive={false} footer="claude-sonnet · 25s" />
      </ToolEnvironmentProvider>,
    )
    expect(screen.getByText('claude-sonnet · 25s')).toBeInTheDocument()
  })
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/chat`
Expected: FAIL with `formatCost is not a function`,
`Failed to resolve import "./turns"` and
`Failed to resolve import "./UsageSummary"`.

- [ ] **Step 3: Write the cost format and the footers**

Append to `web/src/chat/format.ts`:

```ts
// formatCost writes a dollar cost to the cent; a cost under a cent that is
// not zero says so instead of rounding to nothing.
export function formatCost(cost: number): string {
  if (cost > 0 && cost < 0.01) {
    return '<$0.01'
  }
  return `$${Math.max(0, cost).toFixed(2)}`
}
```

`web/src/chat/turns.ts`:

```ts
import type { WireMessage } from '../api/wire'
import { formatDuration } from './format'
import { finishOf, messageParts } from './parts'

// turnFooters writes the footer of every assistant message that ends a
// turn: one whose finish is anything but tool_use. It reads "model ·
// duration", the duration running from the turn's user message to the
// finish, in the seconds the gateway stamps. A footer is a string so a
// memoised message view compares it by value.
export function turnFooters(messages: WireMessage[]): Map<string, string> {
  const out = new Map<string, string>()
  let turnStart: number | null = null
  for (const message of messages) {
    if (message.role === 'user') {
      turnStart = message.created_at
      continue
    }
    if (message.role !== 'assistant') {
      continue
    }
    const finish = finishOf(messageParts(message))
    if (finish === null || finish.reason === 'tool_use') {
      continue
    }
    const started = turnStart ?? message.created_at
    const pieces: string[] = []
    if (message.model) {
      pieces.push(message.model)
    }
    if (finish.time > 0 && finish.time >= started) {
      pieces.push(formatDuration(finish.time - started))
    }
    if (pieces.length > 0) {
      out.set(message.id, pieces.join(' · '))
    }
  }
  return out
}
```

- [ ] **Step 4: Write the summary and the ring**

`web/src/chat/ContextRing.tsx`:

```tsx
import { clsx } from 'clsx'

import styles from './Usage.module.css'

const radius = 6
const circumference = 2 * Math.PI * radius

// ContextRing is the share of the context window in use. The arc length is
// an SVG presentation attribute, not a style, so the CSP has no say in it.
export default function ContextRing({ fraction }: { fraction: number }) {
  const clamped = Math.min(1, Math.max(0, fraction))
  const percent = Math.round(clamped * 100)
  return (
    <svg
      className={clsx(styles.ring, clamped >= 0.8 && styles.ringHigh)}
      width="16"
      height="16"
      viewBox="0 0 16 16"
      role="img"
      aria-label={`${percent}% of the context window used`}
    >
      <circle className={styles.ringTrack} cx="8" cy="8" r={radius} fill="none" strokeWidth="2" />
      <circle
        className={styles.ringFill}
        cx="8"
        cy="8"
        r={radius}
        fill="none"
        strokeWidth="2"
        strokeDasharray={`${(circumference * clamped).toFixed(2)} ${circumference.toFixed(2)}`}
        transform="rotate(-90 8 8)"
      />
    </svg>
  )
}
```

`web/src/chat/UsageSummary.tsx`:

```tsx
import ContextRing from './ContextRing'
import { formatCost, formatTokens } from './format'
import styles from './Usage.module.css'

export interface Usage {
  // promptTokens and completionTokens are the last step's context, which
  // is what the window holds; cost is the session's running total.
  promptTokens: number
  completionTokens: number
  cost: number
  contextWindow: number
}

export default function UsageSummary({ usage }: { usage: Usage }) {
  const used = usage.promptTokens + usage.completionTokens
  if (used === 0 && usage.cost === 0) {
    return null
  }
  const fraction = usage.contextWindow > 0 ? used / usage.contextWindow : 0
  return (
    <div className={styles.summary} data-testid="usage-summary">
      {usage.contextWindow > 0 ? <ContextRing fraction={fraction} /> : null}
      <span>{formatTokens(used)} tokens</span>
      {usage.contextWindow > 0 ? (
        <span>
          {Math.round(Math.min(1, fraction) * 100)}% of {formatTokens(usage.contextWindow)}
        </span>
      ) : null}
      <span>{formatCost(usage.cost)}</span>
    </div>
  )
}
```

`web/src/chat/Usage.module.css`:

```css
.summary {
  display: flex;
  flex: none;
  gap: 8px;
  align-items: center;
  color: var(--dsw-alias-label-secondary);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.ring {
  flex: none;
}

.ringTrack {
  stroke: var(--dsw-alias-border-l2);
}

.ringFill {
  stroke: var(--dsw-alias-state-business-primary);
}

.ringHigh .ringFill {
  stroke: var(--dsw-alias-state-warn-primary);
}
```

- [ ] **Step 5: Put the footer under the message**

In `web/src/chat/MessageView.tsx`, add to `MessageViewProps`:

```tsx
  // footer is the turn footer for a message that ends a turn.
  footer?: string | null
```

take `footer = null` in the component's parameters, and render it after
the actions block, inside the `article`:

```tsx
      {footer !== null && footer !== '' && depth === 0 ? <div className={styles.footer}>{footer}</div> : null}
```

Append to `MessageView.module.css`:

```css
.footer {
  color: var(--dsw-alias-label-tertiary);
  font-size: 11px;
}
```

- [ ] **Step 6: Show the usage and the footers in the conversation**

In `web/src/conversation/Conversation.tsx`, import:

```tsx
import { turnFooters } from '../chat/turns'
import UsageSummary, { type Usage } from '../chat/UsageSummary'
```

add to `ConversationProps`:

```tsx
  // usage is the session's context and cost, for the header.
  usage?: Usage | null
```

take `usage = null` in the parameters, add after the `results` memo:

```tsx
  const footers = useMemo(() => turnFooters(messages), [messages])
```

replace the header with:

```tsx
      <header className={styles.header}>
        <h1 className={styles.title}>{title === '' ? untitled : title}</h1>
        {usage !== null ? <UsageSummary usage={usage} /> : null}
      </header>
```

and pass `footer={footers.get(message.id) ?? null}` to `MessageView`.

In `Conversation.module.css`, replace `.header` and `.title` with:

```css
.header {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
  padding: 8px 14px;
  border-bottom: 1px solid var(--dsw-alias-border-l2);
}

.title {
  min-width: 0;
  margin: 0;
  overflow: hidden;
  font-size: 14px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
```

In `web/src/conversation/ConversationContainer.tsx`, in
`SessionConversation`, replace the `status` selector with:

```tsx
  // The row keeps its identity until a frame changes it, so this selector
  // re-renders only on a real change.
  const row = useStore(store, (state) => state.sessions.find((entry) => entry.id === sessionId))
  const contextWindow = useStore(store, (state) => state.bootstrap?.model.context_window ?? 0)
  const usage = useMemo(
    () =>
      row === undefined
        ? null
        : {
            promptTokens: row.prompt_tokens ?? 0,
            completionTokens: row.completion_tokens ?? 0,
            cost: row.cost ?? 0,
            contextWindow,
          },
    [row, contextWindow],
  )
```

and pass `status={row?.status}` and `usage={usage}` to `Conversation`.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/chat src/conversation`
Expected: PASS.

- [ ] **Step 8: Run the frontend checks and commit**

```bash
cd web && npm run typecheck && npm test && npm run build && cd ..
git add web/src/chat web/src/conversation internal/web/dist
git commit -m "feat(web): show turn footers, session usage and a context ring"
```

---

## Task 13: The virtualized log: follow in place and anchored paging

The log renders only the rows in view with `@tanstack/react-virtual`.
Row 0 is the history edge (the load-older button, or "Start of the
session"); every other row is one message, keyed by its id and measured
after it renders. Three scroll rules replace M1's:

1. Pinned to the end: after every commit, the log sits at its end. A
   reply streaming in place (the newest id unchanged, its row taller) is
   followed because the measurement re-renders the list and the commit
   scrolls again. M1 only followed a new id.
2. Scrolled up: nothing moves the view. The browser's own scroll
   anchoring is off (`overflow-anchor: none`) so it cannot fight the
   virtualizer; rows above the view that change height are compensated
   by the virtualizer itself.
3. An older page prepended while scrolled up: the old first message
   keeps its place on screen. The new scroll top is the scroll top before
   the page plus how far the old first message moved, read from the
   virtualizer's measurements after this commit's rows were measured.
   Setting it absolutely overrides any adjustment the virtualizer made
   for the same rows in the same commit, so nothing is counted twice.

A "Back to bottom" button shows while the reader is away from the end.

**Files:**
- Create: `web/src/conversation/scroll.ts`, `web/src/conversation/scroll.test.ts`
- Create: `web/src/test/layout.ts`
- Modify: `web/src/test/setup.ts` (`Element.prototype.scrollTo`)
- Modify: `web/src/conversation/Conversation.tsx` (rewrite)
- Modify: `web/src/conversation/Conversation.module.css` (rewrite)
- Modify: `web/src/conversation/Conversation.test.tsx` (rewrite)
- Modify: `web/package.json`, `web/package-lock.json`

**Interfaces:**
- Consumes: everything `Conversation` used after Task 12;
  `ConversationProps` keeps its shape.
- Produces:
  - `followDistance = 24`;
    `isAtEnd(metrics: { scrollTop: number; scrollHeight: number; clientHeight: number }): boolean`;
    `prependedCount(before: readonly string[], after: readonly string[]): number`;
    `anchoredScrollTop(previousTop: number, previousStart: number, nextStart: number): number`
  - `stubLayout(sizes?: { viewport?: number; row?: number }): () => void`,
    `scrollState(element: HTMLElement, metrics?: { scrollTop?: number; scrollHeight?: number; clientHeight?: number }): void`
    (test helpers)
  - The log keeps `data-testid="conversation-log"` on the scrolling
    element, which the M1 e2e scroll spec reads.

- [ ] **Step 1: Add the virtualizer**

```bash
cd web && npm install --save-exact @tanstack/react-virtual@3.14.28 && cd ..
```

It pulls `@tanstack/virtual-core` 3.19.13, whose `Virtualizer` exposes
`measurementsCache`, `getTotalSize`, `getVirtualItems` and
`measureElement` publicly. If the installed version lacks
`measurementsCache` in its `index.d.ts`, stop and report: the anchoring
rule depends on it.

- [ ] **Step 2: Write the failing tests**

`web/src/conversation/scroll.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { anchoredScrollTop, followDistance, isAtEnd, prependedCount } from './scroll'

describe('isAtEnd', () => {
  it('counts a few pixels of slack as the end', () => {
    expect(isAtEnd({ scrollTop: 400, scrollHeight: 1000, clientHeight: 600 })).toBe(true)
    expect(isAtEnd({ scrollTop: 400 - followDistance, scrollHeight: 1000, clientHeight: 600 })).toBe(true)
    expect(isAtEnd({ scrollTop: 300, scrollHeight: 1000, clientHeight: 600 })).toBe(false)
  })
})

describe('prependedCount', () => {
  it('counts the rows put in front of the old first one', () => {
    expect(prependedCount(['m3', 'm4'], ['m1', 'm2', 'm3', 'm4'])).toBe(2)
  })

  it('answers zero for an append, a stream, a reset or a first render', () => {
    expect(prependedCount(['m3'], ['m3', 'm4'])).toBe(0)
    expect(prependedCount(['m3'], ['m3'])).toBe(0)
    expect(prependedCount(['m3'], ['x1', 'x2'])).toBe(0)
    expect(prependedCount([], ['m1'])).toBe(0)
  })
})

describe('anchoredScrollTop', () => {
  it('moves the view by as much as the anchor moved', () => {
    expect(anchoredScrollTop(1000, 60, 360)).toBe(1300)
    expect(anchoredScrollTop(1000, 60, 60)).toBe(1000)
  })
})
```

`web/src/test/layout.ts`:

```ts
// jsdom lays nothing out, and the virtualizer reads the log's clientHeight
// and every row's offsetHeight. stubLayout answers fixed sizes for both;
// call the function it returns to restore jsdom's own.
export function stubLayout({ viewport = 600, row = 60 }: { viewport?: number; row?: number } = {}): () => void {
  const client = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight')
  const offset = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.dataset.testid === 'conversation-log' ? viewport : 0
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.dataset.index === undefined ? 0 : row
    },
  })
  return () => {
    restore('clientHeight', client)
    restore('offsetHeight', offset)
  }
}

function restore(key: 'clientHeight' | 'offsetHeight', descriptor: PropertyDescriptor | undefined) {
  if (descriptor === undefined) {
    Reflect.deleteProperty(HTMLElement.prototype, key)
  } else {
    Object.defineProperty(HTMLElement.prototype, key, descriptor)
  }
}

// scrollState gives one element a scroll position the component can read
// and write, and a scrollHeight a test can grow.
export function scrollState(
  element: HTMLElement,
  { scrollTop = 0, scrollHeight = 5000, clientHeight = 600 }: { scrollTop?: number; scrollHeight?: number; clientHeight?: number } = {},
): void {
  let top = scrollTop
  Object.defineProperty(element, 'scrollTop', {
    configurable: true,
    get: () => top,
    set: (value: number) => {
      top = value
    },
  })
  Object.defineProperty(element, 'scrollHeight', { configurable: true, writable: true, value: scrollHeight })
  Object.defineProperty(element, 'clientHeight', { configurable: true, value: clientHeight })
}
```

In `web/src/test/setup.ts`, after the pointer-capture block, add:

```ts
// jsdom has no Element.scrollTo, and the virtualizer scrolls the log with
// it. The stub writes scrollTop, which a test's scrollState records.
if (typeof Element.prototype.scrollTo !== 'function') {
  Element.prototype.scrollTo = function scrollTo(this: Element, options?: ScrollToOptions | number, y?: number) {
    const top = typeof options === 'number' ? y : options?.top
    if (typeof top === 'number') {
      this.scrollTop = top
    }
  } as Element['scrollTo']
}
```

Replace `web/src/conversation/Conversation.test.tsx` with:

```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { scrollState, stubLayout } from '../test/layout'
import type { WireMessage } from '../store/app'
import Conversation, { type ConversationProps } from './Conversation'

function entry(id: string, value: string, role = 'assistant'): WireMessage {
  return { id, role, session_id: 's1', parts: [{ type: 'text', data: { text: value } }], created_at: 1, updated_at: 1 }
}

const messages = [entry('m1', 'hello', 'user'), entry('m2', 'hi there')]

function props(overrides: Partial<ConversationProps> = {}): ConversationProps {
  return { sessionId: 's1', title: 't', messages, hasMore: false, loadingOlder: false, onLoadOlder: vi.fn(), ...overrides }
}

let restore: () => void = () => undefined

beforeEach(() => {
  restore = stubLayout({ viewport: 600, row: 60 })
})

afterEach(() => {
  restore()
})

describe('Conversation', () => {
  it('renders the session title and its messages in order', () => {
    render(<Conversation {...props({ title: 'Port the parser' })} />)
    expect(screen.getByRole('heading', { name: 'Port the parser' })).toBeInTheDocument()
    expect(screen.getAllByRole('article').map((article) => article.textContent)).toEqual(['hello', 'hi there'])
  })

  it('asks for a session when none is open', () => {
    render(<Conversation {...props({ sessionId: null })} />)
    expect(screen.getByText('Select a session')).toBeInTheDocument()
  })

  it('names a session that has no title yet', () => {
    render(<Conversation {...props({ title: '' })} />)
    expect(screen.getByRole('heading', { name: 'Untitled session' })).toBeInTheDocument()
  })

  it('offers the older page only when there is one', async () => {
    const onLoadOlder = vi.fn()
    const { rerender } = render(<Conversation {...props({ hasMore: true, onLoadOlder })} />)
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    expect(onLoadOlder).toHaveBeenCalledOnce()
    rerender(<Conversation {...props({ hasMore: false })} />)
    expect(screen.queryByRole('button', { name: 'Load older messages' })).toBeNull()
    expect(screen.getByText('Start of the session')).toBeInTheDocument()
  })

  it('disables the button while a page is in flight and shows a failure', () => {
    render(<Conversation {...props({ hasMore: true, loadingOlder: true, error: 'no such session' })} />)
    expect(screen.getByRole('button', { name: 'Loading…' })).toBeDisabled()
    expect(screen.getByRole('alert')).toHaveTextContent('no such session')
  })

  it('renders only the rows in view', () => {
    const many = Array.from({ length: 300 }, (_, index) => entry(`m${index}`, `message ${index}`))
    render(<Conversation {...props({ messages: many })} />)
    const rendered = screen.getAllByRole('article').length
    expect(rendered).toBeGreaterThan(0)
    expect(rendered).toBeLessThan(40)
  })

  it('follows a reply that grows in place while the log is at its end', () => {
    const { rerender } = render(<Conversation {...props()} />)
    const log = screen.getByTestId('conversation-log')
    scrollState(log, { scrollTop: 4400, scrollHeight: 5000, clientHeight: 600 })
    fireEvent.scroll(log)
    ;(log as { scrollHeight: number }).scrollHeight = 5600
    // Same id, more text: M1 followed only a new id.
    rerender(<Conversation {...props({ messages: [messages[0], entry('m2', 'hi there, and more')] })} />)
    expect(log.scrollTop).toBe(5600)
    expect(screen.queryByRole('button', { name: 'Back to bottom' })).toBeNull()
  })

  it('stays where the reader scrolled while a reply grows, and jumps back on request', async () => {
    const { rerender } = render(<Conversation {...props()} />)
    const log = screen.getByTestId('conversation-log')
    scrollState(log, { scrollTop: 1000, scrollHeight: 5000, clientHeight: 600 })
    fireEvent.scroll(log)
    ;(log as { scrollHeight: number }).scrollHeight = 5600
    rerender(<Conversation {...props({ messages: [messages[0], entry('m2', 'hi there, and more')] })} />)
    expect(log.scrollTop).toBe(1000)
    await userEvent.click(screen.getByRole('button', { name: 'Back to bottom' }))
    expect(log.scrollTop).toBe(5600)
  })

  it('keeps the old first message in place when an older page is prepended', () => {
    const { rerender } = render(<Conversation {...props({ hasMore: true })} />)
    const log = screen.getByTestId('conversation-log')
    scrollState(log, { scrollTop: 1000, scrollHeight: 5000, clientHeight: 600 })
    fireEvent.scroll(log)
    rerender(<Conversation {...props({ messages: [entry('m0', 'first words', 'user'), ...messages], hasMore: false })} />)
    // Every row measures 60: the old first message moved down one row.
    expect(log.scrollTop).toBe(1060)
  })

  it('marks a tool call done from a result in a later message', () => {
    render(
      <Conversation
        {...props({
          messages: [
            { ...messages[1], parts: [{ type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{}', finished: true } }] },
            { ...messages[0], id: 'm3', parts: [{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok' } }] },
          ],
        })}
      />,
    )
    expect(screen.getByText('Done')).toBeInTheDocument()
    expect(screen.getAllByRole('article')).toHaveLength(1)
  })
})
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd web && npx vitest run src/conversation`
Expected: FAIL with `Failed to resolve import "./scroll"`, then (once
`scroll.ts` exists) on "Start of the session", "renders only the rows in
view" (all 300 render) and the three scroll cases.

- [ ] **Step 4: Write the scroll rules**

`web/src/conversation/scroll.ts`:

```ts
// followDistance is how close to the end the log counts as "at the end". A
// few pixels of slack keep a fractional device pixel from unpinning it.
export const followDistance = 24

export function isAtEnd(metrics: { scrollTop: number; scrollHeight: number; clientHeight: number }): boolean {
  return metrics.scrollHeight - metrics.scrollTop - metrics.clientHeight <= followDistance
}

// prependedCount is how many rows went in front of the old first row. An
// append, a stream, a reset to another list or a first render is zero.
export function prependedCount(before: readonly string[], after: readonly string[]): number {
  if (before.length === 0) {
    return 0
  }
  const index = after.indexOf(before[0])
  return index > 0 ? index : 0
}

// anchoredScrollTop keeps an anchor row where it was on screen: the view
// moves by exactly as much as the row moved down the list.
export function anchoredScrollTop(previousTop: number, previousStart: number, nextStart: number): number {
  return previousTop + (nextStart - previousStart)
}
```

- [ ] **Step 5: Rewrite the conversation**

Replace `web/src/conversation/Conversation.tsx` with:

```tsx
import { useVirtualizer } from '@tanstack/react-virtual'
import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react'

import type { SessionStatus } from '../api/wire'
import MessageView from '../chat/MessageView'
import { resultIndex, resultSignature } from '../chat/parts'
import { isTurnActive } from '../chat/toolStatus'
import { turnFooters } from '../chat/turns'
import UsageSummary, { type Usage } from '../chat/UsageSummary'
import type { WireMessage } from '../store/app'
import { anchoredScrollTop, isAtEnd, prependedCount } from './scroll'
import styles from './Conversation.module.css'

export interface ConversationProps {
  sessionId: string | null
  title: string
  messages: WireMessage[]
  hasMore: boolean
  loadingOlder: boolean
  error?: string | null
  // status is the session row's run state; it decides which turn is live.
  status?: SessionStatus
  // usage is the session's context and cost, for the header.
  usage?: Usage | null
  onLoadOlder(): void
}

const untitled = 'Untitled session'

// edgeKey is row 0, the history edge, so a message id never collides with
// it.
const edgeKey = 'history-edge'

// estimatedRowHeight is a guess for a row not measured yet. Only rows near
// the view are ever measured, so the spacer's height is partly a guess.
const estimatedRowHeight = 120

// Shape is what the last commit left: the row keys, the scroll top and
// where the first message started, which is what a prepend is measured
// against.
interface Shape {
  keys: string[]
  scrollTop: number
  firstStart: number
}

export default function Conversation(props: ConversationProps) {
  if (props.sessionId === null) {
    return <p className={styles.empty}>Select a session</p>
  }
  // A new session is a new log: it opens at its end with fresh state.
  return <Log key={props.sessionId} {...props} />
}

interface HistoryEdgeProps {
  hasMore: boolean
  loadingOlder: boolean
  error: string | null
  onLoadOlder(): void
}

function HistoryEdge({ hasMore, loadingOlder, error, onLoadOlder }: HistoryEdgeProps) {
  return (
    <div className={styles.edge}>
      {hasMore ? (
        <button type="button" className={styles.older} onClick={onLoadOlder} disabled={loadingOlder}>
          {loadingOlder ? 'Loading…' : 'Load older messages'}
        </button>
      ) : (
        <p className={styles.start}>Start of the session</p>
      )}
      {error ? (
        <p role="alert" className={styles.error}>
          {error}
        </p>
      ) : null}
    </div>
  )
}

function Log({ title, messages, hasMore, loadingOlder, error = null, status, usage = null, onLoadOlder }: ConversationProps) {
  const log = useRef<HTMLDivElement>(null)
  // pinned is whether the reader sits at the end. It is a ref because the
  // commit reads it, and a state would lag one render behind the scroll.
  const pinned = useRef(true)
  const shape = useRef<Shape | null>(null)
  const [away, setAway] = useState(false)

  // The index is rebuilt only when a result changes. The signature is the
  // dependency on purpose: a token in the last message makes a new array,
  // and a new index with it would re-render every memoised message.
  const signature = resultSignature(messages)
  const results = useMemo(() => resultIndex(messages), [signature])
  const footers = useMemo(() => turnFooters(messages), [messages])
  const keys = useMemo(() => messages.map((entry) => entry.id), [messages])
  // Only the turn after the last user message can still be running.
  const lastUser = messages.findLastIndex((entry) => entry.role === 'user')
  const live = isTurnActive(status)

  const virtualizer = useVirtualizer({
    count: messages.length + 1,
    getScrollElement: () => log.current,
    estimateSize: () => estimatedRowHeight,
    overscan: 6,
    getItemKey: (index) => (index === 0 ? edgeKey : (keys[index - 1] ?? index)),
    // A measurement re-renders on the next frame instead of inside the
    // commit that measured it, which React warns about.
    useFlushSync: false,
  })

  // Every commit: follow the end while pinned, or keep the old first
  // message in place after a prepend. Reading the total size first
  // rebuilds the measurements, so the start below includes every row this
  // commit measured.
  useLayoutEffect(() => {
    const element = log.current
    if (element === null) {
      return
    }
    virtualizer.getTotalSize()
    const previous = shape.current
    const added = previous === null ? 0 : prependedCount(previous.keys, keys)
    if (previous === null || pinned.current) {
      element.scrollTop = element.scrollHeight
    } else if (added > 0) {
      const start = virtualizer.measurementsCache[added + 1]?.start
      if (start !== undefined) {
        element.scrollTop = anchoredScrollTop(previous.scrollTop, previous.firstStart, start)
      }
    }
    shape.current = { keys, scrollTop: element.scrollTop, firstStart: virtualizer.measurementsCache[1]?.start ?? 0 }
  })

  const trackScroll = useCallback(() => {
    const element = log.current
    if (element === null) {
      return
    }
    const atEnd = isAtEnd(element)
    pinned.current = atEnd
    setAway(!atEnd)
    if (shape.current !== null) {
      shape.current.scrollTop = element.scrollTop
    }
  }, [])

  const jumpToEnd = useCallback(() => {
    const element = log.current
    if (element === null) {
      return
    }
    pinned.current = true
    setAway(false)
    element.scrollTop = element.scrollHeight
  }, [])

  return (
    <section className={styles.conversation} aria-label="Conversation">
      <header className={styles.header}>
        <h1 className={styles.title}>{title === '' ? untitled : title}</h1>
        {usage !== null ? <UsageSummary usage={usage} /> : null}
      </header>
      <div className={styles.body}>
        <div ref={log} className={styles.log} data-testid="conversation-log" onScroll={trackScroll}>
          {/* The spacer's height and each row's offset are geometry the
              virtualizer computes; React writes them through the CSSOM. */}
          <div className={styles.spacer} style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map((item) => {
              const message = item.index === 0 ? undefined : messages[item.index - 1]
              return (
                <div
                  key={item.key}
                  ref={virtualizer.measureElement}
                  data-index={item.index}
                  className={styles.row}
                  style={{ transform: `translateY(${item.start}px)` }}
                >
                  {message === undefined ? (
                    <HistoryEdge hasMore={hasMore} loadingOlder={loadingOlder} error={error} onLoadOlder={onLoadOlder} />
                  ) : (
                    <MessageView
                      message={message}
                      results={results}
                      turnActive={live && item.index - 1 > lastUser}
                      footer={footers.get(message.id) ?? null}
                    />
                  )}
                </div>
              )
            })}
          </div>
        </div>
        {away ? (
          <button type="button" className={styles.toEnd} onClick={jumpToEnd}>
            Back to bottom
          </button>
        ) : null}
      </div>
    </section>
  )
}
```

Replace `web/src/conversation/Conversation.module.css` with:

```css
.conversation {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}

.header {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
  padding: 8px 14px;
  border-bottom: 1px solid var(--dsw-alias-border-l2);
}

.title {
  min-width: 0;
  margin: 0;
  overflow: hidden;
  font-size: 14px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.body {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
}

/* The browser's own scroll anchoring would fight the virtualizer's
   adjustments and the prepend rule, so it is off. */
.log {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-anchor: none;
}

.spacer {
  position: relative;
  width: 100%;
}

.row {
  position: absolute;
  top: 0;
  left: 0;
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
  width: 100%;
  padding: 4px 12px;
}

.edge {
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-items: center;
  justify-content: center;
  min-height: 40px;
}

.start {
  margin: 0;
  color: var(--dsw-alias-label-tertiary);
  font-size: 12px;
}

.older {
  padding: 4px 10px;
  border: 1px solid var(--dsw-alias-border-l3);
  border-radius: var(--dsw-radius-sm);
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.error {
  margin: 0;
  padding: 6px 8px;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-static-red-400-a12, var(--dsw-alias-bg-layer-2));
  font-size: 12px;
}

.toEnd {
  position: absolute;
  right: 16px;
  bottom: 16px;
  padding: 4px 12px;
  border: 1px solid var(--dsw-alias-border-l3);
  border-radius: 999px;
  background: var(--dsw-alias-bg-layer-1);
  color: inherit;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

.empty {
  margin: auto;
  color: var(--dsw-alias-label-secondary);
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/conversation src/chat`
Expected: PASS, `Conversation.rerender.test.tsx` and the container
cases included (they hold four messages or fewer, which the
virtualizer's overscan renders even with jsdom's zero sizes).

- [ ] **Step 7: Run the frontend checks, the budget and the M1 browser suite**

```bash
cd web && npm run typecheck && npm test && npm run build && npm run budget && npm run e2e && cd ..
```

Expected: every unit test passes; the budget passes with Shiki and KaTeX
lazy; `shell.spec.ts` passes unchanged, including `scrolls the
conversation log instead of the page`, and `accessibility.spec.ts`
passes.

- [ ] **Step 8: Commit**

```bash
git add web/package.json web/package-lock.json web/src/conversation web/src/test internal/web/dist
git commit -m "feat(web): virtualize the log and follow a reply in place"
```

---
## Task 14: The webfake M2 scenario

`WEBFAKE_SCENARIO=m2` seeds three sessions and a subagent session for the
browser specs: one that shows every Markdown block and every tool view,
one with more history than a snapshot holds, and one that streams a
reply in place when the spec writes a file. The seed is Go, so its tests
pin what the specs rely on.

**Files:**
- Create: `internal/web/webtest/seed_m2.go`
- Create: `internal/web/webtest/seed_m2_test.go`
- Modify: `internal/web/webtest/fake.go` (`UpsertMessage`)
- Modify: `internal/web/cmd/webfake/main.go` (the `m2` scenario)

**Interfaces:**
- Consumes: `Fake.AddSession`, `AddMessage`, `SetSessionBusy`, `Emit`,
  `ListMessages`, `AgentIsSessionBusy`, `SubscribeEvents`
  (`internal/web/webtest/fake.go`); `waitForFile` in
  `internal/web/cmd/webfake/main.go`.
- Produces:
  - `func (f *Fake) UpsertMessage(m message.Message)`
  - `SeedM2(f *Fake, clock time.Time)`;
    `PlayM2Stream(ctx context.Context, f *Fake, clock time.Time, step time.Duration)`
  - Constants `M2ChatSession = "s-m2-chat"`, `M2LongSession = "s-m2-long"`,
    `M2StreamSession = "s-m2-stream"`,
    `M2ChildSession = "s-m2-chat-a2$$tc-agent"`,
    `M2StreamMessage = "s-m2-stream-a"`, `M2LongMessages = 130`,
    `M2StreamLines = 24`.
  - Session titles the specs click: `Render the chat`, `Long history`,
    `Stream a reply`; the child is titled `Map the parser package` and
    must never be in the sidebar.
  - webfake reads `WEBFAKE_M2_STREAM_ON_FILE`; without it the stream
    starts two seconds after the gateway.

- [ ] **Step 1: Write the failing seed tests**

`internal/web/webtest/seed_m2_test.go`:

```go
package webtest

import (
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

var m2Clock = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

// TestSeedM2GivesTheChatEveryView pins the fixture the M2 browser specs
// drive: one call per tool view, each answered but the canceled one, and a
// subagent session under the agent call.
func TestSeedM2GivesTheChatEveryView(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedM2(f, m2Clock)

	msgs, err := f.ListMessages(t.Context(), M2ChatSession)
	require.NoError(t, err)
	names := make(map[string]bool)
	answered := make(map[string]bool)
	for _, m := range msgs {
		for _, call := range m.ToolCalls() {
			names[call.Name] = true
		}
		for _, result := range m.ToolResults() {
			answered[result.ToolCallID] = true
		}
	}
	for _, name := range []string{"bash", "view", "edit", "grep", "glob", "fetch", "todos", "mcp_github_search_issues", "agent"} {
		require.True(t, names[name], "the chat session has no %s call", name)
	}
	require.True(t, answered["tc-bash"])
	require.False(t, answered["tc-slow"], "the canceled call must stay unanswered")

	child, err := f.GetSession(t.Context(), M2ChildSession)
	require.NoError(t, err)
	require.Equal(t, M2ChatSession, child.ParentSessionID)
	childMsgs, err := f.ListMessages(t.Context(), M2ChildSession)
	require.NoError(t, err)
	require.NotEmpty(t, childMsgs)

	chat, err := f.GetSession(t.Context(), M2ChatSession)
	require.NoError(t, err)
	require.Equal(t, int64(42000), chat.PromptTokens)
	require.InDelta(t, 0.1234, chat.Cost, 1e-9)
}

// TestSeedM2LongSessionPagesAndSettlesNothing pins the long session: more
// messages than the first page, every assistant turn finished, so the
// fake's settle step adds no interrupted notice to the history.
func TestSeedM2LongSessionPagesAndSettlesNothing(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedM2(f, m2Clock)

	page, hasMore, err := f.ListMessagesPage(t.Context(), M2LongSession, "", 100)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, page, 100)
	all, err := f.ListMessages(t.Context(), M2LongSession)
	require.NoError(t, err)
	require.Len(t, all, M2LongMessages)
	for _, m := range all {
		if m.Role == message.Assistant {
			require.True(t, m.IsFinished(), m.ID)
			require.Equal(t, message.FinishReasonEndTurn, m.FinishReason(), m.ID)
		}
	}
}

// TestPlayM2StreamGrowsOneReplyInPlace pins the stream: one message id,
// growing on every frame, then finished, the session idle and the run
// complete event the gateway recomputes the status on.
func TestPlayM2StreamGrowsOneReplyInPlace(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedM2(f, m2Clock)
	require.True(t, f.AgentIsSessionBusy(M2StreamSession))
	sub := f.SubscribeEvents(t.Context())

	PlayM2Stream(t.Context(), f, m2Clock, time.Millisecond)

	var lengths []int
	completed := false
	for len(sub) > 0 {
		switch ev := (<-sub).(type) {
		case pubsub.Event[message.Message]:
			require.Equal(t, M2StreamMessage, ev.Payload.ID)
			lengths = append(lengths, len(ev.Payload.Content().Text))
		case pubsub.Event[notify.RunComplete]:
			completed = true
		}
	}
	require.Len(t, lengths, M2StreamLines+1)
	require.True(t, slices.IsSorted(lengths))
	require.True(t, completed)
	require.False(t, f.AgentIsSessionBusy(M2StreamSession))

	msgs, err := f.ListMessages(t.Context(), M2StreamSession)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "the stream rewrites its reply instead of adding one")
	require.True(t, msgs[1].IsFinished())
}

func TestUpsertMessageReplacesByID(t *testing.T) {
	t.Parallel()
	f := NewFake()
	f.AddMessage(message.Message{ID: "x", SessionID: "s1", Parts: []message.ContentPart{message.TextContent{Text: "a"}}})
	f.UpsertMessage(message.Message{ID: "x", SessionID: "s1", Parts: []message.ContentPart{message.TextContent{Text: "ab"}}})
	f.UpsertMessage(message.Message{ID: "y", SessionID: "s1"})
	msgs, err := f.ListMessages(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "ab", msgs[0].Content().Text)
}
```

`message.Message` has `FinishReason()`, `IsFinished()`, `Content()`,
`ToolCalls()` and `ToolResults()` on a pointer receiver; the range
variables above are addressable.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/web/webtest -run 'M2|Upsert' -count=1`
Expected: FAIL to build: `undefined: SeedM2`, `undefined: M2ChatSession`,
`f.UpsertMessage undefined`.

- [ ] **Step 3: Add UpsertMessage to the fake**

In `internal/web/webtest/fake.go`, below `AddMessage`:

```go
// UpsertMessage replaces the stored message with the same ID, or appends
// it, so a streamed message reads back the way its last frame left it.
func (f *Fake) UpsertMessage(m message.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs := f.messages[m.SessionID]
	for i := range msgs {
		if msgs[i].ID == m.ID {
			msgs[i] = m
			return
		}
	}
	f.messages[m.SessionID] = append(msgs, m)
}
```

- [ ] **Step 4: Write the seed**

`internal/web/webtest/seed_m2.go`:

```go
package webtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

// The M2 sessions and the ids the browser specs reach for.
const (
	M2ChatSession   = "s-m2-chat"
	M2LongSession   = "s-m2-long"
	M2StreamSession = "s-m2-stream"
	// M2ChildSession is the agent call's subagent session, built the way
	// session.Service.CreateAgentToolSessionID builds it.
	M2ChildSession  = M2ChatSession + "-a2$$tc-agent"
	M2StreamMessage = M2StreamSession + "-a"
	// M2LongMessages is more than the 100 a snapshot holds, so the long
	// session has an older page to load.
	M2LongMessages = 130
	// M2StreamLines is how many lines the streamed reply grows by.
	M2StreamLines = 24
	m2Model       = "claude-sonnet"
)

const fence = "```"

// m2Markdown exercises the renderer: headings, lists, a task list, a
// table, a safe and an unsafe link, an image, a Go fence, inline and
// display math, and raw HTML that must stay text.
var m2Markdown = strings.Join([]string{
	"## Chat rendering",
	"",
	"- **Bold** and *italic* with `inline code`",
	"- [x] A finished task",
	"- [ ] An open task",
	"",
	"See [Charm](https://charm.land) but not [a trap](javascript:alert(1)).",
	"",
	"![the logo](https://example.com/logo.png)",
	"",
	"| Tool | Exit |",
	"| :--- | ---: |",
	"| bash | 1 |",
	"",
	fence + "go",
	"func main() {",
	"\tfmt.Println(\"hello\")",
	"}",
	fence,
	"",
	"The area is $$\\pi r^2$$ for a circle.",
	"",
	"$$",
	"\\int_0^1 x^2\\,dx = \\frac{1}{3}",
	"$$",
	"",
	`<img src="https://example.com/x.png" onerror="alert(1)"> and <script>alert(1)</script> stay text.`,
}, "\n")

// SeedM2 gives the fake the sessions the M2 browser specs drive. Call it
// before the gateway starts; PlayM2Stream then streams the busy session's
// reply.
func SeedM2(f *Fake, clock time.Time) {
	at := clock.Unix()
	f.AddSession(session.Session{
		ID: M2ChatSession, Title: "Render the chat", UpdatedAt: at,
		PromptTokens: 42000, CompletionTokens: 1800, Cost: 0.1234,
	})
	f.AddSession(session.Session{ID: M2StreamSession, Title: "Stream a reply", UpdatedAt: at - 60})
	f.AddSession(session.Session{ID: M2LongSession, Title: "Long history", UpdatedAt: at - 120})
	f.AddSession(session.Session{
		ID: M2ChildSession, ParentSessionID: M2ChatSession, Title: "Map the parser package", UpdatedAt: at - 600,
	})
	seedM2Chat(f, at)
	seedM2Child(f, at)
	seedM2Long(f, at)
	f.AddMessage(message.Message{
		ID: M2StreamSession + "-u", SessionID: M2StreamSession, Role: message.User, CreatedAt: at - 30, UpdatedAt: at - 30,
		Parts: []message.ContentPart{message.TextContent{Text: "Write a long reply, one line at a time"}},
	})
	f.AddMessage(m2StreamReply(at, 0))
	f.SetSessionBusy(M2StreamSession, true)
}

// metadata is a tool result's metadata string.
func metadata(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func m2Assistant(id string, createdAt int64, parts ...message.ContentPart) message.Message {
	return message.Message{
		ID: id, SessionID: M2ChatSession, Role: message.Assistant, CreatedAt: createdAt, UpdatedAt: createdAt,
		Model: m2Model, Provider: "anthropic", Parts: parts,
	}
}

func m2User(sessionID, id string, createdAt int64, text string) message.Message {
	return message.Message{
		ID: id, SessionID: sessionID, Role: message.User, CreatedAt: createdAt, UpdatedAt: createdAt,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}
}

var m2Todos = []map[string]string{
	{"content": "Port the parser", "status": "completed", "active_form": "Porting the parser"},
	{"content": "Write the tests", "status": "in_progress", "active_form": "Writing the tests"},
	{"content": "Update the docs", "status": "pending", "active_form": "Updating the docs"},
}

func seedM2Chat(f *Fake, at int64) {
	f.AddMessage(m2User(M2ChatSession, M2ChatSession+"-u1", at-100, "Show me everything"))
	f.AddMessage(m2Assistant(M2ChatSession+"-a1", at-99,
		message.ReasoningContent{Thinking: "Plan the reply.\nShow every block the renderer knows.", StartedAt: at - 99, FinishedAt: at - 97},
		message.TextContent{Text: m2Markdown},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: at - 95},
	))

	f.AddMessage(m2User(M2ChatSession, M2ChatSession+"-u2", at-90, "Now use every tool"))
	todos := metadata(map[string]any{"todos": m2Todos})
	calls := []message.ToolCall{
		{ID: "tc-bash", Name: "bash", Input: `{"command":"go test ./...","description":"Run the tests"}`},
		{ID: "tc-view", Name: "view", Input: `{"file_path":"/tmp/project/parser.go"}`},
		{ID: "tc-edit", Name: "edit", Input: `{"file_path":"/tmp/project/parser.go","old_string":"func Parse() {}","new_string":"func Parse() error { return nil }"}`},
		{ID: "tc-grep", Name: "grep", Input: `{"pattern":"func Parse","path":"/tmp/project"}`},
		{ID: "tc-glob", Name: "glob", Input: `{"pattern":"**/*.go"}`},
		{ID: "tc-fetch", Name: "fetch", Input: `{"url":"https://charm.land","format":"markdown"}`},
		{ID: "tc-todos", Name: "todos", Input: todos},
		{ID: "tc-mcp", Name: "mcp_github_search_issues", Input: `{"query":"parser bug"}`},
		{ID: "tc-agent", Name: "agent", Input: `{"prompt":"Map the parser package"}`},
	}
	parts := []message.ContentPart{message.TextContent{Text: "Running every tool."}}
	for _, call := range calls {
		call.Finished = true
		parts = append(parts, call)
	}
	parts = append(parts, message.Finish{Reason: message.FinishReasonToolUse, Time: at - 85})
	f.AddMessage(m2Assistant(M2ChatSession+"-a2", at-89, parts...))

	results := []message.ToolResult{
		{ToolCallID: "tc-bash", Name: "bash", Content: "FAIL parser\n<cwd>/tmp/project</cwd>", Metadata: metadata(map[string]any{
			"output":            "\x1b[31mFAIL\x1b[0m parser_test.go:12\n\x1b[32mok\x1b[0m   lexer",
			"exit_code":         1,
			"description":       "Run the tests",
			"working_directory": "/tmp/project",
		})},
		{ToolCallID: "tc-view", Name: "view", Content: "<file>\n     1|package parser\n</file>", Metadata: metadata(map[string]any{
			"file_path": "/tmp/project/parser.go",
			"content":   "package parser\n\nfunc Parse() {}",
		})},
		{ToolCallID: "tc-edit", Name: "edit", Content: "Content replaced in file: /tmp/project/parser.go", Metadata: metadata(map[string]any{
			"additions":   1,
			"removals":    1,
			"old_content": "package parser\n\nfunc Parse() {}\n",
			"new_content": "package parser\n\nfunc Parse() error { return nil }\n",
		})},
		{ToolCallID: "tc-grep", Name: "grep", Content: "Found 1 matches\n/tmp/project/parser.go:\n  Line 3: func Parse() {}", Metadata: metadata(map[string]any{
			"number_of_matches": 1, "truncated": false,
		})},
		{ToolCallID: "tc-glob", Name: "glob", Content: "/tmp/project/parser.go\n/tmp/project/lexer.go", Metadata: metadata(map[string]any{
			"number_of_files": 2, "truncated": false,
		})},
		{ToolCallID: "tc-fetch", Name: "fetch", Content: "# Charm\n\nWe build tools for the terminal."},
		{ToolCallID: "tc-todos", Name: "todos", Content: "Todo list updated", Metadata: metadata(map[string]any{
			"is_new": false, "todos": m2Todos, "completed": 1, "total": 3, "just_started": "Write the tests",
		})},
		{ToolCallID: "tc-mcp", Name: "mcp_github_search_issues", Content: "#12 Parser drops comments"},
		{ToolCallID: "tc-agent", Name: "agent", Content: "The parser has two files."},
	}
	answers := make([]message.ContentPart, 0, len(results))
	for _, result := range results {
		answers = append(answers, result)
	}
	f.AddMessage(message.Message{
		ID: M2ChatSession + "-t2", SessionID: M2ChatSession, Role: message.Tool, CreatedAt: at - 84, UpdatedAt: at - 84,
		Parts: answers,
	})
	f.AddMessage(m2Assistant(M2ChatSession+"-a3", at-83,
		message.TextContent{Text: "Every tool answered."},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: at - 80},
	))

	// A canceled turn whose call never answered: the tab must show it
	// interrupted, never running.
	f.AddMessage(m2User(M2ChatSession, M2ChatSession+"-u4", at-70, "Run the slow command"))
	f.AddMessage(m2Assistant(M2ChatSession+"-a4", at-69,
		message.TextContent{Text: "Starting it."},
		message.ToolCall{ID: "tc-slow", Name: "bash", Input: `{"command":"sleep 600","description":"Wait ten minutes"}`, Finished: true},
		message.Finish{Reason: message.FinishReasonCanceled, Time: at - 60},
	))
}

func seedM2Child(f *Fake, at int64) {
	child := func(m message.Message) {
		m.SessionID = M2ChildSession
		m.UpdatedAt = m.CreatedAt
		if m.Role == message.Assistant {
			m.Model, m.Provider = m2Model, "anthropic"
		}
		f.AddMessage(m)
	}
	child(m2User(M2ChildSession, M2ChildSession+"-u", at-88, "Map the parser package"))
	child(message.Message{ID: M2ChildSession + "-a1", Role: message.Assistant, CreatedAt: at - 87, Parts: []message.ContentPart{
		message.ToolCall{ID: "tc-child-view", Name: "view", Input: `{"file_path":"/tmp/project/lexer.go"}`, Finished: true},
		message.Finish{Reason: message.FinishReasonToolUse, Time: at - 87},
	}})
	child(message.Message{ID: M2ChildSession + "-t1", Role: message.Tool, CreatedAt: at - 86, Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "tc-child-view", Name: "view", Content: "package parser", Metadata: metadata(map[string]any{
			"file_path": "/tmp/project/lexer.go", "content": "package parser\n\nfunc Lex() {}",
		})},
	}})
	child(message.Message{ID: M2ChildSession + "-a2", Role: message.Assistant, CreatedAt: at - 85, Parts: []message.ContentPart{
		message.TextContent{Text: "The parser has two files."},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: at - 85},
	}})
}

func seedM2Long(f *Fake, at int64) {
	for i := range M2LongMessages {
		created := at - 10_000 + int64(i)
		text := fmt.Sprintf("History message %d", i)
		if i%2 == 0 {
			f.AddMessage(m2User(M2LongSession, fmt.Sprintf("%s-m%03d", M2LongSession, i), created, text))
			continue
		}
		// Every assistant turn is finished, or the fake's settle step would
		// stamp each one interrupted.
		f.AddMessage(message.Message{
			ID: fmt.Sprintf("%s-m%03d", M2LongSession, i), SessionID: M2LongSession, Role: message.Assistant,
			CreatedAt: created, UpdatedAt: created, Model: m2Model, Provider: "anthropic",
			Parts: []message.ContentPart{
				message.TextContent{Text: text},
				message.Finish{Reason: message.FinishReasonEndTurn, Time: created},
			},
		})
	}
}

// m2StreamReply is the stream session's reply after lines lines. Every
// line is a paragraph, so the row grows by a visible step each time.
func m2StreamReply(at int64, lines int) message.Message {
	var text strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&text, "Stream line %d: the tokens keep arriving.\n\n", i)
	}
	return message.Message{
		ID: M2StreamMessage, SessionID: M2StreamSession, Role: message.Assistant,
		CreatedAt: at - 29, UpdatedAt: at - 29 + int64(lines), Model: m2Model, Provider: "anthropic",
		Parts: []message.ContentPart{message.TextContent{Text: text.String()}},
	}
}

// PlayM2Stream grows the stream session's reply one line per step, then
// ends the turn the way a run does: the finish part, the session idle, and
// the run_complete event the gateway recomputes the status on.
func PlayM2Stream(ctx context.Context, f *Fake, clock time.Time, step time.Duration) {
	at := clock.Unix()
	for lines := 1; lines <= M2StreamLines; lines++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(step):
		}
		reply := m2StreamReply(at, lines)
		f.UpsertMessage(reply)
		f.Emit(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: reply})
	}
	reply := m2StreamReply(at, M2StreamLines)
	reply.Parts = append(reply.Parts, message.Finish{Reason: message.FinishReasonEndTurn, Time: reply.UpdatedAt})
	f.UpsertMessage(reply)
	f.SetSessionBusy(M2StreamSession, false)
	f.Emit(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: reply})
	f.Emit(pubsub.Event[notify.RunComplete]{Type: pubsub.CreatedEvent, Payload: notify.RunComplete{
		SessionID: M2StreamSession, MessageID: reply.ID,
	}})
}
```

`m2User` stamps the session id it is given and `seedM2Child`'s helper
overwrites it with the child's, so both paths store messages under the
right session.

- [ ] **Step 5: Run the seed tests to verify they pass**

Run: `go test ./internal/web/webtest -run 'M2|Upsert' -count=1 -race`
Expected: `ok`.

- [ ] **Step 6: Wire the scenario into webfake**

In `internal/web/cmd/webfake/main.go`, after the `if m1 { webtest.SeedM1(fake, clock) }`
block, add:

```go
	// The m2 scenario carries the chat sessions the M2 browser specs
	// drive: every Markdown block and tool view, a long history, and a
	// reply that streams in place.
	m2 := os.Getenv("WEBFAKE_SCENARIO") == "m2"
	if m2 {
		webtest.SeedM2(fake, clock)
	}
```

and after the `if m1 { … SeedM1Pending … }` block that follows
`web.Start`, add:

```go
	if m2 {
		// The stream starts when the spec writes the file, so the reply
		// cannot race a page that boots slowly; without one it starts two
		// seconds in, for a person watching the dev server.
		step := 150 * time.Millisecond
		if file := os.Getenv("WEBFAKE_M2_STREAM_ON_FILE"); file != "" {
			go func() {
				if waitForFile(runCtx, file) {
					webtest.PlayM2Stream(runCtx, fake, clock, step)
				}
			}()
		} else {
			time.AfterFunc(2*time.Second, func() { webtest.PlayM2Stream(runCtx, fake, clock, step) })
		}
	}
```

- [ ] **Step 7: Check the scenario end to end**

```bash
go build ./internal/web/cmd/webfake
WEBFAKE_PROJECT_DIR=/tmp/crush-m2-seed WEBFAKE_SCENARIO=m2 go run ./internal/web/cmd/webfake > /tmp/webfake-m2-url.txt 2>/dev/null &
sleep 5
url=$(head -1 /tmp/webfake-m2-url.txt)
origin=${url%%/#t=*}
token=${url##*#t=}
curl -sS "$origin/api/bootstrap" -H "Authorization: Bearer $token" | grep -o '"title":"[^"]*"' | sort
curl -sS "$origin/api/sessions/s-m2-long/messages?limit=100" -H "Authorization: Bearer $token" | grep -c '"has_more":true'
kill %1
gofumpt -l internal/web
go vet ./internal/web/...
```

Expected: the titles include `Render the chat`, `Long history` and
`Stream a reply` (the fake lists the child too; the tab drops it), the
long session answers `1`, `gofumpt -l` prints nothing and `go vet` is
quiet.

- [ ] **Step 8: Commit**

```bash
git add internal/web/webtest internal/web/cmd/webfake
git commit -m "test(web): add the webfake M2 chat scenario"
```

---

## Task 15: The Playwright chat spec, the CSP listener and the axe scan

**Files:**
- Modify: `web/e2e/support/webfake.ts` (scenario option, `chatApp` and
  `streamApp` fixtures)
- Create: `web/e2e/chat.spec.ts`
- Modify: `web/e2e/accessibility.spec.ts` (the chat screen)

**Interfaces:**
- Consumes: the M2 scenario and its titles (Task 14); everything Tasks 1
  to 13 render; `data-testid="tool-call"`, `data-tool`, `data-status`
  (Task 6), `data-kind` (Task 9), `data-testid="usage-summary"` (Task 12),
  `data-testid="conversation-log"` and "Back to bottom" (Task 13).
- Produces: `chatApp: Webfake` (worker fixture, `WEBFAKE_SCENARIO=m2`)
  and `streamApp: StreamWebfake` (`{ streamFile: string }`, a fresh
  gateway whose stream waits for the file).

- [ ] **Step 1: Teach the fixture the M2 scenario**

In `web/e2e/support/webfake.ts`:

Replace `StartOptions` with:

```ts
interface StartOptions {
  // scenario picks the seed; m1 is the default the shell specs use.
  scenario?: 'm1' | 'm2'
  // pendingOnFile asks the fake to hold the M1 permission until the spec
  // writes the file, so the change never races a slow page load.
  pendingOnFile?: boolean
  // streamOnFile asks the M2 fake to start streaming its reply once the
  // spec writes the file.
  streamOnFile?: boolean
}
```

In `startWebfake`, replace the `pendingDir` line, the `env` object and
the `if (pendingFile !== null)` block with:

```ts
  const waits = options.pendingOnFile === true || options.streamOnFile === true
  const pendingDir = waits ? mkdtempSync(join(tmpdir(), 'webfake-pending-')) : null
  const pendingFile = pendingDir === null ? null : join(pendingDir, 'open')
  const env: NodeJS.ProcessEnv = {
    ...process.env,
    WEBFAKE_SCENARIO: options.scenario ?? 'm1',
    WEBFAKE_PROJECT_DIR: join(tmpdir(), 'crush-e2e-project'),
  }
  if (pendingFile !== null) {
    if (options.streamOnFile) {
      env.WEBFAKE_M2_STREAM_ON_FILE = pendingFile
    } else {
      env.WEBFAKE_M1_PENDING_ON_FILE = pendingFile
    }
  }
```

Add next to `PendingWebfake`:

```ts
// StreamWebfake is an M2 fake whose streamed reply waits for the file the
// spec writes.
export interface StreamWebfake extends Webfake {
  streamFile: string
}
```

Change the `test` declaration's type parameters to
`base.extend<{ freshApp: PendingWebfake; streamApp: StreamWebfake }, { app: Webfake; chatApp: Webfake }>`
and add two fixtures beside `app` and `freshApp`:

```ts
  // chatApp is the worker's M2 gateway, for the specs that only read the
  // seeded chat.
  chatApp: [
    async ({}, use) => {
      const running = await startWebfake({ scenario: 'm2' })
      await use(running.app)
      await running.stop()
    },
    { scope: 'worker', timeout: 300_000 },
  ],
  // streamApp is a fresh M2 gateway per test: its reply streams once, when
  // the spec writes streamFile.
  streamApp: async ({}, use) => {
    const running = await startWebfake({ scenario: 'm2', streamOnFile: true })
    const streamFile = running.pendingFile
    if (streamFile === null) {
      await running.stop()
      throw new Error('webfake did not get a stream file')
    }
    await use({ ...running.app, streamFile })
    await running.stop()
  },
```

- [ ] **Step 2: Write the chat spec**

`web/e2e/chat.spec.ts`:

```ts
import { writeFileSync } from 'node:fs'

import type { Page } from '@playwright/test'

import { expect, test } from './support/webfake'

// watchCsp collects every console report of something the CSP blocked: an
// inline style, an eval, a remote image. The chat must cause none.
function watchCsp(page: Page): string[] {
  const reports: string[] = []
  page.on('console', (message) => {
    if (message.type() === 'error' && /Content Security Policy/i.test(message.text())) {
      reports.push(message.text())
    }
  })
  return reports
}

async function openSession(page: Page, title: string) {
  await page.getByRole('navigation', { name: 'Sessions' }).getByText(title, { exact: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible()
}

function log(page: Page) {
  return page.getByTestId('conversation-log')
}

function tool(page: Page, name: string) {
  return log(page).locator(`[data-testid="tool-call"][data-tool="${name}"]`).first()
}

async function openTool(page: Page, name: string) {
  await tool(page, name).getByRole('button').first().click()
}

test('renders Markdown, code and math with no raw HTML and no CSP violation', async ({ page, chatApp }) => {
  const violations = watchCsp(page)
  await page.goto(chatApp.launchUrl)
  await openSession(page, 'Render the chat')
  await log(page).evaluate((element) => {
    element.scrollTop = 0
  })
  const chat = log(page)
  await expect(chat.getByRole('heading', { name: 'Chat rendering' })).toBeVisible()
  await expect(chat.getByRole('link', { name: 'Charm' }).first()).toHaveAttribute('href', 'https://charm.land')
  await expect(chat.getByRole('link', { name: 'a trap' })).toHaveCount(0)
  await expect(chat.getByRole('link', { name: 'Image: the logo' })).toBeVisible()
  // Shiki and KaTeX are lazy chunks: the highlighted tokens and the MathML
  // arrive after the plain text.
  await expect(chat.locator('pre code [class*="tok-"]').first()).toBeVisible()
  await expect(chat.locator('math')).toHaveCount(2)
  await expect(chat.locator('p', { hasText: 'stay text.' })).toContainText('<script>alert(1)</script>')
  await expect(chat.locator('img, script')).toHaveCount(0)
  expect(violations).toEqual([])
})

test('shows every tool view, a nested subagent call and an interrupted call', async ({ page, chatApp }) => {
  const violations = watchCsp(page)
  await page.goto(chatApp.launchUrl)
  await openSession(page, 'Render the chat')

  await expect(tool(page, 'bash')).toHaveAttribute('data-status', 'done')
  await openTool(page, 'bash')
  await expect(tool(page, 'bash')).toContainText('go test ./...')
  await expect(tool(page, 'bash')).toContainText('FAIL parser_test.go:12')
  await expect(tool(page, 'bash')).not.toContainText('[31m')
  await expect(tool(page, 'bash')).toContainText('exit 1')

  await openTool(page, 'view')
  await expect(tool(page, 'view').locator('[class*="tok-"]').first()).toBeVisible()

  await openTool(page, 'edit')
  await expect(tool(page, 'edit').locator('[data-kind="add"]')).toContainText('func Parse() error { return nil }')
  await expect(tool(page, 'edit').locator('[data-kind="del"]')).toContainText('func Parse() {}')

  await expect(tool(page, 'grep')).toContainText('1 match')
  await expect(tool(page, 'glob')).toContainText('2 files')
  await openTool(page, 'fetch')
  await expect(tool(page, 'fetch').getByRole('heading', { name: 'Charm' })).toBeVisible()
  await expect(tool(page, 'todos')).toContainText('1/3 done')
  await expect(tool(page, 'mcp_github_search_issues')).toContainText('github › search_issues')

  await openTool(page, 'agent')
  // The subagent's own call nests under its row, read from its session.
  await expect(tool(page, 'agent').locator('[data-tool="view"]')).toContainText('/tmp/project/lexer.go')
  await expect(tool(page, 'agent')).toContainText('The parser has two files.')

  // The canceled turn's call has no result: interrupted, never running.
  await expect(log(page).locator('[data-tool="bash"][data-status="interrupted"]')).toContainText('Wait ten minutes')
  await expect(log(page).getByRole('note').filter({ hasText: 'Canceled.' })).toBeVisible()
  expect(violations).toEqual([])
})

test('keeps subagent sessions out of the sidebar', async ({ page, chatApp }) => {
  await page.goto(chatApp.launchUrl)
  const sessions = page.getByRole('navigation', { name: 'Sessions' })
  await expect(sessions.getByText('Render the chat', { exact: true })).toBeVisible()
  await expect(sessions.getByText('Map the parser package')).toHaveCount(0)
})

test('shows the session usage and the turn footers', async ({ page, chatApp }) => {
  await page.goto(chatApp.launchUrl)
  await openSession(page, 'Render the chat')
  const usage = page.getByTestId('usage-summary')
  await expect(usage).toContainText('43.8K tokens')
  await expect(usage).toContainText('$0.12')
  await expect(log(page).getByText(/^claude-sonnet · \d+s$/).first()).toBeVisible()
})

test('opens a long session at its end and keeps the view still when an older page loads', async ({ page, chatApp }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  await page.goto(chatApp.launchUrl)
  await openSession(page, 'Long history')
  await expect(log(page).getByText('History message 129', { exact: true })).toBeInViewport()

  await log(page).evaluate((element) => {
    element.scrollTop = 0
  })
  const button = log(page).getByRole('button', { name: 'Load older messages' })
  await expect(button).toBeVisible()
  // The snapshot holds the newest 100, so its first message is number 30.
  const anchor = log(page).getByText('History message 30', { exact: true })
  await expect(anchor).toBeInViewport()
  const before = (await anchor.boundingBox())?.y ?? 0
  const older = page.waitForResponse((response) => response.url().includes('/messages?before='))
  await button.click()
  await older
  await expect
    .poll(async () => Math.abs(((await anchor.boundingBox())?.y ?? -10_000) - before), { timeout: 5_000 })
    .toBeLessThan(4)

  await log(page).evaluate((element) => {
    element.scrollTop = 0
  })
  await expect(log(page).getByText('History message 0', { exact: true })).toBeVisible()
  await expect(log(page).getByText('Start of the session')).toBeVisible()
})

test('follows a reply that streams in place while the reader is at the end', async ({ page, streamApp }) => {
  await page.setViewportSize({ width: 1280, height: 360 })
  await page.goto(streamApp.launchUrl)
  await openSession(page, 'Stream a reply')
  await expect(log(page).getByText('Write a long reply, one line at a time')).toBeVisible()

  writeFileSync(streamApp.streamFile, 'go')
  const last = log(page).getByText('Stream line 24: the tokens keep arriving.')
  await expect(last).toBeVisible({ timeout: 20_000 })
  await expect(last).toBeInViewport()
  const metrics = await log(page).evaluate((element) => ({
    gap: element.scrollHeight - element.scrollTop - element.clientHeight,
    overflow: element.scrollHeight - element.clientHeight,
  }))
  // The reply outgrew the window, and the log still sits at its end.
  expect(metrics.overflow, JSON.stringify(metrics)).toBeGreaterThan(0)
  expect(metrics.gap, JSON.stringify(metrics)).toBeLessThanOrEqual(24)
  await expect(page.getByRole('button', { name: 'Back to bottom' })).toHaveCount(0)
})

test('leaves the view alone while a reply streams and the reader has scrolled up', async ({ page, streamApp }) => {
  await page.setViewportSize({ width: 1280, height: 360 })
  await page.goto(streamApp.launchUrl)
  await openSession(page, 'Stream a reply')
  writeFileSync(streamApp.streamFile, 'go')
  await expect(log(page).getByText('Stream line 12:', { exact: false })).toBeVisible({ timeout: 20_000 })

  await log(page).evaluate((element) => {
    element.scrollTop = 0
  })
  await expect(page.getByRole('button', { name: 'Back to bottom' })).toBeVisible()
  const prompt = log(page).getByText('Write a long reply, one line at a time')
  const before = (await prompt.boundingBox())?.y ?? 0

  // The run ends when the session's running marker clears.
  const row = page.getByRole('navigation', { name: 'Sessions' }).locator('li', { hasText: 'Stream a reply' })
  await expect(row.locator('[aria-label="Running"]')).toHaveCount(0, { timeout: 20_000 })
  expect(await log(page).evaluate((element) => element.scrollTop)).toBeLessThan(5)
  expect(Math.abs(((await prompt.boundingBox())?.y ?? -10_000) - before)).toBeLessThan(4)

  await page.getByRole('button', { name: 'Back to bottom' }).click()
  await expect(log(page).getByText('Stream line 24:', { exact: false })).toBeInViewport()
})
```

- [ ] **Step 3: Scan the chat with axe**

Append to `web/e2e/accessibility.spec.ts`:

```ts
test('the chat has no serious accessibility violations', async ({ page, chatApp }) => {
  await page.goto(chatApp.launchUrl)
  await page.getByRole('navigation', { name: 'Sessions' }).getByText('Render the chat', { exact: true }).click()
  const log = page.getByTestId('conversation-log')
  await log.locator('[data-tool="edit"]').getByRole('button').first().click()
  await expect(log.locator('[data-kind="add"]').first()).toBeVisible()
  await expect(log.locator('pre code [class*="tok-"]').first()).toBeVisible()

  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  const serious = results.violations.filter(
    (violation) => violation.impact === 'serious' || violation.impact === 'critical',
  )
  expect(
    serious.map((violation) => ({
      id: violation.id,
      impact: violation.impact,
      targets: violation.nodes.map((node) => node.target),
    })),
  ).toEqual([])
})
```

A `color-contrast` finding on a token, capsule or diff colour is fixed in
that CSS file; it is never excluded from the scan.

- [ ] **Step 4: Run the suite**

```bash
cd web && npm run typecheck && npm run e2e && cd ..
```

Expected: the M1 shell and accessibility specs and the seven chat specs
pass. Run the streaming pair three times
(`npx playwright test e2e/chat.spec.ts --repeat-each=3 -g streams`) to
show they are not flaky; a flake is a bug in Task 13's rules, not in the
spec.

- [ ] **Step 5: Commit**

```bash
git add web/e2e
git commit -m "test(web): drive the M2 chat in Playwright with a CSP check"
```

CI needs no change: the e2e job runs every spec file under `web/e2e`.

---

## Task 16: Whole-branch verification

No commit: this task either passes or sends the work back to the task
that broke it. Run it from a clean checkout of `main` after PRs 1 to 15
merged.

**Files:** none.

**Interfaces:**
- Consumes: every task above.
- Produces: a verified M2 on `main`, the baseline M3 builds on.

- [ ] **Step 1: Run the Go side**

```bash
go build ./...
go test ./internal/web/... ./internal/wire/... -count=1
go vet ./...
gofumpt -l . | tee /tmp/gofumpt-m2.txt
test ! -s /tmp/gofumpt-m2.txt
./scripts/check_log_capitalization.sh
```

Expected: the build prints nothing, every package reports `ok`, `go vet`
is quiet, `gofumpt -l` finds nothing, the log check exits 0.

- [ ] **Step 2: Run the frontend side**

```bash
cd web
npm ci
npm run typecheck
npm test
npm run build
npm run budget
cd ..
git diff --exit-code -- internal/web/dist
test -z "$(git status --porcelain --untracked-files=all -- internal/web/dist)"
```

Expected: typecheck quiet, every test passing, the budget printing
`Shiki: lazy` and `KaTeX: lazy` with first paint under 600 KiB, and the
committed build matching a fresh one byte for byte.

- [ ] **Step 3: Run the browser suite**

```bash
cd web && npm run e2e && cd ..
```

Expected: the shell, chat and accessibility specs pass.

- [ ] **Step 4: Check the global rules hold in the source**

```bash
grep -rnE 'dangerouslySetInnerHTML|innerHTML|outerHTML|insertAdjacentHTML' web/src || echo "no HTML injection"
grep -rnE "new Function|\beval\(" web/src || echo "no eval"
grep -rnE "shiki/wasm|engine/oniguruma|onig\.wasm" web/src || echo "no WASM engine"
grep -rnE "katex/dist/katex(\.min)?\.css|@font-face|\.woff2?" web/src web/index.html || echo "no bundled fonts or KaTeX CSS"
grep -rnE "style=\{\{" web/src --include=*.tsx | grep -v 'conversation/Conversation.tsx' || echo "inline style only for virtualizer geometry"
grep -rn "4b399c39a6d1479cd84ca591b4d7c88e24d38a96" web/src | cut -d: -f1 | sort -u
```

Expected: five echoes, then exactly `web/src/chat/markdown/highlighter.ts`,
`web/src/chat/markdown/parse.ts`, `web/src/theme/shiki.css` and the M1
theme sheets; each is listed in `web/THIRD_PARTY_NOTICES.md`.

- [ ] **Step 5: Check the milestone by hand**

Run `go run . --web` in a real session and confirm, in the tab:

1. A reply with a fenced Go block renders plain first, then highlighted
   within a second; a `$$x^2$$` formula renders as math; neither the
   page nor the console shows a CSP report.
2. A running turn's reply grows in place and the log follows it; scroll
   up during the reply and the view stays still, "Back to bottom" shows,
   and clicking it returns to the live end.
3. Every tool row opens: bash shows its colours and exit code, an edit
   shows a diff, a read shows the file with line numbers, todos show
   their states, an MCP call names its server.
4. A subagent's row (an `agent` call) shows its own calls nested while it
   runs, and the subagent never appears in the sidebar.
5. Cancel a turn while a tool runs: its row turns `Interrupted` and the
   message ends with `Canceled.`.
6. The header shows the tokens in context, the share of the window and
   the cost, and they move after each step.
7. In a session with more than 100 messages, "Load older messages"
   loads the page above without moving what is on screen.
8. Light and dark themes both keep code tokens, diff rows and capsules
   legible.

---

### Decisions this plan made that the brief did not

- **No gateway field.** The usage footer reads what already travels: the
  message's `model` and `finish` part, and the session row's
  `prompt_tokens`, `completion_tokens` and `cost`. Per-message token
  counts do not exist in the schema, and the spec forbids a schema
  change, so the footer shows the model and the turn's duration, and the
  header shows the session's context and cost. The golden gains only a
  fixture (a tool-result message in the snapshot example) so the
  frontend's part schemas are pinned to the Go field names.
- **KaTeX renders MathML only.** Its HTML output needs `katex.css`, the
  KaTeX fonts and inline styles, all forbidden; MathML needs none and
  modern browsers draw it with their own math fonts. The string is
  rebuilt through an allowlist with `DOMParser`, never `innerHTML`.
- **Inline math needs `$$`.** `singleDollarTextMath` is off: a coding
  chat writes prices far more often than inline TeX, and a stray `$5 and
  $10` would otherwise turn into math.
- **Shiki uses its JavaScript regex engine and the CSS-variables theme,
  mapped to `tok-*` classes.** The Oniguruma engine needs WebAssembly,
  which the CSP blocks; inline token styles are blocked too. The
  first-paint chunk carries only the alias table.
- **The budget script checks marker strings** (`ShikiError`, `KaTeX
  parse error`) in first-paint chunks, and fails if a marker is found in
  no chunk at all, so the guard cannot go stale silently.
- **zod runs `jitless`.** Its JIT probe calls `new Function`, which the
  CSP reports as a violation; the chat spec asserts no CSP report at all.
- **Images in Markdown never load**; they become a link labelled
  `Image: <alt>`. Relative links render as text: they would navigate the
  gateway's own pages. File-path links arrive with the M5 file tabs.
- **User text stays plain**; only assistant text is Markdown, as in DSH.
- **Tool status has four values.** A result decides; without one, a call
  runs only while its own turn is live (the session is `running` or
  `pending_interaction` and no user message follows); a canceled or
  failed finish, the settle step's interrupted sentence, or bash's
  `interrupted` metadata all read `Interrupted`.
- **Older pages live in the store** with a per-session `generation` that
  every snapshot and every bootstrap moves. Live edits and deletes reach
  them; a page that answers under another generation is dropped.
- **Subagent sessions are never rows.** The store drops a child from the
  bootstrap list and from `session` frames (the real gateway publishes
  one when a subagent starts). An open agent row watches the child, which
  is subscribed after the live sessions under the same cap of 32 and
  dropped on close or when the selection changes. Nesting stops at depth
  3.
- **Open tool rows live in a context above the virtualizer**, so a row
  scrolled out and back keeps its state; an open agent row resubscribes
  its child when it mounts again.
- **The virtualizer's built-in `anchorTo: 'end'` mode is not used.** The
  three explicit rules in Task 13 are testable on their own and keep the
  reader's intent (pinned or not) in one ref; the browser's own
  `overflow-anchor` is off so nothing else moves the view.
- **The turn rail, retry row and toasts are not in M2.** The M2 row of
  the spec does not list them; the retry row needs the composer (M3),
  and errors keep M1's alert row.
- **DSH lifting is limited to leaves with no Cordis coupling**: the
  Markdown grammar setup, the Shiki core with its Go grammar patch
  (which matters for a Go codebase), and the token palette. DSH's
  Markdown, tool and terminal CSS modules are bound to its slot markup
  and inline-style ANSI, so the M2 styles are written against the copied
  DSH tokens instead.
- **A streamed code fence keeps its finished lines highlighted** and
  renders only the growing tail plain until the next pass, instead of
  DSH's incremental grammar-state tokenizer (YAGNI for M2).

### Notes for whoever executes this

- The Go tests must keep working at every intermediate commit; only
  Tasks 1 and 14 touch Go.
- `docs/web-mode.md` keeps its "being rebuilt" note until M6.
- The preview banner stays until M6.
- If a version pinned above is no longer on the registry when a task
  runs, use the newest patch of the same minor, record it in the PR
  description, and rerun `npm run budget`.
