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
  // The toggle reaches the page on a frame and the roster arrives in frames
  // after boot, so both wait past a loaded host's frame budget.
  await expect(page.locator('svg.constellation-svg')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('listbox', { name: 'Agents' })).toContainText('Coder', { timeout: 15_000 });
  await expect(page.locator('[data-agent-node]').first()).toBeVisible({ timeout: 15_000 });
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

// The diff editor is CodeMirror, which style-mod would style with an inline
// <style> element in a document root and the CSP blocks. It therefore mounts
// in a shadow root and adopts its sheets there. Opening parser.go must reach
// that merge view, and no violation may fire on the way.
test('the diff editor mounts in a shadow root without a CSP violation', async ({ page, gateway }) => {
  await page.addInitScript(() => {
    const violations: string[] = [];
    Object.assign(window, { __cspViolations: violations });
    window.addEventListener('securitypolicyviolation', (e) => {
      violations.push(`${e.violatedDirective} ${e.blockedURI}`);
    });
  });
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  await page.evaluate(() => document.querySelector<HTMLElement>('.drawer-handle')?.click());
  const drawer = page.locator('#file-drawer');
  await expect(drawer).toBeVisible();
  await drawer.locator('.file-row', { hasText: 'parser.go' }).click();

  await expect.poll(async () => page.evaluate(() => {
    const host = document.querySelector('.diffpanel__view');
    const root = host?.shadowRoot;
    if (!root) return 'no shadow root';
    if (root.querySelector('.cm-mergeView') === null) return 'no merge view';
    const editors = root.querySelectorAll('.cm-editor');
    if (editors.length !== 2) return `${editors.length} editors`;
    return 'mounted';
  }), { message: 'the merge view mounts inside the shadow root' }).toBe('mounted');

  // version_ids are history positions, not history versions: the seeded
  // parser.go was first edited in two sessions, both numbering from 0.
  const versions = await page.evaluate(() => {
    const from = document.querySelector<HTMLSelectElement>('.diffpanel select[name="from"]')!;
    const to = document.querySelector<HTMLSelectElement>('.diffpanel select[name="to"]')!;
    return {
      from: [...from.options].map((o) => o.value),
      to: [...to.options].map((o) => o.value),
      picked: [from.value, to.value],
    };
  });
  expect(versions.from).toEqual(['0', '1', '2']);
  expect(versions.to).toEqual(['0', '1', '2']);
  expect(versions.picked).toEqual(['0', '2']);

  const report = await page.evaluate(() => {
    const root = document.querySelector('.diffpanel__view')!.shadowRoot!;
    return {
      violations: (window as unknown as { __cspViolations: string[] }).__cspViolations,
      inlineStyles: document.querySelectorAll('style').length,
      shadowStyles: root.querySelectorAll('style').length,
      adopted: root.adoptedStyleSheets.length,
      // The Go grammar is its own lazy chunk; keywords prove it loaded.
      keywords: root.querySelectorAll('.cm-content .tok-keyword').length,
    };
  });
  expect(report.violations).toEqual([]);
  expect(report.inlineStyles).toBe(0);
  expect(report.shadowStyles).toBe(0);
  // Our diff sheet and CodeMirror's share the root, both constructed.
  expect(report.adopted).toBeGreaterThanOrEqual(2);
  expect(report.keywords).toBeGreaterThan(0);

  // CodeMirror's merge base theme paints removal gutters red and addition
  // gutters green; the diff sheet has to win with the amber and teal tokens.
  const splitColours = await page.evaluate(() => {
    const root = document.querySelector('.diffpanel__view')!.shadowRoot!;
    const bg = (sel: string) => {
      const el = root.querySelector(sel);
      return el === null ? null : getComputedStyle(el).backgroundColor;
    };
    return {
      removed: bg('.cm-merge-a .cm-changedLineGutter'),
      added: bg('.cm-merge-b .cm-changedLineGutter'),
    };
  });
  expect(splitColours.removed).toContain('0.83 0.17 75');
  expect(splitColours.added).toContain('0.82 0.16 170');
  expect([splitColours.removed, splitColours.added]).not.toContain('rgb(238, 68, 51)');
  expect([splitColours.removed, splitColours.added]).not.toContain('rgb(34, 187, 34)');

  await page.locator('.diffpanel button[data-mode="unified"]').click();
  await expect.poll(async () => page.evaluate(() => {
    const root = document.querySelector('.diffpanel__view')?.shadowRoot;
    return root ? root.querySelectorAll('.cm-editor').length : 0;
  }), { message: 'the unified view mounts exactly one editor' }).toBe(1);

  const unifiedColours = await page.evaluate(() => {
    const root = document.querySelector('.diffpanel__view')!.shadowRoot!;
    const style = (sel: string) => {
      const el = root.querySelector(sel);
      return el === null ? null : getComputedStyle(el);
    };
    const text = style('.cm-deletedText');
    const gutter = style('.cm-deletedLineGutter');
    return {
      text: text === null ? null : { background: text.backgroundColor, image: text.backgroundImage },
      gutter: gutter === null ? null : gutter.backgroundColor,
      violations: (window as unknown as { __cspViolations: string[] }).__cspViolations,
    };
  });
  expect(unifiedColours.text).not.toBeNull();
  expect(unifiedColours.text!.background).toContain('0.83 0.17 75');
  expect(unifiedColours.text!.background).not.toBe('rgba(255, 0, 0, 0.2)');
  // The base theme's red underline is a background gradient, not a border.
  expect(unifiedColours.text!.image).toBe('none');
  expect(unifiedColours.gutter).toContain('0.83 0.17 75');
  expect(unifiedColours.gutter).not.toBe('rgb(238, 68, 51)');
  expect(unifiedColours.violations).toEqual([]);
});

test('the diff view gives the editor room at 1440x860', async ({ page, gateway }) => {
  await page.addInitScript(() => {
    const violations: string[] = [];
    Object.assign(window, { __cspViolations: violations });
    window.addEventListener('securitypolicyviolation', (e) => {
      violations.push(`${e.violatedDirective} ${e.blockedURI}`);
    });
  });
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  await page.setViewportSize({ width: 1440, height: 860 });
  await page.evaluate(() => document.querySelector<HTMLElement>('.drawer-handle')?.click());
  const drawer = page.locator('#file-drawer');
  await expect(drawer).toBeVisible();
  await drawer.locator('.file-row', { hasText: 'parser.go' }).click();

  const measure = () => page.evaluate(() => {
    const host = document.querySelector('.diffpanel__view');
    const root = host?.shadowRoot;
    const drawerEl = document.querySelector('#file-drawer')!;
    const diff = document.querySelector('.drawer__diff')!;
    const editors = root === null || root === undefined ? [] : [...root.querySelectorAll('.cm-editor')];
    // The pane the editor draws in: the merge view in split mode, the
    // editor itself in unified mode. Its visible height is what the view,
    // the drawer and the viewport all leave it.
    const surface = root === null || root === undefined
      ? null
      : root.querySelector('.cm-mergeView') ?? root.querySelector('.cm-editor');
    const visible = (el: Element) => {
      const r = el.getBoundingClientRect();
      const d = drawerEl.getBoundingClientRect();
      const v = host!.getBoundingClientRect();
      return Math.min(r.bottom, d.bottom, v.bottom) - Math.max(r.top, d.top, v.top);
    };
    return {
      editors: editors.length,
      heights: surface === null ? [] : [visible(surface)],
      diffScroll: [diff.clientHeight, diff.scrollHeight],
      drawerScroll: [drawerEl.clientHeight, drawerEl.scrollHeight],
      violations: (window as unknown as { __cspViolations: string[] }).__cspViolations,
    };
  });

  const check = async (label: string, count: number) => {
    await expect.poll(async () => {
      const m = await measure();
      if (m.editors !== count) return `${m.editors} editors`;
      if (m.heights.some((h) => h < 240)) return `the editor is ${Math.round(Math.min(...m.heights))}px tall`;
      if (m.diffScroll[1] > m.diffScroll[0] + 2) {
        return `the diff slot scrolls on its own (${m.diffScroll[0]}/${m.diffScroll[1]})`;
      }
      if (m.drawerScroll[1] > m.drawerScroll[0] + 2) {
        return `the drawer scrolls (${m.drawerScroll[0]}/${m.drawerScroll[1]})`;
      }
      if (m.violations.length > 0) return m.violations.join('; ');
      return 'ok';
    }, { message: `${label}: the editor is at least 240px tall with no competing scroll` }).toBe('ok');
  };

  await check('split', 2);
  await page.locator('.diffpanel button[data-mode="unified"]').click();
  await check('unified', 1);
});

// The drawer handle must stay the topmost thing at its own centre without
// stealing the inspector's close button or the first agent option, and the
// closed drawer must not add a sideways scroll. The layout changes with the
// drawer, the inspector and the width, so every case in CASES is checked with
// the drawer both closed and open.
async function reachability(page: Page) {
  return page.evaluate(() => {
    const hitSelf = (el: Element) => {
      const r = el.getBoundingClientRect();
      const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return hit !== null && (hit === el || el.contains(hit));
    };
    const overlaps = (a: Element, b: Element) => {
      const x = a.getBoundingClientRect();
      const y = b.getBoundingClientRect();
      return Math.min(x.right, y.right) > Math.max(x.left, y.left)
        && Math.min(x.bottom, y.bottom) > Math.max(x.top, y.top);
    };
    const handle = document.querySelector('.drawer-handle');
    const list = document.querySelector('.agent-list');
    const option = document.querySelector('.agent-list [role="option"]');
    const inspector = document.querySelector('.panel--inspector');
    const openInspector = inspector !== null && !inspector.hasAttribute('hidden');
    const close = openInspector ? inspector.querySelector('.inspector__close') : null;
    const title = openInspector ? inspector.querySelector('#inspector-title') : null;
    const head = openInspector ? inspector.querySelector('.inspector__head') : null;
    const titleBox = title === null ? null : title.getBoundingClientRect();
    // An inert drawer is the closed one: it waits off the page edge.
    const drawer = document.querySelector('#file-drawer');
    const openDrawer = drawer !== null && !drawer.hasAttribute('inert');
    const drawerClose = openDrawer ? drawer.querySelector('.drawer__head button') : null;
    const firstFile = openDrawer ? drawer.querySelector('.file-row') : null;
    return {
      handle: handle !== null && hitSelf(handle),
      option: option !== null && hitSelf(option),
      close: close === null ? null : hitSelf(close),
      title: title === null ? null : hitSelf(title),
      titleHeight: titleBox === null ? null : titleBox.height,
      drawerClose: drawerClose === null ? null : hitSelf(drawerClose),
      firstFile: firstFile === null ? null : hitSelf(firstFile),
      overlapsOption: handle !== null && option !== null && overlaps(handle, option),
      overlapsClose: handle !== null && close !== null && overlaps(handle, close),
      overlapsTitle: handle !== null && title !== null && overlaps(handle, title),
      listOverlapsHead: list !== null && head !== null && overlaps(list, head),
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    };
  });
}

// State changes are setup: call the handlers directly so a handle that
// covers the control cannot block the change, and so the animated scene
// does not make every actionability check retry. The hit tests below are
// what prove the handle is reachable.
async function setDrawer(page: Page, open: boolean) {
  const handle = page.locator('.drawer-handle');
  if (await handle.getAttribute('aria-expanded') === String(open)) return;
  await page.evaluate(() => document.querySelector<HTMLElement>('.drawer-handle')?.click());
  await expect(handle).toHaveAttribute('aria-expanded', String(open));
}

async function setInspector(page: Page, open: boolean) {
  const panel = page.locator('.panel--inspector');
  if (await panel.isVisible() === open) return;
  if (open) {
    // The fake's subagent carries the long name, so an open inspector always
    // shows a wrapped head: that is the case the head has to survive.
    await page.evaluate(() => {
      const options = [...document.querySelectorAll<HTMLElement>('.agent-list [role="option"]')];
      const long = options.find((o) => (o.textContent ?? '').includes('Map the parser entry points'));
      (long ?? options[0])?.click();
    });
  } else {
    await page.evaluate(() => {
      document.querySelector<HTMLElement>('.panel--inspector .inspector__close')?.click();
    });
  }
  await expect.poll(() => panel.isVisible()).toBe(open);
}

// Every state the layout switches on: the everyday widths with and without
// the inspector, then either side of the two agents.css bands with it open,
// where the list and the head compete for the stage.
const CASES: Array<{ width: number; height: number; inspector: boolean }> = [
  { width: 1440, height: 860, inspector: false },
  { width: 1440, height: 860, inspector: true },
  { width: 1100, height: 800, inspector: false },
  { width: 1100, height: 800, inspector: true },
  ...[1279, 1280, 1311, 1312, 1631, 1632].map((width) => ({ width, height: 860, inspector: true })),
];

test('the drawer handle and inspector stay reachable at every layout', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  await expect(page.locator('.drawer-handle')).toBeVisible();
  await expect(page.locator('.agent-list [role="option"]').first()).toBeVisible();

  for (const { width, height, inspector } of CASES) {
    await page.setViewportSize({ width, height });
    await setDrawer(page, false);
    await setInspector(page, inspector);
    for (const drawer of [false, true]) {
      await setDrawer(page, drawer);
      await expect.poll(async () => {
        const g = await reachability(page);
        const problems: string[] = [];
        if (!g.handle) problems.push('the handle does not take its centre');
        if (!g.option) problems.push('the first agent option does not take its centre');
        if (g.overlapsOption) problems.push('the handle overlaps the first agent option');
        if (inspector && g.close !== true) problems.push('the close button does not take its centre');
        if (inspector && g.title !== true) problems.push('the inspector title does not take its centre');
        if (inspector && g.titleHeight !== null && g.titleHeight < 40) problems.push(`the title is ${Math.round(g.titleHeight)}px tall, not a wrapped one`);
        if (inspector && g.listOverlapsHead) problems.push('the agent list overlaps the inspector head');
        if (g.overlapsClose) problems.push('the handle overlaps the close button');
        if (g.overlapsTitle) problems.push('the handle overlaps the inspector title');
        if (!inspector && g.close !== null) problems.push('the inspector is not the hidden one');
        if (drawer && g.drawerClose !== true) problems.push('the drawer close button does not take its centre');
        if (drawer && g.firstFile !== true) problems.push('the first file row does not take its centre');
        if (!drawer && g.firstFile !== null) problems.push('the closed drawer still answers');
        if (g.overflow > 0) problems.push(`the page scrolls sideways by ${g.overflow}px`);
        return problems.length === 0 ? 'ok' : problems.join('; ');
      }, {
        message: `${width}x${height} drawer ${drawer ? 'open' : 'closed'}, inspector ${inspector ? 'open' : 'closed'}`,
      }).toBe('ok');
    }
  }
});

test('the files tab gives the diff room at 900x800', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  await page.setViewportSize({ width: 900, height: 800 });
  await expect(page.locator('.panel-tabs')).toBeVisible();
  // The handle opens the files tab, which is what the narrow layout shows.
  await page.evaluate(() => document.querySelector<HTMLElement>('.drawer-handle')?.click());
  const grid = page.locator('.holodeck__grid');
  await expect(grid).toHaveAttribute('data-panel-tab', 'files');
  const drawer = page.locator('#file-drawer');
  await expect(drawer).toBeVisible();
  await page.evaluate(() => document.querySelector<HTMLElement>('#file-drawer .file-row')?.click());
  await expect(drawer.locator('.file-row').first()).toHaveAttribute('aria-pressed', 'true');

  // Task 18's diff needs a real box, not the few pixels left over when the
  // list takes 40% of the 120px panel slot; the panel scrolls instead.
  await expect.poll(async () => {
    const d = await page.evaluate(() => {
      const diff = document.querySelector('.drawer__diff');
      return {
        height: diff?.getBoundingClientRect().height ?? 0,
        path: diff?.querySelector('.diffpanel__title')?.textContent ?? '',
      };
    });
    if (d.path === '') return 'the selected file is not shown in the diff';
    return d.height >= 240 ? 'ok' : `${Math.round(d.height)}px tall`;
  }, { message: 'the selected diff is at least 240px tall at 900x800' }).toBe('ok');
});

// The inspector's head is the top of the panel, so a long agent name wraps
// it. The handle keeps the stage corner and the head leaves that corner
// empty, whatever the name.
test('a long inspector title stays clear of the drawer handle', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  await expect(page.locator('.agent-list [role="option"]').first()).toBeVisible();
  // The fake's subagent carries the long name.
  await page.evaluate(() => {
    const option = [...document.querySelectorAll<HTMLElement>('.agent-list [role="option"]')]
      .find((o) => (o.textContent ?? '').includes('Map the parser entry points'));
    option?.click();
  });
  await expect(page.locator('.panel--inspector')).toBeVisible();
  await expect(page.locator('#inspector-title')).toContainText('Map the parser entry points');

  for (const viewport of [{ width: 1440, height: 860 }, { width: 1100, height: 800 }, { width: 1024, height: 768 }]) {
    await page.setViewportSize(viewport);
    await expect.poll(async () => {
      const g = await page.evaluate(() => {
        const box = (sel: string) => {
          const el = document.querySelector(sel);
          if (el === null) return null;
          const r = el.getBoundingClientRect();
          return { x: r.x, y: r.y, w: r.width, h: r.height, right: r.right, bottom: r.bottom };
        };
        const overlaps = (a: { x: number; y: number; right: number; bottom: number }, b: typeof a) => (
          Math.min(a.right, b.right) > Math.max(a.x, b.x) && Math.min(a.bottom, b.bottom) > Math.max(a.y, b.y)
        );
        const handle = box('.drawer-handle');
        const title = box('#inspector-title');
        return {
          handle,
          title,
          overlap: handle !== null && title !== null && overlaps(handle, title),
          centre: (() => {
            const el = document.querySelector('#inspector-title');
            if (el === null) return 'missing';
            const r = el.getBoundingClientRect();
            const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
            return hit !== null && el.contains(hit) ? 'title' : 'other';
          })(),
        };
      });
      if (g.title === null || g.title.h < 40) return `the title is ${g.title?.h ?? 0}px tall, not a wrapped one`;
      if (g.overlap) return `the handle covers the title at ${Math.round(g.title.y)}-${Math.round(g.title.bottom)}`;
      if (g.centre !== 'title') return 'the title centre is covered';
      return 'ok';
    }, { message: `${viewport.width}x${viewport.height}: the handle leaves the title alone` }).toBe('ok');
  }
});

// In the row form the list can outgrow the stage. The arrows must bring the
// option they move to into view, since the row has no scrollbar to drag.
test('the agent list row keeps the focused option in view', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  await expect(page.locator('.agent-list [role="option"]').first()).toBeVisible();
  await page.setViewportSize({ width: 1024, height: 768 });
  await setInspector(page, true);

  const view = () => page.evaluate(() => {
    const list = document.querySelector('.agent-list')!;
    const options = [...list.querySelectorAll<HTMLElement>('[role="option"]')];
    const box = list.getBoundingClientRect();
    const focused = document.activeElement instanceof HTMLElement ? options.indexOf(document.activeElement) : -1;
    const target = options[focused < 0 ? options.length - 1 : focused].getBoundingClientRect();
    return {
      row: getComputedStyle(list).display,
      overflows: list.scrollWidth > list.clientWidth,
      options: options.length,
      focused,
      // With nothing focused this measures the last option, which starts
      // past the row's right edge.
      visible: target.left >= box.left - 1 && target.right <= box.right + 1,
    };
  });

  const start = await view();
  expect(start.row, 'the list is a row at 1024 with the inspector open').toBe('flex');
  expect(start.overflows, 'the row outgrows the stage').toBe(true);
  expect(start.visible, 'the last option starts out of view').toBe(false);

  // The tab stop is the first option; End walks to the last in one press.
  await page.locator('.agent-list [role="option"][tabindex="0"]').first().focus();
  await page.keyboard.press('End');
  await expect.poll(async () => (await view()).focused, {
    message: 'End moves the focus to the last option',
  }).toBe(start.options - 1);
  expect((await view()).visible, 'the focused last option is inside the row').toBe(true);

  // The arrows step back without leaving the option behind an edge.
  await page.keyboard.press('ArrowLeft');
  await expect.poll(async () => (await view()).focused, {
    message: 'ArrowLeft steps off the last option',
  }).toBe(start.options - 2);
  expect((await view()).visible, 'the option the arrow moved to is inside the row').toBe(true);
  await page.keyboard.press('Home');
  await expect.poll(async () => (await view()).focused, {
    message: 'Home moves the focus back to the first option',
  }).toBe(0);
  expect((await view()).visible, 'the focused first option is inside the row').toBe(true);
});

// Between 1024px and 1279px the open drawer sits over the log column, so the
// log it covers leaves the tab order. The states either side of the band keep
// it reachable.
test('the covered log leaves the tab order over the overlay drawer', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  const log = page.locator('.panel--log');
  // The transcript arrives in frames after boot; the summaries are its tab
  // stops and have to exist before the walk below means anything. A loaded
  // host can take most of the default timeout to deliver it.
  await expect(page.locator('.panel--log summary, .panel--log button').first()).toBeVisible({ timeout: 20_000 });
  const focusLogControl = () => page.evaluate(() => {
    const el = document.querySelector<HTMLElement>('.panel--log summary, .panel--log button');
    if (el === null) return 'no control';
    el.focus();
    return document.activeElement === el ? 'focused' : 'blocked';
  });

  // A drawer with its own column never makes the log inert.
  await page.setViewportSize({ width: 1440, height: 860 });
  await setDrawer(page, true);
  await expect(log).not.toHaveAttribute('inert', '');
  expect(await focusLogControl(), 'the log control takes focus in its own layout').toBe('focused');

  await page.setViewportSize({ width: 1100, height: 800 });
  await expect(log).toHaveAttribute('inert', '');
  expect(await focusLogControl(), 'the covered log is out of reach').toBe('blocked');
  // From inside the drawer, twenty four tabs must never stop in the log.
  await page.evaluate(() => document.querySelector<HTMLElement>('#file-drawer .file-row')?.focus());
  const stops: string[] = [];
  for (let i = 0; i < 24; i++) {
    await page.keyboard.press('Tab');
    stops.push(await page.evaluate(() => {
      const el = document.activeElement;
      if (el === null || el === document.body) return 'body';
      return el.closest('.panel--log') !== null ? 'log' : el.tagName.toLowerCase();
    }));
  }
  expect(stops, 'no tab stop lands in the covered log').not.toContain('log');
  expect(stops.some((s) => s !== 'body'), 'the tabs still move through the page').toBe(true);

  // Closing the drawer, or a layout with its own column, gives the tab stops
  // back.
  await setDrawer(page, false);
  await expect(log).not.toHaveAttribute('inert', '');
  expect(await focusLogControl(), 'the log control takes focus again').toBe('focused');
  await setDrawer(page, true);
  await page.setViewportSize({ width: 900, height: 800 });
  await expect(log).not.toHaveAttribute('inert', '');
});

// The log leaves the tab order over the overlay drawer, so the focus it held
// needs somewhere better than an inert control: the drawer takes it and gives
// it back to the handle when it closes over it.
test('the overlay drawer takes the focus off the covered log', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  const log = page.locator('.panel--log');
  // The log has no tab stop until the session has a message, so a stand-in
  // control keeps this test runnable on a fresh worker session.
  await page.evaluate(() => {
    const probe = document.createElement('button');
    probe.id = 'focus-probe';
    probe.textContent = 'probe';
    document.querySelector('.panel--log')?.append(probe);
  });
  const focusLog = () => page.evaluate(() => {
    const el = document.getElementById('focus-probe');
    el?.focus();
    return document.activeElement === el;
  });
  const focused = () => page.evaluate(() => {
    const el = document.activeElement;
    if (el === null || el === document.body) return 'body';
    if (el.closest('#file-drawer') !== null) return 'drawer';
    if (el.closest('.panel--log') !== null) return 'log';
    return el.classList.contains('drawer-handle') ? 'handle' : el.tagName.toLowerCase();
  });

  // A window that narrows into the band takes the log out of the tab order
  // under the focus that was reading it.
  await page.setViewportSize({ width: 1440, height: 860 });
  await setDrawer(page, true);
  await expect(log).not.toHaveAttribute('inert', '');
  expect(await focusLog(), 'the log control takes focus in its own layout').toBe(true);
  await page.setViewportSize({ width: 1100, height: 800 });
  await expect(log).toHaveAttribute('inert', '');
  await expect.poll(focused, { message: 'the drawer takes the focus off the log it covers' }).toBe('drawer');

  // The open drawer keeps a focus it dropped (a click on its padding or a
  // window switch): only closing hands it to the handle.
  await page.evaluate(() => document.querySelector<HTMLElement>('#file-drawer .drawer__head button')?.blur());
  expect(await focused(), 'the open drawer keeps the focus it dropped').toBe('body');
  await page.locator('#file-drawer .drawer__head button').focus();

  // The keyboard route in: closing the drawer gives the handle the focus
  // back, and opening it again takes the focus into the drawer, not the body.
  await page.keyboard.press('Enter');
  await expect(page.locator('.drawer-handle')).toHaveAttribute('aria-expanded', 'false');
  await expect.poll(focused, { message: 'the handle takes the focus back' }).toBe('handle');
  expect(await focusLog(), 'the log control takes focus once the drawer is closed').toBe(true);
  await page.locator('.drawer-handle').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('.drawer-handle')).toHaveAttribute('aria-expanded', 'true');
  await expect.poll(focused, { message: 'the keyboard open lands in the drawer, not on the body' }).toBe('drawer');
  await page.evaluate(() => { document.getElementById('focus-probe')?.remove(); });
});

// The closed drawer waits off the page edge. In the overlay layout it must
// share its open anchor, or opening it teleports the panel across the grid
// before the transform starts.
test('the overlay drawer opens from one anchor', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  const anchor = () => page.evaluate(() => {
    const el = document.querySelector('#file-drawer');
    if (el === null) return null;
    const style = getComputedStyle(el);
    return { start: style.insetInlineStart, end: style.insetInlineEnd, opacity: style.opacity };
  });
  await page.setViewportSize({ width: 1100, height: 800 });
  await setDrawer(page, false);
  // Opacity is the transitioned property: wait for it to land on both ends.
  await expect.poll(async () => (await anchor())?.opacity).toBe('0');
  const closed = await anchor();
  await setDrawer(page, true);
  await expect.poll(async () => (await anchor())?.opacity).toBe('1');
  const open = await anchor();
  expect(closed!.start, 'the closed drawer keeps the open anchor').toBe(open!.start);
  expect(closed!.end, 'the closed drawer keeps the open anchor').toBe(open!.end);
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
});

// Cinematic mode gives the scene its drags back. The task board and the todo
// orbit stay on screen for information, but only their own controls take a
// pointer.
test('cinematic mode passes orbit drags through the stage panels', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.getByRole('status').filter({ hasText: 'Linked' })).toBeVisible();
  await expect(page.locator('.boot')).toBeHidden();
  const panels = () => page.evaluate(() => {
    const read = (sel: string) => {
      const el = document.querySelector(sel);
      if (el === null) return null;
      const r = el.getBoundingClientRect();
      const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return {
        pointer: getComputedStyle(el).pointerEvents,
        hit: hit === null ? 'null' : (hit === el || el.contains(hit) ? 'self' : hit.tagName.toLowerCase()),
      };
    };
    return { taskboard: read('.taskboard'), orbit: read('.todo-orbit'), owner: read('.taskcard__owner') };
  });
  const stage = await panels();
  expect(stage.taskboard?.pointer, 'the task board takes pointers on the stage').toBe('auto');
  expect(stage.orbit?.pointer, 'the todo orbit takes pointers on the stage').toBe('auto');

  await page.keyboard.press('Alt+KeyC');
  await expect(page.locator('.holodeck')).toHaveAttribute('data-cinematic', 'true');
  await expect.poll(async () => {
    const p = await panels();
    const problems: string[] = [];
    if (p.taskboard?.pointer !== 'none') problems.push(`the task board still takes pointers (${p.taskboard?.pointer})`);
    if (p.orbit?.pointer !== 'none') problems.push(`the todo orbit still takes pointers (${p.orbit?.pointer})`);
    if (p.taskboard?.hit === 'self') problems.push('the task board takes the drag');
    if (p.orbit?.hit === 'self') problems.push('the todo orbit takes the drag');
    if (p.owner?.pointer !== 'auto') problems.push('the owner chip lost its pointer');
    if (p.owner?.hit !== 'self') problems.push('the owner chip does not take its centre');
    return problems.length === 0 ? 'ok' : problems.join('; ');
  }, { message: 'the scene keeps the drags in cinematic mode' }).toBe('ok');
});

// Log text under a 0.70 glass reads through the panel, so the overlay state
// takes the solid surface. Wider layouts keep the lighter glass: nothing
// sits under them.
test('the overlay drawer is opaque enough to read over the log', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  const glass = () => page.evaluate(() => {
    const el = document.querySelector('#file-drawer');
    return el === null ? '' : getComputedStyle(el).backgroundImage;
  });
  await page.setViewportSize({ width: 1100, height: 800 });
  await setDrawer(page, true);
  await expect.poll(async () => (await glass()).includes('0.16 0.05 265 / 0.94') ? 'solid' : (await glass()).slice(0, 90))
    .toBe('solid');
  await page.setViewportSize({ width: 1440, height: 860 });
  await expect.poll(async () => (await glass()).includes('0.16 0.05 265 / 0.7)') ? 'glass' : (await glass()).slice(0, 90))
    .toBe('glass');
});

// The overlay drawer is only 392px tall at 1100x800, and the diff below the
// file list has to keep a usable editor. The list gives up its height there.
test('the overlay drawer gives the diff room at 1100x800', async ({ page, gateway }) => {
  await page.goto(gateway.launch);
  await expect(page.locator('.boot')).toBeHidden();
  await page.setViewportSize({ width: 1100, height: 800 });
  await setDrawer(page, true);
  await page.evaluate(() => document.querySelector<HTMLElement>('#file-drawer .file-row')?.click());
  await expect(page.locator('.diffpanel__title')).toBeVisible();

  await expect.poll(async () => {
    const m = await page.evaluate(() => {
      const view = document.querySelector('.diffpanel__view');
      const diff = document.querySelector('.drawer__diff');
      const drawer = document.querySelector('#file-drawer');
      const surface = document.querySelector('.diffpanel__view')?.shadowRoot?.querySelector('.cm-mergeView') ?? null;
      const visible = (el: Element | null) => {
        if (el === null || drawer === null) return 0;
        const r = el.getBoundingClientRect();
        const d = drawer.getBoundingClientRect();
        return Math.round(Math.min(r.bottom, d.bottom) - Math.max(r.top, d.top));
      };
      return {
        view: visible(view),
        editor: visible(surface),
        scroll: diff === null ? [0, 0] : [diff.clientHeight, diff.scrollHeight],
      };
    });
    if (m.view < 200) return `the diff view is ${m.view}px tall`;
    if (m.editor < 190) return `the editor is ${m.editor}px tall`;
    if (m.scroll[1] > m.scroll[0] + 2) return 'the diff slot scrolls on its own';
    return 'ok';
  }, { message: 'the diff view keeps 200px at 1100x800' }).toBe('ok');

  // A selected diff does not move the drawer's own controls out of reach.
  for (const inspector of [false, true]) {
    await setInspector(page, inspector);
    const g = await reachability(page);
    expect(g.handle, 'the handle takes its centre').toBe(true);
    expect(g.drawerClose, 'the drawer close button takes its centre').toBe(true);
    expect(g.firstFile, 'the first file row takes its centre').toBe(true);
    if (inspector) expect(g.title, 'the inspector title takes its centre').toBe(true);
    expect(g.overflow, 'no sideways scroll').toBe(0);
  }
});
