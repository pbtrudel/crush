import { axeScan, expect, m3FreshTest as freshTest, m3Test as test, m4bTest } from './fixture';

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
