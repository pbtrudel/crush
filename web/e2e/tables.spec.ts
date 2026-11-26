import type { Page } from '@playwright/test';
import { expect, freshTest } from './fixture';

// No seed carries a markdown table, and seeding one would move every
// baseline taken against the seeded log. A prompt is enough: the scripted
// agent echoes it back, and the reply renders as markdown, so the log holds
// a real table either way. The cells hold unbreakable words, because a
// hyphen would give the line breaker a legal place to split the word.
const TABLE = [
  'Audit the database for leftovers.',
  '',
  '| Check | Finding | Owner | Status |',
  '| --- | --- | --- | --- |',
  '| redundancyaudit | duplicateindexeseverywhere | platformobservability | awaitingsecurityreview |',
  '| connectionpool | idleconnectionsneverclosed | databasereplication | mitigationunderway |',
].join('\n');

const linked = (page: Page) =>
  expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });

freshTest('keeps a wide table readable and scrolling in the log', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await linked(page);
  await page.getByLabel('Transmit to crush').fill(TABLE);
  await page.keyboard.press('Enter');
  await expect(page.getByRole('log')).toContainText('idleconnectionsneverclosed');

  // A cell may not be narrower than its longest word, and a table wider
  // than the message scrolls inside it instead of breaking the words.
  const tables = await page.getByRole('log').evaluate((log) => {
    const longestWord = (cell: Element) => {
      let width = 0;
      for (const word of (cell.textContent ?? '').trim().split(/\s+/)) {
        const probe = document.createElement('span');
        probe.style.cssText = 'position: absolute; visibility: hidden; white-space: nowrap;';
        probe.textContent = word;
        cell.appendChild(probe);
        width = Math.max(width, probe.getBoundingClientRect().width);
        probe.remove();
      }
      return width;
    };
    return [...log.querySelectorAll('.md table')].map((table) => ({
      clientWidth: table.clientWidth,
      scrollWidth: table.scrollWidth,
      cells: [...table.querySelectorAll('th, td')].map((cell) => ({
        text: cell.textContent ?? '',
        width: cell.getBoundingClientRect().width,
        longestWord: longestWord(cell),
      })),
    }));
  });

  expect(tables.length, 'the log holds the echoed table').toBeGreaterThan(0);
  for (const table of tables) {
    for (const cell of table.cells) {
      expect(cell.width, `cell "${cell.text}" keeps its longest word`)
        .toBeGreaterThanOrEqual(cell.longestWord);
    }
    expect(table.scrollWidth, 'the table scrolls sideways').toBeGreaterThan(table.clientWidth);
  }
});
