import type { ModelInfo } from '../api/types';

// showThinkingToggle reports whether the model selector offers the
// thinking switch: a model that reasons with effort levels gets the level
// picker instead, the same choice the terminal's dialog makes.
export function showThinkingToggle(m: ModelInfo): boolean {
  return !!m.can_reason && !(m.effort_levels?.length);
}
