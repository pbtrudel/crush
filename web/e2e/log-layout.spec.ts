import type { Page } from '@playwright/test';
import { expect, longPathTest } from './fixture';

const linked = (page: Page) =>
  expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });

longPathTest('keeps a long tool path inside the log panel', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);
  const log = page.getByRole('log');
  await expect(log).toContainText('Checking the audit report before the next step.');
  await expect(log.locator('.tool__summary')).toContainText('final-database-redundancy-report');
  // The card's output holds one unbroken line far wider than the log. An
  // expanded card is where the widest content shows.
  await log.locator('.tool summary').click();
  await expect(log.locator('.tool__body')).toBeVisible();

  // At 1440 the log is the narrow column, so a nowrap path wider than it
  // must ellipsize, and a long output line must scroll in its own box,
  // rather than widening the message and the whole log.
  const geometry = await log.evaluate((el) => {
    const box = el.getBoundingClientRect();
    const paragraph = el.querySelector('.md p')!.getBoundingClientRect();
    const summary = el.querySelector('.tool__summary') as HTMLElement;
    const output = [...el.querySelectorAll<HTMLElement>('.tool__body pre')].at(-1)!;
    return {
      width: box.width,
      clientWidth: el.clientWidth,
      scrollWidth: el.scrollWidth,
      right: box.right,
      paragraphRight: paragraph.right,
      summaryClientWidth: summary.clientWidth,
      summaryScrollWidth: summary.scrollWidth,
      outputClientWidth: output.clientWidth,
      outputScrollWidth: output.scrollWidth,
    };
  });

  expect(geometry.width, 'the log is the narrow column at 1440').toBeLessThan(600);
  expect(geometry.scrollWidth, 'the log does not scroll sideways')
    .toBeLessThanOrEqual(geometry.clientWidth);
  expect(geometry.paragraphRight - geometry.right, 'the paragraph ends inside the log')
    .toBeLessThanOrEqual(0.5);
  expect(geometry.summaryScrollWidth, 'the path is ellipsized')
    .toBeGreaterThan(geometry.summaryClientWidth);
  expect(geometry.outputScrollWidth, 'the output line scrolls in its own box')
    .toBeGreaterThan(geometry.outputClientWidth);
});
