import { test, expect } from './fixture';
import type { Page } from '@playwright/test';

interface Box { x: number; y: number; w: number; h: number }

const inside = (inner: Box, outer: Box, tolerance = 0.5) => (
  inner.x >= outer.x - tolerance
  && inner.y >= outer.y - tolerance
  && inner.x + inner.w <= outer.x + outer.w + tolerance
  && inner.y + inner.h <= outer.y + outer.h + tolerance
);

// The stage owns its box: the svg fills it, every 2D node sits inside it, and
// the list stays clickable above the drawing.
async function stageGeometry(page: Page) {
  return page.evaluate(() => {
    const rect = (el: Element) => {
      const r = el.getBoundingClientRect();
      return { x: r.x, y: r.y, w: r.width, h: r.height };
    };
    const stage = document.querySelector('.stage')!;
    const list = document.querySelector('.agent-list')!;
    const nodes = [...document.querySelectorAll('[data-agent-node]')].map((n) => {
      const h = n.querySelector('.cs-node__hex')!.getBoundingClientRect();
      const hit = document.elementFromPoint(h.x + h.width / 2, h.y + h.height / 2);
      return { id: n.getAttribute('data-agent-node'), box: rect(n), hitInside: hit !== null && n.contains(hit) };
    });
    const option = list.querySelector('[role="option"]')!.getBoundingClientRect();
    const overList = document.elementFromPoint(option.x + option.width / 2, option.y + option.height / 2);
    return {
      stage: rect(stage),
      svg: rect(document.querySelector('svg.constellation-svg')!),
      listTakesClicks: overList !== null && overList.closest('.agent-list') !== null,
      nodes,
    };
  });
}

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
  await expect(page.getByRole('listbox', { name: 'Agents' })).toContainText('Coder');
  // The roster arrives in frames after boot, so wait for the first node.
  await expect(page.locator('[data-agent-node]').first()).toBeVisible();
  const wide = await stageGeometry(page);
  expect(wide.nodes.length).toBeGreaterThan(0);
  expect(inside(wide.svg, wide.stage), 'svg inside the stage').toBe(true);
  for (const node of wide.nodes) {
    expect(inside(node.box, wide.stage), `node ${node.id} inside the stage`).toBe(true);
    expect(node.hitInside, `node ${node.id} takes the click at its center`).toBe(true);
  }
  expect(wide.listTakesClicks, 'the list takes clicks above the drawing').toBe(true);
  // The narrow breakpoints keep the drawing inside the 220px row and the
  // 96px strip.
  for (const width of [900, 600]) {
    await page.setViewportSize({ width, height: 860 });
    const narrow = await stageGeometry(page);
    expect(inside(narrow.svg, narrow.stage), `svg inside the stage at ${width}px`).toBe(true);
  }
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

// The inspector is the second projected screen: at the end of the flight it
// has to sit whole inside the viewport, with its close button reachable, not
// just have its body text on screen.
test('the cinematic inspector fits the viewport', async ({ page, gateway }) => {
  // Motion off settles the flight in one frame, so the geometry below is the
  // layout the full flight lands on without waiting out the clock.
  await page.addInitScript(() => localStorage.setItem('crush-web-motion', 'off'));
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  await page.locator('.agent-list [role="option"]', { hasText: 'Map the parser' }).click();
  const panel = page.locator('.panel--inspector');
  await expect(panel).toBeVisible();
  await page.keyboard.press('Alt+KeyC');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'true');

  // The projection is written a frame after the mode flips, so the poll
  // retries until the panel carries its matrix; it is bounded by the default
  // expect timeout.
  await expect.poll(async () => panel.evaluate((el) => {
    if (!(el instanceof HTMLElement)) return 'not an element';
    if (el.hidden) return 'closed';
    if (getComputedStyle(document.querySelector('.cine-view')!).opacity !== '1') return 'fading';
    if (!el.style.transform) return 'unprojected';
    const box = el.getBoundingClientRect();
    if (box.x < 0 || box.y < 0 || box.right > window.innerWidth || box.bottom > window.innerHeight) {
      return `box ${Math.round(box.x)},${Math.round(box.y)} to ${Math.round(box.right)},${Math.round(box.bottom)}`
        + ` leaves ${window.innerWidth}x${window.innerHeight}`;
    }
    const close = el.querySelector<HTMLElement>('.inspector__close');
    if (close === null) return 'no close button';
    const c = close.getBoundingClientRect();
    const hit = document.elementFromPoint(c.x + c.width / 2, c.y + c.height / 2);
    if (hit === null || !(hit === close || close.contains(hit))) return 'the close button is not reachable';
    return 'inside';
  })).toBe('inside');
});

// The 3D tags are DOM labels the scene moves over the canvas. A tag may only
// be visible while its node projects inside the stage, and a hidden one takes
// no input; the agent list is the accessible path and keeps the clicks it
// shares with a tag.
async function tagGeometry(page: Page) {
  return page.evaluate(() => {
    const rect = (el: Element) => {
      const r = el.getBoundingClientRect();
      return { x: r.x, y: r.y, w: r.width, h: r.height, right: r.right, bottom: r.bottom };
    };
    const stage = rect(document.querySelector('.stage')!);
    const tags = [...document.querySelectorAll('.agent-tag')].map((el) => {
      const at = /translate\(([-\d.]+)px, ([-\d.]+)px\)/.exec(el.getAttribute('style') ?? '');
      const box = rect(el);
      const hit = document.elementFromPoint(box.x + box.w / 2, box.y + box.h / 2);
      return {
        id: el.getAttribute('data-agent-node'),
        visible: el.getAttribute('data-visible') === 'true',
        anchor: at === null ? null : { x: Number(at[1]), y: Number(at[2]) },
        pointerEvents: getComputedStyle(el).pointerEvents,
        hitSelf: hit !== null && el.contains(hit),
      };
    });
    const optionEls = [...document.querySelectorAll('.agent-list [role="option"]')];
    const options = optionEls.map((o) => {
      const box = rect(o);
      const hit = document.elementFromPoint(box.x + box.w / 2, box.y + box.h / 2);
      return { text: o.textContent, takesClick: hit !== null && hit.closest('.agent-list') !== null };
    });
    // Every child agent gets a tag; the main agent only has a list entry.
    const roster = optionEls.filter((o) => o.getAttribute('data-kind') !== 'main').length;
    return { stage, tags, options, roster };
  });
}

test('3D tags stay inside the stage and never take the agent list clicks', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();

  // The roster arrives in frames after boot, each tag is drawn by its own
  // published projection, and the boot overlay covers the page until it
  // fades. Every check therefore runs inside one poll per viewport, so a
  // page that is still settling retries instead of failing; the poll is
  // bounded by the default expect timeout.
  const tagsSettled = (where: string, wantHidden: boolean) => expect.poll(async () => {
    const g = await tagGeometry(page);
    if (g.roster === 0 || g.tags.length !== g.roster) return `${where}: ${g.tags.length} of ${g.roster} tags`;
    // The overlay is the last thing above the page; no option takes a click
    // until it is gone, and neither does a tag.
    if (g.options.some((o) => !o.takesClick)) return `${where}: the list does not take its clicks`;
    const hidden = g.tags.filter((t) => !t.visible);
    if (wantHidden && hidden.length === 0) return `${where}: no tag has left the stage`;
    if (!wantHidden && hidden.length > 0) return `${where}: ${hidden[0].id} is hidden`;
    for (const tag of g.tags) {
      if (!tag.visible) {
        if (tag.pointerEvents !== 'none' || tag.hitSelf) return `${where}: hidden ${tag.id} takes clicks`;
        continue;
      }
      if (tag.anchor === null) return `${where}: ${tag.id} has no projected point`;
      const a = tag.anchor;
      if (a.x < g.stage.x || a.x > g.stage.right || a.y < g.stage.y || a.y > g.stage.bottom) {
        return `${where}: ${tag.id} point outside the stage`;
      }
      if (tag.pointerEvents !== 'auto') return `${where}: ${tag.id} does not take input`;
      if (!tag.hitSelf) return `${where}: ${tag.id} does not take its centre`;
    }
    return 'settled';
  }, { message: `${where}: tags settle` }).toBe('settled');

  await tagsSettled('1440px', false);
  const wide = await tagGeometry(page);
  expect(wide.tags.length).toBe(wide.roster);
  for (const option of wide.options) {
    expect(option.takesClick, `option ${option.text} takes the click at 1440px`).toBe(true);
  }

  // 900px shrinks the stage under the nodes, so their tags have to go away
  // instead of floating over the HUD and the deck. A tag starts hidden
  // before its first projection, so the wait is for a node to actually
  // leave the stage after the resize.
  await page.setViewportSize({ width: 900, height: 860 });
  await tagsSettled('900px', true);

  const narrow = await tagGeometry(page);
  expect(narrow.tags.length).toBe(narrow.roster);
  expect(narrow.tags.some((t) => !t.visible), 'a node leaves the stage at 900px').toBe(true);
  for (const option of narrow.options) {
    expect(option.takesClick, `option ${option.text} takes the click at 900px`).toBe(true);
  }
});
