import { expect, freshTest, test } from './fixture';
import type { Page } from '@playwright/test';

// A spec that launches its own webfake pays for a `go build` and a server
// start inside the fixture, so give the setup more than the default.
test.setTimeout(120_000);

// The HUD's connection strip is one polite live region: the short word sits
// in an aria-hidden span and a visually hidden sentence repeats it, so the
// filter reads the text content the way smoke.spec.ts does.
async function linked(page: Page) {
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();
}

test('selects an agent with the keyboard and reads what it is doing', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);

  const list = page.getByRole('listbox', { name: 'Agents' });
  await expect(list.getByRole('option')).toHaveCount(3);
  await list.getByRole('option', { name: /Coder/ }).focus();
  await page.keyboard.press('ArrowDown');
  await expect(list.getByRole('option', { name: /Map the parser/ })).toBeFocused();
  await page.keyboard.press('Enter');

  const inspector = page.getByRole('region', { name: /Map the parser/ });
  await expect(inspector).toBeVisible();
  await expect(inspector).toContainText('grep');
  await expect(inspector.getByRole('log')).toContainText('Scanning the parser package');

  // Escape closes the inspector and hands the focus back to the option it
  // was opened from, so the keyboard keeps its place in the list.
  await page.keyboard.press('Escape');
  await expect(inspector).toBeHidden();
  await expect(list.getByRole('option', { name: /Map the parser/ })).toBeFocused();
});

test('selects an agent node with the pointer and messages it', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);
  // The boot overlay fades over the scene; a raw pointer click while it is
  // up lands on the overlay instead of the tag.
  await expect(page.locator('.boot')).toBeHidden();

  const node = page.locator('[data-agent-node="team-m-1"]');
  await expect(node).toHaveAttribute('data-visible', 'true', { timeout: 15_000 });
  // A tag tracks its node's projection, so it is never perfectly still:
  // click it where it is instead of waiting for a stable box.
  const box = (await node.boundingBox())!;
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);

  const inspector = page.getByRole('region', { name: 'alice' });
  await expect(inspector).toBeVisible();
  await expect(inspector).toContainText('Port the parser');
  await inspector.getByLabel('Message alice').fill('rebase first');
  await inspector.getByRole('button', { name: 'Send' }).click();
  await expect(inspector.getByRole('status')).toContainText('Sent to alice');
});

// Low power is a gateway setting now, so this test writes it and takes its
// own webfake rather than changing the worker's shared one.
freshTest('selects a 2D node in low power mode', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);
  await page.getByRole('button', { name: 'Display', exact: true }).click();
  const settings = page.getByRole('dialog', { name: 'Display settings' });
  await settings.getByRole('checkbox', { name: 'Low power (2D constellation)' }).check();
  await page.keyboard.press('Escape');
  await expect(settings).toBeHidden();
  await expect(page.locator('svg.constellation-svg')).toBeVisible();
  await page.locator('svg [data-agent-node="sub-1"]').click();
  await expect(page.getByRole('region', { name: /Map the parser/ })).toBeVisible();
});

// The subagent's edit adds a parser.go version, so this test keeps its own
// webfake instead of changing the versions the smoke specs assert on.
freshTest('delegated work streams into the inspector and the drawer', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);

  await page.getByRole('listbox', { name: 'Agents' }).getByRole('option', { name: /Map the parser/ }).click();
  await page.getByLabel('Transmit to crush').fill('delegate the tokenizer');
  await page.keyboard.press('Enter');

  const inspector = page.getByRole('region', { name: /Map the parser/ });
  await expect(inspector.getByRole('log')).toContainText('Rewriting the tokenizer loop.');
  await expect(inspector).toContainText('edit');

  await page.getByRole('button', { name: /^Files/ }).click();
  await page.getByRole('complementary', { name: /Edited files/ }).getByRole('button', { name: /parser\.go/ }).click();
  await expect(page.locator('.cm-mergeView')).toContainText('func tokenize');
});

// The M2 fix hides a tag whose anchor lies under the open inspector, but with
// motion off nothing republishes the projection, so opening or closing the
// panel has to re-run the placement on its own.
test('re-evaluates tags when the inspector opens and closes with motion off', async ({ page, gateway }) => {
  await page.addInitScript(() => localStorage.setItem('crush-web-motion', 'off'));
  // The seeded scene is deterministic. Find a viewport where a visible tag
  // lies under the inspector once it opens; fail if none does, so the test
  // can never pass vacuously.
  const sizes = [{ width: 1440, height: 860 }, { width: 1280, height: 800 }, { width: 1100, height: 800 }];
  let found: string | null = null;
  for (const size of sizes) {
    await page.setViewportSize(size);
    await page.goto(gateway.launch);
    await linked(page);
    await expect(page.locator('.boot')).toBeHidden();
    // Read the tags before the inspector opens: only a tag that was visible
    // then can prove the close restored it.
    await page.waitForFunction(
      () => document.querySelectorAll('[data-agent-node][data-visible="true"]').length > 0,
      undefined,
      { timeout: 15_000 },
    );
    const tags = await page.evaluate(() =>
      Array.from(document.querySelectorAll<HTMLElement>('[data-agent-node]'))
        .filter((tag) => tag.dataset.visible === 'true')
        .map((tag) => {
          const r = tag.getBoundingClientRect();
          return { id: tag.dataset.agentNode ?? '', left: r.left, top: r.top, right: r.right, bottom: r.bottom };
        }),
    );
    await page.getByRole('listbox', { name: 'Agents' }).getByRole('option', { name: /Map the parser/ }).click();
    const panel = page.locator('.panel--inspector:not([hidden])');
    await expect(panel).toBeVisible();
    const pbox = (await panel.boundingBox())!;
    found = await page.evaluate(({ p, tags }) => {
      const hits = (
        a: { left: number; top: number; right: number; bottom: number },
        b: { left: number; top: number; right: number; bottom: number },
      ) => a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
      const list = document.querySelector('.agent-list')?.getBoundingClientRect();
      for (const tag of tags) {
        // A tag the agent list covers stays hidden either way, so it could
        // not prove the inspector restored it.
        if (!hits(tag, p) || (list && hits(tag, list))) continue;
        return tag.id;
      }
      return null;
    }, { p: { left: pbox.x, top: pbox.y, right: pbox.x + pbox.width, bottom: pbox.y + pbox.height }, tags });
    if (found) break;
    await page.keyboard.press('Escape');
  }
  expect(found, 'no seeded tag lies under the inspector at any tested size').not.toBeNull();
  const tag = page.locator(`[data-agent-node="${found}"]`);
  await expect(tag).toHaveAttribute('data-visible', 'false');
  await page.keyboard.press('Escape');
  await expect(page.locator('.panel--inspector')).toBeHidden();
  // Nothing moved, so only the close itself can have re-run the placement.
  await expect(tag).toHaveAttribute('data-visible', 'true');
});
