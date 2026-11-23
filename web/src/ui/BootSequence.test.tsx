import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { lowPower } from '../state/motion';
import { resetWebglProbe, sceneFailed } from '../state/effects';
import { BootSequence } from './BootSequence';

const original = HTMLCanvasElement.prototype.getContext;

beforeEach(() => {
  lowPower.value = false;
  sceneFailed.value = false;
  resetWebglProbe();
});

afterEach(() => {
  HTMLCanvasElement.prototype.getContext = original;
  resetWebglProbe();
});

async function mountBoot(done: boolean) {
  const root = document.createElement('div');
  await act(() => {
    render(<BootSequence done={done} />, root);
  });
  return root;
}

describe('BootSequence', () => {
  it('keeps the CSS rings as the loading state when WebGL is off', async () => {
    // The jsdom getContext returns null, so the probe is false.
    const root = await mountBoot(false);
    const boot = root.querySelector('.boot')!;
    expect(boot.classList.contains('holo')).toBe(true);
    expect(boot.querySelector('.boot__rings')).not.toBeNull();
    expect(root.querySelector('[role="status"]')!.textContent).toBe('Linking to the crush session');
    render(null, root);
  });

  it('drops the CSS rings when the scene draws the boot', async () => {
    HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof original;
    const root = await mountBoot(false);
    const boot = root.querySelector('.boot')!;
    expect(boot.classList.contains('holo')).toBe(true);
    expect(boot.querySelector('.boot__rings')).toBeNull();
    expect(root.querySelector('[role="status"]')!.textContent).toBe('Linking to the crush session');
    render(null, root);
  });

  it('says the link is established once done', async () => {
    const root = await mountBoot(true);
    expect(root.querySelector('[role="status"]')!.textContent).toBe('Link established');
    expect(root.querySelector('.boot')!.getAttribute('data-done')).toBe('true');
    render(null, root);
  });
});
