import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { effectsLevel, sceneFailed } from '../state/effects';
import { lowPower } from '../state/motion';
import { MotionMenu } from './MotionMenu';

const root = document.createElement('div');

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  effectsLevel.value = 0;
  sceneFailed.value = false;
  lowPower.value = false;
});

async function mount() {
  await act(() => {
    render(<MotionMenu />, root);
  });
}

const restore = () => Array.from(root.querySelectorAll('button')).find((b) => b.textContent === 'Restore effects');

describe('Display menu recovery', () => {
  it('offers restore effects only after a step-down or a scene failure', async () => {
    await mount();
    expect(restore()).toBeUndefined();

    await act(() => {
      effectsLevel.value = 1;
    });
    expect(restore()).toBeDefined();

    await act(() => {
      effectsLevel.value = 0;
    });
    expect(restore()).toBeUndefined();

    await act(() => {
      sceneFailed.value = true;
    });
    expect(restore()).toBeDefined();
  });

  it('restores the effects and clears the failure from the menu', async () => {
    effectsLevel.value = 2;
    sceneFailed.value = true;
    await mount();
    expect(restore()).toBeDefined();

    await act(() => {
      restore()!.click();
    });
    expect(effectsLevel.value).toBe(0);
    expect(sceneFailed.value).toBe(false);
    expect(restore()).toBeUndefined();
  });
});
