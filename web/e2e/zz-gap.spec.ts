import { expect, m4bFullServersTest, m4bTest } from './fixture';

m4bTest.setTimeout(120_000);
m4bFullServersTest.setTimeout(120_000);

async function probe(where: string, page: any) {
  for (const width of [1280, 1366, 1440]) {
    await page.setViewportSize({ width, height: 860 });
    await expect.poll(() => page.evaluate(() => window.innerWidth)).toBe(width);
    await page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))));
    const info = await page.evaluate(() => {
      const rect = (el: Element | null) => {
        if (!el) return null;
        const r = el.getBoundingClientRect();
        return { x: Math.round(r.x * 10) / 10, right: Math.round(r.right * 10) / 10, w: Math.round(r.width * 10) / 10 };
      };
      const servers = document.querySelector('.hud__servers')!;
      const controls = document.querySelector('.hud__controls')!;
      const hexes = Array.from(document.querySelectorAll('.hud__servers .hex'));
      const last = hexes.at(-1);
      const mcpHexes = document.querySelectorAll('[data-kind="mcp"] .hex').length;
      return {
        servers: rect(servers), controls: rect(controls),
        gap: Math.round((controls.getBoundingClientRect().left - servers.getBoundingClientRect().right) * 10) / 10,
        lastHex: rect(last ?? null),
        lastHexGap: last ? Math.round((controls.getBoundingClientRect().left - last.getBoundingClientRect().right) * 10) / 10 : null,
        mcpHexes,
        cols: getComputedStyle(document.querySelector('.hud')!).gridTemplateColumns,
        gaps: getComputedStyle(document.querySelector('.hud')!).gap,
      };
    });
    console.log('GAP', where, width, JSON.stringify(info));
  }
}

m4bTest('probe gap m4b', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });
  await probe('m4b', page);
});

m4bFullServersTest('probe gap full', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible({ timeout: 30_000 });
  await probe('full', page);
});
