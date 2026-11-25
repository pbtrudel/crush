import type { Page } from '@playwright/test';
import { expect, m4bFreshTest } from './fixture';

// The display settings live in the gateway's config, so every case that
// writes one or stops web mode gets a gateway of its own: a write would reach
// the tests that follow it.
const linked = (page: Page) =>
  expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });

m4bFreshTest('keeps the theme across a reload', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Display settings' });
  await expect(dialog).toBeVisible();

  await dialog.getByRole('radio', { name: 'Solid glass' }).check();
  await expect(page.locator('.holodeck')).toHaveAttribute('data-theme', 'solid');

  // The setting is the gateway's now, so a reload reads it back instead of
  // falling back to the default theme.
  await page.reload();
  await expect(page.locator('.holodeck')).toHaveAttribute('data-theme', 'solid');
});

m4bFreshTest('hides the orbit hint in cinematic mode', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);

  // The hint is there before the setting is turned off.
  await page.keyboard.press('Alt+KeyC');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'true');
  await expect(page.locator('.cine-hint')).toBeVisible();
  await page.keyboard.press('Alt+KeyC');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'false');

  await page.getByRole('button', { name: 'Display', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Display settings' });
  await dialog.getByRole('checkbox', { name: 'Show the orbit hint in cinematic view' }).uncheck();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();

  await page.keyboard.press('Alt+KeyC');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'true');
  await expect(page.locator('.cine-hint')).toHaveCount(0);
});

m4bFreshTest('stops web mode from the browser', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Display settings' });
  await dialog.getByRole('button', { name: 'Stop web mode', exact: true }).click();
  await dialog.getByRole('button', { name: 'Stop web mode now' }).click();

  await expect(dialog).toBeHidden();
  const stopped = 'Web mode was stopped. Run /web in the terminal for a new link.';
  await expect(page.getByRole('status').filter({ hasText: stopped })).toBeVisible({ timeout: 30_000 });
  // The deck's input is disabled for the same reason its help line names.
  const deck = page.getByLabel('Transmit to crush');
  await expect(deck).toBeDisabled();
  await expect(deck).toHaveAttribute('placeholder', 'Web mode stopped');
  await expect(page.locator('#deck-help')).toHaveText(stopped);
});
