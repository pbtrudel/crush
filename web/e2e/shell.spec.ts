import { writeFileSync } from 'node:fs'

import type { Page } from '@playwright/test'

import { expect, test } from './support/webfake'

// row is the sidebar entry for a seeded session. The title is the locator's
// text and the markers live inside the same list item.
function row(page: Page, title: string) {
  return page.locator('li', { hasText: title })
}

test('boots from the launch link and drops the token from the URL', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await expect(page.getByText('Preview: the new web UI is incomplete')).toBeVisible()
  await expect(page.getByTestId('app-frame')).toBeVisible()
  // A plain boolean keeps a failure from printing the token with the URL.
  expect(page.url().includes('#t='), 'the token stayed in the URL').toBe(false)
})

test('lists the seeded sessions with their markers', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await expect(page.getByText('Port the parser')).toBeVisible()
  await expect(page.getByText('Write parser tests')).toBeVisible()
  await expect(page.getByText('Rename the grammar')).toBeVisible()
  await expect(page.getByText('Old scratch session')).toBeVisible()
  await expect(row(page, 'Port the parser').locator('[aria-label="Running"]')).toBeVisible()
  await expect(row(page, 'Write parser tests').locator('[aria-label="Queued"]')).toBeVisible()
})

test('opens one session after another', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await page.getByText('Port the parser').click()
  await expect(page.getByText('Port the tokenizer')).toBeVisible()
  await page.getByText('Write parser tests').click()
  await expect(page.getByText('Draft the table tests')).toBeVisible()
})

test('shows a marker that appears without a reload', async ({ page, freshApp }) => {
  // This spec gets its own gateway, and that gateway holds s-m1-wait's
  // permission until the file below is written: the change cannot land while
  // the page is still booting, however slow the runner is.
  await page.goto(freshApp.launchUrl)
  // The row must be on screen before its missing marker means anything.
  const waiting = row(page, 'Rename the grammar')
  await expect(waiting).toBeVisible()
  await expect(row(page, 'Port the parser').locator('[aria-label="Running"]')).toBeVisible()
  await expect(waiting.locator('[aria-label="Waiting for you"]')).toHaveCount(0)
  // The permission is published on a page that is already open: no reload
  // stands between the two assertions.
  writeFileSync(freshApp.pendingFile, '')
  await expect(waiting.locator('[aria-label="Waiting for you"]')).toBeVisible({ timeout: 15_000 })
})

test('keeps the shell clickable while a permission is open', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  // p-m1 opened on the worker's gateway before this spec ran. M1 renders no
  // permission dialog, so the row marker is its only trace and nothing covers
  // the shell.
  await expect(
    row(page, 'Rename the grammar').locator('[aria-label="Waiting for you"]'),
  ).toBeVisible({ timeout: 15_000 })
  await page.getByText('Port the parser').click()
  await expect(page.getByText('Port the tokenizer')).toBeVisible()
})

test('keeps a background session live while another is open', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await page.getByText('Write parser tests').click()
  await expect(page.getByText('Draft the table tests')).toBeVisible()
  // The running session is not the open one, and its marker stays: the tab
  // keeps a subscription per running session.
  const running = row(page, 'Port the parser')
  await expect(running.locator('[aria-label="Running"]')).toBeVisible()
})

test('shows the reason the gateway refused a delete', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  // s-m1-run is the busy session SeedM1 leaves a run in, so the gateway
  // answers 409 session_busy and the sidebar shows its sentence.
  const running = row(page, 'Port the parser')
  await running.hover()
  await running.getByLabel('Delete session').click()
  await expect(page.getByRole('alert')).toContainText('Crush is working in this session')
})

test('opens the drawer at 375px and returns focus to the opener', async ({ page, app }) => {
  await page.setViewportSize({ width: 375, height: 800 })
  await page.goto(app.launchUrl)
  const opener = page.getByRole('button', { name: 'Open sidebar' })
  await expect(opener).toBeVisible()
  const panel = page.getByTestId('sidebar-panel')
  await expect(panel).not.toBeVisible()

  await opener.click()
  await expect(panel).toBeVisible()
  await expect(panel).toHaveAttribute('role', 'dialog')
  const close = page.getByRole('button', { name: 'Close sidebar' })
  await expect(close).toBeFocused()

  await page.keyboard.press('Escape')
  await expect(panel).not.toBeVisible()
  await expect(opener).toBeFocused()

  await opener.click()
  await expect(close).toBeVisible()
  await close.click()
  await expect(panel).not.toBeVisible()
  await expect(opener).toBeFocused()
})

test('scrolls the conversation log instead of the page', async ({ page, app }) => {
  // "Port the parser" is the longest seeded session, and two messages do not
  // fill a real window: the viewport is shorter than the shell's own chrome
  // plus the log's natural height, so the log overflows and the check says
  // something. The width keeps the sidebar in its column layout.
  await page.setViewportSize({ width: 900, height: 150 })
  await page.goto(app.launchUrl)
  await page.getByText('Port the parser').click()
  const log = page.getByTestId('conversation-log')
  await expect(log).toBeVisible()
  const metrics = await log.evaluate((element) => ({
    scrollHeight: element.scrollHeight,
    clientHeight: element.clientHeight,
    pageHeight: document.scrollingElement?.scrollHeight ?? 0,
    viewportHeight: window.innerHeight,
  }))
  expect(metrics.scrollHeight, JSON.stringify(metrics)).toBeGreaterThan(metrics.clientHeight)
  // The page is exactly one viewport tall, so only the panes scroll.
  expect(metrics.pageHeight, JSON.stringify(metrics)).toBeLessThanOrEqual(metrics.viewportHeight)

  // The session list scrolls in its own column the same way.
  const nav = await page.getByRole('navigation', { name: 'Sessions' }).evaluate((element) => ({
    scrollHeight: element.scrollHeight,
    clientHeight: element.clientHeight,
  }))
  expect(nav.scrollHeight, JSON.stringify(nav)).toBeGreaterThan(nav.clientHeight)

  const after = await log.evaluate((element) => {
    element.scrollTop = element.scrollHeight
    return { logTop: element.scrollTop, pageTop: document.scrollingElement?.scrollTop ?? -1 }
  })
  expect(after.logTop).toBeGreaterThan(0)
  expect(after.pageTop).toBe(0)
})
