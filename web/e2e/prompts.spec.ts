import { setTimeout as sleep } from 'node:timers/promises';
import type { Page, Request } from '@playwright/test';
import {
  confirmTest, expect, promptElsewhereTest, promptLateTest, promptTest, touchTest,
} from './fixture';

// Each test launches its own webfake, and a cold Go build counts towards the
// test's own timeout.
promptTest.setTimeout(60_000);
promptElsewhereTest.setTimeout(60_000);
promptLateTest.setTimeout(60_000);
confirmTest.setTimeout(60_000);
touchTest.setTimeout(60_000);

const passphrase = (page: Page) => page.getByRole('dialog', { name: 'GPG passphrase' });

// sampleToasts collects the toast text over a window, every 100 ms, so a
// toast the reducer shows late cannot slip past a single check.
async function sampleToasts(page: Page, ms: number): Promise<string[]> {
  const seen = new Set<string>();
  const deadline = Date.now() + ms;
  for (;;) {
    for (const text of await page.locator('.toast').allTextContents()) seen.add(text);
    if (Date.now() >= deadline) return [...seen];
    await sleep(100);
  }
}

promptTest('answers a GPG passphrase and clears the field', async ({ page, gateway }) => {
  const requests: Request[] = [];
  page.on('request', (r) => requests.push(r));

  await page.goto(gateway.launch);
  const dialog = passphrase(page);
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel('Passphrase', { exact: true })).toBeFocused();

  await dialog.getByLabel('Passphrase', { exact: true }).fill('hunter2');
  await page.keyboard.press('Enter');
  await expect(dialog).toBeHidden();

  const answer = requests.find((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/pinentry/answer');
  expect(answer, 'the answer request').toBeDefined();
  const target = new URL(answer!.url());
  expect(target.search).toBe('');
  expect(target.searchParams.size).toBe(0);
  // allHeaders() also reads the headers Chromium adds, which headers() does
  // not, so the scan covers everything the page sent.
  const headers = await answer!.allHeaders();
  const leaked = Object.entries(headers).filter(([name, value]) => `${name}: ${value}`.includes('hunter2'));
  expect(leaked).toEqual([]);
  expect(answer!.postDataJSON()).toEqual({ id: 'secure-pinentry', secret: 'hunter2' });

  // A secret belongs in the body only: no request URL of this page carries it.
  expect(requests.filter((r) => r.url().includes('hunter2')).map((r) => r.url())).toEqual([]);
  expect(await page.content()).not.toContain('hunter2');
  expect(await page.evaluate(() => JSON.stringify({ ...sessionStorage, ...localStorage }))).not.toContain('hunter2');
});

promptElsewhereTest('closes when the terminal answers first', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  const dialog = passphrase(page);
  await expect(dialog).toBeVisible();
  // webfake answers it from the terminal 5.3 s after it starts, which leaves
  // the tab time to show the prompt first.
  await expect(dialog).toBeHidden({ timeout: 15_000 });
  await expect(page.locator('.toast').filter({ hasText: 'Answered in the terminal' })).toBeVisible();
});

confirmTest('confirms an SSH host key', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  const dialog = page.getByRole('dialog', { name: 'New Host' });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('OpenSSH has not seen this host before. Trust its key?');
  await expect(dialog.locator('input[type="password"]')).toHaveCount(0);

  await dialog.getByRole('button', { name: 'Confirm' }).click();
  await expect(dialog).toBeHidden();
});

// The sign-in dialog is the layer a prompt can open over. webfake publishes
// its prompt 300 ms after it starts, long before a tab can open a dialog, so
// the test keeps the prompt back (whichever frame carries it) and hands it
// over once the dialog owns the page.
promptTest('returns the focus to the sign-in dialog when a prompt closes over it', async ({ page, gateway }) => {
  let held: unknown[] | null = null;
  let last = 0;
  let send: ((message: string) => void) | null = null;
  await page.routeWebSocket(/\/api\/stream/, (ws) => {
    const server = ws.connectToServer();
    send = (message) => ws.send(message);
    ws.onMessage((message) => server.send(message));
    server.onMessage((message) => {
      const text = message.toString();
      // The snapshot is the one frame the page cannot do without, so it is
      // forwarded with the prompt taken out of it.
      try {
        const frame = JSON.parse(text) as {
          seq?: number;
          type?: string;
          payload?: { secure_prompts?: unknown[]; prompts?: unknown[] };
        };
        if (frame.type === 'snapshot' && frame.payload?.secure_prompts?.length) {
          held = frame.payload.secure_prompts;
          delete frame.payload.secure_prompts;
          ws.send(JSON.stringify(frame));
          return;
        }
        if (frame.type === 'secure_prompt' && frame.payload?.prompts?.length) {
          held = frame.payload.prompts;
          return;
        }
        if (typeof frame.seq === 'number') last = frame.seq;
      } catch {
        // A frame that is not JSON is forwarded untouched.
      }
      ws.send(text);
    });
  });

  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();
  await page.getByRole('button', { name: 'Model: Model One. Switch model' }).click();
  await page.getByRole('dialog', { name: 'Models' }).getByRole('button', { name: 'Sign in to another provider' }).click();
  const dialog = page.getByRole('dialog', { name: 'Sign in', exact: true });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel('Filter providers')).toBeFocused();

  // The prompt arrives now, over the open dialog, and the tab answers it. Its
  // seq is the last one the page has seen, which the reducer takes for a
  // replay and applies all the same.
  expect(held).not.toBeNull();
  send!(JSON.stringify({ seq: last, type: 'secure_prompt', payload: { prompts: held } }));
  const prompt = page.getByRole('dialog', { name: 'GPG passphrase' });
  await expect(prompt).toBeVisible();
  await prompt.getByLabel('Passphrase', { exact: true }).fill('hunter2');
  await page.keyboard.press('Enter');
  await expect(prompt).toBeHidden();

  // The dialog stays open, so it takes the focus back from the body, on the
  // control that had it before the prompt.
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel('Filter providers')).toBeFocused();
});

touchTest('shows a touch notice without a dialog', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.toast').filter({ hasText: 'Tap your security key to continue.' })).toBeVisible();

  // A touch prompt is a notice the terminal answers itself: it never takes
  // the interrupt layer, so the deck stays usable.
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.locator('.interrupt')).toHaveCount(0);
  await expect(page.locator('.holodeck__deck')).toHaveJSProperty('inert', false);
  // webfake does not answer the touch prompt, so the terminal's own answer
  // must not be announced over the next two seconds either.
  const seen = await sampleToasts(page, 2000);
  expect(seen.filter((text) => text.includes('Answered in the terminal'))).toEqual([]);
});

// A panel an earlier test could not open under a prompt: the prompt is held
// back six seconds, so the switcher opens first and the prompt closes over
// it. The panel is the layer that stays open, so it takes its focus back.
promptLateTest('returns the focus to the panel when a prompt closes over it', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();
  await page.getByRole('button', { name: 'Session: Holodeck test. Switch session' }).click();
  const sessions = page.getByRole('dialog', { name: 'Sessions' });
  await expect(sessions).toBeVisible();
  await expect(sessions.getByLabel('Filter sessions', { exact: true })).toBeFocused();
  // ArrowDown only reaches a row once the list has answered, so the first
  // one is waited for explicitly.
  await expect(sessions.locator('.sessions__title').first()).toBeVisible();

  // ArrowDown moves the focus from the filter to the first row, which is the
  // control the prompt interrupts.
  await page.keyboard.press('ArrowDown');
  const row = await page.evaluate(() => document.activeElement?.id ?? '');
  expect(row).toMatch(/^session-open-/);

  const prompt = page.getByRole('dialog', { name: 'GPG passphrase' });
  await expect(prompt).toBeVisible({ timeout: 20_000 });
  // The page behind a prompt is inert, the open panel included.
  await expect(page.locator('.sessions-layer')).toHaveJSProperty('inert', true);

  await prompt.getByLabel('Passphrase', { exact: true }).fill('hunter2');
  await page.keyboard.press('Enter');
  await expect(prompt).toBeHidden();

  // The panel stays open, so it takes the focus back from the body, on the
  // control that had it before the prompt.
  await expect(sessions).toBeVisible();
  await expect(page.locator(`#${row}`)).toBeFocused();
});
