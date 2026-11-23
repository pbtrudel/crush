import AxeBuilder from '@axe-core/playwright';
import { visualTest as test, expect } from './fixture';

// The software renderer needs more than the default 30 seconds to draw the
// clock-driven frames behind each screenshot.
test.setTimeout(120_000);

// A fixed instant for Date and for the scene clock. The clock is installed
// before the page loads and paused at this instant: one that kept flowing
// would leave the scene at a different frame on every run.
const TEST_TIME = new Date('2026-11-22T12:00:00Z');

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('crush-web-seed', '1234'));
  await page.clock.install({ time: new Date(TEST_TIME.getTime() - 10_000) });
  await page.clock.pauseAt(TEST_TIME);
});

async function settle(page: import('@playwright/test').Page) {
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });
  // The lazy scene mounts after the link. Waking frames before it exists
  // would start the power-up at a different frame on each run.
  await page.waitForFunction(() => {
    const canvas = document.querySelector<HTMLCanvasElement>('canvas.scene-layer__canvas');
    return canvas !== null && canvas.width > 300;
  }, undefined, { polling: 50, timeout: 10_000 });
  await page.clock.runFor(3000);
}

async function axe(page: import('@playwright/test').Page) {
  // Axe waits on animation frames, and a paused clock never delivers one.
  await page.clock.resume();
  const { violations } = await new AxeBuilder({ page }).analyze();
  expect(violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([]);
}

test('normal layout', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await expect(page).toHaveScreenshot('normal.png', { timeout: 30_000 });
  await axe(page);
});

test('cinematic mode', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  // The flight is a lazy chunk. Waiting for it puts the first animated frame
  // after the import instead of somewhere inside the clock run.
  const flight = page.waitForResponse((r) => /\/assets\/cinematic-[^/]*\.js$/.test(r.url()));
  await page.keyboard.press('Alt+KeyC');
  await (await flight).finished();
  await page.clock.runFor(1200);
  await expect(page).toHaveScreenshot('cinematic.png', { timeout: 30_000 });
  await axe(page);
});

test('permission request', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await page.getByLabel('Transmit to crush').fill('run permission');
  await page.keyboard.press('Enter');
  // The stream batches events into animation frames, so a paused clock
  // needs a frame after the request arrives before the dialog can render.
  const dialog = page.getByRole('alertdialog');
  for (let i = 0; i < 120 && !(await dialog.isVisible()); i++) {
    await page.clock.runFor(16);
  }
  await expect(dialog).toBeVisible({ timeout: 30_000 });
  await page.clock.runFor(1000);
  await expect(page).toHaveScreenshot('permission.png', { timeout: 30_000 });
  await axe(page);
  await page.keyboard.press('d');
  await expect(dialog).toBeHidden();
});

test('reduced motion', async ({ page, gateway }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(gateway.launch);
  await settle(page);
  await expect(page).toHaveScreenshot('reduced.png', { timeout: 30_000 });
  await axe(page);
});
