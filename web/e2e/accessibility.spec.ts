import AxeBuilder from '@axe-core/playwright'

import { expect, test } from './support/webfake'

test('the shell has no serious accessibility violations', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await expect(page.getByText('Preview: the new web UI is incomplete')).toBeVisible()
  await page.getByText('Port the parser').click()
  await expect(page.getByText('Port the tokenizer')).toBeVisible()

  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  const serious = results.violations.filter(
    (violation) => violation.impact === 'serious' || violation.impact === 'critical',
  )
  // Name the rules and their targets in the failure: an empty array says
  // nothing about what to fix.
  expect(
    serious.map((violation) => ({
      id: violation.id,
      impact: violation.impact,
      targets: violation.nodes.map((node) => node.target),
    })),
  ).toEqual([])
})

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
