import {
  axeScan, expect, LONG_MODEL_NAME, m3FreshTest as freshTest, m3Test as test, m4bLongModelTest, m4bTest,
} from './fixture';

// Axe keeps the software renderer busy well past the default 30 seconds.
test.setTimeout(120_000);
freshTest.setTimeout(120_000);

// Opening the palette needs only the seeded catalog, so the shared worker
// gateway serves it. Anything that sends, switches or renames gets its own.
test('opens with a slash in an empty deck', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  const deck = page.getByLabel('Transmit to crush');
  await deck.focus();
  await page.keyboard.press('/');

  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await expect(palette).toBeVisible();
  await expect(palette.getByRole('combobox', { name: 'Command palette' })).toBeFocused();
  await expect(deck).toHaveValue('');
  await expect(palette.getByRole('option', { name: /user:fix/ })).toBeVisible();
  await axeScan(page);

  await page.keyboard.press('Escape');
  await expect(palette).toBeHidden();
  await expect(deck).toHaveValue('');
});

freshTest('opens with Ctrl+K from the prompt and runs a command with an argument', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await expect(palette).toBeVisible();

  const search = palette.getByRole('combobox', { name: 'Command palette' });
  await search.fill('fix');
  await expect(palette.getByRole('option', { name: /user:fix/ })).toBeVisible();
  await page.keyboard.press('Enter');

  const form = palette.getByRole('form', { name: 'Arguments for user:fix' });
  await expect(form).toBeVisible();
  await form.getByLabel('File (required)').fill('a.go');
  await form.getByRole('button', { name: 'Run' }).click();

  await expect(palette).toBeHidden();
  await expect(page.getByRole('log')).toContainText('Fix a.go and make sure the tests still pass.');
});

freshTest('compacts the session from the palette', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  const compacted = page.waitForRequest((r) => r.method() === 'POST' && /\/summarize$/.test(r.url()));
  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('combobox', { name: 'Command palette' }).fill('compact');
  // Enter only runs the item once the list has answered, so wait for it.
  await expect(palette.getByRole('option', { name: /Compact/ })).toBeVisible();
  await page.keyboard.press('Enter');
  await compacted;

  await expect(palette).toBeHidden();
  // Every surface keeps an empty alert in the DOM, so what must not appear
  // is an alert that says something.
  await expect(page.getByRole('alert').filter({ hasText: /\S/ })).toHaveCount(0);
});

freshTest('switches the model from the HUD', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByRole('button', { name: 'Model: Model One. Switch model' }).click();
  const dialog = page.getByRole('dialog', { name: 'Models' });
  const choices = dialog.getByRole('listbox', { name: 'Model choices' });
  const two = choices.getByRole('option', { name: /Model Two/ });
  await expect(two).toBeVisible();
  await axeScan(page);

  await two.click();
  await expect(dialog).toBeHidden();
  // The HUD follows the model_changed frame, never the request.
  await expect(page.getByRole('button', { name: 'Model: Model Two. Switch model' })).toBeVisible();
});

freshTest('switches, renames and refuses to delete the current session from the HUD', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByRole('button', { name: 'Session: Holodeck test. Switch session' }).click();
  const dialog = page.getByRole('dialog', { name: 'Sessions' });
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('.sessions__row[data-current="true"]')).toContainText('Holodeck test');
  await expect(dialog.getByRole('button', { name: 'Delete Holodeck test' })).toBeDisabled();
  await expect(dialog.getByText('The terminal is showing this session')).toBeVisible();
  await axeScan(page);

  await dialog.getByRole('button', { name: 'Open Tokenizer port in the terminal' }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole('button', { name: 'Session: Tokenizer port. Switch session' })).toBeVisible();

  await page.getByRole('button', { name: 'Session: Tokenizer port. Switch session' }).click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Delete Tokenizer port' })).toBeDisabled();
  await expect(dialog.getByRole('button', { name: 'Delete Holodeck test' })).toBeEnabled();

  await dialog.getByRole('button', { name: 'Rename Tokenizer port' }).click();
  const rename = dialog.getByRole('textbox', { name: 'Rename Tokenizer port' });
  await rename.fill('Tokenizer port v2');
  await rename.press('Enter');
  await expect(dialog.getByRole('button', { name: 'Open Tokenizer port v2 in the terminal' })).toBeVisible();

  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(page.getByRole('button', { name: 'Session: Tokenizer port v2. Switch session' })).toBeVisible();
});

freshTest('runs a shell command and streams its output', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  const deck = page.getByLabel('Transmit to crush');
  await deck.fill('!ls');
  await page.keyboard.press('Enter');

  const card = page.locator('.shell-card');
  await expect(card.locator('.shell-card__command')).toHaveText('$ ls');
  await expect(card.locator('.shell-card__output')).toContainText('building the parser');
  await expect(card.locator('.shell-card__output')).toContainText('linting the parser');
  await expect(card.locator('.shell-card__output')).toContainText('3 tests passed');
  await expect(card.locator('.shell-card__exit')).toHaveText('Exit 0');
  await expect(deck).toHaveValue('');
  await axeScan(page);
});

freshTest('asks a side question', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('combobox', { name: 'Command palette' }).fill('side question');
  // Enter only runs the item once the list has answered, so wait for it.
  await expect(palette.getByRole('option', { name: /Ask side question/ })).toBeVisible();
  await page.keyboard.press('Enter');
  const form = palette.getByRole('form', { name: 'Arguments for Ask side question' });
  await form.getByLabel('Question (required)').fill('How does the parser work?');
  await form.getByRole('button', { name: 'Run' }).click();

  const card = page.getByLabel('Side answer');
  await expect(card).toContainText('Side answer, not added to the conversation');
  await expect(card).toContainText('How does the parser work?');
  await expect(card).toContainText('The parser uses a recursive descent.');
  await expect(page.getByRole('log')).not.toContainText('The parser uses a recursive descent.');
  await axeScan(page);
});

freshTest('adds a note', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('combobox', { name: 'Command palette' }).fill('add note');
  // Enter only runs the item once the list has answered, so wait for it.
  await expect(palette.getByRole('option', { name: /Add note/ })).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(palette).toBeHidden();

  const deck = page.getByLabel('Transmit to crush');
  await deck.fill('Remember the tokenizer.');
  await page.keyboard.press('Enter');
  await expect(page.getByText('Note kept for the next prompt')).toBeVisible();
  await expect(deck).toHaveValue('');
});

// The M4b palette items. The shared m4b gateway carries the seeded channel
// server, the globally disabled skill and the terminal automation replies.
m4bTest.setTimeout(120_000);

m4bTest('sets a goal with its argument', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  const sent = page.waitForRequest((r) => r.method() === 'POST' && /\/automation$/.test(r.url()));
  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('combobox', { name: 'Command palette' }).fill('set goal');
  // Enter only runs the item once the list has answered, so wait for it.
  await expect(palette.getByRole('option', { name: /Set goal/ })).toBeVisible();
  await page.keyboard.press('Enter');

  const form = palette.getByRole('form', { name: 'Arguments for Set goal' });
  await expect(form).toBeVisible();
  await form.getByLabel('Objective (required)').fill('Ship the parser');
  await form.getByRole('button', { name: 'Run' }).click();

  const request = await sent;
  expect(request.postDataJSON()).toEqual({ name: 'goal', args: 'Ship the parser' });
  await expect(palette).toBeHidden();
  await expect(page.locator('.toast').filter({ hasText: 'Goal set: Ship the parser' })).toBeVisible();
});

m4bTest('exports the session as Markdown', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('combobox', { name: 'Command palette' }).fill('export session');
  // Enter only runs the item once the list has answered, so wait for it.
  await expect(palette.getByRole('option', { name: /Export session as Markdown/ })).toBeVisible();
  await page.keyboard.press('Enter');

  await expect(palette).toBeHidden();
  await expect(page.locator('.toast').filter({ hasText: /Session exported to .*crush-session-s1\.md/ }))
    .toBeVisible();
});

m4bTest('opens Channels in the systems panel', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('combobox', { name: 'Command palette' }).fill('channels');
  // Enter only runs the item once the list has answered, so wait for it.
  await expect(palette.getByRole('option', { name: /Channels/ })).toBeVisible();
  await page.keyboard.press('Enter');

  const panel = page.getByRole('dialog', { name: 'Systems' });
  await expect(panel).toBeVisible();
  const discord = panel.locator('.systems__card[data-server="discord"]');
  await expect(discord).toBeVisible();
  await expect(discord).toContainText('Channel');
  await expect(panel.locator('.systems__card[data-server="github"]')).toHaveCount(0);
  await expect(panel.getByRole('button', { name: 'Channels' })).toHaveAttribute('aria-pressed', 'true');
  await axeScan(page);
});

m4bTest('keeps the model and branch on one line', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  const model = page.locator('p.hud__meta .hud__model');
  const branch = page.locator('p.hud__meta .hud__branch');
  await expect(branch).toBeVisible();
  // A narrowed strip truncates both names, so both carry their full text.
  await expect(model).toHaveAttribute('title', 'Model One');
  await expect(branch).toHaveAttribute('title', 'main');
  const [m, b] = await Promise.all([model.boundingBox(), branch.boundingBox()]);
  expect(m).not.toBeNull();
  expect(b).not.toBeNull();
  // The branch shares the model line even with the m4b seed's three MCP
  // dots: never the orphan second line that made the HUD grow.
  const modelCenter = m!.y + m!.height / 2;
  const branchCenter = b!.y + b!.height / 2;
  expect(Math.abs(branchCenter - modelCenter)).toBeLessThanOrEqual(2);
  expect(b!.x).toBeGreaterThanOrEqual(m!.x + m!.width);
  // Short names fit whole at 1440, and the page does not overflow sideways.
  const short = await page.evaluate(() => {
    const name = document.querySelector('.hud__model-name') as HTMLElement;
    const label = document.querySelector('.hud__branch') as HTMLElement;
    const right = (sel: string) => document.querySelector(sel)!.getBoundingClientRect().right;
    return {
      model: name.scrollWidth === name.clientWidth,
      branch: label.scrollWidth === label.clientWidth,
      overflow: document.documentElement.scrollWidth - window.innerWidth,
      controls: right('.hud__controls'),
      deck: right('.holodeck__deck'),
      viewport: window.innerWidth,
    };
  });
  expect(short.model).toBe(true);
  expect(short.branch).toBe(true);
  expect(short.overflow).toBeLessThanOrEqual(0);
  expect(short.controls).toBeLessThanOrEqual(short.viewport);
  expect(short.deck).toBeLessThanOrEqual(short.viewport);
});

m4bLongModelTest('clips a long model name before the context gauge', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  const model = page.locator('p.hud__meta .hud__model');
  const name = page.locator('.hud__model-name');
  await expect(name).toHaveText(LONG_MODEL_NAME);
  await expect(model).toHaveAttribute('title', LONG_MODEL_NAME);
  for (const width of [1280, 1366, 1440]) {
    await page.setViewportSize({ width, height: 860 });
    await expect.poll(() => page.evaluate(() => window.innerWidth)).toBe(width);
    const geometry = await page.evaluate(() => {
      const n = document.querySelector('.hud__model-name') as HTMLElement;
      const b = document.querySelector('.hud__branch') as HTMLElement;
      const m = document.querySelector('.hud__model') as HTMLElement;
      const meta = document.querySelector('.hud__meta') as HTMLElement;
      const gauge = document.querySelector('.gauge') as HTMLElement;
      const center = (el: Element) => {
        const r = el.getBoundingClientRect();
        return r.y + r.height / 2;
      };
      return {
        clipped: n.scrollWidth > n.clientWidth,
        metaRight: meta.getBoundingClientRect().right,
        gaugeLeft: gauge.getBoundingClientRect().left,
        sameLine: Math.abs(center(b) - center(m)) <= 2,
        overflow: document.documentElement.scrollWidth - window.innerWidth,
        hudRight: document.querySelector('.hud__controls')!.getBoundingClientRect().right,
      };
    });
    expect(geometry.metaRight).toBeLessThanOrEqual(geometry.gaugeLeft);
    expect(geometry.clipped).toBe(true);
    expect(geometry.sameLine).toBe(true);
    expect(geometry.overflow).toBeLessThanOrEqual(0);
    expect(geometry.hudRight).toBeLessThanOrEqual(width);
  }
});
