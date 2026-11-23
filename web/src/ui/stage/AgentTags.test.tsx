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

  it('selects the agent on click', async () => {
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    await act(() => { root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!.click(); });
    expect(inspect).toHaveBeenCalledWith('sub-1');
  });
});
