import { expect, MANY_TOOL_CALLS, overflowTest as test } from './fixture';
import type { Page } from '@playwright/test';

// A real ~2055px window on a session holding hundreds of tool calls reported
// the whole page wider than the window: the deck, the Transmit button and
// everything right-aligned ran off the right edge, and no page scrollbar
// appeared because .holodeck clips its overflow. The chronoline draws the
// calls on one scrolling row, so its width must not size the shell. The HUD's
// own fit is a separate concern and is not asserted here.

interface Edge { left: number; right: number; width: number }

interface Geometry {
  innerWidth: number;
  scrollWidth: number;
  deck: Edge | null;
  stage: Edge | null;
  transmit: Edge | null;
  list: { scrollWidth: number; clientWidth: number } | null;
}

function geometry(page: Page): Promise<Geometry> {
  return page.evaluate(() => {
    const edge = (selector: string) => {
      const el = document.querySelector(selector);
      if (el === null) return null;
      const r = el.getBoundingClientRect();
      return { left: r.left, right: r.right, width: r.width };
    };
    const list = document.querySelector<HTMLElement>('.chronoline__list');
    return {
      innerWidth: window.innerWidth,
      scrollWidth: document.documentElement.scrollWidth,
      deck: edge('.holodeck__deck'),
      stage: edge('.stage'),
      transmit: edge('.deck button[type="submit"]'),
      list: list === null ? null : { scrollWidth: list.scrollWidth, clientWidth: list.clientWidth },
    };
  });
}

for (const viewport of [{ width: 1440, height: 860 }, { width: 1920, height: 1080 }]) {
  test(`the shell stays inside a ${viewport.width}px window with hundreds of calls`, async ({ page, gateway }) => {
    await page.setViewportSize(viewport);
    await page.goto(gateway.launch);
    // The seeded calls arrive with the first snapshot; waiting for the strip
    // to draw every one of them is the point where the layout is final.
    await expect(page.locator('.chronoline__event')).toHaveCount(MANY_TOOL_CALLS, { timeout: 15_000 });
    const g = await geometry(page);

    const inside = (label: string, box: Edge | null) => {
      expect(box, `${label} renders`).not.toBeNull();
      // Soft assertions so one run reports every box that leaves the window.
      expect.soft(box!.right, `${label} ends inside the window`).toBeLessThanOrEqual(g.innerWidth + 0.5);
    };
    inside('the deck', g.deck);
    inside('the stage', g.stage);
    inside('the Transmit button', g.transmit);

    expect(g.list, 'the chronoline list renders').not.toBeNull();
    expect.soft(g.list!.scrollWidth, 'the chronoline scrolls sideways instead of stretching the deck')
      .toBeGreaterThan(g.list!.clientWidth);
    expect.soft(g.scrollWidth, 'the document does not scroll sideways').toBeLessThanOrEqual(g.innerWidth);
  });
}
