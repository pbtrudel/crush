import { describe, expect, it } from 'vitest';
import type { ModelInfo } from '../api/types';
import { showThinkingToggle } from './model';

const model = (over: Partial<ModelInfo> = {}): ModelInfo => ({
  id: 'm1', name: 'Model One', provider: 'p1', context_window: 200000, agent_ready: true, ...over,
});

describe('showThinkingToggle', () => {
  it('shows the toggle for a reasoning model with no effort levels', () => {
    expect(showThinkingToggle(model({ can_reason: true }))).toBe(true);
  });

  it('hides the toggle once the model offers effort levels', () => {
    expect(showThinkingToggle(model({ can_reason: true, effort_levels: ['low', 'high'] }))).toBe(false);
  });

  it('hides the toggle for a model that cannot reason', () => {
    expect(showThinkingToggle(model({ can_reason: false }))).toBe(false);
    expect(showThinkingToggle(model())).toBe(false);
  });
});
