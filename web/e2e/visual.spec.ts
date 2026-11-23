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

test('agents and the inspector', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await page.getByRole('listbox', { name: 'Agents' }).getByRole('option', { name: /alice/ }).click();
  // The transcript is fetched after the pick, so wait for its text before
  // the clock is woken for the shot.
  await expect(page.locator('.panel--inspector')).toContainText('Porting the parser to the new token stream.');
  await page.clock.runFor(1000);
  await expect(page).toHaveScreenshot('inspector.png', { timeout: 30_000 });
  await axe(page);
});

test('file drawer with a diff', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await page.getByRole('button', { name: /^Files/ }).click();
  // The diff is fetched from an effect, and a paused clock only runs effects
  // when it moves: wait for the answer, then step the frame that mounts the
  // editor. Both steps are a fixed length, so the scene behind the drawer
  // reaches the same frame on every run.
  const answer = page.waitForResponse((r) => /\/files\/diff\?/.test(r.url()));
  await page.getByRole('button', { name: /parser\.go/ }).click();
  await answer;
  await page.clock.runFor(50);
  await expect(page.locator('.cm-mergeView')).toBeVisible();
  // The drawer changes the stage's box, so the projector moves to the new
  // centre once the resize lands. Park the pointer in the middle (no
  // parallax) and give both the turn and the offset two seconds of frames,
  // or the object order of the setup clicks decides where the core sits.
  await page.mouse.move(720, 430);
  await page.clock.runFor(2000);
  await expect(page).toHaveScreenshot('drawer.png', { timeout: 30_000 });
  await axe(page);
});

test('agents in the 2D view', async ({ page, gateway }) => {
  await page.addInitScript(() => localStorage.setItem('crush-web-low-power', '1'));
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });
  // The roster arrives in stream frames, and a paused clock only delivers
  // them when it moves. The 2D constellation is drawn from state alone, so
  // how far the clock walked here does not change the picture.
  await expect.poll(async () => {
    await page.clock.runFor(100);
    return page.locator('svg [data-agent-node]').count();
  }, { message: 'the 2D nodes render', timeout: 20_000 }).toBeGreaterThan(0);
  await page.clock.runFor(1000);
  await expect(page).toHaveScreenshot('agents-2d.png', { timeout: 30_000 });
  await axe(page);
});

// G2-43: the HUD strip wraps to a second row on a narrower laptop. Two rows
// measured 151px in M1; one row stays within a few pixels of the 1440px
// strip.
test('the HUD keeps one row at 1280px', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });
  await page.clock.runFor(500);
  const wide = (await page.locator('.hud').boundingBox())!.height;
  await page.setViewportSize({ width: 1280, height: 860 });
  await page.clock.runFor(500);
  const laptop = (await page.locator('.hud').boundingBox())!.height;
  expect(laptop).toBeLessThanOrEqual(wide + 8);
});
