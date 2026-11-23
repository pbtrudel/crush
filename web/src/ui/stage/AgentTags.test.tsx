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

  it('selects the agent on click', async () => {
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    await act(() => { root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!.click(); });
    expect(inspect).toHaveBeenCalledWith('sub-1');
  });
});
