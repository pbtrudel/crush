import type { Page } from '@playwright/test';
import { expect, m4bFreshTest } from './fixture';

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

m4bFreshTest('browses another session read-only and returns to the terminal', async ({ page, gateway }) => {
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

  // Enter reaches no prompt: the deck stays read-only and the log gains no
  // transmission.
  await area.click();
  await page.keyboard.type('Ship it');
  await page.keyboard.press('Enter');
  await expect(area).toHaveValue('');
  await expect(page.locator('.panel--log')).not.toContainText('Ship it');

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

m4bFreshTest('returns from the browsed deck at 760 by 900', async ({ page, gateway }) => {
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
