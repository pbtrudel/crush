import { expect, test } from '@playwright/test';
import { stderrBuffer } from './stderr';

test('redacts a token split across chunks', () => {
  const b = stderrBuffer(64);
  b.push('Dev server: open http://localhost:5173/#t=abc');
  b.push('def123\nnext line\n');
  expect(b.text()).not.toContain('def123');
  expect(b.text()).toContain('#t=<redacted>');
});

test('keeps only the tail and never a token fragment', () => {
  const b = stderrBuffer(32);
  for (let i = 0; i < 10; i++) b.push(`line ${i} #t=SECRET${i}\n`);
  expect(b.text()).not.toMatch(/SECRET/);
  expect(b.text().length).toBeLessThanOrEqual(32);
});

test('redacts a partial last line', () => {
  const b = stderrBuffer(64);
  b.push('boom #t=HALF');
  expect(b.text()).toBe('boom #t=<redacted>');
});
