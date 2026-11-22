import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { Clipped, PREVIEW } from './Clipped';

const root = document.createElement('div');
afterEach(async () => { await act(() => { render(null, root); }); });

async function show(text: string) {
  await act(() => { render(<Clipped text={text} label="output" />, root); });
}

describe('Clipped', () => {
  it('does not offer the full text at exactly the preview length', async () => {
    await show('x'.repeat(PREVIEW));
    expect(root.querySelector('pre')!.textContent!.length).toBe(PREVIEW);
    expect(root.querySelector('button')).toBeNull();
  });

  it('offers the full text one character past the preview', async () => {
    await show('x'.repeat(PREVIEW + 1));
    expect(root.querySelector('pre')!.textContent!.length).toBe(PREVIEW);
    expect(root.querySelector('button')?.textContent).toBe('Show full output (4 KB)');
  });
});
