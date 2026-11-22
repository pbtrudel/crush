import { test, expect } from './fixture';

test('links, shows the session and streams a reply', async ({ page, gateway }) => {
  // A violation means the CSP blocks the app itself. Collect every report
  // so the assertions below fail with the directive that fired.
  await page.addInitScript(() => {
    const violations: string[] = [];
    Object.assign(window, { __cspViolations: violations });
    window.addEventListener('securitypolicyviolation', (e) => {
      violations.push(`${e.violatedDirective} ${e.blockedURI}`);
    });
  });
  await page.goto(gateway.launch);
  await expect(page).toHaveURL(gateway.origin + '/');
  await expect(page.getByRole('heading', { name: 'Holodeck test' })).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();
  await page.getByRole('button', { name: 'Plan', exact: true }).click();
  await expect(page.locator('.deck__plan-badge')).toBeVisible();
  await page.getByRole('button', { name: 'Code', exact: true }).click();
  await expect(page.locator('.deck__plan-badge')).toBeHidden();
  await page.getByLabel('Transmit to crush').fill('hello');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('log')).toContainText('Signal received. hello');
  expect(await page.evaluate(() => (window as unknown as { __cspViolations: string[] }).__cspViolations)).toEqual([]);
});

test('permission round trip with keyboard', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await page.getByLabel('Transmit to crush').fill('run permission');
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toContainText('bash');
  await expect(page.getByRole('button', { name: /Allow once/ })).toBeFocused();
  await page.keyboard.press('a');
  await expect(dialog).toBeHidden();
  await expect(page.getByRole('log')).toContainText('Permission granted, continuing.');
});

test('question form', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await page.getByLabel('Transmit to crush').fill('ask question');
  await page.keyboard.press('Enter');
  await page.getByLabel('Magenta').check();
  await page.getByRole('button', { name: 'Send answers' }).click();
  await expect(page.getByRole('log')).toContainText('You chose magenta.');
});

test('empty prompt shows an inline error', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await page.getByRole('button', { name: 'Transmit' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Type a task' })).toBeVisible();
});

test('reduced motion and low power', async ({ page, gateway }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(gateway.launch);
  await expect(page.locator('.holodeck')).toHaveAttribute('data-motion', 'reduced');
  await page.getByText('Display').click();
  await page.getByLabel('Low power (2D constellation)').check();
  await expect(page.locator('svg.constellation-svg')).toBeVisible();
  await expect(page.getByRole('list', { name: 'Agents' })).toContainText('Coder');
});

test('cinematic mode works with motion off', async ({ page, gateway }) => {
  // The Display menu writes this key; seeding it is the same preference
  // without a click through the menu.
  await page.addInitScript(() => localStorage.setItem('crush-web-motion', 'off'));
  await page.goto(gateway.launch);
  await expect(page.locator('.holodeck')).toHaveAttribute('data-motion', 'off');
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.keyboard.press('Alt+KeyC');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'true');
  // Off motion jumps instead of flying, and the panel cross-fade still has
  // to finish: a scene that only renders on the entry frame strands it part
  // way through the fade.
  await expect(page.locator('.cine-view')).toHaveCSS('opacity', '1');

  await page.locator('.panel--log').click();
  await page.keyboard.press('Escape');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'false');
});

test('a tab without the token is not linked', async ({ browser, gateway }) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(gateway.origin + '/');
  await expect(page.getByRole('heading', { name: /not linked/ })).toBeVisible();
  await ctx.close();
});

test('Alt+C enters and Esc leaves cinematic mode', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();
  const prompt = page.getByLabel('Transmit to crush');
  await prompt.focus();
  await page.keyboard.press('Alt+KeyC');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'true');
  await expect(prompt).toHaveValue('');
  await page.keyboard.press('Escape');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'true');
  // Click something that stays visible in cinematic mode: the HUD title is
  // hidden there, so use the log panel, which cinematic.css keeps (it holds
  // no input, so focus leaves the prompt).
  await page.locator('.panel--log').click();
  await page.keyboard.press('Escape');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'false');
});
