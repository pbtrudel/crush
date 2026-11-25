import {
  axeScan, awsTest, expect, freshTest, m3VisualTest, m4VisualTest, m4bVisualTest, promptVisualTest, seedDisplay,
  visualTest as test,
} from './fixture';
import type { Page } from '@playwright/test';

// The software renderer needs more than the default 30 seconds to draw the
// clock-driven frames behind each screenshot.
test.setTimeout(120_000);
m3VisualTest.setTimeout(120_000);
m4VisualTest.setTimeout(120_000);
m4bVisualTest.setTimeout(120_000);
promptVisualTest.setTimeout(120_000);
awsTest.setTimeout(120_000);

// A fixed instant for Date and for the scene clock. The clock is installed
// before the page loads and paused at this instant: one that kept flowing
// would leave the scene at a different frame on every run.
const TEST_TIME = new Date('2026-11-22T12:00:00Z');

async function prepare(page: Page) {
  await page.addInitScript(() => localStorage.setItem('crush-web-seed', '1234'));
  await page.clock.install({ time: new Date(TEST_TIME.getTime() - 10_000) });
  await page.clock.pauseAt(TEST_TIME);
}

test.beforeEach(async ({ page }) => { await prepare(page); });
m3VisualTest.beforeEach(async ({ page }) => { await prepare(page); });
m4VisualTest.beforeEach(async ({ page }) => { await prepare(page); });
m4bVisualTest.beforeEach(async ({ page }) => { await prepare(page); });
freshTest.beforeEach(async ({ page }) => { await prepare(page); });
promptVisualTest.beforeEach(async ({ page }) => { await prepare(page); });

async function settle(page: Page) {
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });
  // The lazy scene mounts after the link. Waking frames before it exists
  // would start the power-up at a different frame on each run.
  await page.waitForFunction(() => {
    const canvas = document.querySelector<HTMLCanvasElement>('canvas.scene-layer__canvas');
    return canvas !== null && canvas.width > 300;
  }, undefined, { polling: 50, timeout: 10_000 });
  await page.clock.runFor(3000);
}

async function axe(page: Page) {
  // Axe waits on animation frames, and a paused clock never delivers one.
  await page.clock.resume();
  await axeScan(page);
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

freshTest('file drawer with a diff', async ({ page, gateway }) => {
  // The scene behind the drawer orbits on the frame clock, and its phase is
  // the integral of the frames that rendered: a lazy chunk or a resize
  // observer landing between two clock runs changes how many frames each run
  // gets, which moved the scene one frame and about 6% of the pixels between
  // otherwise identical CI runs. Off freezes the scene after the drawer's
  // resize has moved the projector, so the shot no longer depends on where
  // the setup's real-time work landed. The drawer and the diff editor keep
  // animating normally.
  await seedDisplay(page, gateway, 'options.web_display.motion', 'off');
  await page.goto(gateway.launch);
  await settle(page);
  await page.getByRole('button', { name: /^Files/ }).click();
  // The diff is fetched from an effect, and a paused clock only runs effects
  // when it moves: wait for the answer, then step the frame that mounts the
  // editor. The drawer changes the stage's box, so the same frames also land
  // the resize that moves the projector to the stage's new centre.
  const answer = page.waitForResponse((r) => /\/files\/diff\?/.test(r.url()));
  await page.getByRole('button', { name: /parser\.go/ }).click();
  await answer;
  await page.clock.runFor(50);
  await expect(page.locator('.cm-mergeView')).toBeVisible();
  // Park the pointer off the file row so no hover state leaks into the shot,
  // then give the editor's deferred measurement a frame to land.
  await page.mouse.move(720, 430);
  await page.clock.runFor(2000);
  await expect(page).toHaveScreenshot('drawer.png', { timeout: 30_000 });
  await axe(page);
});

// Low power is the gateway's setting now, so the test turns it on through
// the dialog. Its own webfake keeps the write off the worker's shared one.
freshTest('agents in the 2D view', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  const settings = page.getByRole('dialog', { name: 'Display settings' });
  await settings.getByRole('checkbox', { name: 'Low power (2D constellation)' }).check();
  await page.keyboard.press('Escape');
  await expect(settings).toBeHidden();
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

// The three M3 overlays, on the M3 scenario with the same fixed clock. They
// are test-scoped webfakes: each overlay is opened from a fresh page. A
// paused clock delivers no effect frame, so each wait steps the clock until
// the fetched sources have rendered.
m3VisualTest('command palette', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  // One name per fetched group proves the whole list has rendered.
  for (const name of ['user:fix', 'go-tests', 'Triage an issue', 'Holodeck test', 'Model One']) {
    await expect.poll(async () => {
      await page.clock.runFor(50);
      return palette.getByRole('option', { name: new RegExp(name) }).count();
    }, { message: `the ${name} option renders`, timeout: 10_000 }).toBeGreaterThan(0);
  }
  await page.clock.runFor(1000);
  await expect(page).toHaveScreenshot('palette.png', { timeout: 30_000 });
  await axe(page);
});

m3VisualTest('systems panel', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await page.getByRole('button', { name: /^MCP servers:/ }).click();
  const panel = page.getByRole('dialog', { name: 'Systems' });
  // The counts only exist in the fetched list, so this is the whole panel.
  const gopls = panel.locator('.systems__card[data-server="gopls"]');
  await expect.poll(async () => {
    await page.clock.runFor(50);
    return gopls.textContent();
  }, { message: 'the LSP list renders', timeout: 10_000 }).toContain('2 errors, 1 warning');
  await page.clock.runFor(1000);
  await expect(page).toHaveScreenshot('systems.png', { timeout: 30_000 });
  await axe(page);
});

m3VisualTest('model selector', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await page.getByRole('button', { name: 'Model: Model One. Switch model' }).click();
  const choices = page.getByRole('dialog', { name: 'Models' }).getByRole('listbox', { name: 'Model choices' });
  await expect.poll(async () => {
    await page.clock.runFor(50);
    return choices.getByRole('option').count();
  }, { message: 'the provider list renders', timeout: 10_000 }).toBe(2);
  await page.clock.runFor(1000);
  await expect(page).toHaveScreenshot('models.png', { timeout: 30_000 });
  await axe(page);
});

// The M4a surfaces, on the M4 scenario. Each gateway scripts the prompt or
// starts the flow before the shot, and the paused clock is stepped until the
// frame that renders it has run.
promptVisualTest('secure prompt', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  const dialog = page.getByRole('dialog', { name: 'GPG passphrase' });
  // The stream batches events into animation frames, so a paused clock needs
  // a frame after the prompt arrives before the dialog can render.
  for (let i = 0; i < 120 && !(await dialog.isVisible()); i++) {
    await page.clock.runFor(16);
  }
  await expect(dialog).toBeVisible({ timeout: 30_000 });
  await page.clock.runFor(1000);
  await expect(page).toHaveScreenshot('secure-prompt.png', { timeout: 30_000 });
  await axe(page);
});

m4VisualTest('sign-in device code', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  await page.getByRole('button', { name: 'Model: Model One. Switch model' }).click();
  await page.getByRole('dialog', { name: 'Models' }).getByRole('button', { name: 'Sign in to another provider' }).click();
  const dialog = page.getByRole('dialog', { name: 'Sign in', exact: true });
  const copilot = dialog.getByRole('option', { name: 'GitHub Copilot' });
  // The providers come from a fetch, so the list renders on a later frame.
  await expect.poll(async () => {
    await page.clock.runFor(50);
    return copilot.count();
  }, { message: 'the provider list renders', timeout: 10_000 }).toBeGreaterThan(0);
  await copilot.click();
  await dialog.getByRole('button', { name: 'Start sign-in' }).click();
  await expect.poll(async () => {
    await page.clock.runFor(50);
    return dialog.locator('.signin__code-value').textContent();
  }, { message: 'the device code renders', timeout: 10_000 }).toBe('WDJB-MJHT');
  await page.clock.runFor(1000);
  // webfake dates the code from WEBFAKE_CLOCK, the instant this file freezes
  // the clock at, so the countdown on the status line is the same on every
  // run and needs no mask.
  await expect(page).toHaveScreenshot('signin-device.png', { timeout: 30_000 });
  await axe(page);
});

// The M4b surfaces, on the M4b scenario: the display settings dialog, and the
// solid theme a gateway starts on when the run asks for it.
m4bVisualTest('display settings', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await settle(page);
  // The dialog opens from a click, so no clock from the gateway's start is
  // involved.
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Display settings' })).toBeVisible();
  await page.clock.runFor(1000);
  await expect(page).toHaveScreenshot('display-settings.png', { timeout: 30_000 });
  await axe(page);
});

m4bVisualTest.describe('solid theme', () => {
  m4bVisualTest.use({ theme: 'solid' });

  m4bVisualTest('the solid theme', async ({ page, gateway }) => {
    await page.goto(gateway.launch);
    await settle(page);
    await expect(page.locator('.holodeck')).toHaveAttribute('data-theme', 'solid');
    await page.clock.runFor(1000);
    await expect(page).toHaveScreenshot('solid-theme.png', { timeout: 30_000 });
    await axe(page);
  });
});

// The AWS SSO card is a reading surface with no baseline of its own: the axe
// scan is the check that it stays accessible. Every test in this file gets the
// paused clock, so this one steps frames until the card's URL frame lands.
awsTest('the AWS SSO card', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  // The scan only means something once the tab holds a live stream.
  await expect.poll(async () => {
    await page.clock.runFor(50);
    return page.getByRole('status').filter({ hasText: 'Linked' }).count();
  }, { message: 'the tab links', timeout: 30_000 }).toBeGreaterThan(0);
  const card = page.locator('.aws-sso');
  const link = card.getByRole('link', { name: 'Open the AWS sign-in page' });
  await expect.poll(async () => {
    await page.clock.runFor(50);
    return link.count();
  }, { message: 'the AWS SSO card renders', timeout: 10_000 }).toBe(1);
  await expect(card).toContainText('aws sso login --profile dev');
  await expect(link).toHaveAttribute('href', 'https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD');
  await axe(page);
});
