import { writeFileSync } from 'node:fs'

import type { Locator, Page } from '@playwright/test'

import { expect, test as base } from './support/webfake'

// CspWatch is the violations one page reported: the browser's console
// message for each refusal, whether it came from a real console report or
// from the securitypolicyviolation listener installed below.
interface CspWatch {
  violations: string[]
}

// watchCsp listens before the page's own scripts run. Chrome reports a
// refused inline style, eval or remote image on the console and fires
// securitypolicyviolation; the listener feeds the same console channel so
// one list carries both. The virtualizer writes its geometry through the
// CSSOM, which style-src 'self' allows, so a violation here is a real bug.
async function watchCsp(page: Page): Promise<CspWatch> {
  const watch: CspWatch = { violations: [] }
  page.on('console', (message) => {
    if (message.type() === 'error' && /Content Security Policy/i.test(message.text())) {
      watch.violations.push(`console: ${message.text()}`)
    }
  })
  await page.addInitScript(() => {
    window.addEventListener('securitypolicyviolation', (event) => {
      const blocked = event.blockedURI === '' ? 'inline' : event.blockedURI
      console.error(
        `Content Security Policy: ${event.violatedDirective} blocked ${blocked}` +
          ` at ${event.sourceFile}:${event.lineNumber}`,
      )
    })
  })
  return watch
}

// Every chat spec collects the page's CSP violations and fails on any,
// even when the body's own assertions passed.
const test = base.extend<{ csp: CspWatch }>({
  csp: [
    async ({ page }, use) => {
      const watch = await watchCsp(page)
      await use(watch)
      expect(watch.violations, 'the chat page broke the content security policy').toEqual([])
    },
    { auto: true },
  ],
})

// SamplerWindow is the page-side channel the paging spec uses to read its
// animation-frame row counts.
interface SamplerWindow extends Window {
  __rowCounts?: number[]
  __stopRowSampler?: () => void
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

// startRowSampler records how many virtualized rows the log holds on every
// animation frame until stopRowSampler reads the list.
async function startRowSampler(view: Locator) {
  await view.evaluate((element) => {
    const window_ = window as SamplerWindow
    const counts: number[] = []
    window_.__rowCounts = counts
    let stop = false
    window_.__stopRowSampler = () => {
      stop = true
    }
    const tick = () => {
      counts.push(element.querySelectorAll('[data-index]').length)
      if (!stop) {
        requestAnimationFrame(tick)
      }
    }
    requestAnimationFrame(tick)
  })
}

async function stopRowSampler(view: Locator): Promise<number[]> {
  return view.evaluate(() => {
    const window_ = window as SamplerWindow
    window_.__stopRowSampler?.()
    const counts = window_.__rowCounts ?? []
    delete window_.__rowCounts
    delete window_.__stopRowSampler
    return counts
  })
}

// lastStreamLineInView reads the last streamed paragraph and says whether
// the log's viewport holds all of it.
function lastStreamLineInView(view: Locator) {
  return view.evaluate((element) => {
    const lines = [...element.querySelectorAll('p')].filter((paragraph) =>
      /^Stream line \d+: the tokens keep arriving\.$/.test(paragraph.textContent ?? ''),
    )
    const last = lines.at(-1)
    if (last === undefined) {
      return { lines: 0, inView: true }
    }
    const line = last.getBoundingClientRect()
    const box = element.getBoundingClientRect()
    return {
      lines: lines.length,
      inView: line.top >= box.top - 1 && line.bottom <= box.bottom + 1,
      scrollTop: element.scrollTop,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
      lineTop: line.top,
      lineBottom: line.bottom,
      boxTop: box.top,
      boxBottom: box.bottom,
    }
  })
}

test('renders Markdown, code and math with no raw HTML and no CSP violation', async ({ page, chatApp }) => {
  await page.goto(chatApp.launchUrl)
  await openSession(page, 'Render the chat')
  await log(page).evaluate((element) => {
    element.scrollTop = 0
  })
  const chat = log(page)
  await expect(chat.getByRole('heading', { name: 'Chat rendering' })).toBeVisible()
  await expect(chat.getByRole('link', { name: 'Charm' }).first()).toHaveAttribute(
    'href',
    'https://charm.land/',
  )
  await expect(chat.getByRole('link', { name: 'a trap' })).toHaveCount(0)
  await expect(chat.getByRole('link', { name: 'Image: the logo' })).toBeVisible()
  // Shiki and KaTeX are lazy chunks: the highlighted tokens and the MathML
  // arrive after the plain text.
  await expect(chat.locator('pre code [class*="tok-"]').first()).toBeVisible()
  await expect(chat.locator('math')).toHaveCount(2)
  await expect(chat.locator('p', { hasText: 'stay text.' })).toContainText('<script>alert(1)</script>')
  await expect(chat.locator('img, script')).toHaveCount(0)
})

test('shows every tool view, a nested subagent call and an interrupted call', async ({ page, chatApp }) => {
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
  await expect(tool(page, 'edit').locator('[data-kind="add"]')).toContainText(
    'func Parse() error { return nil }',
  )
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
  await expect(log(page).locator('[data-tool="bash"][data-status="interrupted"]')).toContainText(
    'Wait ten minutes',
  )
  await expect(log(page).getByRole('note').filter({ hasText: 'Canceled.' })).toBeVisible()
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

test('opens a long session at its end and keeps the view still when an older page loads', async ({
  page,
  chatApp,
}) => {
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
  // It is a user message, one pre-wrap paragraph of five lines, so the
  // anchor is the assistant message after it, whose first paragraph is
  // exactly one line.
  const anchor = log(page).getByText('History message 31', { exact: true })
  await expect(anchor).toBeInViewport()
  const before = (await anchor.boundingBox())?.y ?? 0
  const beforeScroll = await log(page).evaluate((element) => element.scrollTop)

  // Sample every animation frame across the prepend: the virtualizer
  // remeasures 30 prepended rows, and the view must never paint an empty
  // log while it does.
  await startRowSampler(log(page))
  const older = page.waitForResponse((response) => response.url().includes('/messages?before='))
  await button.click()
  await older
  // The prepend commits when the anchored scroll top jumps by the older
  // page's height; the response alone arrives before React runs.
  await expect
    .poll(async () => log(page).evaluate((element) => element.scrollTop), { timeout: 5_000 })
    .toBeGreaterThan(beforeScroll + 100)
  await expect
    .poll(async () => Math.abs(((await anchor.boundingBox())?.y ?? -10_000) - before), { timeout: 5_000 })
    .toBeLessThan(4)
  const counts = await stopRowSampler(log(page))
  expect(counts.length).toBeGreaterThan(0)
  expect(Math.min(...counts), `row counts across the prepend: ${counts.join(',')}`).toBeGreaterThan(0)

  await log(page).evaluate((element) => {
    element.scrollTop = 0
  })
  // History message 0 is a user row too; its assistant neighbour is the
  // first exact one-line locator after it.
  await expect(log(page).getByText('History message 1', { exact: true })).toBeInViewport()
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

test('keeps the last streamed line in view on every sampled tick', async ({ page, streamApp }) => {
  await page.setViewportSize({ width: 1280, height: 360 })
  await page.goto(streamApp.launchUrl)
  await openSession(page, 'Stream a reply')
  writeFileSync(streamApp.streamFile, 'go')

  // The reply grows in place, one child message view at a time. Sample the
  // last paragraph while the stream runs: it must already be inside the
  // log's viewport each time, or the follow ran a frame behind.
  const outOfView: string[] = []
  await expect
    .poll(
      async () => {
        const sample = await lastStreamLineInView(log(page))
        if (sample.lines > 0 && !sample.inView) {
          outOfView.push(JSON.stringify(sample))
        }
        return sample.lines
      },
      { timeout: 20_000, intervals: [50] },
    )
    .toBe(24)
  expect(outOfView).toEqual([])
  await expect(log(page).getByText('Stream line 24: the tokens keep arriving.')).toBeInViewport()
})

test('keeps the focus in a tool row while following a reply past the window', async ({
  page,
  streamApp,
}) => {
  await page.setViewportSize({ width: 1280, height: 360 })
  await page.goto(streamApp.launchUrl)
  await openSession(page, 'Stream a reply')

  // The bash row is two rows from the top; the live end is sixteen rows
  // below it, outside the virtualizer's overscan.
  await log(page).evaluate((element) => {
    element.scrollTop = 0
  })
  await openTool(page, 'bash')
  const showAll = tool(page, 'bash').getByRole('button', { name: 'Show all 252 lines' })
  await expect(showAll).toBeVisible()
  await showAll.focus()
  await expect(showAll).toBeFocused()

  // Back to the live end without Back to bottom, which would move the
  // focus to the log itself.
  await log(page).evaluate((element) => {
    element.scrollTop = element.scrollHeight
  })
  await expect(page.getByRole('button', { name: 'Back to bottom' })).toHaveCount(0)

  writeFileSync(streamApp.streamFile, 'go')
  await expect(log(page).getByText('Stream line 24: the tokens keep arriving.')).toBeVisible({
    timeout: 20_000,
  })
  // The focused row stayed mounted even though the window moved far away
  // from it, and the button still owns the focus.
  expect(await showAll.evaluate((element) => element.isConnected)).toBe(true)
  await expect(showAll).toBeFocused()
})

test('leaves the view alone while a reply streams and the reader has scrolled up', async ({
  page,
  streamApp,
}) => {
  await page.setViewportSize({ width: 1280, height: 360 })
  await page.goto(streamApp.launchUrl)
  await openSession(page, 'Stream a reply')
  writeFileSync(streamApp.streamFile, 'go')
  await expect(log(page).getByText('Stream line 12:', { exact: false })).toBeVisible({ timeout: 20_000 })

  await log(page).evaluate((element) => {
    element.scrollTop = 0
  })
  await expect(page.getByRole('button', { name: 'Back to bottom' })).toBeVisible()
  // A row at the top of the log: the prompt is row 17 of 18, and a 360 px
  // log at the top does not mount it. The tool summary carries the same
  // words, so the anchor is the user paragraph only.
  const anchor = log(page).locator('p', { hasText: 'Run the tests' })
  const before = (await anchor.boundingBox())?.y ?? 0

  // The run ends when the session's running marker clears.
  const row = page
    .getByRole('navigation', { name: 'Sessions' })
    .locator('li', { hasText: 'Stream a reply' })
  await expect(row.locator('[aria-label="Running"]')).toHaveCount(0, { timeout: 20_000 })
  expect(await log(page).evaluate((element) => element.scrollTop)).toBeLessThan(5)
  expect(Math.abs(((await anchor.boundingBox())?.y ?? -10_000) - before)).toBeLessThan(4)

  await page.getByRole('button', { name: 'Back to bottom' }).click()
  await expect(log(page).getByText('Stream line 24:', { exact: false })).toBeInViewport()
})
