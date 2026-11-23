import { expect, test } from './fixture';

test('opens a diff from the file drawer without a CSP violation', async ({ page, gateway }) => {
  // A violation means CodeMirror injected a style element into the document
  // root instead of adopting its sheet into the shadow root.
  await page.addInitScript(() => {
    const violations: string[] = [];
    Object.assign(window, { __cspViolations: violations });
    window.addEventListener('securitypolicyviolation', (e) => {
      violations.push(`${e.violatedDirective} ${e.blockedURI}`);
    });
  });
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  const handle = page.getByRole('button', { name: /^Files/ });
  await expect(handle).toHaveAttribute('aria-expanded', 'false');
  await handle.click();
  await expect(handle).toHaveAttribute('aria-expanded', 'true');

  const drawer = page.getByRole('complementary', { name: /Edited files/ });
  await expect(drawer).toContainText('app.ts');
  await drawer.getByRole('button', { name: /parser\.go/ }).click();

  const diff = drawer.getByRole('region', { name: /parser\.go/ });
  await expect(diff.locator('.cm-mergeView')).toBeVisible();
  await expect(diff.locator('.cm-mergeView')).toContainText('func Parse(src string) []Token');

  await diff.getByRole('button', { name: 'Unified' }).click();
  await expect(diff.locator('.cm-mergeView')).toHaveCount(0);
  await expect(diff.locator('.cm-editor')).toHaveCount(1);
  // The deleted line is the oldest version's; switching the range away from
  // it removes it from the unified view.
  await expect(diff.locator('.cm-editor')).toContainText('func Parse() {}');
  await diff.getByLabel('From').selectOption('1');
  await expect(diff.locator('.cm-editor')).not.toContainText('func Parse() {}');

  expect(await page.evaluate(() => (window as unknown as { __cspViolations: string[] }).__cspViolations)).toEqual([]);
});
