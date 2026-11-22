import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import type { Agent } from '../../state/agents';
import { publishAgentScreens } from '../../state/layout';
import { actions } from '../../state/store';
import { AgentTags } from './AgentTags';

const root = document.createElement('div');
afterEach(async () => {
  await act(() => { render(null, root); });
  publishAgentScreens([]);
  vi.restoreAllMocks();
});

const agent: Agent = { sessionId: 'sub-1', parentSessionId: 's1', name: 'Map the parser', kind: 'subagent', status: 'running', model: '', modelProvider: '', startedAt: 0, finishedAt: 0 };

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

  it('selects the agent on click', async () => {
    const inspect = vi.spyOn(actions, 'inspect').mockResolvedValue(undefined);
    await act(() => { render(<AgentTags agents={[agent]} selected={null} />, root); });
    await act(() => { root.querySelector<HTMLElement>('[data-agent-node="sub-1"]')!.click(); });
    expect(inspect).toHaveBeenCalledWith('sub-1');
  });
});
