import {
  axeScan, expect, m3Test as test, planTest, startingTest,
} from './fixture';

// Axe keeps the software renderer busy well past the default 30 seconds.
test.setTimeout(120_000);
startingTest.setTimeout(120_000);
planTest.setTimeout(120_000);

const STARTING_TEXT = 'Crush is starting the agent. Try again in a moment.';

// The systems panel lists what the seed carries, so the shared worker
// gateway serves the read-only tests.
test('opens the systems panel from the HUD tiles and refreshes a server', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByRole('button', { name: /^MCP servers:/ }).click();
  const panel = page.getByRole('dialog', { name: 'Systems' });
  await expect(panel).toBeVisible();
  await expect(panel.getByRole('heading', { name: 'Language servers' })).toBeVisible();
  await expect(panel.getByRole('heading', { name: 'MCP servers' })).toBeVisible();

  const github = panel.locator('.systems__card[data-server="github"]');
  await expect(github).toContainText('running');
  await expect(github).toContainText('12 tools');
  await axeScan(page);

  const refresh = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/mcp/github/refresh'));
  await github.getByRole('button', { name: 'Refresh' }).click();
  await refresh;
  await expect(github).toContainText('running');
});

test('shows the notes server error with its word and glyph', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByRole('button', { name: /^MCP servers:/ }).click();
  const panel = page.getByRole('dialog', { name: 'Systems' });
  const notes = panel.locator('.systems__card[data-server="notes"]');
  await expect(notes).toContainText('needs sign-in');
  await expect(notes.locator('.hex')).toHaveAttribute('data-tone', 'error');
  await expect(notes.locator('.hex__bang')).toHaveText('!');
  await expect(notes.getByRole('button', { name: 'Authenticate' })).toBeVisible();
});

test('opens the MCP auth URL in a new tab', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByRole('button', { name: /^MCP servers:/ }).click();
  const panel = page.getByRole('dialog', { name: 'Systems' });
  const notes = panel.locator('.systems__card[data-server="notes"]');
  await expect(notes).toBeVisible();

  const popup = page.context().waitForEvent('page');
  // The seeded address never resolves, so the page itself lands on the
  // browser's error screen; the request the app opened is the target.
  const navigation = page.context().waitForEvent(
    'request',
    (r) => r.isNavigationRequest() && r.url().startsWith('https://auth.example/notes'),
  );
  await notes.getByRole('button', { name: 'Authenticate' }).click();
  const [opened, request] = await Promise.all([popup, navigation]);
  expect(request.url()).toBe('https://auth.example/notes');
  await opened.close();
  await expect(notes.getByRole('link', { name: 'Open the sign-in page' })).toHaveAttribute(
    'href', 'https://auth.example/notes',
  );
});

startingTest('shows a starting state instead of an error', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.getByLabel('Transmit to crush').focus();
  await page.keyboard.press('Control+K');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('combobox', { name: 'Command palette' }).fill('compact');
  await page.keyboard.press('Enter');

  const starting = palette.getByRole('status').filter({ hasText: STARTING_TEXT });
  await expect(starting).toBeVisible();
  await expect(starting).toHaveAttribute('data-tone', 'starting');
  await expect(palette).toBeVisible();
  await expect(page.getByRole('alert').filter({ hasText: /\S/ })).toHaveCount(0);
  await axeScan(page);
});

planTest('confirms a ready plan from the browser', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  const card = page.locator('.plan-handoff');
  await expect(card.getByRole('heading', { name: 'The plan is ready' })).toBeVisible();
  await expect(card).toContainText('Ready to start coding?');
  await axeScan(page);

  const handoff = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/plan/handoff'));
  await card.getByRole('button', { name: 'Start coding' }).click();
  const sent = await handoff;
  expect(sent.postDataJSON()).toMatchObject({ action: 'code' });

  // The terminal clears the pending plan, and the card leaves with it.
  await expect(card).toHaveCount(0);
});
