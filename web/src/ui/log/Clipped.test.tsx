import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { Clipped, FULL_CAP, PREVIEW } from './Clipped';

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

  it('forgets the shown-full state when the text changes', async () => {
    await show('x'.repeat(PREVIEW + 1));
    await act(() => { root.querySelector('button')!.click(); });
    expect(root.querySelector('button')).toBeNull();

    // A new command's output is a new text, so it starts clipped again.
    await show('y'.repeat(PREVIEW + 1));
    expect(root.querySelector('pre')!.textContent!.length).toBe(PREVIEW);
    expect(root.querySelector('button')).not.toBeNull();
  });

  it('never cuts a surrogate pair in half', async () => {
    // One astral character straddles the preview boundary: its two code units
    // sit on either side of it.
    const text = 'x'.repeat(PREVIEW - 1) + '😀'.repeat(10);
    await show(text);
    const shown = root.querySelector('pre')!.textContent!;
    // The cut falls before the pair, so the last unit is not a lone surrogate.
    expect(shown.length).toBe(PREVIEW - 1);
    expect(shown.charCodeAt(shown.length - 1)).toBeLessThan(0xd800);

    await act(() => { root.querySelector('button')!.click(); });
    const fullText = 'x'.repeat(FULL_CAP - 1) + '😀'.repeat(10);
    await show(fullText);
    await act(() => { root.querySelector('button')!.click(); });
    const capped = root.querySelector('pre')!.textContent!;
    expect(capped.length).toBe(FULL_CAP - 1);
    expect(capped.charCodeAt(capped.length - 1)).toBeLessThan(0xd800);
  });
});
