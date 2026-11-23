import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { effectsLevel, sceneFailed } from '../state/effects';
import { lowPower } from '../state/motion';
import { MotionMenu } from './MotionMenu';

const root = document.createElement('div');

beforeEach(() => {
  document.body.append(root);
});

afterEach(async () => {
  await act(() => {
    render(null, root);
  });
  root.remove();
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

  it('closes on Escape and gives focus back to the summary', async () => {
    await mount();
    const details = root.querySelector('details')!;
    details.open = true;
    root.querySelector<HTMLInputElement>('input[type="radio"]')!.focus();
    const onWindow = vi.fn();
    window.addEventListener('keydown', onWindow);
    try {
      await act(() => { details.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })); });
    } finally {
      window.removeEventListener('keydown', onWindow);
    }
    expect(details.open).toBe(false);
    expect(document.activeElement).toBe(root.querySelector('summary'));
    // The key belongs to the menu: nothing outside it may act on the same
    // press, so the inspector and the cinematic view keep their state.
    expect(onWindow).not.toHaveBeenCalled();
  });

  it('closes on a pointer down outside the menu', async () => {
    await mount();
    const details = root.querySelector('details')!;
    details.open = true;
    document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    expect(details.open).toBe(false);
  });

  it('stays open on a pointer down inside the menu', async () => {
    await mount();
    const details = root.querySelector('details')!;
    details.open = true;
    details.querySelector('input[type="radio"]')!.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    expect(details.open).toBe(true);
  });

  it('keeps focus in the menu when the restore button goes away', async () => {
    effectsLevel.value = 1;
    await mount();
    const button = restore()!;
    await act(() => {
      button.focus();
    });
    expect(document.activeElement).toBe(button);

    await act(() => {
      button.click();
    });
    expect(restore()).toBeUndefined();
    // The button unmounts with the recovery; focus lands on the Display
    // summary instead of dropping to the body.
    expect(document.activeElement).toBe(root.querySelector('summary'));
  });
});
