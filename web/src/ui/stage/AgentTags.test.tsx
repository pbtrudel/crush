import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import type { Agent } from '../../state/agents';
import { publishAgentScreens, stageRect } from '../../state/layout';
import { actions } from '../../state/store';
import { AgentTags } from './AgentTags';

const root = document.createElement('div');
afterEach(async () => {
  await act(() => { render(null, root); });
  publishAgentScreens([]);
  stageRect.value = null;
  vi.restoreAllMocks();
});

const agent: Agent = { sessionId: 'sub-1', parentSessionId: 's1', name: 'Map the parser', kind: 'subagent', status: 'running', model: '', modelProvider: '', startedAt: 0, finishedAt: 0 };

function box(left: number, top: number, width: number, height: number): DOMRectReadOnly {
  return { x: left, y: top, left, top, width, height, right: left + width, bottom: top + height } as DOMRectReadOnly;
}

describe('AgentTags', () => {
  it('moves a tag when a board that mounts later is resized', async () => {
    const observed: Element[] = [];
    const callbacks: Array<() => void> = [];
    class FakeResizeObserver {
      constructor(cb: () => void) { callbacks.push(cb); }
      observe(el: Element) { observed.push(el); }
      disconnect() {}
    }
    const original = globalThis.ResizeObserver;
    (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = FakeResizeObserver;
    const board = document.createElement('section');
    board.className = 'taskboard';
    // A board that covers the tag once it grows, and a tag with a size the
    // placement can measure.
    board.getBoundingClientRect = () => box(0, 100, 400, 300) as DOMRect;
    try {
      stageRect.value = box(0, 0, 800, 600);
      await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
      const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
      Object.defineProperty(tag, 'offsetWidth', { configurable: true, value: 80 });
      Object.defineProperty(tag, 'offsetHeight', { configurable: true, value: 24 });
      publishAgentScreens([{ id: 'sub-1', x: 300, y: 200, visible: true }]);
      // No board yet, so the tag sits above its node.
      expect(tag.style.transform).toContain('translate(-50%, -140%)');
      expect(observed).toHaveLength(0);

      // The board mounts later, the way it does once a task arrives, and the
      // watcher has to pick it up.
      document.body.append(board);
      await act(async () => { await Promise.resolve(); await Promise.resolve(); });
      expect(observed).toContain(board);

      // Its resize re-runs placement: the tag now parks above the board.
      await act(() => { for (const cb of callbacks) cb(); });
      expect(tag.style.transform).toContain('translate(-50%, -100%)');
      expect(tag.style.transform).not.toContain('-140%');
    } finally {
      board.remove();
      (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = original;
    }
  });

  it('moves tags through the CSSOM without re-rendering', async () => {
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    publishAgentScreens([{ id: 'sub-1', x: 300, y: 200, visible: true }]);
    expect(tag.style.transform).toContain('translate(300px, 200px)');
    expect(tag.dataset.visible).toBe('true');
    expect(tag.getAttribute('style')).toContain('transform');
  });

  it('places a new tag at the last published position', async () => {
    publishAgentScreens([{ id: 'sub-1', x: 10, y: 20, visible: true }]);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    expect(root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!.style.transform).toContain('translate(10px, 20px)');
  });

  it('hides a tag whose node stopped projecting', async () => {
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    publishAgentScreens([{ id: 'sub-1', x: 300, y: 200, visible: true }]);
    expect(tag.dataset.visible).toBe('true');
    // A node collapses into the core when it is done or leaves the roster,
    // and stops projecting. Its tag must not stay behind as a ghost.
    publishAgentScreens([]);
    expect(tag.dataset.visible).toBe('false');
  });

  it('hides a tag whose point leaves the stage box', async () => {
    stageRect.value = box(100, 100, 400, 300);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    publishAgentScreens([{ id: 'sub-1', x: 700, y: 200, visible: true }]);
    expect(tag.dataset.visible).toBe('false');
    // The point is kept, so the tag is in place the moment it comes back.
    expect(tag.style.transform).toContain('translate(700px, 200px)');
    publishAgentScreens([{ id: 'sub-1', x: 300, y: 200, visible: true }]);
    expect(tag.dataset.visible).toBe('true');
  });

  // The task board is a docked panel over the bottom of the stage. A tag
  // that lands on it covers a lane heading, and the board takes clicks, so
  // that tag alone moves up to sit clear above the board.
  function dockBoard(rect: DOMRectReadOnly) {
    const board = document.createElement('section');
    board.className = 'taskboard';
    document.body.append(board);
    vi.spyOn(board, 'getBoundingClientRect').mockReturnValue(rect);
    return board;
  }

  function sizeTag(tag: HTMLElement, width: number, height: number) {
    Object.defineProperty(tag, 'offsetWidth', { configurable: true, get: () => width });
    Object.defineProperty(tag, 'offsetHeight', { configurable: true, get: () => height });
  }

  it('raises a tag that would sit on the task board', async () => {
    const board = dockBoard(box(592, 489, 816, 147));
    stageRect.value = box(576, 173, 848, 463);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    sizeTag(tag, 64, 40);
    publishAgentScreens([{ id: 'sub-1', x: 900, y: 560, visible: true }]);
    // The default offset puts the tag at 504..544, inside the board; it moves
    // to sit just above the board's top edge instead.
    expect(tag.style.transform).toBe('translate(900px, 481px) translate(-50%, -100%)');
    expect(tag.dataset.visible).toBe('true');
    board.remove();
  });

  it('keeps the default offset for a tag that clears the board', async () => {
    const board = dockBoard(box(592, 489, 816, 147));
    stageRect.value = box(576, 173, 848, 463);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    sizeTag(tag, 64, 40);
    // Above the board's top edge: the default offset already clears it.
    publishAgentScreens([{ id: 'sub-1', x: 900, y: 400, visible: true }]);
    expect(tag.style.transform).toBe('translate(900px, 400px) translate(-50%, -140%)');
    // Level with the board but far to its left: no overlap either.
    publishAgentScreens([{ id: 'sub-1', x: 100, y: 560, visible: true }]);
    expect(tag.style.transform).toBe('translate(100px, 560px) translate(-50%, -140%)');
    board.remove();
  });

  it('keeps a raised tag inside the stage', async () => {
    const board = dockBoard(box(592, 180, 816, 400));
    stageRect.value = box(576, 173, 848, 463);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    sizeTag(tag, 64, 40);
    publishAgentScreens([{ id: 'sub-1', x: 900, y: 560, visible: true }]);
    // The board's top is under the stage's top, so the stage edge wins.
    expect(tag.style.transform).toBe('translate(900px, 221px) translate(-50%, -100%)');
    board.remove();
  });

  it('treats a board with no box as absent', async () => {
    // A hidden or collapsed board reports a degenerate rect, which a tag
    // straddling it would read as an overlap and climb above. It is absent.
    const board = dockBoard(box(600, 520, 0, 0));
    stageRect.value = box(576, 173, 848, 463);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    sizeTag(tag, 64, 40);
    publishAgentScreens([{ id: 'sub-1', x: 600, y: 560, visible: true }]);
    expect(tag.style.transform).toBe('translate(600px, 560px) translate(-50%, -140%)');
    board.remove();
  });

  // The inspector is a docked panel in the same stacking context as the
  // tags, and it takes clicks. A tag over it would both paint on its
  // controls and steal the press from them.
  function dockInspector(rect: DOMRectReadOnly, hidden = false) {
    const panel = document.createElement('section');
    panel.className = 'panel panel--inspector';
    if (hidden) panel.setAttribute('hidden', '');
    document.body.append(panel);
    vi.spyOn(panel, 'getBoundingClientRect').mockReturnValue(rect);
    return panel;
  }

  it('hides a tag whose point lies in the open inspector', async () => {
    const inspector = dockInspector(box(600, 100, 300, 400));
    stageRect.value = box(0, 0, 1200, 800);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    publishAgentScreens([{ id: 'sub-1', x: 700, y: 200, visible: true }]);
    expect(tag.dataset.visible).toBe('false');
    // A point beside the panel is still on the stage, so it stays visible.
    publishAgentScreens([{ id: 'sub-1', x: 300, y: 200, visible: true }]);
    expect(tag.dataset.visible).toBe('true');
    inspector.remove();
  });

  it('keeps a tag visible when the inspector is hidden', async () => {
    // A closed panel reports a box too, and the selector must ignore it.
    const inspector = dockInspector(box(600, 100, 300, 400), true);
    stageRect.value = box(0, 0, 1200, 800);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    publishAgentScreens([{ id: 'sub-1', x: 700, y: 200, visible: true }]);
    expect(tag.dataset.visible).toBe('true');
    inspector.remove();
  });

  it('hides a tag when the inspector opens with no new projection', async () => {
    stageRect.value = box(0, 0, 1200, 800);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    sizeTag(tag, 60, 20);
    publishAgentScreens([{ id: 'sub-1', x: 700, y: 200, visible: true }]);
    expect(tag.dataset.visible).toBe('true');
    // Motion off: the scene publishes nothing more. Opening the inspector is
    // a selection change and must re-run the placement on its own.
    const panel = dockInspector(box(600, 100, 300, 400));
    await act(() => { render(<AgentTags agents={[agent]} selected="sub-1" />, root); });
    expect(tag.dataset.visible).toBe('false');
    panel.setAttribute('hidden', '');
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    expect(tag.dataset.visible).toBe('true');
    panel.remove();
  });

  it('hides a tag whose box overlaps the inspector though its point does not', async () => {
    const panel = dockInspector(box(600, 100, 300, 400));
    stageRect.value = box(0, 0, 1200, 800);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    sizeTag(tag, 60, 20);
    // The point sits 10px left of the panel; the tag is 60px wide and
    // centered on it, so its right half covers the panel edge.
    publishAgentScreens([{ id: 'sub-1', x: 590, y: 300, visible: true }]);
    expect(tag.dataset.visible).toBe('false');
    publishAgentScreens([{ id: 'sub-1', x: 560, y: 300, visible: true }]);
    expect(tag.dataset.visible).toBe('true');
    panel.remove();
  });

  it('hides a tag whose box overlaps the agent list', async () => {
    const list = document.createElement('ul');
    list.className = 'agent-list';
    document.body.append(list);
    vi.spyOn(list, 'getBoundingClientRect').mockReturnValue(box(20, 600, 200, 120));
    stageRect.value = box(0, 0, 1200, 800);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    sizeTag(tag, 60, 20);
    publishAgentScreens([{ id: 'sub-1', x: 100, y: 640, visible: true }]);
    expect(tag.dataset.visible).toBe('false');
    list.remove();
  });

  it('reapplies when the stage box changes', async () => {
    stageRect.value = box(100, 100, 400, 300);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    const tag = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    publishAgentScreens([{ id: 'sub-1', x: 700, y: 200, visible: true }]);
    expect(tag.dataset.visible).toBe('false');
    await act(() => { stageRect.value = box(0, 0, 1200, 800); });
    expect(tag.dataset.visible).toBe('true');
  });

  it('lifts a tag clear of a neighbour tag', async () => {
    const other: Agent = { ...agent, sessionId: 'sub-2', name: 'Write the tests' };
    stageRect.value = box(0, 0, 1200, 800);
    await act(() => { render(<AgentTags agents={[agent, other]} selected={null} />, root); });
    const a = root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!;
    const b = root.querySelector<HTMLElement>('[data-agent-node="sub-2"]')!;
    sizeTag(a, 80, 20);
    sizeTag(b, 80, 20);
    publishAgentScreens([
      { id: 'sub-1', x: 400, y: 400, visible: true },
      { id: 'sub-2', x: 420, y: 404, visible: true },
    ]);
    // The first tag keeps the default offset (top 372); the second would sit
    // at top 376, overlapping it, and moves to end 4px above the first.
    expect(a.style.transform).toBe('translate(400px, 400px) translate(-50%, -140%)');
    expect(b.style.transform).toBe('translate(420px, 368px) translate(-50%, -100%)');
  });

  it('selects the agent on click', async () => {
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    await act(() => { root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!.click(); });
    expect(inspect).toHaveBeenCalledWith('sub-1');
  });
});
