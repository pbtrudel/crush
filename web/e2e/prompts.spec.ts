import { setTimeout as sleep } from 'node:timers/promises';
import type { Page, Request } from '@playwright/test';
import {
  confirmTest, expect, promptElsewhereTest, promptTest, touchTest,
} from './fixture';

// Each test launches its own webfake, and a cold Go build counts towards the
// test's own timeout.
promptTest.setTimeout(60_000);
promptElsewhereTest.setTimeout(60_000);
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
// the test holds the frame and hands it over once the dialog owns the page.
promptTest('returns the focus to the sign-in dialog when a prompt closes over it', async ({ page, gateway }) => {
  const held: string[] = [];
  let send: ((message: string) => void) | null = null;
  await page.routeWebSocket(/\/api\/stream/, (ws) => {
    const server = ws.connectToServer();
    send = (message) => ws.send(message);
    ws.onMessage((message) => server.send(message));
    server.onMessage((message) => {
      const text = message.toString();
      // The snapshot is the one frame the page cannot do without, so only the
      // prompt it carries is dropped.
      try {
        const frame = JSON.parse(text) as { type?: string; payload?: Record<string, unknown> };
        if (frame.type === 'snapshot' && frame.payload) {
          delete frame.payload.secure_prompts;
          ws.send(JSON.stringify(frame));
          return;
        }
      } catch {
        // A frame that is not JSON is forwarded untouched.
      }
      if (text.includes('secure-pinentry')) {
        held.push(text);
        return;
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

  // The prompt arrives now, over the open dialog, and the tab answers it.
  expect(held).toHaveLength(1);
  for (const message of held.splice(0)) send!(message);
  const prompt = page.getByRole('dialog', { name: 'GPG passphrase' });
  await expect(prompt).toBeVisible();
  await prompt.getByLabel('Passphrase', { exact: true }).fill('hunter2');
  await page.keyboard.press('Enter');
  await expect(prompt).toBeHidden();

  // The dialog stays open, so it must take the focus back from the body.
  await expect(dialog).toBeVisible();
  await expect(dialog.locator(':focus')).toHaveCount(1);
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
