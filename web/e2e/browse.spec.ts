import type { Page } from '@playwright/test';
import { expect, m4bFreshTest, m4bTest } from './fixture';

const linked = (page: Page) =>
  expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });

async function browseTokenizerPort(page: Page) {
  await page.getByRole('button', { name: 'Session: Holodeck test. Switch session' }).click();
  const dialog = page.getByRole('dialog', { name: 'Sessions' });
  await expect(dialog).toBeVisible();
  // The list is fetched, so the row exists only after its answer.
  const row = dialog.locator('.sessions__row').filter({ hasText: 'Tokenizer port' });
  await expect(row).toBeVisible({ timeout: 10_000 });
  await row.getByRole('button', { name: 'Browse here: Tokenizer port' }).click();
  await expect(dialog).toBeHidden();
  // The HUD keeps the pin only on the named desktop layouts; narrower
  // widths reach the same state through the session title and the deck.
  if ((page.viewportSize()?.width ?? 0) >= 1280) {
    await expect(page.getByRole('button', { name: 'Browsing Tokenizer port' })).toHaveAttribute('aria-pressed', 'false');
  }
}

m4bTest('browses another session read-only and returns to the terminal', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);
  await browseTokenizerPort(page);

  // The log shows the browsed session's seeded message.
  await expect(page.locator('.panel--log')).toContainText('Porting the tokenizer.');
  await expect(page.getByRole('button', { name: 'Session: Tokenizer port. Switch session' })).toBeVisible();

  // The deck explains why it is read-only and offers both ways out.
  const area = page.getByLabel('Browsing Tokenizer port');
  await expect(area).not.toBeEditable();
  await expect(page.locator('#deck-readonly'))
    .toHaveText("Prompts go to the terminal's session. Return to it, or open this session in the terminal.");
  const deck = page.locator('.holodeck__deck');
  await expect(deck.getByRole('button', { name: 'Return to the terminal\'s session' })).toBeVisible();
  await expect(deck.getByRole('button', { name: 'Open this session in the terminal' })).toBeVisible();

  // Enter reaches no prompt, counted at the wire rather than by the log:
  // the deck stays read-only and no prompt request leaves the page.
  let prompts = 0;
  page.on('request', (r) => {
    if (r.method() === 'POST' && /\/api\/sessions\/[^/]+\/prompt$/.test(new URL(r.url()).pathname)) prompts++;
  });
  await area.click();
  await page.keyboard.type('Ship it');
  await page.keyboard.press('Enter');
  await expect(area).toHaveValue('');
  await page.waitForTimeout(300);
  expect(prompts).toBe(0);

  await deck.getByRole('button', { name: 'Return to the terminal\'s session' }).click();
  await expect(page.getByRole('button', { name: 'Session: Holodeck test. Switch session' })).toBeVisible();
  await expect(page.getByLabel('Transmit to crush')).toBeEditable();
  await expect(page.getByRole('button', { name: 'Following the terminal' })).toHaveAttribute('aria-pressed', 'true');
});

m4bFreshTest('opens the browsed session in the terminal', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);
  await browseTokenizerPort(page);

  const followed = page.waitForResponse((r) => r.url().endsWith('/api/follow') && r.request().method() === 'POST');
  await page.locator('.holodeck__deck').getByRole('button', { name: 'Open this session in the terminal' }).click();
  expect((await followed).ok()).toBe(true);

  // The session_follow frame pins the tab back to the terminal, which now
  // shows the session the tab was browsing.
  await expect(page.getByRole('button', { name: 'Browsing Tokenizer port' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Following the terminal' })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByRole('button', { name: 'Session: Tokenizer port. Switch session' })).toBeVisible();
  await expect(page.getByLabel('Transmit to crush')).toBeEditable();
  await expect(page.locator('.panel--log')).toContainText('Porting the tokenizer.');
});

m4bTest('returns from the browsed deck at 760 by 900', async ({ page, gateway }) => {
  await page.setViewportSize({ width: 760, height: 900 });
  await page.goto(gateway.launch);
  await linked(page);
  await browseTokenizerPort(page);

  // Playwright hit-tests the click, so a control covered by another layer
  // fails instead of being clicked at its center.
  const back = page.locator('.holodeck__deck').getByRole('button', { name: 'Return to the terminal\'s session' });
  await expect(back).toBeVisible();
  await back.click();
  await expect(page.getByLabel('Transmit to crush')).toBeEditable();
  await expect(page.getByRole('button', { name: 'Session: Holodeck test. Switch session' })).toBeVisible();
});

// Both surfaces resolve to the same action: scroll the log to the tool card,
// open it and put the focus on it.
m4bTest('focuses a seeded tool call from the chronoline and the drawer', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);

  const timeline = page.getByRole('list', { name: 'Timeline of tool calls' });
  await expect(timeline).toBeVisible();
  const events = timeline.getByRole('button');
  await expect(events).toHaveCount(2);
  await expect(events.nth(0)).toHaveAttribute('data-call-id', 'tc-edit-1');
  await expect(events.nth(1)).toHaveAttribute('data-call-id', 'tc-edit-2');

  await events.nth(1).click();
  const second = page.locator('details.tool[data-tool-call-id="tc-edit-2"]');
  await expect(second).toHaveAttribute('open', '');
  await expect(second.locator('summary')).toBeFocused();
  await expect(second).toBeInViewport();

  // The drawer's link for tc-edit-1 reaches the other card. To picks a
  // single version, so the panel draws one line, not a recorded and a
  // linked one. The drawer has its own column at this width, so it stays
  // open and the reader can step through versions.
  await page.getByRole('button', { name: /^Files/ }).click();
  const drawer = page.locator('#file-drawer');
  await page.locator('.drawer__list').getByRole('button', { name: /parser\.go/ }).click();
  const to = page.locator('select[name="to"]');
  await expect(to.locator('option[value="3"]')).toHaveCount(1);
  await to.selectOption('3');
  const made = page.locator('.diffpanel__made');
  const link = made.getByRole('button', { name: 'Made by edit in Coder' });
  await expect(link).toBeVisible();
  await link.click();

  await expect(drawer).toHaveAttribute('data-open', 'true');
  const first = page.locator('details.tool[data-tool-call-id="tc-edit-1"]');
  await expect(first).toHaveAttribute('open', '');
  await expect(first.locator('summary')).toBeFocused();
  await expect(first).toBeInViewport();
});

// Between 1024 and 1279 the drawer covers the log column and the log is
// inert under it, so a version link has to close the drawer before the card
// can take the focus.
m4bTest('focuses a version link at 1100 wide', async ({ page, gateway }) => {
  await page.setViewportSize({ width: 1100, height: 800 });
  await page.goto(gateway.launch);
  await linked(page);

  await page.getByRole('button', { name: /^Files/ }).click();
  const drawer = page.locator('#file-drawer');
  await expect(drawer).toHaveAttribute('data-open', 'true');
  await page.locator('.drawer__list').getByRole('button', { name: /parser\.go/ }).click();
  const to = page.locator('select[name="to"]');
  await expect(to.locator('option[value="3"]')).toHaveCount(1);
  await to.selectOption('3');
  const link = page.locator('.diffpanel__made').getByRole('button', { name: 'Made by edit in Coder' });
  await expect(link).toBeVisible();
  await link.click();

  await expect(drawer).toHaveAttribute('data-open', 'false');
  await expect(page.locator('.panel--log')).not.toHaveAttribute('inert');
  const card = page.locator('details.tool[data-tool-call-id="tc-edit-1"]');
  await expect(card).toHaveAttribute('open', '');
  await expect(card.locator('summary')).toBeFocused();
  await expect(card).toBeInViewport();
});
